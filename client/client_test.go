package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/rpc"
)

func TestNewClient(t *testing.T) {
	// Test with empty endpoints
	_, err := NewClient(Config{})
	if err == nil {
		t.Fatal("Expected error when no endpoints provided")
	}
	if !strings.Contains(err.Error(), "at least one endpoint must be provided") {
		t.Fatalf("Expected error about missing endpoints, got: %v", err)
	}

	// Setup a mock RPC server that responds to eth_chainId
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if it's a POST request (RPC uses POST)
		if r.Method != "POST" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// Respond with a valid chain ID response
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
	}))
	defer mockServer.Close()

	// Test with a valid configuration using our mock server
	config := Config{
		Endpoints: []string{mockServer.URL},
		Timeout:   5 * time.Second,
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create client with valid endpoint: %v", err)
	}
	defer client.Close()

	// Verify the client works by making a simple call
	var chainID string
	err = client.Call(context.Background(), &chainID, "eth_chainId")
	if err != nil {
		t.Fatalf("Call to mock server failed: %v", err)
	}
	if chainID != "0x1" {
		t.Fatalf("Expected chain ID 0x1, got %s", chainID)
	}

	// Test with multiple endpoints
	mockServer2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x2"}`))
	}))
	defer mockServer2.Close()

	config = Config{
		Endpoints: []string{mockServer.URL, mockServer2.URL},
		Timeout:   5 * time.Second,
	}

	client, err = NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create client with multiple endpoints: %v", err)
	}
	defer client.Close()

	// Test with invalid endpoint
	config = Config{
		Endpoints:            []string{"http://invalid.endpoint.that.doesnt.exist"},
		Timeout:              1 * time.Second, // Short timeout for faster test
		MaxConnectionRetries: 1,               // Only try once to speed up the test
	}

	_, err = NewClient(config)
	if err == nil {
		t.Fatal("Expected error with invalid endpoint, got nil")
	}
}

func TestClassifyError(t *testing.T) {
	testCases := []struct {
		name         string
		err          error
		endpoint     string
		expectedType ErrorType
	}{
		{
			name:         "nil error",
			err:          nil,
			endpoint:     "test",
			expectedType: "",
		},
		{
			name:         "connection error",
			err:          errors.New("connection refused"),
			endpoint:     "http://example.com",
			expectedType: ConnectionError,
		},
		{
			name:         "timeout error",
			err:          errors.New("context deadline exceeded"),
			endpoint:     "http://example.com",
			expectedType: TimeoutError,
		},
		{
			name:         "not found error",
			err:          errors.New("block not found"),
			endpoint:     "http://example.com",
			expectedType: NotFoundError,
		},
		{
			name:         "rate limit error",
			err:          errors.New("rate limit exceeded"),
			endpoint:     "http://example.com",
			expectedType: RateLimitError,
		},
		{
			name:         "validation error",
			err:          errors.New("invalid parameters"),
			endpoint:     "http://example.com",
			expectedType: ValidationRequestError,
		},
		{
			name:         "server error",
			err:          errors.New("internal server error"),
			endpoint:     "http://example.com",
			expectedType: ServerError,
		},
		{
			name:         "unknown error",
			err:          errors.New("some random error"),
			endpoint:     "http://example.com",
			expectedType: UnknownError,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := ClassifyError(tc.err, tc.endpoint)
			if tc.err == nil {
				if result != nil {
					t.Fatalf("Expected nil result for nil error, got %v", result)
				}
				return
			}

			if result.Type != tc.expectedType {
				t.Fatalf("Expected error type %s, got %s", tc.expectedType, result.Type)
			}
			if result.Endpoint != tc.endpoint {
				t.Fatalf("Expected endpoint %s, got %s", tc.endpoint, result.Endpoint)
			}
			if result.OriginalErr != tc.err {
				t.Fatalf("Original error was not preserved")
			}
		})
	}
}

