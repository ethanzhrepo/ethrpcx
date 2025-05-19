package client

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

// Client is a high-availability Ethereum RPC client
type Client struct {
	config    Config
	endpoints []*Endpoint
	mu        sync.RWMutex
}

// NewClient creates a new eth client with the given options
func NewClient(config Config) (*Client, error) {
	if len(config.Endpoints) == 0 {
		return nil, errors.New("at least one endpoint must be provided")
	}

	// Use default config values for any unset fields
	if config.Timeout == 0 {
		config.Timeout = DefaultConfig.Timeout
	}
	if config.ConnectionTimeout == 0 {
		config.ConnectionTimeout = DefaultConfig.ConnectionTimeout
	}
	if config.MaxConnectionRetries == 0 {
		config.MaxConnectionRetries = DefaultConfig.MaxConnectionRetries
	}
	if config.RetryDelay == 0 {
		config.RetryDelay = DefaultConfig.RetryDelay
	}
	if config.MaxRetryDelay == 0 {
		config.MaxRetryDelay = DefaultConfig.MaxRetryDelay
	}

	client := &Client{
		config:    config,
		endpoints: make([]*Endpoint, 0, len(config.Endpoints)),
	}

	// Initialize endpoints
	for _, url := range config.Endpoints {
		endpoint := &Endpoint{
			URL:      url,
			IsWss:    strings.HasPrefix(url, "ws://") || strings.HasPrefix(url, "wss://"),
			IsClosed: true,
		}
		client.endpoints = append(client.endpoints, endpoint)
	}

	// Connect to at least one endpoint
	err := client.connectToAnyEndpoint()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to any endpoint: %w", err)
	}

	if config.EnableMetrics {
		initMetrics()
	}

	if config.EnableTracing {
		initTracing()
	}

	return client, nil
}

// Close closes all endpoint connections
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, endpoint := range c.endpoints {
		endpoint.Close()
	}
}

// connectToAnyEndpoint tries to connect to any available endpoint
func (c *Client) connectToAnyEndpoint() error {
	var lastErr error

	for _, endpoint := range c.endpoints {
		if err := c.connectToEndpoint(endpoint); err != nil {
			lastErr = err
			continue
		}
		return nil
	}

	return fmt.Errorf("failed to connect to any endpoint: %w", lastErr)
}

// connectToEndpoint establishes a connection to the specified endpoint
func (c *Client) connectToEndpoint(endpoint *Endpoint) error {
	endpoint.mu.Lock()
	defer endpoint.mu.Unlock()

	if !endpoint.IsClosed {
		// Already connected
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.config.ConnectionTimeout)
	defer cancel()

	var (
		rpcClient  *rpc.Client
		err        error
		retryDelay = c.config.RetryDelay
	)

	for i := 0; i < c.config.MaxConnectionRetries; i++ {
		log.Printf("Attempting to connect to endpoint %s (attempt %d/%d)...",
			endpoint.URL, i+1, c.config.MaxConnectionRetries)

		rpcClient, err = rpc.DialContext(ctx, endpoint.URL)
		if err == nil {
			// Test the connection with a simple call
			chainIDCtx, chainIDCancel := context.WithTimeout(ctx, 5*time.Second)
			var chainID string
			callErr := rpcClient.CallContext(chainIDCtx, &chainID, "eth_chainId")
			chainIDCancel()

			if callErr == nil {
				ethClient := ethclient.NewClient(rpcClient)

				endpoint.Client = ethClient
				endpoint.RpcClient = rpcClient
				endpoint.IsClosed = false
				endpoint.IsHealthy = true
				endpoint.LastUsed = time.Now()
				endpoint.ConsecutiveFailures = 0 // Reset failures on successful connection
				log.Printf("Successfully connected to endpoint %s", endpoint.URL)
				return nil
			}

			// Close the rpc client if the test call failed
			rpcClient.Close()
			err = callErr
			log.Printf("Connected but chain verification failed: %v", callErr)
		} else {
			log.Printf("Connection to endpoint %s failed: %v", endpoint.URL, err)
		}

		// Wait before retrying, but check context timeout
		select {
		case <-ctx.Done():
			return ClassifyError(ctx.Err(), endpoint.URL)
		case <-time.After(retryDelay):
			// Increase delay for next attempt with a cap
			nextDelay := retryDelay * 2
			if nextDelay > c.config.MaxRetryDelay {
				nextDelay = c.config.MaxRetryDelay
			}
			retryDelay = nextDelay
		}
	}

	// Update consecutive failures after all connection attempts failed
	endpoint.ConsecutiveFailures++
	endpoint.LastFailure = time.Now()

	return ClassifyError(
		fmt.Errorf("failed to connect after %d attempts: %w",
			c.config.MaxConnectionRetries, err),
		endpoint.URL,
	)
}

