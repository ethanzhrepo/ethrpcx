# ethrpcx API Documentation

## Overview

`ethrpcx` is a high-availability Ethereum RPC client library for Go that offers robust interaction with Ethereum nodes. It provides automatic failover between endpoints, WebSocket subscription support, result aggregation, and advanced monitoring features.

## Table of Contents

- [Installation](#installation)
- [Core Concepts](#core-concepts)
- [Client Configuration](#client-configuration)
- [Basic Usage](#basic-usage)
- [Error Handling](#error-handling)
- [RPC Methods](#rpc-methods)
- [Subscriptions](#subscriptions)
- [EIP-1898 Support](#eip-1898-support)
- [Metrics and Monitoring](#metrics-and-monitoring)
- [Advanced Features](#advanced-features)

## Installation

```bash
go get github.com/ethanzhrepo/ethrpcx
```

## Core Concepts

The ethrpcx library is built around several key components:

1. **Client**: The main entry point for interacting with Ethereum nodes.
2. **Endpoint Management**: Automatically handles connection management across multiple RPC endpoints.
3. **Subscriptions**: Provides real-time event notifications through WebSocket connections.
4. **Aggregation**: Can query multiple endpoints and aggregate results for improved reliability.
5. **Error Handling**: Comprehensive error classification and retry mechanisms.
6. **Monitoring**: Built-in support for Prometheus metrics and OpenTelemetry tracing.

## Client Configuration

The `client.Config` struct allows you to configure the behavior of the ethrpcx client:

| Field | Type | Description | Default |
|-------|------|-------------|---------|
| Endpoints | []string | List of RPC endpoint URLs (HTTP or WebSocket) | required |
| Timeout | time.Duration | Default timeout for RPC operations | 15s |
| ConnectionTimeout | time.Duration | Timeout for establishing connections | 30s |
| MaxConnectionRetries | int | Maximum number of connection retry attempts | 3 |
| RetryDelay | time.Duration | Initial delay between retry attempts | 5s |
| MaxRetryDelay | time.Duration | Maximum delay between retries | 60s |
| EnableAggregation | bool | Enable result aggregation from multiple endpoints | false |
| PreferFirstResult | bool | When aggregation is enabled, prefer first result over waiting for consensus | false |
| EnableTracing | bool | Enable OpenTelemetry tracing | false |
| EnableMetrics | bool | Enable Prometheus metrics | false |

Example configuration:

```go
config := client.Config{
    Endpoints: []string{
        "wss://mainnet.infura.io/ws/v3/your_project_id",
        "https://eth-mainnet.g.alchemy.com/v2/your_api_key",
        "https://eth.llamarpc.com",
    },
    Timeout:           15 * time.Second,
    ConnectionTimeout: 30 * time.Second,
    EnableAggregation: true,
    EnableMetrics:     true,
}

ethClient, err := ethrpcx.NewClient(config)
if err != nil {
    log.Fatalf("Failed to create client: %v", err)
}
defer ethClient.Close()
```

## Basic Usage

### Creating a Client

```go
import (
    "github.com/ethanzhrepo/ethrpcx"
    "github.com/ethanzhrepo/ethrpcx/client"
)

config := client.Config{
    Endpoints: []string{
        "wss://mainnet.infura.io/ws/v3/your_project_id",
        "https://eth-mainnet.g.alchemy.com/v2/your_api_key",
    },
    Timeout: 10 * time.Second,
}

ethClient, err := ethrpcx.NewClient(config)
if err != nil {
    log.Fatalf("Failed to create client: %v", err)
}
defer ethClient.Close()
```

### Getting the Latest Block Number

```go
blockNum, err := ethClient.BlockNumber(ctx)
if err != nil {
    log.Fatalf("Failed to get block number: %v", err)
}
fmt.Printf("Latest block number: %d\n", blockNum)
```

### Retrieving a Block by Number

```go
block, err := ethClient.BlockByNumber(ctx, big.NewInt(14000000))
if err != nil {
    log.Fatalf("Failed to get block: %v", err)
}
fmt.Printf("Block %d has %d transactions\n", block.Number(), len(block.Transactions()))
```

### Retrieving a Block by Hash

```go
blockHash := common.HexToHash("0x...")
block, err := ethClient.BlockByHash(ctx, blockHash)
if err != nil {
    log.Fatalf("Failed to get block by hash: %v", err)
}
```

### Direct RPC Calls

```go
var balance string
address := common.HexToAddress("0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045")
err = ethClient.Call(ctx, &balance, "eth_getBalance", address, "latest")
if err != nil {
    log.Fatalf("Failed to get balance: %v", err)
}
fmt.Printf("Balance: %s wei\n", balance)
```

## Error Handling

ethrpcx provides comprehensive error handling with categorized error types:

| Error Type | Description |
|------------|-------------|
| ConnectionError | Connection failures |
| TimeoutError | Request timeouts |
| NotFoundError | Resources not found (blocks, transactions) |
| RateLimitError | Rate limiting by RPC provider |
| ValidationError | Invalid parameters or reverted execution |
| ServerError | RPC server errors |
| UnknownError | Other uncategorized errors |

Error handling example:

```go
result, err := ethClient.BlockNumber(ctx)
if err != nil {
    var rpcErr *client.Error
    if errors.As(err, &rpcErr) {
        switch rpcErr.Type {
        case client.ConnectionError:
            log.Printf("Connection error: %v", err)
        case client.TimeoutError:
            log.Printf("Timeout error: %v", err)
        case client.RateLimitError:
            log.Printf("Rate limit exceeded: %v", err)
        default:
            log.Printf("Other error: %v", err)
        }
    } else {
        log.Printf("Non-RPC error: %v", err)
    }
}
```

## RPC Methods

### BlockNumber

Retrieves the current block number.

```go
blockNum, err := ethClient.BlockNumber(ctx)
```

### BlockByNumber

Retrieves a block by its number.

```go
// Get the latest block
latestBlock, err := ethClient.BlockByNumber(ctx, nil)

// Get a specific block
block, err := ethClient.BlockByNumber(ctx, big.NewInt(14000000))
```

### BlockByHash

Retrieves a block by its hash.

```go
blockHash := common.HexToHash("0x...")
block, err := ethClient.BlockByHash(ctx, blockHash)
```

### BlockByReference

Retrieves a block using an EIP-1898 block reference.

```go
blockRef := eip1898.BlockNumberOrHash{
    BlockNumber: big.NewInt(14000000),
}
// or
blockRef := eip1898.BlockNumberOrHash{
    BlockHash: &blockHash,
}
block, err := ethClient.BlockByReference(ctx, &blockRef)
```

### Call

General-purpose method for any Ethereum JSON-RPC call.

```go
var result YourResultType
err := ethClient.Call(ctx, &result, "eth_methodName", param1, param2)
```

## Subscriptions

### SubscribeNewHeads

Subscribes to new block headers (requires WebSocket endpoint).

```go
headerCh := make(chan *types.Header)
sub, err := ethClient.SubscribeNewHeads(ctx, headerCh)
if err != nil {
    log.Fatalf("Failed to subscribe to new headers: %v", err)
}

// Process incoming headers
go func() {
    for header := range headerCh {
        fmt.Printf("New block: #%d with hash %s\n", header.Number.Uint64(), header.Hash().Hex())
    }
}()

// Handle subscription errors
go func() {
    for {
        err := <-sub.Err()
        if err != nil {
            fmt.Printf("Subscription error: %v\n", err)
        }
    }
}()
```

### SubscribeLogs

Subscribes to logs matching the given filter criteria (requires WebSocket endpoint).

```go
// Define filter query
query := ethereum.FilterQuery{
    Addresses: []common.Address{contractAddress},
    Topics:    [][]common.Hash{{eventSignature}},
}

// Create channel for logs
logsCh := make(chan types.Log)

// Subscribe to logs
sub, err := ethClient.SubscribeLogs(ctx, query, logsCh)
if err != nil {
    log.Fatalf("Failed to subscribe to logs: %v", err)
}

// Process logs
go func() {
    for log := range logsCh {
        fmt.Printf("Log received: %+v\n", log)
    }
}()

// Handle subscription errors
go func() {
    for {
        err := <-sub.Err()
        if err != nil {
            fmt.Printf("Subscription error: %v\n", err)
        }
    }
}()
```

## EIP-1898 Support

EIP-1898 allows querying blockchain state by block hash or number:

```go
import "github.com/ethanzhrepo/ethrpcx/eip1898"

// Create a BlockNumberOrHash reference
blockRef, err := eip1898.NewBlockNumberOrHash("0x...")
if err != nil {
    log.Fatalf("Failed to create block reference: %v", err)
}

// Query by reference
block, err := ethClient.BlockByReference(ctx, blockRef)
if err != nil {
    log.Fatalf("Failed to get block: %v", err)
}
```

## Metrics and Monitoring

### Enabling Metrics

```go
// Enable metrics in config
config := client.Config{
    Endpoints:     []string{"..."},
    EnableMetrics: true,
}

// Or enable them globally
ethrpcx.EnableMetrics()

// Get the metrics registry for custom configuration
registry := ethrpcx.GetMetricsRegistry()
```

Available metrics include:

- `ethrpcx_rpc_requests_total` - Total number of RPC requests
- `ethrpcx_rpc_request_duration_seconds` - RPC request latency
- `ethrpcx_endpoint_health_status` - Endpoint health status (1=healthy, 0=unhealthy)
- `ethrpcx_subscription_events_total` - Total number of subscription events
- `ethrpcx_subscription_errors_total` - Total number of subscription errors
- `ethrpcx_aggregation_discrepancies_total` - Discrepancies in aggregated results

### Enabling Tracing

```go
config := client.Config{
    Endpoints:     []string{"..."},
    EnableTracing: true,
}
```

## Advanced Features

### Result Aggregation

When enabled, the client can call multiple endpoints and aggregate results for enhanced reliability:

```go
config := client.Config{
    Endpoints:         []string{"endpoint1", "endpoint2", "endpoint3"},
    EnableAggregation: true,
    // PreferFirstResult: true, // Optionally prefer first result over waiting for consensus
}
```

### WebSocket Reconnection

The client automatically handles WebSocket reconnection for subscriptions:

```go
// The subscription will automatically reconnect if the connection is lost
sub, err := ethClient.SubscribeNewHeads(ctx, headerCh)
```

## Complete Example

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/ethanzhrepo/ethrpcx"
    "github.com/ethanzhrepo/ethrpcx/client"
    "github.com/ethereum/go-ethereum/common"
    "github.com/ethereum/go-ethereum/core/types"
)

func main() {
    // Create context
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // Configure client
    config := client.Config{
        Endpoints: []string{
            "wss://ethereum-rpc.publicnode.com",
            "https://ethereum-rpc.publicnode.com",
        },
        Timeout:           15 * time.Second,
        EnableAggregation: true,
        EnableMetrics:     true,
    }

    // Create client
    ethClient, err := ethrpcx.NewClient(config)
    if err != nil {
        log.Fatalf("Failed to create client: %v", err)
    }
    defer ethClient.Close()

    // Get latest block number
    blockNum, err := ethClient.BlockNumber(ctx)
    if err != nil {
        log.Fatalf("Failed to get block number: %v", err)
    }
    fmt.Printf("Latest block number: %d\n", blockNum)

    // Subscribe to new block headers
    headerCh := make(chan *types.Header)
    sub, err := ethClient.SubscribeNewHeads(ctx, headerCh)
    if err != nil {
        fmt.Printf("Failed to subscribe to new headers: %v\n", err)
    } else {
        // Process headers in background
        go func() {
            for {
                select {
                case header := <-headerCh:
                    fmt.Printf("New block: #%d with hash %s\n", header.Number.Uint64(), header.Hash().Hex())
                case err := <-sub.Err():
                    fmt.Printf("Subscription error: %v\n", err)
                case <-ctx.Done():
                    return
                }
            }
        }()
    }

    // Get balance of a wallet
    addr := common.HexToAddress("0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045")
    var balance string
    err = ethClient.Call(ctx, &balance, "eth_getBalance", addr, "latest")
    if err != nil {
        log.Fatalf("Failed to get balance: %v", err)
    }
    fmt.Printf("Balance of %s: %s wei\n", addr.Hex(), balance)

    // Wait for some events
    time.Sleep(30 * time.Second)
} 