func TestEndpoint_Close(t *testing.T) {
	// Create a test server that can be closed
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
	}))
	defer server.Close()

	// Create an rpc client
	rpcClient, err := rpc.DialHTTP(server.URL)
	if err != nil {
		t.Fatalf("Failed to create RPC client: %v", err)
	}

	// Create an endpoint
	endpoint := &Endpoint{
		URL:       server.URL,
		RpcClient: rpcClient,
		IsClosed:  false,
		IsHealthy: true,
	}

	// Close the endpoint
	endpoint.Close()

	// Verify it's marked as closed
	if !endpoint.IsClosed {
		t.Fatal("Endpoint should be marked as closed")
	}
	if endpoint.RpcClient != nil {
		t.Fatal("RpcClient should be nil after closing")
	}
}

func TestIsFatalConnectionError(t *testing.T) {
	testCases := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "connection refused",
			err:      errors.New("connection refused"),
			expected: true,
		},
		{
			name:     "no such host",
			err:      errors.New("no such host"),
			expected: true,
		},
		{
			name:     "EOF error",
			err:      errors.New("EOF"),
			expected: true,
		},
		{
			name:     "websocket close",
			err:      errors.New("websocket: close"),
			expected: true,
		},
		{
			name:     "broken pipe",
			err:      errors.New("broken pipe"),
			expected: true,
		},
		{
			name:     "timeout error",
			err:      errors.New("context deadline exceeded"),
			expected: false,
		},
		{
			name:     "validation error",
			err:      errors.New("invalid parameters"),
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := IsFatalConnectionError(tc.err)
			if result != tc.expected {
				t.Fatalf("Expected IsFatalConnectionError to return %v for %q, got %v",
					tc.expected, tc.err, result)
			}
		})
	}
}

func TestGetActiveEndpoint(t *testing.T) {
	// Create a client with multiple endpoints
	client := &Client{
		endpoints: []*Endpoint{
			// Closed endpoint
			{
				URL:      "http://closed.example.com",
				IsClosed: true,
			},
			// Unhealthy endpoint with recent failure
			{
				URL:         "http://unhealthy.example.com",
				IsClosed:    false,
				IsHealthy:   false,
				LastFailure: time.Now().Add(-2 * time.Second), // Failed 2 seconds ago
			},
			// Unhealthy endpoint with older failure
			{
				URL:         "http://recovering.example.com",
				IsClosed:    false,
				IsHealthy:   false,
				LastFailure: time.Now().Add(-10 * time.Minute), // Failed 10 minutes ago
			},
			// Healthy endpoint
			{
				URL:       "http://healthy.example.com",
				IsClosed:  false,
				IsHealthy: true,
			},
		},
	}

	// Get an active endpoint
	endpoint, err := client.GetActiveEndpoint()
	if err != nil {
		t.Fatalf("Failed to get active endpoint: %v", err)
	}

	// It should prioritize the healthy endpoint
	if endpoint.URL != "http://healthy.example.com" {
		t.Fatalf("Expected healthy endpoint, got: %s", endpoint.URL)
	}

	// Now make all endpoints unhealthy
	client.endpoints[3].IsHealthy = false
	client.endpoints[3].LastFailure = time.Now() // Just failed

	// Get an active endpoint again
	endpoint, err = client.GetActiveEndpoint()
	if err != nil {
		t.Fatalf("Failed to get active endpoint: %v", err)
	}

	// It should choose the endpoint with the oldest failure time
	if endpoint.URL != "http://recovering.example.com" {
		t.Fatalf("Expected recovering endpoint, got: %s", endpoint.URL)
	}
}

func TestCalculateBackoff(t *testing.T) {
	testCases := []struct {
		name                string
		consecutiveFailures uint
		minExpected         time.Duration
		maxExpected         time.Duration
	}{
		{
			name:                "First failure",
			consecutiveFailures: 1,
			minExpected:         4 * time.Second, // 5s * 0.8 (min jitter)
			maxExpected:         7 * time.Second, // 5s * 1.2 (max jitter)
		},
		{
			name:                "Two failures",
			consecutiveFailures: 2,
			minExpected:         8 * time.Second,  // 10s * 0.8 (min jitter)
			maxExpected:         14 * time.Second, // 10s * 1.2 (max jitter)
		},
		{
			name:                "Three failures",
			consecutiveFailures: 3,
			minExpected:         16 * time.Second, // 20s * 0.8 (min jitter)
			maxExpected:         28 * time.Second, // 20s * 1.2 (max jitter)
		},
		{
			name:                "Many failures (should cap)",
			consecutiveFailures: 10,
			minExpected:         96 * time.Second,  // 120s * 0.8 (min jitter)
			maxExpected:         120 * time.Second, // Cap at 120s even with jitter
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a test endpoint with the specified number of failures
			endpoint := &Endpoint{
				ConsecutiveFailures: tc.consecutiveFailures,
			}

			// Calculate the backoff
			backoff := calculateBackoff(endpoint)

			// Check that the result is within expected range
			if backoff < tc.minExpected || backoff > tc.maxExpected {
				t.Errorf("Expected backoff between %v and %v, got %v",
					tc.minExpected, tc.maxExpected, backoff)
			}
		})
	}
}

