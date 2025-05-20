// Package ethrpcx provides a high-availability Ethereum RPC client
package ethrpcx

import (
	"context"
	"math/big"
	"sync"

	"github.com/ethanzhrepo/ethrpcx/client"
	"github.com/ethanzhrepo/ethrpcx/eip1898"
	"github.com/ethanzhrepo/ethrpcx/metrics"
	"github.com/ethanzhrepo/ethrpcx/subscribe"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/prometheus/client_golang/prometheus"
)

// Client is a high-availability Ethereum RPC client
type Client struct {
	*client.Client
	sharedWatcher *subscribe.EventWatcher
	watcherMu     sync.Mutex
}

// NewClient creates a new eth client with the given configuration
func NewClient(config client.Config) (*Client, error) {
	c, err := client.NewClient(config)
	if err != nil {
		return nil, err
	}

	if config.EnableMetrics {
		metrics.Initialize()
	}

	return &Client{Client: c}, nil
}

// Close closes the client
func (c *Client) Close() {
	c.watcherMu.Lock()
	if c.sharedWatcher != nil {
		c.sharedWatcher.Close()
		c.sharedWatcher = nil
	}
	c.watcherMu.Unlock()

	c.Client.Close()
}

// getSharedWatcher returns the shared EventWatcher instance, creating it if necessary
func (c *Client) getSharedWatcher() *subscribe.EventWatcher {
	c.watcherMu.Lock()
	defer c.watcherMu.Unlock()

	if c.sharedWatcher == nil {
		c.sharedWatcher = subscribe.NewEventWatcher(c.Client)
	}

	return c.sharedWatcher
}

// Call executes an RPC call with the given method and arguments
func (c *Client) Call(ctx context.Context, result interface{}, method string, args ...interface{}) error {
	return c.Client.Call(ctx, result, method, args...)
}

// BlockNumber retrieves the current block number
func (c *Client) BlockNumber(ctx context.Context) (uint64, error) {
	return c.Client.BlockNumber(ctx)
}

// BlockByNumber retrieves a block by its number
func (c *Client) BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error) {
	return c.Client.BlockByNumber(ctx, number, true)
}

// BlockByHash retrieves a block by its hash
func (c *Client) BlockByHash(ctx context.Context, hash common.Hash) (*types.Block, error) {
	endpoint, err := c.Client.GetActiveEndpoint()
	if err != nil {
		return nil, err
	}

	return endpoint.Client.BlockByHash(ctx, hash)
}

// BlockByReference retrieves a block by its EIP-1898 reference
func (c *Client) BlockByReference(ctx context.Context, blockRef *eip1898.BlockNumberOrHash) (*types.Block, error) {
	endpoint, err := c.Client.GetActiveEndpoint()
	if err != nil {
		return nil, err
	}

	if blockRef.BlockHash != nil {
		return endpoint.Client.BlockByHash(ctx, *blockRef.BlockHash)
	}

	return endpoint.Client.BlockByNumber(ctx, blockRef.BlockNumber)
}

// SubscribeNewHeads subscribes to new block headers
func (c *Client) SubscribeNewHeads(ctx context.Context, ch chan<- *types.Header) (ethereum.Subscription, error) {
	watcher := c.getSharedWatcher()
	return watcher.SubscribeNewHeads(ctx, ch)
}

// SubscribeLogs subscribes to logs matching the given filter criteria
func (c *Client) SubscribeLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error) {
	watcher := c.getSharedWatcher()
	return watcher.SubscribeLogs(ctx, q, ch)
}

// Ping checks connection health by making a lightweight eth_chainId call
func (c *Client) Ping(ctx context.Context) error {
	return c.Client.Ping(ctx)
}

// GetEthClient returns the underlying native Ethereum client from the active endpoint
func (c *Client) GetEthClient(ctx context.Context) (*ethclient.Client, error) {
	endpoint, err := c.Client.GetActiveEndpoint()
	if err != nil {
		return nil, err
	}

	return endpoint.Client, nil
}

// BlockHeader re-exports client.BlockHeader for convenience
// Note: This is a simplified representation used for internal purposes.
// For subscribing to block headers, use *types.Header from go-ethereum
// as shown in the SubscribeNewHeads method.
type BlockHeader = client.BlockHeader

// EnableMetrics initializes the Prometheus metrics collection
func EnableMetrics() {
	metrics.Initialize()
}

// GetMetricsRegistry returns the Prometheus metrics registry
func GetMetricsRegistry() *prometheus.Registry {
	return metrics.GetRegistry()
}
