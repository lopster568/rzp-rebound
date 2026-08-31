package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/batch"
	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/poller"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/recovery"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Layers, per ADR-0004. No table sums across them.
const (
	LayerFake = "fake"
	LayerLive = "live"
)

// LiveMaxConcurrent caps requests in flight on the live layer.
//
// Two rather than the client's default of four. PRD Q5 is open: no 429 came
// back at 1.4 requests per second on 2026-08-31, which bounds nothing, and a
// batch run makes far more calls than that walk did. Two is the conservative
// setting until a deliberate ramp measures the limit.
const LiveMaxConcurrent = 2

// TracerName names the tracer the batch runner opens its own spans on.
const TracerName = "github.com/lopster568/rzp-recovery-agent/cmd/rzp/run"

// GatewayRig is one layer's gateway plus everything built on it.
type GatewayRig struct {
	Layer        string
	Port         razorpay.Port
	fake         *razorpay.Fake
	live         *LiveRig
	Tracer       trace.Tracer
	clock        clock.Clock
	wait         poller.WaitFunc
	pollInterval time.Duration
	pollMaxWait  time.Duration
	apiCalls     *int
	// outcomes is the per-order settle outcome for the live layer, computed
	// from the manifest at materialisation time. It never reaches an arm: it
	// is handed to recovery.NewLiveAttempter, which keeps it unexported.
	outcomes map[string]string
}

// PollInterval is the interval this rig's layer polls at.
func (r *GatewayRig) PollInterval() time.Duration { return r.pollInterval }

// PollMaxWait is how long this rig's layer waits for a poll to settle.
func (r *GatewayRig) PollMaxWait() time.Duration { return r.pollMaxWait }

// Wait is the wait function this rig's layer polls with.
func (r *GatewayRig) Wait() poller.WaitFunc { return r.wait }

func NewGatewayRig(ctx context.Context, layer string, file *BatchFile, c clock.Clock) (*GatewayRig, error) {
	calls := 0
	rig := &GatewayRig{
		Layer:    layer,
		clock:    c,
		apiCalls: &calls,
		outcomes: make(map[string]string),
		Tracer:   noop.NewTracerProvider().Tracer(TracerName),
	}

	if layer == LayerFake {
		fake, err := razorpay.NewFake(razorpay.FakeOptions{Seed: file.Seed, Clock: c})
		if err != nil {
			return nil, err
		}
		rig.fake = fake
		rig.Port = &countingPort{inner: fake, calls: &calls}
		// The fake settles instantly, so a poll never has to wait. The wait
		// still advances the clock: nothing else moves a fake clock, and a
		// poll run whose elapsed time never grows cannot reach its deadline.
		fakeClock, _ := c.(*clock.FakeClock)
		rig.wait = func(_ context.Context, d time.Duration) error {
			if fakeClock != nil {
				fakeClock.Advance(d)
			}
			return nil
		}
		rig.pollInterval = time.Millisecond
		rig.pollMaxWait = 3 * time.Millisecond
		return rig, nil
	}

	live, err := NewLiveRig(ctx, "", nil, LiveMaxConcurrent)
	if err != nil {
		return nil, err
	}
	rig.live = live
	rig.Port = &countingPort{inner: live.Client, calls: &calls}
	rig.Tracer = live.Telemetry.Tracer(TracerName)
	// The order is already at attempted when the run starts: materialise
	// drove its failure and that settled synchronously. So the poll is a read
	// of current state rather than a wait for one, and three reads is enough
	// to see it. A longer budget would spend a minute per arm waiting for a
	// transition that has already happened.
	rig.pollInterval = 500 * time.Millisecond
	rig.pollMaxWait = 1200 * time.Millisecond
	return rig, nil
}

func (r *GatewayRig) Close(ctx context.Context) {
	if r.live != nil {
		_ = r.live.Close()
	}
	_ = ctx
}

func (r *GatewayRig) Calls() int { return *r.apiCalls }

// Attempter returns the layer's adapter. Both keep the gateway's own settle
// schedule unexported, so an arm holding the Attempter interface cannot read
// how the world is going to decide.
//
// It has to be built after materialise, because materialise is what fills the
// live layer's outcome map.
func (r *GatewayRig) Attempter() recovery.Attempter {
	if r.Layer == LayerFake {
		return recovery.NewFakeAttempter(r.fake)
	}
	return recovery.NewLiveAttempter(r.live.Attempter, r.outcomes)
}

