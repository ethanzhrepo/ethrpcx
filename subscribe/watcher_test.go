package subscribe

import (
	"context"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ethanzhrepo/ethrpcx/client"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

// MockClient implements ClientInterface for testing
type MockClient struct {
	endpoints       []*client.Endpoint
	mu              sync.Mutex
	callCount       int
	shouldFail      bool
	failureCount    int
	currentFailures int
}

// NewMockClient creates a new mock client
func NewMockClient() *MockClient {
	return &MockClient{
		endpoints: []*client.Endpoint{
			{
				URL:       "wss://mock.endpoint",
				IsWss:     true,
				IsHealthy: true,
				IsClosed:  false,
			},
		},
	}
}

// GetActiveEndpoint implements ClientInterface
func (m *MockClient) GetActiveEndpoint() (*client.Endpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.callCount++

	if m.shouldFail && m.currentFailures < m.failureCount {
		m.currentFailures++
		return nil, errors.New("mock endpoint failure")
	}

	return m.endpoints[0], nil
}

// MockSubscription implements ethereum.Subscription for testing
type MockSubscription struct {
	errChan    chan error
	unsubCount int
	mu         sync.Mutex
	closed     bool
}

// Err implements ethereum.Subscription
func (m *MockSubscription) Err() <-chan error {
	return m.errChan
}

// Unsubscribe implements ethereum.Subscription
func (m *MockSubscription) Unsubscribe() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.closed {
		m.unsubCount++
		m.closed = true
		close(m.errChan)
	}
}

// SimulateError injects an error into the subscription
func (m *MockSubscription) SimulateError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.closed {
		m.errChan <- err
	}
}

// GetUnsubscribeCount returns the number of times Unsubscribe was called
func (m *MockSubscription) GetUnsubscribeCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.unsubCount
}

// IsClosed returns whether the subscription is closed
func (m *MockSubscription) IsClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// MockEndpoint represents a mock RPC endpoint
type MockEndpoint struct {
	client.Endpoint

	// Mocking behavior
	headers      chan *types.Header
	logs         chan types.Log
	subscription *MockSubscription
}

// MockSubscribeProvider is an interface that the test can use to inject mocks
type MockSubscribeProvider interface {
	SubscribeNewHeads(ctx context.Context, ch chan<- *types.Header) (ethereum.Subscription, error)
	SubscribeLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error)
}

// NewMockSubscription creates a new mock subscription
func NewMockSubscription() *MockSubscription {
	return &MockSubscription{
		errChan: make(chan error, 10), // Buffered to avoid blocking
	}
}

// MockEventWatcher extends EventWatcher for testing
type MockEventWatcher struct {
	*EventWatcher
	mockSubscribeNewHeads func(ctx context.Context, ch chan<- *types.Header) (ethereum.Subscription, error)
	mockSubscribeLogs     func(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error)
	subscribeCallCount    int
	mu                    sync.Mutex
}

// SubscribeNewHeads overrides the base method for testing
func (m *MockEventWatcher) SubscribeNewHeads(ctx context.Context, ch chan<- *types.Header) (ethereum.Subscription, error) {
	m.mu.Lock()
	m.subscribeCallCount++
	m.mu.Unlock()

	if m.mockSubscribeNewHeads != nil {
		return m.mockSubscribeNewHeads(ctx, ch)
	}
	// Return an error instead of calling the real method to avoid nil pointer issues
	return nil, errors.New("mock subscription not configured")
}

// GetSubscribeCallCount returns the number of times SubscribeNewHeads was called
func (m *MockEventWatcher) GetSubscribeCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.subscribeCallCount
}

// SubscribeLogs overrides the base method for testing
func (m *MockEventWatcher) SubscribeLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error) {
	m.mu.Lock()
	m.subscribeCallCount++
	m.mu.Unlock()

	if m.mockSubscribeLogs != nil {
		return m.mockSubscribeLogs(ctx, q, ch)
	}
	return m.EventWatcher.SubscribeLogs(ctx, q, ch)
}

