# ethrpcx

`ethrpcx` is a high-availability Ethereum RPC client library written in Go, featuring:

- Support for WebSocket (WSS) connections with automatic reconnect
- Automatic fallback and switching between multiple RPC endpoints
- Aggregation of multiple RPC calls for improved reliability
- Full support for Ethereum JSON-RPC including EIP-1898 block number based calls
- Subscription to on-chain events such as new blocks and logs
- Built-in distributed tracing support (OpenTelemetry)
- Prometheus metrics integration for monitoring
- Modular design for easy integration and extension

![GitHub commit activity](https://img.shields.io/github/commit-activity/w/ethanzhrepo/ethrpcx)
![GitHub Release](https://img.shields.io/github/v/release/ethanzhrepo/ethrpcx)
![GitHub Repo stars](https://img.shields.io/github/stars/ethanzhrepo/ethrpcx)
![GitHub License](https://img.shields.io/github/license/ethanzhrepo/ethrpcx)


<a href="https://t.me/ethanatca"><img alt="" src="https://img.shields.io/badge/Telegram-%40ethanatca-blue" /></a>
<a href="https://x.com/intent/follow?screen_name=0x99_Ethan">
<img alt="X (formerly Twitter) Follow" src="https://img.shields.io/twitter/follow/0x99_Ethan">
</a>


## Installation

```bash
go get github.com/ethanzhrepo/ethrpcx
```

## Quick Start

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "github.com/ethanzhrepo/ethrpcx"
    "github.com/ethanzhrepo/ethrpcx/client"
)

func main() {
    // Configure multiple RPC endpoints with automatic fallback
    config := client.Config{
        Endpoints: []string{
            "wss://mainnet.infura.io/ws/v3/your_project_id",
            "https://eth-mainnet.g.alchemy.com/v2/your_api_key",
        },
        Timeout: 10 * time.Second,
        EnableAggregation: true,  // Enable result aggregation for consistency
    }

    ethClient, err := ethrpcx.NewClient(config)
    if err != nil {
        log.Fatalf("failed to create ethrpcx: %v", err)
    }
    defer ethClient.Close()

    ctx := context.Background()

    // Get latest block number
    var blockNumber uint64
    blockNumber, err = ethClient.BlockNumber(ctx)
    if err != nil {
        log.Fatalf("call eth_blockNumber failed: %v", err)
    }
    fmt.Println("Latest block number:", blockNumber)

    // Subscribe to new block headers
    headerCh := make(chan *types.Header)
    sub, err := ethClient.SubscribeNewHeads(ctx, headerCh)
    if err != nil {
        log.Fatalf("subscribe new heads failed: %v", err)
    }

    // Process incoming headers
    go func() {
        for header := range headerCh {
            fmt.Printf("New block received: %d with hash %s\n", header.Number.Uint64(), header.Hash().Hex())
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

    // Wait for incoming blocks
    time.Sleep(60 * time.Second)
}
```

## Features

### Multiple Endpoint Support with Automatic Failover

The client can be configured with multiple endpoints and will automatically switch between them if one becomes unresponsive:

```go
config := client.Config{
    Endpoints: []string{
        "wss://mainnet.infura.io/ws/v3/your_project_id",
        "https://eth-mainnet.g.alchemy.com/v2/your_api_key",
        "https://eth.llamarpc.com",
    },
}
```

### WebSocket Support for Real-time Subscriptions

WebSocket endpoints are automatically detected and used for subscriptions:

```go
// Subscribe to new block headers
headerCh := make(chan types.Header)
sub, err := ethClient.SubscribeNewHeads(ctx, headerCh)

// Subscribe to specific logs
query := ethereum.FilterQuery{
    Addresses: []common.Address{contractAddress},
    Topics:    [][]common.Hash{{eventSignature}},
}
logsCh := make(chan types.Log)
sub, err := ethClient.SubscribeLogs(ctx, query, logsCh)
```

### Result Aggregation for Enhanced Reliability

When enabled, the client can call multiple endpoints and aggregate results for consistency:

```go
config := client.Config{
    Endpoints:         []string{"endpoint1", "endpoint2", "endpoint3"},
    EnableAggregation: true,
}
```

### EIP-1898 Support for Blockchain State Queries

Easily query the blockchain state using block hash or number:

```go
// Query by block hash
blockHash := common.HexToHash("0x...")
block, err := ethClient.BlockByHash(ctx, blockHash)

// Use EIP-1898 reference
blockRef, err := eip1898.NewBlockNumberOrHash("0x...")
state, err := ethClient.BlockByReference(ctx, blockRef)
```

### Prometheus Metrics

Built-in Prometheus metrics to monitor performance and reliability:

```go
// Enable metrics collection
ethrpcx.EnableMetrics()

// Get the metrics registry for custom configuration
registry := ethrpcx.GetMetricsRegistry()
```

Available metrics include:
- RPC request counts and latencies (ethrpcx_rpc_requests_total, ethrpcx_rpc_request_duration_seconds)
- Connection tracking (ethrpcx_active_connections, ethrpcx_connection_attempts_total)
- Subscription tracking (ethrpcx_subscription_errors_total, ethrpcx_subscription_reconnects_total)
- Aggregation metrics (ethrpcx_aggregation_discrepancies_total)

### OpenTelemetry Integration

Support for distributed tracing with OpenTelemetry:

```go
config := client.Config{
    EnableTracing: true,
}
```

## Advanced Usage

### Custom Retry Configuration

```go
import "github.com/ethanzhrepo/ethrpcx/internal"

retryConfig := internal.RetryConfig{
    MaxAttempts:       5,
    InitialDelay:      100 * time.Millisecond,
    MaxDelay:          10 * time.Second,
    BackoffMultiplier: 2.0,
    Jitter:            0.1,
}
```

### Direct Access to RPC Methods

```go
var balance string
err := ethClient.Call(ctx, &balance, "eth_getBalance", address, "latest")
```

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Contributing

Contributions are welcome! Please open issues or submit pull requests.