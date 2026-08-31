package poller_test

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/poller"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
)

// insufficientFundCard is the number testdata/magic_cards.json documents for
// the insufficient_fund reason.
const insufficientFundCard = "4100280000080001"

var pollStart = time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)

// recordingWait writes down every backoff the poller asked for and moves the
// fake clock by it. Nothing in this file sleeps.
type recordingWait struct {
	mu    sync.Mutex
	waits []time.Duration
	clock *clock.FakeClock
}

func (w *recordingWait) Wait(_ context.Context, d time.Duration) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.waits = append(w.waits, d)
	w.clock.Advance(d)
	return nil
}

func (w *recordingWait) recorded() []time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]time.Duration(nil), w.waits...)
}

// gateway builds a fake gateway, a fake clock, and a wait function that share
// the same instant.
func gateway(t *testing.T, seed int64) (*razorpay.Fake, *recordingWait) {
	t.Helper()

	c := clock.NewFake(pollStart)
	f, err := razorpay.NewFake(razorpay.FakeOptions{Seed: seed, Clock: c})
	if err != nil {
		t.Fatalf("NewFake: %v", err)
	}
	return f, &recordingWait{clock: c}
}

func newOrder(t *testing.T, f *razorpay.Fake, amountPaise int64) razorpay.Order {
	t.Helper()

	order, err := f.CreateOrder(context.Background(), razorpay.CreateOrderRequest{
		AmountPaise: amountPaise,
		Currency:    "INR",
		Receipt:     "rcpt_poller",
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	return order
}

func newPoller(t *testing.T, f *razorpay.Fake, w *recordingWait, mutate func(*poller.Options)) *poller.Poller {
	t.Helper()

	opts := poller.Options{
		Port:       f,
		Clock:      w.clock,
		Wait:       w.Wait,
		Interval:   100 * time.Millisecond,
		MaxBackoff: 400 * time.Millisecond,
		MaxWait:    time.Second,
	}
	if mutate != nil {
		mutate(&opts)
	}

	p, err := poller.New(opts)
	if err != nil {
		t.Fatalf("poller.New: %v", err)
	}
	return p
}

func TestPollerReturnsOnTerminalOrderState(t *testing.T) {
	f, w := gateway(t, 1)
	ctx := context.Background()
	order := newOrder(t, f, 120000)

	if _, err := f.AttemptPayment(ctx, order.ID, f.Cards().SuccessCard()); err != nil {
		t.Fatalf("AttemptPayment: %v", err)
	}

	p := newPoller(t, f, w, nil)

	res, err := p.PollUntilTerminal(ctx, order.ID)
	if err != nil {
		t.Fatalf("PollUntilTerminal: %v", err)
	}

	if !res.Terminal {
		t.Error("a paid order did not end the poll")
	}
	if res.TimedOut {
		t.Error("a paid order reported a timeout")
	}
	if res.Order.Status != razorpay.OrderStatusPaid {
		t.Errorf("order status = %q, want %q", res.Order.Status, razorpay.OrderStatusPaid)
	}
	if res.Order.ID != order.ID {
		t.Errorf("order id = %q, want %q", res.Order.ID, order.ID)
	}
	if res.Polls != 1 {
		t.Errorf("polled %d times for an already terminal order, want 1", res.Polls)
	}
	if got := w.recorded(); len(got) != 0 {
		t.Errorf("backed off %v before reading a terminal order, want no wait", got)
	}
}

func TestPollerTimesOutAndReportsLastKnownState(t *testing.T) {
	f, w := gateway(t, 2)
	ctx := context.Background()
	order := newOrder(t, f, 90000)

	if _, err := f.AttemptPayment(ctx, order.ID, insufficientFundCard); err != nil {
		t.Fatalf("AttemptPayment: %v", err)
	}

	p := newPoller(t, f, w, nil)

	res, err := p.PollUntilTerminal(ctx, order.ID)
	if err != nil {
		t.Fatalf("PollUntilTerminal: %v", err)
	}

	if !res.TimedOut {
		t.Error("an order that never settled did not report a timeout")
	}
	if res.Terminal {
		t.Error("an order that never settled reported a terminal state")
	}

	// The point of the timeout path: the caller still gets what was seen.
	if res.Order.ID != order.ID {
		t.Errorf("timed-out result carries order id %q, want %q", res.Order.ID, order.ID)
	}
	if res.Order.Status != razorpay.OrderStatusAttempted {
		t.Errorf("last known order status = %q, want %q", res.Order.Status, razorpay.OrderStatusAttempted)
	}
	if len(res.Payments) != 1 {
		t.Fatalf("timed-out result carries %d payment(s), want 1", len(res.Payments))
	}
	if res.Payments[0].Status != razorpay.PaymentStatusFailed {
		t.Errorf("last known payment status = %q, want %q", res.Payments[0].Status, razorpay.PaymentStatusFailed)
	}
	if res.Polls < 2 {
		t.Errorf("polled %d times before the timeout, want more than one", res.Polls)
	}
}

func TestPollerUsesInjectedClockForBackoff(t *testing.T) {
	f, w := gateway(t, 3)
	ctx := context.Background()
	order := newOrder(t, f, 60000)

	if _, err := f.AttemptPayment(ctx, order.ID, insufficientFundCard); err != nil {
		t.Fatalf("AttemptPayment: %v", err)
	}

	p := newPoller(t, f, w, nil)

	res, err := p.PollUntilTerminal(ctx, order.ID)
	if err != nil {
		t.Fatalf("PollUntilTerminal: %v", err)
	}

	// Interval 100ms, doubling, capped at 400ms, with a 1s budget. The fourth
	// wait would take the run past MaxWait, so the poller stops instead.
	//
	// This list is the assertion that the poller reads the injected clock. The
	// run stops after exactly three waits because elapsed time, read from that
	// clock, plus the next backoff crosses the budget. A poller reading the
	// wall clock would see almost no elapsed time and keep going.
	want := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
	}
	if got := w.recorded(); !reflect.DeepEqual(got, want) {
		t.Errorf("backoff waits = %v, want %v", got, want)
	}
	if res.Waited != 700*time.Millisecond {
		t.Errorf("Result.Waited = %v, want 700ms", res.Waited)
	}

	if !res.TimedOut {
		t.Error("the run did not end on the wait budget, so the backoff never reached it")
	}
}

func TestPollerDetectsFailedPaymentOnOrderStillAttempted(t *testing.T) {
	f, w := gateway(t, 4)
	ctx := context.Background()
	order := newOrder(t, f, 150000)

	if _, err := f.AttemptPayment(ctx, order.ID, insufficientFundCard); err != nil {
		t.Fatalf("AttemptPayment: %v", err)
	}

	p := newPoller(t, f, w, nil)

	res, err := p.PollUntilTerminal(ctx, order.ID)
	if err != nil {
		t.Fatalf("PollUntilTerminal: %v", err)
	}

	if res.FailedPayment == nil {
		t.Fatal("an order at attempted with a failed payment under it reported no failed payment")
	}
	if res.FailedPayment.ErrorReason != "insufficient_fund" {
		t.Errorf("failed payment error_reason = %q, want insufficient_fund", res.FailedPayment.ErrorReason)
	}
	if res.FailedPayment.OrderID != order.ID {
		t.Errorf("failed payment order_id = %q, want %q", res.FailedPayment.OrderID, order.ID)
	}

	// A failed payment is not the end of the story. Another attempt can still
	// pay the order, so attempted is not terminal.
	if res.Terminal {
		t.Error("an order at attempted was treated as terminal")
	}
	if poller.IsTerminal(razorpay.OrderStatusAttempted) {
		t.Error("IsTerminal says attempted ends a poll")
	}
	if !poller.IsTerminal(razorpay.OrderStatusPaid) {
		t.Error("IsTerminal says paid does not end a poll")
	}
}
