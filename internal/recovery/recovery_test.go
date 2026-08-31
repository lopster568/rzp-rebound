package recovery_test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/audit"
	"github.com/lopster568/rzp-recovery-agent/internal/batch"
	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/poller"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/recovery"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// insufficientFundCard is the number testdata/magic_cards.json documents for
// the insufficient_fund reason.
const insufficientFundCard = "4100280000080001"

var recoveryStart = time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)

// spyPort writes down every port call in order, so a test can assert what
// happened after what. The gateway behind it is the real fake.
type spyPort struct {
	inner razorpay.Port

	mu    sync.Mutex
	calls []string
}

func (s *spyPort) record(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, name)
}

func (s *spyPort) recorded() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func (s *spyPort) CreateOrder(ctx context.Context, req razorpay.CreateOrderRequest) (razorpay.Order, error) {
	s.record("CreateOrder")
	return s.inner.CreateOrder(ctx, req)
}

func (s *spyPort) FetchOrder(ctx context.Context, orderID string) (razorpay.Order, error) {
	s.record("FetchOrder")
	return s.inner.FetchOrder(ctx, orderID)
}

func (s *spyPort) ListPaymentsForOrder(ctx context.Context, orderID string) ([]razorpay.Payment, error) {
	s.record("ListPaymentsForOrder")
	return s.inner.ListPaymentsForOrder(ctx, orderID)
}

func (s *spyPort) FetchPayment(ctx context.Context, paymentID string) (razorpay.Payment, error) {
	s.record("FetchPayment")
	return s.inner.FetchPayment(ctx, paymentID)
}

func (s *spyPort) CreatePaymentLink(ctx context.Context, req razorpay.CreatePaymentLinkRequest) (razorpay.PaymentLink, error) {
	s.record("CreatePaymentLink")
	return s.inner.CreatePaymentLink(ctx, req)
}

func (s *spyPort) ResendPaymentLinkNotification(ctx context.Context, linkID, medium string) (razorpay.NotifyReceipt, error) {
	s.record("ResendPaymentLinkNotification")
	return s.inner.ResendPaymentLinkNotification(ctx, linkID, medium)
}

// rig is one order sitting at attempted with a failed payment under it, plus
// everything needed to run it through the orchestrator offline.
type rig struct {
	fake    *razorpay.Fake
	spy     *spyPort
	order   razorpay.Order
	ledger  *bytes.Buffer
	spans   *tracetest.SpanRecorder
	clock   *clock.FakeClock
	visible batch.AgentVisibleOrder
}

func newRig(t *testing.T) *rig {
	t.Helper()

	c := clock.NewFake(recoveryStart)
	f, err := razorpay.NewFake(razorpay.FakeOptions{Seed: 21, Clock: c})
	if err != nil {
		t.Fatalf("NewFake: %v", err)
	}

	ctx := context.Background()
	order, err := f.CreateOrder(ctx, razorpay.CreateOrderRequest{
		AmountPaise: 145000,
		Currency:    "INR",
		Receipt:     "rcpt_recovery",
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if _, err := f.AttemptPayment(ctx, order.ID, insufficientFundCard); err != nil {
		t.Fatalf("AttemptPayment: %v", err)
	}

	return &rig{
		fake:   f,
		spy:    &spyPort{inner: f},
		order:  order,
		ledger: &bytes.Buffer{},
		spans:  tracetest.NewSpanRecorder(),
		clock:  c,
		visible: batch.AgentVisibleOrder{
			OrderID:     order.ID,
			AmountPaise: order.AmountPaise,
			Currency:    order.Currency,
			Receipt:     order.Receipt,
		},
	}
}

func (r *rig) orchestrator(t *testing.T, action recovery.ActionFunc) *recovery.Orchestrator {
	t.Helper()

	p, err := poller.New(poller.Options{
		Port:       r.spy,
		Clock:      r.clock,
		Wait:       func(_ context.Context, d time.Duration) error { r.clock.Advance(d); return nil },
		Interval:   100 * time.Millisecond,
		MaxBackoff: 200 * time.Millisecond,
		MaxWait:    300 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("poller.New: %v", err)
	}

	rec, err := audit.NewRecorder(audit.Options{Writer: r.ledger, Clock: r.clock})
	if err != nil {
		t.Fatalf("audit.NewRecorder: %v", err)
	}

	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(r.spans))
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Errorf("tracer provider shutdown: %v", err)
		}
	})

	o, err := recovery.New(recovery.Options{
		Port:     r.spy,
		Poller:   p,
		Recorder: rec,
		Action:   action,
		Tracer:   tp.Tracer("recovery_test"),
		Clock:    r.clock,
	})
	if err != nil {
		t.Fatalf("recovery.New: %v", err)
	}
	return o
}