// GetActiveEndpoint returns a suitable endpoint based on health status and backoff strategy
func (c *Client) GetActiveEndpoint() (*Endpoint, error) {
	c.mu.RLock()

	// Variables to track best endpoints
	var (
		healthyEndpoints      []*Endpoint
		bestUnhealthyEndpoint *Endpoint
		oldestFailureTime     time.Time
	)

	// Filter endpoints by status
	for _, endpoint := range c.endpoints {
		endpoint.mu.RLock()
		if !endpoint.IsClosed {
			if endpoint.IsHealthy {
				healthyEndpoints = append(healthyEndpoints, endpoint)
			} else {
				// Track the endpoint with the oldest failure time
				if bestUnhealthyEndpoint == nil || endpoint.LastFailure.Before(oldestFailureTime) {
					bestUnhealthyEndpoint = endpoint
					oldestFailureTime = endpoint.LastFailure
				}
			}
		}
		endpoint.mu.RUnlock()
	}
	c.mu.RUnlock()

	// First try to return a healthy endpoint
	if len(healthyEndpoints) > 0 {
		// Select a random healthy endpoint to distribute load
		selectedIndex := 0
		if len(healthyEndpoints) > 1 {
			selectedIndex = int(time.Now().UnixNano() % int64(len(healthyEndpoints)))
		}

		healthyEndpoints[selectedIndex].mu.Lock()
		healthyEndpoints[selectedIndex].LastUsed = time.Now()
		healthyEndpoints[selectedIndex].mu.Unlock()

		return healthyEndpoints[selectedIndex], nil
	}

	// If no healthy endpoints, check if we have any unhealthy ones
	if bestUnhealthyEndpoint != nil {
		// Check if enough time has passed since the last failure
		// Use exponential backoff based on consecutive failures
		backoffDuration := calculateBackoff(bestUnhealthyEndpoint)
		timeSinceFailure := time.Since(bestUnhealthyEndpoint.LastFailure)

		if timeSinceFailure > backoffDuration {
			// Enough time has passed, try this endpoint again
			bestUnhealthyEndpoint.mu.Lock()
			bestUnhealthyEndpoint.LastUsed = time.Now()
			bestUnhealthyEndpoint.mu.Unlock()

			return bestUnhealthyEndpoint, nil
		}
	}

	// No active endpoints, try to connect to any endpoint
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	for _, endpoint := range c.endpoints {
		endpoint.mu.RLock()
		if !endpoint.IsClosed {
			endpoint.mu.RUnlock()

			if err := c.connectToEndpoint(endpoint); err == nil {
				endpoint.mu.Lock()
				endpoint.IsHealthy = true
				endpoint.LastUsed = time.Now()
				endpoint.mu.Unlock()

				return endpoint, nil
			}
			continue
		}
		endpoint.mu.RUnlock()
	}

	// Try to connect to any endpoint
	if err := c.connectToAnyEndpoint(); err != nil {
		return nil, err
	}

	// Find the newly connected endpoint
	for _, endpoint := range c.endpoints {
		endpoint.mu.RLock()
		if !endpoint.IsClosed {
			endpoint.LastUsed = time.Now()
			endpoint.mu.RUnlock()
			return endpoint, nil
		}
		endpoint.mu.RUnlock()
	}

	return nil, errors.New("failed to get active endpoint")
}