// SubscribeWithReconnect overrides the base method to use our mock implementations
func (m *MockEventWatcher) SubscribeWithReconnect(ctx context.Context, eventType EventType, ch interface{}, filterQuery *ethereum.FilterQuery) (ethereum.Subscription, error) {
	return m.EventWatcher.SubscribeWithReconnect(ctx, eventType, ch, filterQuery)
}

// TestEventWatcherSubscribeWithReconnect tests the basic reconnection flow
func TestEventWatcherSubscribeWithReconnect(t *testing.T) {
	mockClient := NewMockClient()
	baseWatcher := NewEventWatcher(mockClient)

	// Create a mock watcher that extends the base watcher
	mockSub := NewMockSubscription()

	// Track subscription sequence
	var subscriptions []*MockSubscription
	var subsLock sync.Mutex

	// Create our mock implementation that will return a new mock subscription on each call
	mockWatcher := &MockEventWatcher{
		EventWatcher: baseWatcher,
		mockSubscribeNewHeads: func(ctx context.Context, ch chan<- *types.Header) (ethereum.Subscription, error) {
			// For the first call, return our predefined mock
			subsLock.Lock()
			defer subsLock.Unlock()

			if len(subscriptions) == 0 {
				subscriptions = append(subscriptions, mockSub)
				return mockSub, nil
			}

			// For subsequent calls (during reconnection), create a new mock subscription
			newSub := NewMockSubscription()
			subscriptions = append(subscriptions, newSub)

			// Send a header to verify data flow works after reconnection
			go func() {
				// Give time for subscription setup to complete
				time.Sleep(50 * time.Millisecond)

				// Create and send a mock header
				header := &types.Header{
					Number: big.NewInt(100),
					Time:   uint64(time.Now().Unix()),
				}
				ch <- header
			}()

			return newSub, nil
		},
	}

	// Create a test channel and add a checker for received headers
	fullHeaderCh := make(chan *types.Header, 10)
	var headerCh chan<- *types.Header = fullHeaderCh
	headerReceived := make(chan struct{})

	go func() {
		header := <-fullHeaderCh
		if header != nil && header.Number.Uint64() == 100 {
			close(headerReceived)
		}
	}()

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Subscribe to new heads with reconnect
	sub, err := mockWatcher.SubscribeWithReconnect(ctx, NewHeads, headerCh, nil)
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	// Verify initial subscription was created
	if mockWatcher.GetSubscribeCallCount() != 1 {
		t.Fatalf("Expected 1 subscription call, got %d", mockWatcher.GetSubscribeCallCount())
	}

	// Simulate a subscription error to trigger reconnection
	mockSub.SimulateError(errors.New("connection lost"))

	// Wait a bit for reconnection and the header to be processed
	select {
	case <-headerReceived:
		// Success! We received a header after reconnection
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for header after reconnection")
	}

	// Verify reconnection happened
	subsLock.Lock()
	if len(subscriptions) < 2 {
		t.Fatalf("Expected at least 2 subscriptions (original + reconnect), got %d", len(subscriptions))
	}
	subsLock.Unlock()

	// Verify subscription call count increased
	if count := mockWatcher.GetSubscribeCallCount(); count < 2 {
		t.Fatalf("Expected at least 2 subscription calls after reconnect, got %d", count)
	}

	// Check if unsubscribe was called on the original subscription
	if mockSub.GetUnsubscribeCount() == 0 {
		t.Fatal("Expected original subscription to be unsubscribed on error")
	}

	// Clean up
	sub.Unsubscribe()

	// Ensure the subscription is properly cleaned up
	subsLock.Lock()
	for i, s := range subscriptions {
		if !s.IsClosed() {
			t.Fatalf("Expected subscription %d to be closed after Unsubscribe", i)
		}
	}
	subsLock.Unlock()

	// Test with a clean shutdown
	mockWatcher.Close()
}

