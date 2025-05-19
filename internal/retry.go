package internal

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/ethanzhrepo/ethrpcx/client"
)

// RetryConfig defines configuration for retry operations
type RetryConfig struct {
	// Maximum number of retry attempts
	MaxAttempts int

	// Initial delay between retries
	InitialDelay time.Duration

	// Maximum delay between retries
	MaxDelay time.Duration

	// Backoff multiplier for increasing delay
	BackoffMultiplier float64

	// Jitter to add randomness to delay (0-1)
	Jitter float64
}

// DefaultRetryConfig provides sensible defaults for retry operations
var DefaultRetryConfig = RetryConfig{
	MaxAttempts:       3,
	InitialDelay:      500 * time.Millisecond,
	MaxDelay:          30 * time.Second,
	BackoffMultiplier: 2.0,
	Jitter:            0.2,
}

// Thread-safe random number generator
var (
	randMu sync.Mutex
	rng    = rand.New(rand.NewSource(time.Now().UnixNano()))
)

// threadSafeRandom returns a random float64 in a thread-safe manner
func threadSafeRandom() float64 {
	randMu.Lock()
	defer randMu.Unlock()
	return rng.Float64()
}

// Retry executes the given function with retry logic
func Retry(ctx context.Context, config RetryConfig, op func() error) error {
	var err error

	delay := config.InitialDelay

	for attempt := 0; attempt < config.MaxAttempts; attempt++ {
		// Execute the operation
		err = op()

		// If succeeded or not retryable error, return immediately
		if err == nil || !client.ShouldRetry(err) {
			return err
		}

		// Check if context is canceled before sleeping
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// Context still valid, continue with retry
		}

		// Last attempt, don't sleep
		if attempt == config.MaxAttempts-1 {
			break
		}

		// Calculate next delay with jitter
		jitterRange := float64(delay) * config.Jitter
		jitteredDelay := float64(delay) - (jitterRange / 2) + (jitterRange * threadSafeRandom())

		// Sleep with context awareness
		timer := time.NewTimer(time.Duration(jitteredDelay))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			// Timer expired, continue with next attempt
		}

		// Increase delay for next attempt
		delay = time.Duration(float64(delay) * config.BackoffMultiplier)
		if delay > config.MaxDelay {
			delay = config.MaxDelay
		}
	}

	return err
}

// RetryWithResult executes the given function with retry logic and returns a result
func RetryWithResult[T any](ctx context.Context, config RetryConfig, op func() (T, error)) (T, error) {
	var (
		result T
		err    error
	)

	delay := config.InitialDelay

	for attempt := 0; attempt < config.MaxAttempts; attempt++ {
		// Execute the operation
		result, err = op()

		// If succeeded or not retryable error, return immediately
		if err == nil || !client.ShouldRetry(err) {
			return result, err
		}

		// Check if context is canceled before sleeping
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
			// Context still valid, continue with retry
		}

		// Last attempt, don't sleep
		if attempt == config.MaxAttempts-1 {
			break
		}

		// Calculate next delay with jitter
		jitterRange := float64(delay) * config.Jitter
		jitteredDelay := float64(delay) - (jitterRange / 2) + (jitterRange * threadSafeRandom())

		// Sleep with context awareness
		timer := time.NewTimer(time.Duration(jitteredDelay))
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, ctx.Err()
		case <-timer.C:
			// Timer expired, continue with next attempt
		}

		// Increase delay for next attempt
		delay = time.Duration(float64(delay) * config.BackoffMultiplier)
		if delay > config.MaxDelay {
			delay = config.MaxDelay
		}
	}

	return result, err
}
