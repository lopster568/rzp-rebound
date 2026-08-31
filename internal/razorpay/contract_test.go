package razorpay_test

import (
	"context"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
)

// contractHarness is what a port implementation has to supply for the shared
// contract suite to drive it. Port covers the calls the API exposes;
// AttemptPayment covers the one step the API does not, because a real attempt
// happens in checkout rather than over the server API.
type contractHarness interface {
	Port() razorpay.Port
	AttemptPayment(ctx context.Context, orderID, cardNumber string) (razorpay.Payment, error)
	// FailureCard returns a card number documented to force the given
	// failure reason.
	FailureCard(t *testing.T, reason string) string
}

// contractHarnesses is the set of implementations the contract runs against.
// Phase 1 adds a live test-mode client here and phase 2 adds a replay client.
// Neither needs the assertions copied.
var contractHarnesses = map[string]func(t *testing.T) contractHarness{
	"fake": newFakeHarness,
}

type fakeHarness struct {
	fake *razorpay.Fake
}

func newFakeHarness(t *testing.T) contractHarness {
	t.Helper()

	f, err := razorpay.NewFake(razorpay.FakeOptions{
		Seed:  7,
		Clock: clock.NewFake(fakeStart),
	})
	if err != nil {
		t.Fatalf("NewFake: %v", err)
	}
	return &fakeHarness{fake: f}
}

func (h *fakeHarness) Port() razorpay.Port { return h.fake }

func (h *fakeHarness) AttemptPayment(ctx context.Context, orderID, cardNumber string) (razorpay.Payment, error) {
	return h.fake.AttemptPayment(ctx, orderID, cardNumber)
}

func (h *fakeHarness) FailureCard(t *testing.T, reason string) string {
	t.Helper()

	card, ok := h.fake.Cards().CardForErrorCode(reason)
	if !ok {
		t.Fatalf("no documented card forces %s", reason)
	}
	return card.Number
}

func TestPortContract_CreateOrderThenFetchOrderRoundTrips(t *testing.T) {
	for name, newHarness := range contractHarnesses {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			port := h.Port()
			ctx := context.Background()

			created, err := port.CreateOrder(ctx, razorpay.CreateOrderRequest{
				AmountPaise: 349900,
				Currency:    "INR",
				Receipt:     "rcpt_contract",
			})
			if err != nil {
				t.Fatalf("CreateOrder: %v", err)
			}
			if created.ID == "" {
				t.Fatal("CreateOrder returned an empty id")
			}

			fetched, err := port.FetchOrder(ctx, created.ID)
			if err != nil {
				t.Fatalf("FetchOrder(%s): %v", created.ID, err)
			}

			if fetched.ID != created.ID {
				t.Errorf("id = %q, want %q", fetched.ID, created.ID)
			}
			if fetched.AmountPaise != created.AmountPaise {
				t.Errorf("amount = %d paise, want %d", fetched.AmountPaise, created.AmountPaise)
			}
			if fetched.Currency != created.Currency {
				t.Errorf("currency = %q, want %q", fetched.Currency, created.Currency)
			}
		})
	}
}

func TestPortContract_FailedPaymentCarriesErrorCodeAndSource(t *testing.T) {
	for name, newHarness := range contractHarnesses {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			port := h.Port()
			ctx := context.Background()

			order, err := port.CreateOrder(ctx, razorpay.CreateOrderRequest{
				AmountPaise: 100000,
				Currency:    "INR",
				Receipt:     "rcpt_contract_failure",
			})
			if err != nil {
				t.Fatalf("CreateOrder: %v", err)
			}

			payment, err := h.AttemptPayment(ctx, order.ID, h.FailureCard(t, "insufficient_fund"))
			if err != nil {
				t.Fatalf("AttemptPayment: %v", err)
			}
			if payment.Status != razorpay.PaymentStatusFailed {
				t.Fatalf("status = %q, want %q", payment.Status, razorpay.PaymentStatusFailed)
			}

			// Downstream reads these fields. Nothing may have to parse a
			// human-readable description to find out what went wrong.
			if payment.ErrorCode == "" {
				t.Error("a failed payment carries no error_code")
			}
			if payment.ErrorSource == "" {
				t.Error("a failed payment carries no error_source")
			}
			if payment.ErrorStep == "" {
				t.Error("a failed payment carries no error_step")
			}

			fetched, err := port.FetchPayment(ctx, payment.ID)
			if err != nil {
				t.Fatalf("FetchPayment(%s): %v", payment.ID, err)
			}
			if fetched.ErrorCode != payment.ErrorCode {
				t.Errorf("FetchPayment error_code = %q, want %q", fetched.ErrorCode, payment.ErrorCode)
			}
			if fetched.ErrorSource != payment.ErrorSource {
				t.Errorf("FetchPayment error_source = %q, want %q", fetched.ErrorSource, payment.ErrorSource)
			}
		})
	}
}
