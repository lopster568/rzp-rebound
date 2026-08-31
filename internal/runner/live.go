package runner

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/config"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/telemetry"
)

// LiveRig is the set of things a subcommand needs to talk to test mode: a
// client for the documented API, an attempter for the checkout sequence, and a
// tracer provider pointed wherever the environment says.
//
// Both the client and the attempter get the same raw capture writer, so one
// run records every response it saw in one stream, in order.
type LiveRig struct {
	cfg       config.Config
	Client    *razorpay.Client
	Attempter *razorpay.Attempter
	Telemetry *telemetry.Provider
}

// NewLiveRig builds the rig. capture may be nil, and maxConcurrent zero means
// razorpay.DefaultMaxConcurrent.
//
// The cap is a parameter rather than a constant because a batch run makes two
// orders of magnitude more calls than a demo does, and PRD Q5 is open: no 429
// has ever been observed here, which bounds nothing. The batch runner passes a
// smaller number than the demo needs to.
func NewLiveRig(ctx context.Context, serviceName string, capture io.Writer, maxConcurrent int) (*LiveRig, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if err := cfg.RequireLiveAccess(); err != nil {
		return nil, err
	}

	name := serviceName
	if name == "" {
		name = cfg.ServiceName
	}
	provider, err := telemetry.NewTracerProvider(ctx, telemetry.Config{
		ServiceName:  name,
		OTLPEndpoint: cfg.OTLPEndpoint,
		// Test mode against a collector on a build machine over a private
		// network. Nothing here carries a credential: the exporter sends
		// spans, and no span attribute is allowed to hold one.
		Insecure: true,
	})
	if err != nil {
		return nil, err
	}

	client, err := razorpay.NewClient(razorpay.ClientOptions{
		KeyID:          cfg.RazorpayKeyID,
		KeySecret:      cfg.RazorpayKeySecret,
		TracerProvider: provider.TracerProvider,
		RawCapture:     capture,
		MaxConcurrent:  maxConcurrent,
	})
	if err != nil {
		_ = provider.Shutdown(ctx)
		return nil, err
	}

	attempter, err := razorpay.NewAttempter(razorpay.AttempterOptions{
		KeyID:          cfg.RazorpayKeyID,
		KeySecret:      cfg.RazorpayKeySecret,
		TracerProvider: provider.TracerProvider,
		RawCapture:     capture,
	})
	if err != nil {
		_ = provider.Shutdown(ctx)
		return nil, err
	}

	return &LiveRig{cfg: cfg, Client: client, Attempter: attempter, Telemetry: provider}, nil
}

// Close flushes the span exporter. A run that exits without this loses the
// trace it just produced, which is the whole artefact.
func (r *LiveRig) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return r.Telemetry.Shutdown(ctx)
}

// TraceURL returns the Jaeger link for one trace, or an empty string when
// none applies. cfg stays unexported on LiveRig, so this is the accessor a
// caller outside the package reaches it through.
func (r *LiveRig) TraceURL(traceID string) string {
	return r.cfg.TraceURL(traceID)
}

// Receipt is the caller-facing label for an order this project created, so a
// reviewer looking at the Razorpay dashboard can tell a demo order from
// anything else in the account.
func Receipt(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UTC().Unix())
}