// calculateBackoff determines how long to wait before retrying an unhealthy endpoint
// Uses exponential backoff with a maximum delay and jitter
func calculateBackoff(endpoint *Endpoint) time.Duration {
	// Base backoff duration
	baseDuration := 5 * time.Second

	// Maximum backoff duration
	maxDuration := 2 * time.Minute

	// Apply exponential backoff based on consecutive failures
	// Default to 1 if ConsecutiveFailures isn't set yet
	failures := endpoint.ConsecutiveFailures
	if failures < 1 {
		failures = 1
	}

	// Calculate backoff: base * 2^(failures-1) with a maximum
	backoff := baseDuration
	for i := uint(1); i < failures && backoff < maxDuration/2; i++ {
		backoff *= 2
	}

	// Cap at maximum duration
	if backoff > maxDuration {
		backoff = maxDuration
	}

	// Add jitter (±20%)
	jitterFactor := 0.8 + (0.4 * float64(time.Now().UnixNano()%100) / 100.0)
	backoff = time.Duration(float64(backoff) * jitterFactor)

	return backoff
}

// createContext creates a context with the client's default timeout
func (c *Client) createContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), c.config.Timeout)
}

// Call executes an RPC call with the given method and arguments
func (c *Client) Call(ctx context.Context, result interface{}, method string, args ...interface{}) error {
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = c.createContext()
		defer cancel()
	}

	var lastErr error

	// If aggregation is enabled and there are multiple endpoints, use aggregation
	if c.config.EnableAggregation && len(c.endpoints) > 1 {
		return c.aggregateCall(ctx, result, method, args...)
	}

	// Get an active endpoint
	endpoint, err := c.GetActiveEndpoint()
	if err != nil {
		return err
	}

	// Execute the call
	err = endpoint.RpcClient.CallContext(ctx, result, method, args...)
	if err == nil {
		// Successful call - reset failure count
		endpoint.mu.Lock()
		endpoint.IsHealthy = true
		endpoint.ConsecutiveFailures = 0
		endpoint.mu.Unlock()
		return nil
	}

	// Classify the error
	rpcErr := ClassifyError(err, endpoint.URL)
	lastErr = rpcErr

	// Update endpoint health metrics
	endpoint.mu.Lock()
	endpoint.LastFailure = time.Now()
	endpoint.ConsecutiveFailures++

	// Only change health status and close for serious errors
	if IsFatalConnectionError(rpcErr) {
		endpoint.Close()
	} else {
		endpoint.IsHealthy = false
	}
	endpoint.mu.Unlock()

	// If error doesn't merit retry, return immediately
	if !ShouldRetry(rpcErr) {
		return rpcErr
	}

	// If the error indicates a connection issue, try to connect to another endpoint
	log.Printf("RPC call failed with error: %v, trying another endpoint", rpcErr)

	// Try all other endpoints in succession
	for _, e := range c.endpoints {
		if e == endpoint {
			continue
		}

		// Try to connect to this endpoint
		if err := c.connectToEndpoint(e); err != nil {
			lastErr = err
			continue
		}

		// Attempt the call with this endpoint
		err = e.RpcClient.CallContext(ctx, result, method, args...)
		if err == nil {
			// Reset failure count on successful call
			e.mu.Lock()
			e.IsHealthy = true
			e.ConsecutiveFailures = 0
			e.mu.Unlock()
			return nil
		}

		rpcErr = ClassifyError(err, e.URL)
		lastErr = rpcErr

		// Update endpoint failure metrics
		e.mu.Lock()
		e.LastFailure = time.Now()
		e.ConsecutiveFailures++

		// Only change health status for serious errors
		if IsFatalConnectionError(rpcErr) {
			e.Close()
		} else {
			e.IsHealthy = false
		}
		e.mu.Unlock()

		// Break if we shouldn't retry this type of error
		if !ShouldRetry(rpcErr) {
			break
		}
	}

	return lastErr
}

// BlockNumber retrieves the current block number
func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	var result string
	err := c.Call(ctx, &result, "eth_blockNumber")
	if err != nil {
		return 0, err
	}

	// Using hexutil to convert hex string to uint64
	return hexutil.DecodeUint64(result)
}

// BlockByNumber retrieves a block by its number
func (c *Client) BlockByNumber(ctx context.Context, number *big.Int, full bool) (*types.Block, error) {
	endpoint, err := c.GetActiveEndpoint()
	if err != nil {
		return nil, err
	}

	return endpoint.Client.BlockByNumber(ctx, number)
}

// initMetrics initializes Prometheus metrics
func initMetrics() {
	// Placeholder for metrics initialization
}

// initTracing initializes OpenTelemetry tracing
func initTracing() {
	// Placeholder for tracing initialization
}