func TestCallWithFailover(t *testing.T) {
	// Create a server that fails first time then succeeds
	attemptCount := 0
	failingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount++
		if attemptCount <= 1 {
			// First attempt fails
			http.Error(w, "Server Error", http.StatusInternalServerError)
			return
		}

		// Second attempt succeeds
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
	}))
	defer failingServer.Close()

	// Create a server that always succeeds
	workingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x2"}`))
	}))
	defer workingServer.Close()

	// Setup client with both endpoints
	config := Config{
		Endpoints: []string{failingServer.URL, workingServer.URL},
		Timeout:   5 * time.Second,
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Make a call that should first fail on the failing server, then succeed on working server
	var result string
	err = client.Call(context.Background(), &result, "eth_chainId")

	// Check results
	if err != nil {
		t.Fatalf("Expected successful failover, got error: %v", err)
	}

	// Should get result from working server
	if result != "0x2" {
		t.Fatalf("Expected result 0x2 from second server, got %s", result)
	}

	// Reset and check that it now uses the second endpoint directly
	attemptCount = 0
	result = ""
	err = client.Call(context.Background(), &result, "eth_chainId")

	if err != nil {
		t.Fatalf("Expected success on second call, got error: %v", err)
	}

	// Should still get result from working server
	if result != "0x2" {
		t.Fatalf("Expected result 0x2 from second server, got %s", result)
	}
}

func TestAggregateCall(t *testing.T) {
	// Create three servers with different responses
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
	}))
	defer server2.Close()

	server3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x2"}`))
	}))
	defer server3.Close()

	// Setup client with all three endpoints and aggregation enabled
	config := Config{
		Endpoints:         []string{server1.URL, server2.URL, server3.URL},
		Timeout:           5 * time.Second,
		EnableAggregation: true,
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Make a call that should aggregate results
	var result string
	err = client.Call(context.Background(), &result, "eth_chainId")

	// Check results
	if err != nil {
		t.Fatalf("Expected successful call with aggregation, got error: %v", err)
	}

	// Should get consensus result (0x1 from two servers)
	if result != "0x1" {
		t.Fatalf("Expected result 0x1 from consensus, got %s", result)
	}

	// Now create a scenario with timeout
	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x3"}`))
	}))
	defer slowServer.Close()

	// Fast server with minority result
	fastServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x4"}`))
	}))
	defer fastServer.Close()

	// Setup client with preference for first result
	config = Config{
		Endpoints:         []string{slowServer.URL, fastServer.URL},
		Timeout:           1 * time.Second,
		EnableAggregation: true,
		PreferFirstResult: true,
	}

	client, err = NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Make a call that should return the fast result without waiting for consensus
	result = ""
	err = client.Call(context.Background(), &result, "eth_chainId")

	// Check results
	if err != nil {
		t.Fatalf("Expected successful call with fast result, got error: %v", err)
	}

	// Should get fast result (0x4)
	if result != "0x4" {
		t.Fatalf("Expected result 0x4 from fast server, got %s", result)
	}

	// Test Case: All endpoints fail
	failingServer1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"internal error"}}`))
	}))
	defer failingServer1.Close()

	failingServer2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"internal error"}}`))
	}))
	defer failingServer2.Close()

	// Setup client with failing endpoints
	config = Config{
		Endpoints:         []string{failingServer1.URL, failingServer2.URL},
		Timeout:           1 * time.Second,
		EnableAggregation: true,
	}

	client, err = NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Make a call that should fail as all endpoints return errors
	result = ""
	err = client.Call(context.Background(), &result, "eth_chainId")

	// Check results - should fail with an error
	if err == nil {
		t.Fatal("Expected error when all endpoints fail, got success")
	}

	// Test Case: All endpoints timeout
	timeoutServer1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // Longer than the timeout
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x5"}`))
	}))
	defer timeoutServer1.Close()

	timeoutServer2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // Longer than the timeout
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x5"}`))
	}))
	defer timeoutServer2.Close()

	// Setup client with timeout endpoints
	config = Config{
		Endpoints:         []string{timeoutServer1.URL, timeoutServer2.URL},
		Timeout:           500 * time.Millisecond, // Set short timeout
		EnableAggregation: true,
	}

	client, err = NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Make a call that should timeout
	result = ""
	err = client.Call(context.Background(), &result, "eth_chainId")

	// Check results - should fail with timeout error
	if err == nil {
		t.Fatal("Expected timeout error, got success")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "timeout") &&
		!strings.Contains(strings.ToLower(err.Error()), "deadline") {
		t.Fatalf("Expected timeout error, got: %v", err)
	}

	// Test Case: Not enough endpoints for majority
	// One server returns success, one fails
	goodServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x6"}`))
	}))
	defer goodServer.Close()

	// Setup client with mixed endpoints
	config = Config{
		Endpoints:         []string{goodServer.URL, failingServer1.URL},
		Timeout:           1 * time.Second,
		EnableAggregation: true,
		PreferFirstResult: false, // We want to test majority logic
	}

	client, err = NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// Make a call that can't achieve majority consensus
	result = ""
	err = client.Call(context.Background(), &result, "eth_chainId")

	// Should still succeed with the one good result
	if err != nil {
		t.Fatalf("Expected success with single result when no majority, got error: %v", err)
	}

	// Should get the only available result
	if result != "0x6" {
		t.Fatalf("Expected result 0x6 from only successful endpoint, got %s", result)
	}
}

