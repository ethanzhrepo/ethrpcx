package metrics

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	registry              = prometheus.NewRegistry()
	defaultRegisterer     = prometheus.WrapRegistererWithPrefix("ethrpcx_", registry)
	defaultRegistererOnce sync.Once

	// RPC call metrics
	rpcRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "rpc_requests_total",
			Help: "Total number of RPC requests",
		},
		[]string{"method", "endpoint", "status"},
	)

	rpcRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "rpc_request_duration_seconds",
			Help:    "Duration of RPC requests in seconds",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15), // from 1ms to 16s
		},
		[]string{"method", "endpoint"},
	)

	// Connection metrics
	connectionAttempts = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "connection_attempts_total",
			Help: "Total number of connection attempts",
		},
		[]string{"endpoint", "status"},
	)

	activeConnections = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "active_connections",
			Help: "Number of active connections per endpoint",
		},
		[]string{"endpoint"},
	)

	// Subscription metrics
	activeSubscriptions = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "active_subscriptions",
			Help: "Number of active subscriptions",
		},
		[]string{"type"},
	)

	subscriptionErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "subscription_errors_total",
			Help: "Total number of subscription errors",
		},
		[]string{"type"},
	)

	subscriptionReconnects = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "subscription_reconnects_total",
			Help: "Total number of subscription reconnects",
		},
		[]string{"type"},
	)

	// Aggregation metrics
	aggregationRequests = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "aggregation_requests_total",
			Help: "Total number of aggregated requests",
		},
		[]string{"method", "result"},
	)

	aggregationDiscrepancies = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "aggregation_discrepancies_total",
			Help: "Total number of discrepancies in aggregated results",
		},
		[]string{"method"},
	)
)

// Initialize registers the metrics with Prometheus
func Initialize() {
	defaultRegistererOnce.Do(func() {
		// Register all metrics
		defaultRegisterer.MustRegister(rpcRequests)
		defaultRegisterer.MustRegister(rpcRequestDuration)
		defaultRegisterer.MustRegister(connectionAttempts)
		defaultRegisterer.MustRegister(activeConnections)
		defaultRegisterer.MustRegister(activeSubscriptions)
		defaultRegisterer.MustRegister(subscriptionErrors)
		defaultRegisterer.MustRegister(subscriptionReconnects)
		defaultRegisterer.MustRegister(aggregationRequests)
		defaultRegisterer.MustRegister(aggregationDiscrepancies)
	})
}

// GetRegistry returns the metrics registry
func GetRegistry() *prometheus.Registry {
	return registry
}

// RecordRPCRequest records an RPC request metric
func RecordRPCRequest(method, endpoint, status string) {
	rpcRequests.WithLabelValues(method, endpoint, status).Inc()
}

// RecordRPCRequestDuration records the duration of an RPC request
func RecordRPCRequestDuration(method, endpoint string, duration time.Duration) {
	rpcRequestDuration.WithLabelValues(method, endpoint).Observe(duration.Seconds())
}

// RecordConnectionAttempt records a connection attempt
func RecordConnectionAttempt(endpoint, status string) {
	connectionAttempts.WithLabelValues(endpoint, status).Inc()
}

// UpdateActiveConnections updates the active connections gauge
func UpdateActiveConnections(endpoint string, count int) {
	activeConnections.WithLabelValues(endpoint).Set(float64(count))
}

// UpdateActiveSubscriptions updates the active subscriptions gauge
func UpdateActiveSubscriptions(subType string, count int) {
	activeSubscriptions.WithLabelValues(subType).Set(float64(count))
}

// RecordSubscriptionError records a subscription error
func RecordSubscriptionError(subType string) {
	subscriptionErrors.WithLabelValues(subType).Inc()
}

// RecordSubscriptionReconnect records a subscription reconnect
func RecordSubscriptionReconnect(subType string) {
	subscriptionReconnects.WithLabelValues(subType).Inc()
}

// RecordAggregationRequest records an aggregation request
func RecordAggregationRequest(method, result string) {
	aggregationRequests.WithLabelValues(method, result).Inc()
}

// RecordAggregationDiscrepancy records a discrepancy in aggregated results
func RecordAggregationDiscrepancy(method string) {
	aggregationDiscrepancies.WithLabelValues(method).Inc()
}
