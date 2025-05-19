# ethrpcx

`ethrpcx` 是一个用 Go 编写的高可用性以太坊 RPC 客户端库，具有以下特点：

- 支持 WebSocket (WSS) 连接，具有自动重连功能
- 多个 RPC 端点之间的自动故障转移和切换
- 聚合多个 RPC 调用以提高可靠性
- 完整支持以太坊 JSON-RPC，包括基于 EIP-1898 的区块号调用
- 订阅链上事件，如新区块和日志
- 内置分布式追踪支持 (OpenTelemetry)
- Prometheus 指标集成，用于监控
- 模块化设计，便于集成和扩展

## 安装

```bash
go get github.com/ethanzhrepo/ethrpcx
```

## 快速开始

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
    // 配置多个 RPC 端点，具有自动故障转移功能
    config := client.Config{
        Endpoints: []string{
            "wss://mainnet.infura.io/ws/v3/your_project_id",
            "https://eth-mainnet.g.alchemy.com/v2/your_api_key",
        },
        Timeout: 10 * time.Second,
        EnableAggregation: true,  // 启用结果聚合以保持一致性
    }

    ethClient, err := ethrpcx.NewClient(config)
    if err != nil {
        log.Fatalf("创建 ethrpcx 失败: %v", err)
    }
    defer ethClient.Close()

    ctx := context.Background()

    // 获取最新区块号
    var blockNumber uint64
    blockNumber, err = ethClient.BlockNumber(ctx)
    if err != nil {
        log.Fatalf("调用 eth_blockNumber 失败: %v", err)
    }
    fmt.Println("最新区块号:", blockNumber)

    // 订阅新区块头
    headerCh := make(chan *types.Header)
    sub, err := ethClient.SubscribeNewHeads(ctx, headerCh)
    if err != nil {
        log.Fatalf("订阅新区块头失败: %v", err)
    }

    // 处理接收到的区块头
    go func() {
        for header := range headerCh {
            fmt.Printf("收到新区块: %d, 哈希 %s\n", header.Number.Uint64(), header.Hash().Hex())
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

    // 等待接收区块
    time.Sleep(60 * time.Second)
}
```

## 功能特性

### 多端点支持与自动故障转移

客户端可以配置多个端点，如果一个端点无响应，将自动切换到其他端点：

```go
config := client.Config{
    Endpoints: []string{
        "wss://mainnet.infura.io/ws/v3/your_project_id",
        "https://eth-mainnet.g.alchemy.com/v2/your_api_key",
        "https://eth.llamarpc.com",
    },
}
```

### WebSocket 支持实时订阅

WebSocket 端点会被自动检测并用于订阅：

```go
// 订阅新区块头
headerCh := make(chan *types.Header)
sub, err := ethClient.SubscribeNewHeads(ctx, headerCh)

// 订阅特定日志
query := ethereum.FilterQuery{
    Addresses: []common.Address{contractAddress},
    Topics:    [][]common.Hash{{eventSignature}},
}
logsCh := make(chan types.Log)
sub, err := ethClient.SubscribeLogs(ctx, query, logsCh)
```

### 结果聚合增强可靠性

启用后，客户端可以调用多个端点并聚合结果以确保一致性：

```go
config := client.Config{
    Endpoints:         []string{"endpoint1", "endpoint2", "endpoint3"},
    EnableAggregation: true,
}
```

### EIP-1898 支持区块链状态查询

轻松使用区块哈希或区块号查询区块链状态：

```go
// 按区块哈希查询
blockHash := common.HexToHash("0x...")
block, err := ethClient.BlockByHash(ctx, blockHash)

// 使用 EIP-1898 引用
blockRef, err := eip1898.NewBlockNumberOrHash("0x...")
state, err := ethClient.BlockByReference(ctx, blockRef)
```

### Prometheus 指标

内置 Prometheus 指标，用于监控性能和可靠性：

```go
// 启用指标收集
ethrpcx.EnableMetrics()

// 获取指标注册表进行自定义配置
registry := ethrpcx.GetMetricsRegistry()
```

可用指标包括：
- RPC 请求计数和延迟
- 连接成功/失败率
- 订阅事件和错误
- 聚合差异

### OpenTelemetry 集成

支持使用 OpenTelemetry 进行分布式追踪：

```go
config := client.Config{
    EnableTracing: true,
}
```

## 高级用法

### 自定义重试配置

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

### 直接访问 RPC 方法

```go
var balance string
err := ethClient.Call(ctx, &balance, "eth_getBalance", address, "latest")
```

## 许可证

本项目采用 MIT 许可证 - 详见 [LICENSE](LICENSE) 文件。

## 贡献

欢迎贡献！请提交 issue 或 pull request。