// TestConnectionRetryLogic 测试连接重试逻辑如何处理间歇性故障
func TestConnectionRetryLogic(t *testing.T) {
	// 创建一个模拟服务器，第一次和第三次调用时返回错误，其他调用正常
	callCount := 0
	intermittentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++

		// 第1次和第3次请求失败，模拟间歇性问题
		if callCount == 1 || callCount == 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		// 解析请求体，确定这是什么请求
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Logf("Failed to read request body: %v", err)
			http.Error(w, "Failed to read request", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		// 检查是否为 eth_chainId 请求（连接测试用）
		if strings.Contains(string(body), "eth_chainId") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
			return
		}

		// 默认响应
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"success"}`))
	}))
	defer intermittentServer.Close()

	// 创建客户端配置，设置多次重试
	config := Config{
		Endpoints:            []string{intermittentServer.URL},
		Timeout:              2 * time.Second,
		ConnectionTimeout:    5 * time.Second,
		MaxConnectionRetries: 5,
		RetryDelay:           100 * time.Millisecond, // 短暂延迟以加快测试
		MaxRetryDelay:        500 * time.Millisecond,
	}

	// 创建客户端
	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create client despite retry logic: %v", err)
	}
	defer client.Close()

	// 重置调用计数以测试 Call 方法的重试
	callCount = 0

	// 调用API，应该能够处理间歇性故障
	var result string
	err = client.Call(context.Background(), &result, "test_method")

	// 验证调用是否最终成功
	if err != nil {
		t.Fatalf("Call failed despite retry logic: %v", err)
	}

	// 验证是否实际进行了重试
	if callCount < 2 {
		t.Fatalf("Expected multiple call attempts, got %d", callCount)
	}
}

// TestAllEndpointsFailing 测试当所有端点都持续失败时的行为
func TestAllEndpointsFailing(t *testing.T) {
	// 创建两个总是失败的服务器
	failingServer1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"jsonrpc":"2.0","error":{"code":-32000,"message":"service unavailable"}}`))
	}))
	defer failingServer1.Close()

	failingServer2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"jsonrpc":"2.0","error":{"code":-32603,"message":"internal error"}}`))
	}))
	defer failingServer2.Close()

	// 配置使用这两个失败的端点
	config := Config{
		Endpoints:            []string{failingServer1.URL, failingServer2.URL},
		Timeout:              1 * time.Second,
		ConnectionTimeout:    2 * time.Second,
		MaxConnectionRetries: 2, // 限制重试次数以加快测试
		RetryDelay:           50 * time.Millisecond,
	}

	// 尝试创建客户端 - 应该失败，因为所有端点都失败了
	client, err := NewClient(config)
	if err == nil {
		client.Close()
		t.Fatal("Expected NewClient to fail when all endpoints fail, but it succeeded")
	}

	// 创建一个可以初始连接但随后所有调用都失败的客户端
	workingThenFailingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 获取和解析请求
		body, _ := io.ReadAll(r.Body)
		defer r.Body.Close()

		// 如果是 eth_chainId（连接测试用），返回成功
		if strings.Contains(string(body), "eth_chainId") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
			return
		}

		// 所有其他调用都失败
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"jsonrpc":"2.0","error":{"code":-32603,"message":"internal error"}}`))
	}))
	defer workingThenFailingServer.Close()

	// 配置使用这个可初始连接但随后调用失败的端点
	config = Config{
		Endpoints:            []string{workingThenFailingServer.URL},
		Timeout:              1 * time.Second,
		ConnectionTimeout:    2 * time.Second,
		MaxConnectionRetries: 2,
	}

	// 这次创建客户端应该成功（初始连接测试通过）
	client, err = NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create client despite endpoint passing connection test: %v", err)
	}
	defer client.Close()

	// 但随后的调用应该失败
	var result string
	err = client.Call(context.Background(), &result, "test_method")
	if err == nil {
		t.Fatal("Expected Call to fail when endpoint fails, but it succeeded")
	}
}

