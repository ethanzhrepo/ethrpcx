package client

import (
	"context"
	"fmt"
	"log"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.7.0"
	"go.opentelemetry.io/otel/trace"
)

var (
	tracerOnce     sync.Once
	tracerProvider *sdktrace.TracerProvider
	tracer         trace.Tracer
)

// Tracing configuration options
type TracingConfig struct {
	// Service name for the tracer
	ServiceName string

	// Environment (prod, staging, dev)
	Environment string

	// OTLP endpoint for exporting traces (e.g., "localhost:4317")
	OTLPEndpoint string

	// Whether to use insecure connection to OTLP endpoint
	Insecure bool

	// Sample rate (0.0 - 1.0)
	SampleRate float64
}

// Default tracing configuration
var DefaultTracingConfig = TracingConfig{
	ServiceName:  "ethrpcx",
	Environment:  "production",
	OTLPEndpoint: "localhost:4317",
	Insecure:     true,
	SampleRate:   0.1, // Sample 10% of traces by default
}

// InitTracing initializes OpenTelemetry tracing
func InitTracing(config TracingConfig) error {
	var err error

	tracerOnce.Do(func() {
		err = setupTracing(config)
	})

	return err
}

// setupTracing configures the OpenTelemetry tracer
func setupTracing(config TracingConfig) error {
	// Create a resource describing the service
	res, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String(config.ServiceName),
			attribute.String("environment", config.Environment),
		),
	)
	if err != nil {
		return fmt.Errorf("failed to create resource: %w", err)
	}

	// Configure OTLP exporter
	var exporter *otlptrace.Exporter
	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(config.OTLPEndpoint),
	}

	if config.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}

	exporter, err = otlptrace.New(
		context.Background(),
		otlptracegrpc.NewClient(opts...),
	)
	if err != nil {
		return fmt.Errorf("failed to create OTLP trace exporter: %w", err)
	}

	// Configure the trace provider with the exporter
	tracerProvider = sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(config.SampleRate)),
		sdktrace.WithBatcher(exporter),
	)

	// Set the global trace provider
	otel.SetTracerProvider(tracerProvider)

	// Set the global propagator
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Create a tracer
	tracer = otel.Tracer(config.ServiceName)

	log.Printf("OpenTelemetry tracing initialized with endpoint %s", config.OTLPEndpoint)
	return nil
}

// StartSpan starts a new span for the given operation
func StartSpan(ctx context.Context, operation string) (context.Context, trace.Span) {
	if tracer == nil {
		// Tracing not initialized, use a no-op tracer
		return ctx, trace.SpanFromContext(ctx)
	}
	return tracer.Start(ctx, operation)
}

// AddSpanEvent adds an event to the current span
func AddSpanEvent(ctx context.Context, name string, attributes ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	span.AddEvent(name, trace.WithAttributes(attributes...))
}

// AddSpanError adds an error to the current span
func AddSpanError(ctx context.Context, err error) {
	if err == nil {
		return
	}
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
}

// AddSpanAttributes adds attributes to the current span
func AddSpanAttributes(ctx context.Context, attributes ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	span.SetAttributes(attributes...)
}

// TracingEnabled returns true if tracing is enabled
func TracingEnabled() bool {
	return tracer != nil
}

// ShutdownTracing flushes any remaining spans and shuts down the tracer provider
func ShutdownTracing(ctx context.Context) error {
	if tracerProvider == nil {
		return nil
	}
	return tracerProvider.Shutdown(ctx)
}
