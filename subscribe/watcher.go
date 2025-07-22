package subscribe

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/ethanzhrepo/ethrpcx/client"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
)

// ReconnectConfig defines the configuration for subscription reconnection
type ReconnectConfig struct {
	InitialDelay    time.Duration
	MaxDelay        time.Duration
	BackoffFactor   float64
	MaxRetries      int
	ReconnectOnErr  bool
}

// DefaultReconnectConfig provides sensible defaults for reconnection
var DefaultReconnectConfig = ReconnectConfig{
	InitialDelay:   5 * time.Second,
	MaxDelay:       2 * time.Minute,
	BackoffFactor:  2.0,
	MaxRetries:     -1, // Unlimited retries
	ReconnectOnErr: true,
}

// EventType represents the type of Ethereum event to subscribe to
type EventType string

const (
	// NewHeads represents a subscription to new block headers
	NewHeads EventType = "newHeads"

	// Logs represents a subscription to new logs
	Logs EventType = "logs"

	// NewPendingTransactions represents a subscription to new pending transactions
	NewPendingTransactions EventType = "newPendingTransactions"

	// Syncing represents a subscription to syncing events
	Syncing EventType = "syncing"
)

// ClientInterface defines the methods needed by EventWatcher
type ClientInterface interface {
	GetActiveEndpoint() (*client.Endpoint, error)
}

// EventWatcher manages subscriptions to Ethereum events
type EventWatcher struct {
	client        ClientInterface
	subscriptions map[string]ethereum.Subscription
	mu            sync.RWMutex
	closed        bool
}

// NewEventWatcher creates a new event watcher with the given client
func NewEventWatcher(client ClientInterface) *EventWatcher {
	return &EventWatcher{
		client:        client,
		subscriptions: make(map[string]ethereum.Subscription),
	}
}

// SubscribeNewHeads subscribes to new block headers
func (w *EventWatcher) SubscribeNewHeads(ctx context.Context, ch chan<- *types.Header) (ethereum.Subscription, error) {
	if w.closed {
		return nil, errors.New("event watcher is closed")
	}

	// Get an active endpoint
	endpoint, err := w.client.GetActiveEndpoint()
	if err != nil {
		return nil, err
	}

	// Ensure the endpoint is WebSocket
	if !endpoint.IsWss {
		return nil, fmt.Errorf("endpoint %s is not a WebSocket endpoint, required for subscriptions", endpoint.URL)
	}

	// Create the subscription
	sub, err := endpoint.Client.SubscribeNewHead(ctx, ch)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to new heads: %w", err)
	}

	// Register the subscription
	w.mu.Lock()
	subID := fmt.Sprintf("newHeads-%p", sub)
	w.subscriptions[subID] = sub
	w.mu.Unlock()

	// Create a wrapper subscription that will clean up when closed
	return &wrappedSubscription{
		Subscription: sub,
		onUnsubscribe: func() {
			w.mu.Lock()
			delete(w.subscriptions, subID)
			w.mu.Unlock()
		},
	}, nil
}

// SubscribeLogs subscribes to logs matching the given filter criteria
func (w *EventWatcher) SubscribeLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error) {
	if w.closed {
		return nil, errors.New("event watcher is closed")
	}

	// Get an active endpoint
	endpoint, err := w.client.GetActiveEndpoint()
	if err != nil {
		return nil, err
	}

	// Ensure the endpoint is WebSocket
	if !endpoint.IsWss {
		return nil, fmt.Errorf("endpoint %s is not a WebSocket endpoint, required for subscriptions", endpoint.URL)
	}

	// Create the subscription
	sub, err := endpoint.Client.SubscribeFilterLogs(ctx, q, ch)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to logs: %w", err)
	}

	// Register the subscription
	w.mu.Lock()
	subID := fmt.Sprintf("logs-%p", sub)
	w.subscriptions[subID] = sub
	w.mu.Unlock()

	// Create a wrapper subscription that will clean up when closed
	return &wrappedSubscription{
		Subscription: sub,
		onUnsubscribe: func() {
			w.mu.Lock()
			delete(w.subscriptions, subID)
			w.mu.Unlock()
		},
	}, nil
}

