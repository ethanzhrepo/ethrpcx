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
		watcher:    w,
		eventType:  eventType,
		filter:     filterQuery,
		errChan:    make(chan error),
		done:       make(chan struct{}),
		baseSub:    initialSub,
		internalCh: internalCh, // Store the internal channel for reconnection
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

// resilientSubscription represents a subscription that automatically reconnects
type resilientSubscription struct {
	watcher      *EventWatcher
	eventType    EventType
	filter       *ethereum.FilterQuery
	errChan      chan error
	done         chan struct{}
	mu           sync.Mutex
	baseSub      ethereum.Subscription
	internalCh   interface{} // Store the internal channel
	reconnectMu  sync.Mutex  // Mutex for reconnection process
	reconnecting bool        // Flag to track reconnection state
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
	var retryDelay = 5 * time.Second
	maxRetryDelay := 2 * time.Minute

	for {
		// Wait for either a base subscription error or done signal
		select {
		case err, ok := <-s.baseSub.Err():
			// Check if the error channel was closed (subscription ended gracefully)
			if !ok {
				log.Printf("Subscription base error channel closed, monitoring stopped")
				// Close our own error channel to signal proper termination
				s.mu.Lock()
				if s.errChan != nil {
					close(s.errChan)
					s.errChan = nil
				}
				s.baseSub = nil
				s.mu.Unlock()
				return
			}

			// Check if we're already reconnecting
			s.reconnectMu.Lock()
			if s.reconnecting {
				s.reconnectMu.Unlock()
				log.Printf("Ignoring subscription error while already reconnecting: %v", err)
				continue
			}
			s.reconnecting = true
			s.reconnectMu.Unlock()

			// Propagate the error to the user
			select {
			case s.errChan <- err:
				// Successfully sent error
			default:
				// Error channel is full, log it
				log.Printf("Error from subscription: %v (not reported to user - channel full)", err)
			}

			// Attempt to reconnect
			log.Printf("Subscription error: %v, reconnecting in %v...", err, retryDelay)

			// Unsubscribe from the current subscription before reconnecting
			s.mu.Lock()
			if s.baseSub != nil {
				s.baseSub.Unsubscribe()
				s.baseSub = nil // Avoid double unsubscribe
			}
			s.mu.Unlock()

			time.Sleep(retryDelay)

			// Increase retry delay with exponential backoff
			retryDelay *= 2
			if retryDelay > maxRetryDelay {
				retryDelay = maxRetryDelay
			}

			// Try to resubscribe
			var newSub ethereum.Subscription
			var subErr error

			// Create a new context for subscription
			timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)

			// Reuse the same internal channel for the new subscription
			switch s.eventType {
			case NewHeads:
				// Reuse the internal channel stored during initial subscription
				if headerCh, ok := s.internalCh.(chan *types.Header); ok {
					newSub, subErr = s.watcher.SubscribeNewHeads(timeoutCtx, headerCh)
				} else {
					subErr = errors.New("invalid internal channel type for NewHeads event")
				}

			case Logs:
				if s.filter != nil {
					// Reuse the internal channel stored during initial subscription
					if logsCh, ok := s.internalCh.(chan types.Log); ok {
						newSub, subErr = s.watcher.SubscribeLogs(timeoutCtx, *s.filter, logsCh)
					} else {
						subErr = errors.New("invalid internal channel type for Logs event")
					}
				} else {
					subErr = errors.New("missing filter for log subscription")
				}

			default:
				subErr = fmt.Errorf("unsupported event type for reconnect: %s", s.eventType)
			}

			cancel()

			// Reset reconnecting flag
			s.reconnectMu.Lock()
			s.reconnecting = false
			s.reconnectMu.Unlock()

			if subErr != nil {
				log.Printf("Failed to resubscribe: %v, will retry in %v", subErr, retryDelay)
				continue
			}

			// Store the new subscription
			s.mu.Lock()
			s.baseSub = newSub
			s.mu.Unlock()

			// Reset the retry delay on successful reconnection
			retryDelay = 5 * time.Second
			log.Printf("Successfully reconnected subscription for event type: %s", s.eventType)

		case <-s.done:
			return

		case <-ctx.Done():
			s.Unsubscribe()
			return
		}
	}
}
