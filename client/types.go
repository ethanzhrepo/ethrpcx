package client

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rpc"
)

// Config represents configuration for the RPC client
type Config struct {
	// List of RPC endpoints (HTTP or WebSocket)
	Endpoints []string

	// Default timeout for RPC operations
	Timeout time.Duration

	// Connection timeout
	ConnectionTimeout time.Duration

	// Max number of connection retries
	MaxConnectionRetries int

	// Delay between connection retries (increases with backoff)
	RetryDelay time.Duration

	// Maximum delay between retries
	MaxRetryDelay time.Duration

	// Enable aggregation of results from multiple endpoints
	EnableAggregation bool

	// When aggregation is enabled, prefer the first result over waiting for consensus
	PreferFirstResult bool

	// Enable OpenTelemetry tracing
	EnableTracing bool

	// Enable Prometheus metrics
	EnableMetrics bool
}

// Default configuration values
var DefaultConfig = Config{
	Timeout:              15 * time.Second,
	ConnectionTimeout:    30 * time.Second,
	MaxConnectionRetries: 3,
	RetryDelay:           5 * time.Second,
	MaxRetryDelay:        60 * time.Second,
	EnableAggregation:    false,
	EnableTracing:        false,
	EnableMetrics:        false,
}

// ErrorType categorizes RPC errors for better handling
type ErrorType string

const (
	// ConnectionError represents connection failures
	ConnectionError ErrorType = "connection_error"

	// TimeoutError represents timeouts
	TimeoutError ErrorType = "timeout_error"

	// NotFoundError represents elements that don't exist (blocks, txs)
	NotFoundError ErrorType = "not_found_error"

	// RateLimitError represents rate limiting by RPC provider
	RateLimitError ErrorType = "rate_limit_error"

	// ValidationError represents invalid parameters
	ValidationError ErrorType = "validation_error"

	// ServerError represents server errors
	ServerError ErrorType = "server_error"

	// UnknownError represents other errors
	UnknownError ErrorType = "unknown_error"
)

// Error wraps ethereum client errors with more context
type Error struct {
	Type        ErrorType
	OriginalErr error
	Message     string
	Endpoint    string
}

// Error implements the error interface
func (e *Error) Error() string {
	return fmt.Sprintf("[%s - %s] %s: %v",
		e.Type, e.Endpoint, e.Message, e.OriginalErr)
}

// Unwrap returns the original error
func (e *Error) Unwrap() error {
	return e.OriginalErr
}

// ClassifyError determines the type of RPC error
func ClassifyError(err error, endpoint string) *Error {
	if err == nil {
		return nil
	}

	// First check if it's already our Error type
	var ourErr *Error
	if errors.As(err, &ourErr) {
		// Just update the endpoint if it's different
		if ourErr.Endpoint == "" || ourErr.Endpoint != endpoint {
			ourErr.Endpoint = endpoint
		}
		return ourErr
	}

	// Note: specific go-ethereum error checking can be added here in the future
	// if needed, but requires concrete error types from the go-ethereum package

	// Create a map of error patterns to error types for more maintainable matching
	errorPatterns := map[ErrorType][]string{
		ConnectionError: {
			"connection refused",
			"no such host",
			"dial tcp",
			"connection reset by peer",
			"EOF",
			"websocket: close",
			"broken pipe",
			"i/o timeout",
			"write: connection timed out",
		},
		TimeoutError: {
			"deadline exceeded",
			"context deadline exceeded",
			"timeout",
			"timed out",
		},
		NotFoundError: {
			"not found",
			"transaction not found",
			"block not found",
			"404",
		},
		RateLimitError: {
			"rate limit",
			"too many requests",
			"exceeded",
			"429",
		},
		ValidationError: {
			"invalid",
			"execution reverted",
			"bad request",
			"400",
		},
		ServerError: {
			"internal server error",
			"500",
			"502",
			"503",
			"504",
		},
	}

	// Default error type and message
	errorType := UnknownError
	message := "Unknown RPC error"

	// Get the error message
	errorMsg := err.Error()

	// Look through patterns for a match
	for etType, patterns := range errorPatterns {
		for _, pattern := range patterns {
			if strings.Contains(errorMsg, pattern) {
				errorType = etType
				break
			}
		}
		if errorType != UnknownError {
			break
		}
	}

	// Set a more specific message based on type
	switch errorType {
	case ConnectionError:
		message = "Failed to connect to RPC endpoint"
	case TimeoutError:
		message = "RPC request timed out"
	case NotFoundError:
		message = "Requested resource not found"
	case RateLimitError:
		message = "RPC rate limit exceeded"
	case ValidationError:
		message = "Invalid request parameters or execution reverted"
	case ServerError:
		message = "RPC server error"
	}

	return &Error{
		Type:        errorType,
		OriginalErr: err,
		Message:     message,
		Endpoint:    endpoint,
	}
}

// ShouldRetry determines if an operation should be retried based on error type
func ShouldRetry(err error) bool {
	if err == nil {
		return false
	}

	var rpcErr *Error // Using our concrete Error type
	if errors.As(err, &rpcErr) {
		switch rpcErr.Type {
		case ConnectionError, TimeoutError, RateLimitError, ServerError:
			return true
		default:
			return false
		}
	}

	// For non-Error types, check for common retry patterns
	errStr := err.Error()
	return strings.Contains(errStr, "connection") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "server")
}

// BlockHeader represents Ethereum block header for subscription
type BlockHeader struct {
	Hash       common.Hash
	Number     uint64
	ParentHash common.Hash
	Timestamp  uint64
}

// Endpoint represents an RPC endpoint
type Endpoint struct {
	URL                 string
	Client              *ethclient.Client
	RpcClient           *rpc.Client
	LastUsed            time.Time
	IsClosed            bool
	IsHealthy           bool
	LastFailure         time.Time
	IsWss               bool
	mu                  sync.RWMutex
	ConsecutiveFailures uint
}

// Close closes the endpoint clients
func (e *Endpoint) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.IsClosed {
		if e.RpcClient != nil {
			e.RpcClient.Close()
			e.RpcClient = nil
		}
		if e.Client != nil {
			e.Client.Close()
			e.Client = nil
		}
		e.IsClosed = true
	}
}

// IsFatalConnectionError determines if an error indicates a complete connection failure
// that warrants closing and reconnecting
func IsFatalConnectionError(err error) bool {
	if err == nil {
		return false
	}

	var rpcErr *Error // Using our concrete Error type
	if errors.As(err, &rpcErr) {
		// Only consider connection errors fatal
		return rpcErr.Type == ConnectionError
	}

	// For non-Error types, check for critical connection errors
	errStr := err.Error()
	return strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "no such host") ||
		strings.Contains(errStr, "EOF") ||
		strings.Contains(errStr, "websocket: close") ||
		strings.Contains(errStr, "broken pipe")
}
