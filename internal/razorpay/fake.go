package razorpay

import (
	"context"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/testcards"
)

// FakeOptions configures a Fake.
type FakeOptions struct {
	// Seed drives every generated identifier. Two fakes built with the same
	// seed produce the same ids for the same call sequence.
	Seed int64
	// Clock supplies created_at timestamps. Nil means the wall clock.
	Clock clock.Clock
	// Cards is the test-card table. Nil means testcards.Default.
	Cards *testcards.Table
}

// Fake is an in-memory Razorpay gateway. It holds orders, payments, and
// payment links in maps and decides a payment outcome from the card number
// using the shared testcards table.
type Fake struct {
	cards *testcards.Table
}

var _ Port = (*Fake)(nil)

// NewFake returns a Fake.
func NewFake(opts FakeOptions) (*Fake, error) { return nil, nil }

// CreateOrder records a new order in status created.
func (f *Fake) CreateOrder(ctx context.Context, req CreateOrderRequest) (Order, error) {
	return Order{}, nil
}

// FetchOrder returns a recorded order.
func (f *Fake) FetchOrder(ctx context.Context, orderID string) (Order, error) {
	return Order{}, nil
}

// ListPaymentsForOrder returns every attempt made on an order, oldest first.
func (f *Fake) ListPaymentsForOrder(ctx context.Context, orderID string) ([]Payment, error) {
	return nil, nil
}

// FetchPayment returns a recorded payment.
func (f *Fake) FetchPayment(ctx context.Context, paymentID string) (Payment, error) {
	return Payment{}, nil
}

// CreatePaymentLink records a new payment link.
func (f *Fake) CreatePaymentLink(ctx context.Context, req CreatePaymentLinkRequest) (PaymentLink, error) {
	return PaymentLink{}, nil
}

// ResendPaymentLinkNotification records that a resend was requested and
// reports that the call succeeded.
func (f *Fake) ResendPaymentLinkNotification(ctx context.Context, linkID, medium string) (NotifyReceipt, error) {
	return NotifyReceipt{}, nil
}

// AttemptPayment drives one payment attempt on an order with a card number.
// It is not part of Port: the live API has no equivalent, because a real
// attempt happens in checkout. Phase 1 supplies the same behaviour for the
// live client through the contract harness.
func (f *Fake) AttemptPayment(ctx context.Context, orderID, cardNumber string) (Payment, error) {
	return Payment{}, nil
}

// Cards returns the test-card table the fake decides outcomes from.
func (f *Fake) Cards() *testcards.Table { return nil }