// TestConnectToEndpoint 直接测试 connectToEndpoint 方法
func TestConnectToEndpoint(t *testing.T) {
	// 使客户端结构体及其方法可导出以便测试
	// 由于这不可行，我们将使用公共方法间接测试

	// 创建一个模拟超时然后响应的服务器
	delayedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 等待时间超过连接超时
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
	}))
	defer delayedServer.Close()

	// 配置客户端使用短连接超时
	config := Config{
		Endpoints:            []string{delayedServer.URL},
		ConnectionTimeout:    200 * time.Millisecond, // 比服务器延迟短
		MaxConnectionRetries: 1,
	}

	// 尝试创建客户端 - 由于超时，应该失败
	_, err := NewClient(config)
	if err == nil {
		t.Fatal("Expected connection to fail due to timeout, but it succeeded")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "timeout") &&
		!strings.Contains(strings.ToLower(err.Error()), "deadline") {
		t.Fatalf("Expected timeout error, got: %v", err)
	}
}

// TestGetActiveEndpointWithRecovery 测试GetActiveEndpoint如何处理从不健康状态恢复的端点
func TestGetActiveEndpointWithRecovery(t *testing.T) {
	// 创建一个可自主控制健康状态的服务器
	healthStatus := true
	healthToggleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthStatus {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x1"}`))
	}))
	defer healthToggleServer.Close()

	// 配置使用这个可控健康状态的端点
	config := Config{
		Endpoints: []string{healthToggleServer.URL},
		Timeout:   1 * time.Second,
	}

	client, err := NewClient(config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}
	defer client.Close()

	// 初始状态下端点是健康的，调用应该成功
	var result string
	err = client.Call(context.Background(), &result, "eth_chainId")
	if err != nil {
		t.Fatalf("Initial call failed: %v", err)
	}

	// 改变服务器状态为不健康
	healthStatus = false

	// 调用应该失败，端点应该被标记为不健康
	err = client.Call(context.Background(), &result, "eth_chainId")
	if err == nil {
		t.Fatal("Expected call to fail when endpoint is unhealthy")
	}

	// 获取当前端点列表以检查状态
	// 由于我们不能直接访问client.endpoints，使用间接方法验证
	// 将服务器恢复健康
	healthStatus = true

	// 等待一段时间，确保超过退避时间
	time.Sleep(6 * time.Second) // 默认退避是5秒

	// 调用应该再次成功，说明端点已从不健康状态恢复
	err = client.Call(context.Background(), &result, "eth_chainId")
	if err != nil {
		t.Fatalf("Call failed after endpoint recovery: %v", err)
	}
}