// Materialise creates this arm's own copy of every manifest order in the
// gateway, with its seeded failure history already on it.
//
// Each arm gets its own copy on purpose. Three arms sharing one set of orders
// would mean the first arm to recover one changes what the next two see, and
// the three-arm table would be measuring the running order rather than the
// arms.
func (r *GatewayRig) Materialise(ctx context.Context, orders []batch.Order) ([]Materialised, error) {
	out := make([]Materialised, 0, len(orders))

	for _, o := range orders {
		attempts := SeededAttempts(o)

		var gatewayID string
		var visible batch.AgentVisibleOrder

		if r.Layer == LayerFake {
			// The materialisation calls go straight at the fake rather than
			// through the counting port, so they are counted here. Both
			// layers have to count them the same way or the run total means
			// one thing on one layer and another on the other.
			//
			// SeedRecoversAfter below is deliberately not counted. It
			// configures the gateway; it does not talk to one.
			created, err := r.fake.CreateOrder(ctx, razorpay.CreateOrderRequest{
				AmountPaise: o.AmountPaise,
				Currency:    o.Currency,
				Receipt:     o.Receipt,
			})
			*r.apiCalls++
			if err != nil {
				return nil, fmt.Errorf("materialise %s: %w", o.OrderID, err)
			}
			gatewayID = created.ID
			for range attempts {
				if _, err := r.fake.SeedFailedPayment(ctx, gatewayID, o.SeededErrorCode); err != nil {
					return nil, fmt.Errorf("seed the failure on %s: %w", o.OrderID, err)
				}
				*r.apiCalls++
			}
			if RecoversOnRetry(o) {
				r.fake.SeedRecoversAfter(gatewayID, attempts)
			}
			visible = batch.AgentVisibleOrder{
				OrderID:     gatewayID,
				AmountPaise: created.AmountPaise,
				Currency:    created.Currency,
				Receipt:     created.Receipt,
			}
		} else {
			created, err := r.live.Client.CreateOrder(ctx, razorpay.CreateOrderRequest{
				AmountPaise: o.AmountPaise,
				Currency:    o.Currency,
				Receipt:     Receipt("rcpt_batch"),
				Notes:       map[string]string{"purpose": "phase-2 batch"},
			})
			*r.apiCalls++
			if err != nil {
				return nil, fmt.Errorf("materialise %s: %w", o.OrderID, err)
			}
			gatewayID = created.ID

			card := o.SeededCard
			if card == "" {
				// The risk-block reason has no card behind it (PRD Q2). The
				// card does not choose the outcome in test mode anyway: the
				// last checkout call does, and it is being told to fail.
				card = "4100280000080001"
			}
			for range attempts {
				if _, err := r.live.Attempter.Attempt(ctx, razorpay.AttemptRequest{
					OrderID:     gatewayID,
					AmountPaise: created.AmountPaise,
					CardNumber:  card,
					Outcome:     razorpay.AttemptFail,
				}); err != nil {
					return nil, fmt.Errorf("seed the failure on %s: %w", o.OrderID, err)
				}
				*r.apiCalls += 4
			}

			r.outcomes[gatewayID] = razorpay.AttemptFail
			if RecoversOnRetry(o) {
				r.outcomes[gatewayID] = razorpay.AttemptSucceed
			}
			visible = batch.AgentVisibleOrder{
				OrderID:     gatewayID,
				AmountPaise: created.AmountPaise,
				Currency:    created.Currency,
				Receipt:     created.Receipt,
			}
		}

		out = append(out, Materialised{ManifestID: o.OrderID, Visible: visible, Attempts: attempts})
	}
	return out, nil
}

// countingPort counts every call an arm makes through razorpay.Port, so the
// cost column in the report is a count of requests rather than an estimate.
//
// It wraps the port rather than the transport because the checkout sequence
// does not go through Port at all. Those four calls per attempt are reported
// by the Attempter adapter as AttemptRecord.GatewayCalls and added to the row
// total in the run loop, which is why this type has no exported adder.
type countingPort struct {
	inner razorpay.Port
	calls *int
}

func (c *countingPort) add(n int) { *c.calls += n }

func (c *countingPort) CreateOrder(ctx context.Context, req razorpay.CreateOrderRequest) (razorpay.Order, error) {
	c.add(1)
	return c.inner.CreateOrder(ctx, req)
}

func (c *countingPort) FetchOrder(ctx context.Context, orderID string) (razorpay.Order, error) {
	c.add(1)
	return c.inner.FetchOrder(ctx, orderID)
}

func (c *countingPort) ListPaymentsForOrder(ctx context.Context, orderID string) ([]razorpay.Payment, error) {
	c.add(1)
	return c.inner.ListPaymentsForOrder(ctx, orderID)
}

func (c *countingPort) FetchPayment(ctx context.Context, paymentID string) (razorpay.Payment, error) {
	c.add(1)
	return c.inner.FetchPayment(ctx, paymentID)
}

func (c *countingPort) CreatePaymentLink(ctx context.Context, req razorpay.CreatePaymentLinkRequest) (razorpay.PaymentLink, error) {
	c.add(1)
	return c.inner.CreatePaymentLink(ctx, req)
}

func (c *countingPort) ResendPaymentLinkNotification(ctx context.Context, linkID, medium string) (razorpay.NotifyReceipt, error) {
	c.add(1)
	return c.inner.ResendPaymentLinkNotification(ctx, linkID, medium)
}

var _ razorpay.Port = (*countingPort)(nil)
