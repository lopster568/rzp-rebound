package telemetry

import (
	"context"
	"io"

	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
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
	// go to Writer through the stdout exporter instead.
	OTLPEndpoint string
	// Insecure sends OTLP over plaintext gRPC.
	Insecure bool
	// Writer is where the stdout exporter writes. Nil means os.Stdout.
	Writer io.Writer
}

// Provider owns a tracer provider and the pieces a test needs to check it.
type Provider struct {
	// TracerProvider is the configured provider.
	TracerProvider *sdktrace.TracerProvider
	// Resource is what every span is stamped with.
	Resource *resource.Resource
	// ExporterKind is ExporterOTLP or ExporterStdout.
	ExporterKind string
}

// NewTracerProvider builds a tracer provider from cfg.
func NewTracerProvider(ctx context.Context, cfg Config) (*Provider, error) { return nil, nil }

// Tracer returns a named tracer from the provider.
func (p *Provider) Tracer(name string) trace.Tracer { return nil }

// Shutdown flushes and stops the provider. It is safe to call more than once.
func (p *Provider) Shutdown(ctx context.Context) error { return nil }