// SubscribeWithReconnect subscribes to an event with automatic reconnection
func (w *EventWatcher) SubscribeWithReconnect(ctx context.Context, eventType EventType, ch interface{}, filterQuery *ethereum.FilterQuery) (ethereum.Subscription, error) {
	if w.closed {
		return nil, errors.New("event watcher is closed")
	}

	// We no longer need a feed for forwarding events
	var subscription ethereum.Subscription
	var err error
	var internalCh interface{}

	// Create the appropriate subscription based on event type
	switch eventType {
	case NewHeads:
		headersCh, ok := ch.(chan<- *types.Header)
		if !ok {
			return nil, errors.New("channel type must be chan<- *types.Header for NewHeads event")
		}

		internalHeaderCh := make(chan *types.Header)
		internalCh = internalHeaderCh // Save for reconnection logic
		go w.forwardHeaders(ctx, internalHeaderCh, headersCh)
		subscription, err = w.SubscribeNewHeads(ctx, internalHeaderCh)

	case Logs:
		if filterQuery == nil {
			return nil, errors.New("filter query is required for Logs event")
		}

		logsCh, ok := ch.(chan<- types.Log)
		if !ok {
			return nil, errors.New("channel type must be chan<- types.Log for Logs event")
		}

		internalLogCh := make(chan types.Log)
		internalCh = internalLogCh // Save for reconnection logic
		go w.forwardLogs(ctx, internalLogCh, logsCh)
		subscription, err = w.SubscribeLogs(ctx, *filterQuery, internalLogCh)

	default:
		return nil, fmt.Errorf("unsupported event type: %s", eventType)
	}

	if err != nil {
		return nil, err
	}

	// Create a resiliency wrapper around the subscription
	return w.createResilientSubscription(ctx, eventType, subscription, filterQuery, internalCh)
}

// forwardHeaders forwards headers from the internal channel to the user's channel
func (w *EventWatcher) forwardHeaders(ctx context.Context, src <-chan *types.Header, dst chan<- *types.Header) {
	for {
		select {
		case <-ctx.Done():
			return

		case header, ok := <-src:
			if !ok {
				return
			}

			select {
			case dst <- header:
				// Successfully forwarded
			default:
				// User's channel is full, drop the header
				log.Printf("Warning: dropped header %s - channel full", header.Hash().Hex())
			}
		}
	}
}

// forwardLogs forwards logs from the internal channel to the user's channel
func (w *EventWatcher) forwardLogs(ctx context.Context, src <-chan types.Log, dst chan<- types.Log) {
	for {
		select {
		case <-ctx.Done():
			return

		case logEvent, ok := <-src:
			if !ok {
				return
			}

			select {
			case dst <- logEvent:
				// Successfully forwarded
			default:
				// User's channel is full, drop the log
				log.Printf("Warning: dropped log %s - channel full", logEvent.TxHash.Hex())
			}
		}
	}
}

// createResilientSubscription creates a subscription that automatically reconnects
func (w *EventWatcher) createResilientSubscription(
	ctx context.Context,
	eventType EventType,
	initialSub ethereum.Subscription,
	filterQuery *ethereum.FilterQuery,
	internalCh interface{},
) (ethereum.Subscription, error) {

	resilientSub := &resilientSubscription{
		watcher:      w,
		eventType:    eventType,
		filter:       filterQuery,
		errChan:      make(chan error),
		done:         make(chan struct{}),
		baseSub:      initialSub,
		internalCh:   internalCh, // Store the internal channel for reconnection
		reconnectMgr: NewReconnectManager(DefaultReconnectConfig),
	}

	// Start the error monitoring goroutine
	go resilientSub.monitor(ctx)

	return resilientSub, nil
}

// Close closes the event watcher and all subscriptions
func (w *EventWatcher) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return
	}

	for _, sub := range w.subscriptions {
		sub.Unsubscribe()
	}

	w.subscriptions = make(map[string]ethereum.Subscription)
	w.closed = true
}

// wrappedSubscription wraps a subscription with an onUnsubscribe callback
type wrappedSubscription struct {
	ethereum.Subscription
	onUnsubscribe func()
}

// Unsubscribe unsubscribes from the subscription and calls the onUnsubscribe callback
func (s *wrappedSubscription) Unsubscribe() {
	s.Subscription.Unsubscribe()
	if s.onUnsubscribe != nil {
		s.onUnsubscribe()
	}
}

// ReconnectManager handles the reconnection logic
type ReconnectManager struct {
	config       ReconnectConfig
	currentDelay time.Duration
	retryCount   int
	mu           sync.Mutex
}

// NewReconnectManager creates a new reconnect manager
func NewReconnectManager(config ReconnectConfig) *ReconnectManager {
	return &ReconnectManager{
		config:       config,
		currentDelay: config.InitialDelay,
	}
}

// NextDelay calculates and returns the next retry delay
func (rm *ReconnectManager) NextDelay() time.Duration {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	
	if rm.config.MaxRetries > 0 && rm.retryCount >= rm.config.MaxRetries {
		return -1 // No more retries
	}
	
	delay := rm.currentDelay
	rm.currentDelay = time.Duration(float64(rm.currentDelay) * rm.config.BackoffFactor)
	if rm.currentDelay > rm.config.MaxDelay {
		rm.currentDelay = rm.config.MaxDelay
	}
	rm.retryCount++
	
	return delay
}

