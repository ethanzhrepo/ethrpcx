# ethrpcx API 文档

## 概述

`ethrpcx` 是一个用 Go 语言编写的高可用性以太坊 RPC 客户端库，它提供了与以太坊节点的强大交互能力。它支持端点之间的自动故障转移、WebSocket 订阅支持、结果聚合以及高级监控功能。

## 目录

- [安装](#安装)
- [核心概念](#核心概念)
- [客户端配置](#客户端配置)
- [基本用法](#基本用法)
- [错误处理](#错误处理)
- [RPC 方法](#rpc-方法)
- [订阅](#订阅)
- [EIP-1898 支持](#eip-1898-支持)
- [指标和监控](#指标和监控)
- [高级功能](#高级功能)

## 安装

```bash
go get github.com/ethanzhrepo/ethrpcx
```

## 核心概念

ethrpcx 库围绕以下几个关键组件构建：

1. **客户端**：与以太坊节点交互的主要入口点。
2. **端点管理**：自动处理多个 RPC 端点之间的连接管理。
3. **订阅**：通过 WebSocket 连接提供实时事件通知。
4. **聚合**：可以查询多个端点并聚合结果以提高可靠性。
5. **错误处理**：全面的错误分类和重试机制。
6. **监控**：内置支持 Prometheus 指标和 OpenTelemetry 跟踪。

## 客户端配置

`client.Config` 结构允许您配置 ethrpcx 客户端的行为：

| 字段 | 类型 | 描述 | 默认值 |
|-------|------|-------------|---------|
| Endpoints | []string | RPC 端点 URL 列表（HTTP 或 WebSocket） | 必填 |
| Timeout | time.Duration | RPC 操作的默认超时时间 | 15秒 |
| ConnectionTimeout | time.Duration | 建立连接的超时时间 | 30秒 |
| MaxConnectionRetries | int | 连接重试的最大次数 | 3 |
| RetryDelay | time.Duration | 重试尝试之间的初始延迟 | 5秒 |
| MaxRetryDelay | time.Duration | 重试之间的最大延迟 | 60秒 |
| EnableAggregation | bool | 启用来自多个端点的结果聚合 | false |
| PreferFirstResult | bool | 启用聚合时，优先使用第一个结果而不是等待共识 | false |
| EnableTracing | bool | 启用 OpenTelemetry 跟踪 | false |
| EnableMetrics | bool | 启用 Prometheus 指标 | false |

配置示例：

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
    log.Fatalf("创建客户端失败: %v", err)
}
defer ethClient.Close()
```

## 基本用法

### 创建客户端

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
    log.Fatalf("创建客户端失败: %v", err)
}
defer ethClient.Close()
```

### 获取最新区块号

```go
blockNum, err := ethClient.BlockNumber(ctx)
if err != nil {
    log.Fatalf("获取区块号失败: %v", err)
}
fmt.Printf("最新区块号: %d\n", blockNum)
```

### 按编号获取区块

```go
block, err := ethClient.BlockByNumber(ctx, big.NewInt(14000000))
if err != nil {
    log.Fatalf("获取区块失败: %v", err)
}
fmt.Printf("区块 %d 包含 %d 笔交易\n", block.Number(), len(block.Transactions()))
```

### 按哈希获取区块

```go
blockHash := common.HexToHash("0x...")
block, err := ethClient.BlockByHash(ctx, blockHash)
if err != nil {
    log.Fatalf("通过哈希获取区块失败: %v", err)
}
```

### 直接 RPC 调用

```go
var balance string
address := common.HexToAddress("0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045")
err = ethClient.Call(ctx, &balance, "eth_getBalance", address, "latest")
if err != nil {
    log.Fatalf("获取余额失败: %v", err)
}
fmt.Printf("余额: %s wei\n", balance)
```

## 错误处理

ethrpcx 提供全面的错误处理，并对错误类型进行分类：

| 错误类型 | 描述 |
|------------|-------------|
| ConnectionError | 连接失败 |
| TimeoutError | 请求超时 |
| NotFoundError | 资源未找到（区块、交易） |
| RateLimitError | RPC 提供商的速率限制 |
| ValidationError | 无效参数或执行回滚 |
| ServerError | RPC 服务器错误 |
| UnknownError | 其他未分类错误 |

错误处理示例：

```go
result, err := ethClient.BlockNumber(ctx)
if err != nil {
    var rpcErr *client.Error
    if errors.As(err, &rpcErr) {
        switch rpcErr.Type {
        case client.ConnectionError:
            log.Printf("连接错误: %v", err)
        case client.TimeoutError:
            log.Printf("超时错误: %v", err)
        case client.RateLimitError:
            log.Printf("速率限制超出: %v", err)
        default:
            log.Printf("其他错误: %v", err)
        }
    } else {
        log.Printf("非 RPC 错误: %v", err)
    }
}
```

## RPC 方法

### BlockNumber

获取当前区块号。

```go
blockNum, err := ethClient.BlockNumber(ctx)
```

### BlockByNumber

按编号获取区块。

```go
// 获取最新区块
latestBlock, err := ethClient.BlockByNumber(ctx, nil)

// 获取特定区块
block, err := ethClient.BlockByNumber(ctx, big.NewInt(14000000))
```

### BlockByHash

按哈希获取区块。

```go
blockHash := common.HexToHash("0x...")
block, err := ethClient.BlockByHash(ctx, blockHash)
```

### BlockByReference

使用 EIP-1898 区块引用获取区块。

```go
blockRef := eip1898.BlockNumberOrHash{
    BlockNumber: big.NewInt(14000000),
}
// 或者
blockRef := eip1898.BlockNumberOrHash{
    BlockHash: &blockHash,
}
block, err := ethClient.BlockByReference(ctx, &blockRef)
```

### Call

用于任何以太坊 JSON-RPC 调用的通用方法。

```go
var result YourResultType
err := ethClient.Call(ctx, &result, "eth_methodName", param1, param2)
```

### Ping

通过执行轻量级的 `eth_chainId` 调用来检查连接健康状况。

```go
err := ethClient.Ping(ctx)
if err != nil {
    log.Printf("连接健康检查失败: %v", err)
}
```

### GetEthClient

从当前活跃的端点获取底层的 `*ethclient.Client`。

```go
ethClient, err := client.GetEthClient(ctx)
if err != nil {
    log.Fatalf("获取底层eth客户端失败: %v", err)
}
// 现在可以直接使用原生的ethclient
balance, err := ethClient.BalanceAt(ctx, address, nil)
```

## 订阅

### SubscribeNewHeads

订阅新区块头（需要 WebSocket 端点）。

```go
headerCh := make(chan *types.Header)
sub, err := ethClient.SubscribeNewHeads(ctx, headerCh)
if err != nil {
    log.Fatalf("订阅新区块头失败: %v", err)
}

// 处理接收到的区块头
go func() {
    for header := range headerCh {
        fmt.Printf("新区块: #%d，哈希 %s\n", header.Number.Uint64(), header.Hash().Hex())
    }
}()

// 处理订阅错误
go func() {
    for {
        err := <-sub.Err()
        if err != nil {
            fmt.Printf("订阅错误: %v\n", err)
        }
    }
}()
```

### SubscribeLogs

订阅符合给定过滤条件的日志（需要 WebSocket 端点）。

```go
// 定义过滤查询
query := ethereum.FilterQuery{
    Addresses: []common.Address{contractAddress},
    Topics:    [][]common.Hash{{eventSignature}},
}

// 创建日志通道
logsCh := make(chan types.Log)

// 订阅日志
sub, err := ethClient.SubscribeLogs(ctx, query, logsCh)
if err != nil {
    log.Fatalf("订阅日志失败: %v", err)
}

// 处理日志
go func() {
    for log := range logsCh {
        fmt.Printf("接收到日志: %+v\n", log)
    }
}()

// 处理订阅错误
go func() {
    for {
        err := <-sub.Err()
        if err != nil {
            fmt.Printf("订阅错误: %v\n", err)
        }
    }
}()
```

## EIP-1898 支持

EIP-1898 允许通过区块哈希或编号查询区块链状态：

```go
import "github.com/ethanzhrepo/ethrpcx/eip1898"

// 创建 BlockNumberOrHash 引用
blockRef, err := eip1898.NewBlockNumberOrHash("0x...")
if err != nil {
    log.Fatalf("创建区块引用失败: %v", err)
}

// 通过引用查询
block, err := ethClient.BlockByReference(ctx, blockRef)
if err != nil {
    log.Fatalf("获取区块失败: %v", err)
}
```

## 指标和监控

### 启用指标

```go
// 在配置中启用指标
config := client.Config{
    Endpoints:     []string{"..."},
    EnableMetrics: true,
}

// 或者全局启用
ethrpcx.EnableMetrics()

// 获取指标注册表进行自定义配置
registry := ethrpcx.GetMetricsRegistry()
```

可用指标包括：

- `ethrpcx_rpc_requests_total` - RPC 请求总数
- `ethrpcx_rpc_request_duration_seconds` - RPC 请求延迟
- `ethrpcx_active_connections` - 活跃连接数
- `ethrpcx_connection_attempts_total` - 连接尝试总数
- `ethrpcx_subscription_errors_total` - 订阅错误总数
- `ethrpcx_subscription_reconnects_total` - 订阅重连总数
- `ethrpcx_aggregation_discrepancies_total` - 聚合结果中的差异

### 启用跟踪

```go
config := client.Config{
    Endpoints:     []string{"..."},
    EnableTracing: true,
}
```

## 高级功能

### 结果聚合

启用后，客户端可以调用多个端点并聚合结果以提高可靠性：

```go
config := client.Config{
    Endpoints:         []string{"endpoint1", "endpoint2", "endpoint3"},
    EnableAggregation: true,
    // PreferFirstResult: true, // 可选，优先使用第一个结果而不是等待共识
}
```

### WebSocket 重连

客户端自动处理订阅的 WebSocket 重连：

```go
// 如果连接丢失，订阅将自动重连
sub, err := ethClient.SubscribeNewHeads(ctx, headerCh)
```

## 完整示例

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
    // 创建上下文
    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()

    // 配置客户端
    config := client.Config{
        Endpoints: []string{
            "wss://ethereum-rpc.publicnode.com",
            "https://ethereum-rpc.publicnode.com",
        },
        Timeout:           15 * time.Second,
        EnableAggregation: true,
        EnableMetrics:     true,
    }

    // 创建客户端
    ethClient, err := ethrpcx.NewClient(config)
    if err != nil {
        log.Fatalf("创建客户端失败: %v", err)
    }
    defer ethClient.Close()

    // 获取最新区块号
    blockNum, err := ethClient.BlockNumber(ctx)
    if err != nil {
        log.Fatalf("获取区块号失败: %v", err)
    }
    fmt.Printf("最新区块号: %d\n", blockNum)

    // 订阅新区块头
    headerCh := make(chan *types.Header)
    sub, err := ethClient.SubscribeNewHeads(ctx, headerCh)
    if err != nil {
        fmt.Printf("订阅新区块头失败: %v\n", err)
    } else {
        // 在后台处理区块头
        go func() {
            for {
                select {
                case header := <-headerCh:
                    fmt.Printf("新区块: #%d，哈希 %s\n", header.Number.Uint64(), header.Hash().Hex())
                case err := <-sub.Err():
                    fmt.Printf("订阅错误: %v\n", err)
                case <-ctx.Done():
                    return
                }
            }
        }()
    }

    // 获取钱包余额
    addr := common.HexToAddress("0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045")
    var balance string
    err = ethClient.Call(ctx, &balance, "eth_getBalance", addr, "latest")
    if err != nil {
        log.Fatalf("获取余额失败: %v", err)
    }
    fmt.Printf("%s 的余额: %s wei\n", addr.Hex(), balance)

    // 等待一些事件
    time.Sleep(30 * time.Second)
} 