// TestEventWatcherContextCancellation tests that subscriptions respond to context cancellation
func TestEventWatcherContextCancellation(t *testing.T) {
	mockClient := NewMockClient()
	baseWatcher := NewEventWatcher(mockClient)

	// Create a mock subscription
	mockSub := NewMockSubscription()
	mockWatcher := &MockEventWatcher{
		EventWatcher: baseWatcher,
		mockSubscribeNewHeads: func(ctx context.Context, ch chan<- *types.Header) (ethereum.Subscription, error) {
			return mockSub, nil
		},
	}

	// Create a test channel
	fullHeaderCh := make(chan *types.Header, 10)
	var headerCh chan<- *types.Header = fullHeaderCh

	// Create a context that we will cancel
	ctx, cancel := context.WithCancel(context.Background())

	// Subscribe to new heads with reconnect
	sub, err := mockWatcher.SubscribeWithReconnect(ctx, NewHeads, headerCh, nil)
	if err != nil {
		t.Fatalf("Failed to subscribe: %v", err)
	}

	// Cancel the context to simulate cleanup
	cancel()

	// Wait a bit for cleanup to occur
	time.Sleep(100 * time.Millisecond)

	// Verify the subscription was unsubscribed due to context cancellation
	if !mockSub.IsClosed() {
		t.Fatal("Expected subscription to be closed after context cancellation")
	}

	// Ensure the resilient subscription is also properly cleaned up
	select {
	case _, ok := <-sub.Err():
		if ok {
			t.Fatal("Expected subscription.Err() channel to be closed")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timed out waiting for subscription.Err() channel to be closed")
	}
}

// TestConcurrentSubscriptions tests multiple concurrent subscriptions
func TestConcurrentSubscriptions(t *testing.T) {
	mockClient := NewMockClient()
	baseWatcher := NewEventWatcher(mockClient)

	// Track all created subscriptions
	var allSubs []*MockSubscription
	var subsLock sync.Mutex

	// Create mock watcher
	mockWatcher := &MockEventWatcher{
		EventWatcher: baseWatcher,
		mockSubscribeNewHeads: func(ctx context.Context, ch chan<- *types.Header) (ethereum.Subscription, error) {
			mockSub := NewMockSubscription()

			subsLock.Lock()
			allSubs = append(allSubs, mockSub)
			subsLock.Unlock()

			return mockSub, nil
		},
	}

	// Create multiple concurrent subscriptions
	const numSubscriptions = 5
	subs := make([]ethereum.Subscription, numSubscriptions)
	fullChannels := make([]chan *types.Header, numSubscriptions)
	channels := make([]chan<- *types.Header, numSubscriptions)

	// Setup all subscriptions
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(numSubscriptions)

	for i := 0; i < numSubscriptions; i++ {
		fullChannels[i] = make(chan *types.Header, 10)
		channels[i] = fullChannels[i]

		go func(idx int) {
			defer wg.Done()
			var err error
			subs[idx], err = mockWatcher.SubscribeWithReconnect(ctx, NewHeads, channels[idx], nil)
			if err != nil {
				t.Errorf("Failed to create subscription %d: %v", idx, err)
			}
		}(i)
	}

	// Wait for all subscriptions to be created
	wg.Wait()

	// Verify all subscriptions were created
	if count := mockWatcher.GetSubscribeCallCount(); count != numSubscriptions {
		t.Fatalf("Expected %d subscription calls, got %d", numSubscriptions, count)
	}

	// Simulate errors on half of the subscriptions
	subsLock.Lock()
	halfPoint := len(allSubs) / 2
	for i := 0; i < halfPoint; i++ {
		allSubs[i].SimulateError(errors.New("test error"))
	}
	subsLock.Unlock()

	// Wait for reconnections
	time.Sleep(200 * time.Millisecond)

	// Verify reconnections occurred
	if count := mockWatcher.GetSubscribeCallCount(); count != numSubscriptions+halfPoint {
		t.Fatalf("Expected %d total subscription calls after reconnects, got %d",
			numSubscriptions+halfPoint, count)
	}

	// Unsubscribe from all subscriptions
	for _, sub := range subs {
		sub.Unsubscribe()
	}

	// Wait for unsubscribes to take effect
	time.Sleep(100 * time.Millisecond)

	// Verify all subscriptions were unsubscribed
	subsLock.Lock()
	defer subsLock.Unlock()
	for i, sub := range allSubs {
		if !sub.IsClosed() {
			t.Fatalf("Subscription %d not closed after unsubscribe", i)
		}
	}
}

// TestEventWatcherSubscribeLogsWithReconnect tests the logs subscription reconnection flow
func TestEventWatcherSubscribeLogsWithReconnect(t *testing.T) {
	mockClient := NewMockClient()
	baseWatcher := NewEventWatcher(mockClient)

	// Create a mock watcher that extends the base watcher
	mockSub := NewMockSubscription()

	// Track subscription sequence
	var subscriptions []*MockSubscription
	var subsLock sync.Mutex

	// Create a filter query for the logs subscription
	filterQuery := ethereum.FilterQuery{
		Addresses: []common.Address{common.HexToAddress("0x123456789abcdef0")},
		Topics:    [][]common.Hash{{common.HexToHash("0xabcdef")}},
	}

	// Create our mock implementation that will return a new mock subscription on each call
	mockWatcher := &MockEventWatcher{
		EventWatcher: baseWatcher,
		mockSubscribeLogs: func(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error) {
			// For the first call, return our predefined mock
			subsLock.Lock()
			defer subsLock.Unlock()

			if len(subscriptions) == 0 {
				subscriptions = append(subscriptions, mockSub)
				return mockSub, nil
			}

			// For subsequent calls (during reconnection), create a new mock subscription
			newSub := NewMockSubscription()
			subscriptions = append(subscriptions, newSub)

			// Send a log to verify data flow works after reconnection
			go func() {
				// Give time for subscription setup to complete
				time.Sleep(50 * time.Millisecond)

				// Create and send a mock log
				log := types.Log{
					Address:     common.HexToAddress("0x123456789abcdef0"),
					Topics:      []common.Hash{common.HexToHash("0xabcdef")},
					Data:        []byte("test data"),
					BlockNumber: 100,
					TxHash:      common.HexToHash("0x1234"),
				}
				ch <- log
			}()

			return newSub, nil
		},
	}

	// Create a test channel and add a checker for received logs
	fullLogsCh := make(chan types.Log, 10)
	var logsCh chan<- types.Log = fullLogsCh
	logReceived := make(chan struct{})

	go func() {
		log := <-fullLogsCh
		if log.BlockNumber == 100 && log.Address == common.HexToAddress("0x123456789abcdef0") {
			close(logReceived)
		}
	}()

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Subscribe to logs with reconnect
	sub, err := mockWatcher.SubscribeWithReconnect(ctx, Logs, logsCh, &filterQuery)
	if err != nil {
		t.Fatalf("Failed to subscribe to logs: %v", err)
	}

	// Verify initial subscription was created
	if mockWatcher.GetSubscribeCallCount() != 1 {
		t.Fatalf("Expected 1 subscription call, got %d", mockWatcher.GetSubscribeCallCount())
	}

	// Simulate a subscription error to trigger reconnection
	mockSub.SimulateError(errors.New("connection lost"))

	// Wait for reconnection and the log to be processed
	select {
	case <-logReceived:
		// Success! We received a log after reconnection
	case <-time.After(2 * time.Second):
		t.Fatal("Timed out waiting for log after reconnection")
	}

	// Verify reconnection happened
	subsLock.Lock()
	if len(subscriptions) < 2 {
		t.Fatalf("Expected at least 2 subscriptions (original + reconnect), got %d", len(subscriptions))
	}
	subsLock.Unlock()

	// Verify subscription call count increased
	if count := mockWatcher.GetSubscribeCallCount(); count < 2 {
		t.Fatalf("Expected at least 2 subscription calls after reconnect, got %d", count)
	}

	// Check if unsubscribe was called on the original subscription
	if mockSub.GetUnsubscribeCount() == 0 {
		t.Fatal("Expected original subscription to be unsubscribed on error")
	}

	// Clean up
	sub.Unsubscribe()

	// Ensure all subscriptions are properly cleaned up
	subsLock.Lock()
	for i, s := range subscriptions {
		if !s.IsClosed() {
			t.Fatalf("Expected subscription %d to be closed after Unsubscribe", i)
		}
	}
	subsLock.Unlock()
}