// Reset resets the retry state after a successful connection
func (rm *ReconnectManager) Reset() {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	
	rm.currentDelay = rm.config.InitialDelay
	rm.retryCount = 0
}

// resilientSubscription represents a subscription that automatically reconnects
type resilientSubscription struct {
	watcher         *EventWatcher
	eventType       EventType
	filter          *ethereum.FilterQuery
	errChan         chan error
	done            chan struct{}
	mu              sync.Mutex
	baseSub         ethereum.Subscription
	internalCh      interface{} // Store the internal channel
	reconnectMgr    *ReconnectManager
	reconnecting    bool
}

// Err returns a channel that emits subscription errors
func (s *resilientSubscription) Err() <-chan error {
	return s.errChan
}

// Unsubscribe unsubscribes from the subscription
func (s *resilientSubscription) Unsubscribe() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.baseSub != nil {
		s.baseSub.Unsubscribe()
	}

	close(s.done)
}

// monitor monitors the subscription and attempts to reconnect on failure
func (s *resilientSubscription) monitor(ctx context.Context) {
	for {
		select {
		case err, ok := <-s.baseSub.Err():
			if !ok {
				s.handleChannelClosed()
				return
			}
			
			if !s.shouldReconnect(err) {
				continue
			}
			
			s.handleReconnection(ctx, err)

		case <-s.done:
			return

		case <-ctx.Done():
			s.Unsubscribe()
			return
		}
	}
}

// handleChannelClosed handles when the subscription error channel is closed
func (s *resilientSubscription) handleChannelClosed() {
	log.Printf("Subscription base error channel closed, monitoring stopped")
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if s.errChan != nil {
		close(s.errChan)
		s.errChan = nil
	}
	s.baseSub = nil
}

// shouldReconnect determines if we should attempt to reconnect
func (s *resilientSubscription) shouldReconnect(err error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	if s.reconnecting {
		log.Printf("Ignoring subscription error while already reconnecting: %v", err)
		return false
	}
	
	s.reconnecting = true
	
	// Propagate the error to the user
	select {
	case s.errChan <- err:
		// Successfully sent error
	default:
		// Error channel is full, log it
		log.Printf("Error from subscription: %v (not reported to user - channel full)", err)
	}
	
	return true
}

// handleReconnection performs the reconnection logic
func (s *resilientSubscription) handleReconnection(ctx context.Context, err error) {
	defer func() {
		s.mu.Lock()
		s.reconnecting = false
		s.mu.Unlock()
	}()
	
	// Unsubscribe from the current subscription
	s.mu.Lock()
	if s.baseSub != nil {
		s.baseSub.Unsubscribe()
		s.baseSub = nil
	}
	s.mu.Unlock()
	
	// Attempt reconnection with backoff
	for {
		delay := s.reconnectMgr.NextDelay()
		if delay < 0 {
			log.Printf("Max reconnection attempts reached for event type: %s", s.eventType)
			return
		}
		
		log.Printf("Subscription error: %v, reconnecting in %v...", err, delay)
		time.Sleep(delay)
		
		if newSub := s.tryResubscribe(ctx); newSub != nil {
			s.mu.Lock()
			s.baseSub = newSub
			s.mu.Unlock()
			
			s.reconnectMgr.Reset()
			log.Printf("Successfully reconnected subscription for event type: %s", s.eventType)
			return
		}
	}
}

// tryResubscribe attempts to create a new subscription
func (s *resilientSubscription) tryResubscribe(ctx context.Context) ethereum.Subscription {
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	
	switch s.eventType {
	case NewHeads:
		if headerCh, ok := s.internalCh.(chan *types.Header); ok {
			if newSub, err := s.watcher.SubscribeNewHeads(timeoutCtx, headerCh); err == nil {
				return newSub
			} else {
				log.Printf("Failed to resubscribe to new heads: %v", err)
			}
		} else {
			log.Printf("Invalid internal channel type for NewHeads event")
		}
		
	case Logs:
		if s.filter != nil {
			if logsCh, ok := s.internalCh.(chan types.Log); ok {
				if newSub, err := s.watcher.SubscribeLogs(timeoutCtx, *s.filter, logsCh); err == nil {
					return newSub
				} else {
					log.Printf("Failed to resubscribe to logs: %v", err)
				}
			} else {
				log.Printf("Invalid internal channel type for Logs event")
			}
		} else {
			log.Printf("Missing filter for log subscription")
		}
		
	default:
		log.Printf("Unsupported event type for reconnect: %s", s.eventType)
	}
	
	return nil
}