func (r *rig) rows(t *testing.T) []audit.Record {
	t.Helper()

	var out []audit.Record
	for _, line := range strings.Split(strings.TrimSpace(r.ledger.String()), "\n") {
		if line == "" {
			continue
		}
		var rec audit.Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("ledger line is not valid JSON: %v (%q)", err, line)
		}
		out = append(out, rec)
	}
	return out
}

func TestOrchestratorClassifiesThenRecordsAuditEventPerOrder(t *testing.T) {
	r := newRig(t)
	o := r.orchestrator(t, nil)

	outcome, err := o.ProcessOrder(context.Background(), r.visible)
	if err != nil {
		t.Fatalf("ProcessOrder: %v", err)
	}

	if outcome.OrderID != r.order.ID {
		t.Errorf("outcome order id = %q, want %q", outcome.OrderID, r.order.ID)
	}
	if outcome.Class != classify.RetryEligible {
		t.Errorf("class = %q, want %q", outcome.Class, classify.RetryEligible)
	}

	rows := r.rows(t)
	if len(rows) == 0 {
		t.Fatal("processing an order wrote no audit row")
	}

	var classified *audit.Record
	for i := range rows {
		if rows[i].Kind == audit.KindClassified {
			classified = &rows[i]
			break
		}
	}
	if classified == nil {
		t.Fatalf("no %s row in the ledger, got kinds %v", audit.KindClassified, kindsOf(rows))
	}
	if classified.OrderID != r.order.ID {
		t.Errorf("%s row order id = %q, want %q", audit.KindClassified, classified.OrderID, r.order.ID)
	}
	if classified.Class != classify.RetryEligible.String() {
		t.Errorf("%s row class = %q, want %q", audit.KindClassified, classified.Class, classify.RetryEligible)
	}

	// The orchestrator opened a span, which is what the recorder's span sink
	// writes onto and what a ledger row's trace id points at.
	if classified.TraceID == "" {
		t.Error("the audit row carries no trace id, so nothing joins it to a trace")
	}

	if !slices.Contains(kindsOf(rows), audit.KindOutcomeObserved) {
		t.Errorf("no %s row in the ledger, got kinds %v", audit.KindOutcomeObserved, kindsOf(rows))
	}
	if len(outcome.Events) != len(rows) {
		t.Errorf("outcome carries %d event(s) but the ledger holds %d row(s)", len(outcome.Events), len(rows))
	}
}

func TestOrchestratorRefetchesOrderStateForOutcomeRatherThanTrustingAction(t *testing.T) {
	r := newRig(t)

	// An action that reports success while the gateway still says attempted.
	// This is the disagreement the project exists to measure, so the outcome
	// has to come from the gateway.
	action := func(_ context.Context, _ batch.AgentVisibleOrder, _ classify.Class) (recovery.ActionResult, error) {
		r.spy.record("action")
		return recovery.ActionResult{
			Kind:             recovery.ActionRetrySameInstrument,
			ClaimedRecovered: true,
		}, nil
	}

	o := r.orchestrator(t, action)

	outcome, err := o.ProcessOrder(context.Background(), r.visible)
	if err != nil {
		t.Fatalf("ProcessOrder: %v", err)
	}

	if outcome.Recovered {
		t.Error("the outcome says recovered, but the gateway never moved the order to paid")
	}
	if !outcome.ClaimedRecovered {
		t.Error("the outcome dropped what the action claimed, so the disagreement is invisible")
	}
	if outcome.FinalOrderStatus != razorpay.OrderStatusAttempted {
		t.Errorf("final order status = %q, want %q", outcome.FinalOrderStatus, razorpay.OrderStatusAttempted)
	}
	if outcome.ActionKind != recovery.ActionRetrySameInstrument {
		t.Errorf("action kind = %q, want %q", outcome.ActionKind, recovery.ActionRetrySameInstrument)
	}

	calls := r.spy.recorded()
	actionAt := slices.Index(calls, "action")
	if actionAt < 0 {
		t.Fatalf("the action never ran: calls were %v", calls)
	}
	fetchAfter := slices.Index(calls[actionAt:], "FetchOrder")
	if fetchAfter < 0 {
		t.Errorf("no FetchOrder after the action, so the outcome could only have come from the action result: calls were %v", calls)
	}

	// And the gateway agrees with the outcome, which is the whole point.
	final, err := r.fake.FetchOrder(context.Background(), r.order.ID)
	if err != nil {
		t.Fatalf("FetchOrder: %v", err)
	}
	if final.Status != razorpay.OrderStatusAttempted {
		t.Errorf("gateway order status = %q, want %q", final.Status, razorpay.OrderStatusAttempted)
	}
}

func kindsOf(rows []audit.Record) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Kind)
	}
	return out
}
