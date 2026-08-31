package telemetry_test

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/telemetry"
)

// syncBuffer is a bytes.Buffer the exporter can write to from its own
// goroutine while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func newProvider(t *testing.T, cfg telemetry.Config) *telemetry.Provider {
	t.Helper()

	p, err := telemetry.NewTracerProvider(t.Context(), cfg)
	if err != nil {
		t.Fatalf("NewTracerProvider: %v", err)
	}
	if p == nil {
		t.Fatal("NewTracerProvider returned a nil provider and no error")
	}
	return p
}

func TestNewTracerProviderShutsDownCleanly(t *testing.T) {
	p := newProvider(t, telemetry.Config{
		ServiceName: "rzp-recovery-agent",
		Writer:      &syncBuffer{},
	})

	if p.TracerProvider == nil {
		t.Fatal("Provider.TracerProvider is nil")
	}
	if p.Tracer("phase-0") == nil {
		t.Error("Tracer returned nil")
	}

	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
	// A second shutdown happens whenever a caller defers it and an error path
	// also calls it. It must not fail.
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("second Shutdown: %v", err)
	}
}

func TestTracerProviderUsesServiceNameFromConfig(t *testing.T) {
	const serviceName = "rzp-recovery-agent-phase-0"

	p := newProvider(t, telemetry.Config{
		ServiceName:    serviceName,
		ServiceVersion: "0.0.1",
		Writer:         &syncBuffer{},
	})
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })

	if p.Resource == nil {
		t.Fatal("Provider.Resource is nil")
	}

	var got string
	for _, attr := range p.Resource.Attributes() {
		if string(attr.Key) == "service.name" {
			got = attr.Value.AsString()
		}
	}

	if got != serviceName {
		t.Errorf("resource service.name = %q, want %q", got, serviceName)
	}
}

func TestStdoutExporterIsUsedWhenOTLPEndpointIsUnset(t *testing.T) {
	out := &syncBuffer{}

	p := newProvider(t, telemetry.Config{
		ServiceName: "rzp-recovery-agent",
		Writer:      out,
	})
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })

	if p.ExporterKind != telemetry.ExporterStdout {
		t.Errorf("exporter kind = %q, want %q", p.ExporterKind, telemetry.ExporterStdout)
	}

	_, span := p.Tracer("phase-0").Start(t.Context(), "classify-failure")
	span.End()

	if err := p.TracerProvider.ForceFlush(t.Context()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	if written := out.String(); !strings.Contains(written, "classify-failure") {
		t.Errorf("the span did not reach the configured writer, which held:\n%s", written)
	}
}
