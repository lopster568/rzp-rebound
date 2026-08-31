package telemetry

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

// Exporter kinds a Provider can be built with.
const (
	ExporterOTLP   = "otlp"
	ExporterStdout = "stdout"
)

// DefaultServiceName is used when Config.ServiceName is empty.
const DefaultServiceName = "rzp-recovery-agent"

// Config describes the tracer provider to build.
type Config struct {
	// ServiceName goes into the resource as service.name.
	ServiceName string
	// ServiceVersion goes into the resource as service.version.
	ServiceVersion string
	// OTLPEndpoint is a host:port for the gRPC exporter. Empty means traces
	// go to Writer through the stdout exporter instead, so a run with no
	// collector in front of it still produces spans rather than errors.
	OTLPEndpoint string
	// Insecure sends OTLP over plaintext gRPC.
	Insecure bool
	// Writer is where the stdout exporter writes. Nil means os.Stdout.
	Writer io.Writer
}

// Provider owns a tracer provider and the pieces a caller needs to check it.
type Provider struct {
	// TracerProvider is the configured provider.
	TracerProvider *sdktrace.TracerProvider
	// Resource is what every span is stamped with.
	Resource *resource.Resource
	// ExporterKind is ExporterOTLP or ExporterStdout.
	ExporterKind string

	once sync.Once
	err  error
}

// NewTracerProvider builds a tracer provider from cfg. It does not install the
// provider globally: what holds it is the caller's business.
func NewTracerProvider(ctx context.Context, cfg Config) (*Provider, error) {
	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = DefaultServiceName
	}

	attrs := []attribute.KeyValue{semconv.ServiceName(serviceName)}
	if cfg.ServiceVersion != "" {
		attrs = append(attrs, semconv.ServiceVersion(cfg.ServiceVersion))
	}

	// WithFromEnv comes before WithAttributes, and the order is the whole
	// point: resource.New merges in order and the later option wins, so the
	// name this function was configured with has to be last.
	//
	// It used to be the other way round, which made OTEL_SERVICE_NAME override
	// an explicit Config.ServiceName and FR-TEL-2 false. Nothing showed until
	// phase 3, because nothing had ever set the variable: the first thing that
	// did was a run exporting it for Jaeger, and it turned
	// TestTracerProviderUsesServiceNameFromConfig red.
	//
	// The environment is still read, and it is still the way an operator names
	// a service: internal/config reads OTEL_SERVICE_NAME into Config.ServiceName
	// and every caller passes that through. What it no longer does is win
	// against a caller that asked for something else. WithFromEnv stays for the
	// other resource attributes it supplies.
	res, err := resource.New(ctx,
		resource.WithSchemaURL(semconv.SchemaURL),
		resource.WithTelemetrySDK(),
		resource.WithFromEnv(),
		resource.WithAttributes(attrs...),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: build resource: %w", err)
	}

	exporter, kind, err := newExporter(ctx, cfg)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(sdktrace.NewBatchSpanProcessor(exporter)),
		sdktrace.WithResource(res),
	)

	return &Provider{TracerProvider: tp, Resource: res, ExporterKind: kind}, nil
}

func newExporter(ctx context.Context, cfg Config) (sdktrace.SpanExporter, string, error) {
	if cfg.OTLPEndpoint == "" {
		w := cfg.Writer
		if w == nil {
			w = os.Stdout
		}
		exp, err := stdouttrace.New(stdouttrace.WithWriter(w))
		if err != nil {
			return nil, "", fmt.Errorf("telemetry: build stdout exporter: %w", err)
		}
		return exp, ExporterStdout, nil
	}

	opts := []otlptracegrpc.Option{otlptracegrpc.WithEndpoint(cfg.OTLPEndpoint)}
	if cfg.Insecure {
		opts = append(opts, otlptracegrpc.WithInsecure())
	}
	exp, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, "", fmt.Errorf("telemetry: build otlp exporter for %s: %w", cfg.OTLPEndpoint, err)
	}
	return exp, ExporterOTLP, nil
}

// Tracer returns a named tracer from the provider.
func (p *Provider) Tracer(name string) trace.Tracer { return p.TracerProvider.Tracer(name) }

// Shutdown flushes and stops the provider. Callers defer it and error paths
// call it too, so it runs once and returns the same answer after that.
func (p *Provider) Shutdown(ctx context.Context) error {
	p.once.Do(func() { p.err = p.TracerProvider.Shutdown(ctx) })
	return p.err
}
