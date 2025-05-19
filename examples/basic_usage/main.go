package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ethanzhrepo/ethrpcx"
	"github.com/ethanzhrepo/ethrpcx/client"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func main() {
	// Create a context that will be canceled on ctrl+c
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\nShutting down...")
		cancel()
	}()

	// Configure client with multiple RPC endpoints
	config := client.Config{
		Endpoints: []string{
			// Replace with your own endpoints
			"wss://ethereum-rpc.publicnode.com",
			"https://ethereum-rpc.publicnode.com",
		},
		Timeout:           15 * time.Second,
		ConnectionTimeout: 30 * time.Second,
		EnableAggregation: true,
		EnableMetrics:     true,
	}

	// Create a new client
	ethClient, err := ethrpcx.NewClient(config)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	defer ethClient.Close()

	// Get the latest block number
	blockNum, err := ethClient.BlockNumber(ctx)
	if err != nil {
		log.Fatalf("Failed to get block number: %v", err)
	}
	fmt.Printf("Latest block number: %d\n", blockNum)

	// Subscribe to new block headers using the client's built-in methods
	headerCh := make(chan *types.Header, 10)
	subCtx, subCancel := context.WithTimeout(ctx, 5*time.Second)
	defer subCancel()

	// Try to subscribe to new heads
	sub, err := ethClient.SubscribeNewHeads(subCtx, headerCh)
	if err != nil {
		fmt.Printf("Failed to subscribe to new headers (may not be supported on all endpoints): %v\n", err)
	} else {
		fmt.Println("Successfully subscribed to new block headers")

		// Process block headers in a separate goroutine
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
	addr := common.HexToAddress("0xd8dA6BF26964aF9D7eEd9e03E53415D37aA96045") // Vitalik's address
	var balance string
	err = ethClient.Call(ctx, &balance, "eth_getBalance", addr, "latest")
	if err != nil {
		log.Fatalf("Failed to get balance: %v", err)
	}
	fmt.Printf("Balance of %s: %s wei\n", addr.Hex(), balance)

	// Use EIP-1898 to query a specific block by hash
	var blockHashStr string
	err = ethClient.Call(ctx, &blockHashStr, "eth_getBlockByNumber", "0xd56e30", true)
	if err != nil {
		log.Printf("Failed to get block by number: %v\n", err)
	} else {
		var blockByHash map[string]interface{}
		blockParams := map[string]interface{}{
			"blockHash": blockHashStr,
		}
		err = ethClient.Call(ctx, &blockByHash, "eth_getBlockByHash", blockParams, true)
		if err != nil {
			log.Printf("Failed to get block by hash using EIP-1898: %v\n", err)
		} else {
			fmt.Printf("Successfully retrieved block using EIP-1898\n")
		}
	}

	// Add a delay to allow some block headers to be processed
	fmt.Println("Waiting for events for 30 seconds...")
	select {
	case <-time.After(30 * time.Second):
		fmt.Println("Finished waiting")
	case <-ctx.Done():
		// Context canceled, exit
	}
}
