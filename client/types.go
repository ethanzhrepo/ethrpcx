package client

import (
	"errors"
	"fmt"
	"net/url"
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

// ValidationError represents a configuration validation error
type ValidationError struct {
	Field   string
	Message string
}

// Error implements the error interface
func (e ValidationError) Error() string {
	return fmt.Sprintf("validation error in field '%s': %s", e.Field, e.Message)
}

// ConfigValidator provides configuration validation functionality
type ConfigValidator struct{}

// NewConfigValidator creates a new configuration validator
func NewConfigValidator() *ConfigValidator {
	return &ConfigValidator{}
}

// ValidateConfig is a convenience function to validate a configuration
func ValidateConfig(config Config) error {
	validator := NewConfigValidator()
	validationErrors := validator.Validate(config)
	if len(validationErrors) > 0 {
		return validationErrors[0]
	}
	return nil
}

// Validate validates the configuration and returns any validation errors
func (cv *ConfigValidator) Validate(config Config) []ValidationError {
	var errors []ValidationError
	
	// Validate endpoints
	if len(config.Endpoints) == 0 {
		errors = append(errors, ValidationError{
			Field:   "Endpoints",
			Message: "at least one endpoint must be provided",
		})
	} else {
		for i, endpoint := range config.Endpoints {
			if err := cv.validateEndpoint(endpoint); err != nil {
				errors = append(errors, ValidationError{
					Field:   fmt.Sprintf("Endpoints[%d]", i),
					Message: err.Error(),
				})
			}
		}
	}
	
	// Validate timeouts
	if config.Timeout < 0 {
		errors = append(errors, ValidationError{
			Field:   "Timeout",
			Message: "timeout cannot be negative",
		})
	} else if config.Timeout > 0 && config.Timeout < time.Second {
		errors = append(errors, ValidationError{
			Field:   "Timeout",
			Message: "timeout should be at least 1 second for practical use",
		})
	}
	
	if config.ConnectionTimeout < 0 {
		errors = append(errors, ValidationError{
			Field:   "ConnectionTimeout", 
			Message: "connection timeout cannot be negative",
		})
	} else if config.ConnectionTimeout > 0 && config.ConnectionTimeout < time.Second {
		errors = append(errors, ValidationError{
			Field:   "ConnectionTimeout",
			Message: "connection timeout should be at least 1 second",
		})
	}
	
	// Validate retry settings
	if config.MaxConnectionRetries < 0 {
		errors = append(errors, ValidationError{
			Field:   "MaxConnectionRetries",
			Message: "max connection retries cannot be negative",
		})
	} else if config.MaxConnectionRetries > 100 {
		errors = append(errors, ValidationError{
			Field:   "MaxConnectionRetries",
			Message: "max connection retries should not exceed 100 for practical use",
		})
	}
	
	if config.RetryDelay < 0 {
		errors = append(errors, ValidationError{
			Field:   "RetryDelay",
			Message: "retry delay cannot be negative",
		})
	}
	
	if config.MaxRetryDelay < 0 {
		errors = append(errors, ValidationError{
			Field:   "MaxRetryDelay",
			Message: "max retry delay cannot be negative",
		})
	} else if config.RetryDelay > 0 && config.MaxRetryDelay > 0 && config.MaxRetryDelay < config.RetryDelay {
		errors = append(errors, ValidationError{
			Field:   "MaxRetryDelay",
			Message: "max retry delay should be greater than or equal to retry delay",
		})
	}
	
	// Validate aggregation settings
	if config.EnableAggregation && len(config.Endpoints) < 2 {
		errors = append(errors, ValidationError{
			Field:   "EnableAggregation",
			Message: "aggregation requires at least 2 endpoints",
		})
	}
	
	return errors
}

// validateEndpoint validates a single endpoint URL
func (cv *ConfigValidator) validateEndpoint(endpoint string) error {
	if strings.TrimSpace(endpoint) == "" {
		return errors.New("endpoint cannot be empty")
	}
	
	// Parse the URL to validate format
	parsedURL, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid URL format: %v", err)
	}
	
	// Check supported schemes
	scheme := strings.ToLower(parsedURL.Scheme)
	if scheme != "http" && scheme != "https" && scheme != "ws" && scheme != "wss" {
		return fmt.Errorf("unsupported scheme '%s'. Supported schemes: http, https, ws, wss", scheme)
	}
	
	// Check if host is provided
	if parsedURL.Host == "" {
		return errors.New("endpoint must include a host")
	}
	
	return nil
}

// ValidateAndWarn validates configuration and logs warnings for suboptimal settings
func (cv *ConfigValidator) ValidateAndWarn(config Config) []ValidationError {
	errors := cv.Validate(config)
	
	// Check for warnings (non-blocking issues)
	cv.checkWarnings(config)
	
	return errors
}

// checkWarnings checks for configuration that might not be optimal
func (cv *ConfigValidator) checkWarnings(config Config) {
	// Check for very short timeouts
	if config.Timeout > 0 && config.Timeout < 5*time.Second {
		fmt.Printf("Warning: Timeout of %v is quite short and may cause frequent failures\n", config.Timeout)
	}
	
	// Check for very long timeouts
	if config.Timeout > 2*time.Minute {
		fmt.Printf("Warning: Timeout of %v is very long and may affect performance\n", config.Timeout)
	}
	
	// Check aggregation without consensus preference
	if config.EnableAggregation && !config.PreferFirstResult && len(config.Endpoints) > 5 {
		fmt.Printf("Warning: Aggregation with many endpoints (%d) and consensus preference may be slow\n", len(config.Endpoints))
	}
	
	// Check mixed endpoint types
	hasHTTP := false
	hasWS := false
	for _, endpoint := range config.Endpoints {
		if strings.HasPrefix(endpoint, "http://") || strings.HasPrefix(endpoint, "https://") {
			hasHTTP = true
		} else if strings.HasPrefix(endpoint, "ws://") || strings.HasPrefix(endpoint, "wss://") {
			hasWS = true
		}
	}
	
	if hasHTTP && hasWS {
		fmt.Printf("Warning: Mixed HTTP and WebSocket endpoints may have different capabilities for subscriptions\n")
	}
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

	// ValidationRequestError represents invalid parameters
	ValidationRequestError ErrorType = "validation_error"

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
		ValidationRequestError: {
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
	case ValidationRequestError:
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
