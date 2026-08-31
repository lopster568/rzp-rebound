package razorpay_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
)

// insufficientFundCard is the number testdata/magic_cards.json documents for
// the insufficient_fund reason.
const insufficientFundCard = "4100280000080001"

var fakeStart = time.Date(2026, 8, 31, 9, 0, 0, 0, time.UTC)

func newFake(t *testing.T, seed int64) *razorpay.Fake {
	t.Helper()

	f, err := razorpay.NewFake(razorpay.FakeOptions{
		Seed:  seed,
		Clock: clock.NewFake(fakeStart),
	})
	if err != nil {
		t.Fatalf("NewFake: %v", err)
	}
	return f
}

func createOrder(t *testing.T, f *razorpay.Fake, amountPaise int64) razorpay.Order {
	t.Helper()

	order, err := f.CreateOrder(context.Background(), razorpay.CreateOrderRequest{
		AmountPaise: amountPaise,
		Currency:    "INR",
		Receipt:     "rcpt_test",
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	return order
}

func TestFakeCreateOrderReturnsCreatedStatus(t *testing.T) {
	f := newFake(t, 1)

	order := createOrder(t, f, 250000)

	if order.ID == "" {
		t.Error("CreateOrder returned an order with no id")
	}
	if order.Status != razorpay.OrderStatusCreated {
		t.Errorf("status = %q, want %q", order.Status, razorpay.OrderStatusCreated)
	}
	if order.AmountPaise != 250000 {
		t.Errorf("amount = %d paise, want 250000", order.AmountPaise)
	}
	if order.AmountDue != 250000 {
		t.Errorf("amount_due = %d paise, want 250000", order.AmountDue)
	}
	if order.AmountPaid != 0 {
		t.Errorf("amount_paid = %d paise, want 0", order.AmountPaid)
	}
	if order.Attempts != 0 {
		t.Errorf("attempts = %d, want 0", order.Attempts)
	}
}

func TestFakeMagicCardInsufficientFundProducesFailedPaymentWithErrorCode(t *testing.T) {
	f := newFake(t, 2)
	order := createOrder(t, f, 100000)

	payment, err := f.AttemptPayment(context.Background(), order.ID, insufficientFundCard)
	if err != nil {
		t.Fatalf("AttemptPayment: %v", err)
	}

	if payment.Status != razorpay.PaymentStatusFailed {
		t.Errorf("status = %q, want %q", payment.Status, razorpay.PaymentStatusFailed)
	}
	if payment.ErrorCode != "insufficient_fund" {
		t.Errorf("error_code = %q, want %q", payment.ErrorCode, "insufficient_fund")
	}
	if payment.OrderID != order.ID {
		t.Errorf("order_id = %q, want %q", payment.OrderID, order.ID)
	}

	after, err := f.FetchOrder(context.Background(), order.ID)
	if err != nil {
		t.Fatalf("FetchOrder: %v", err)
	}
	if after.Status != razorpay.OrderStatusAttempted {
		t.Errorf("order status after a failed attempt = %q, want %q", after.Status, razorpay.OrderStatusAttempted)
	}
	if after.Attempts != 1 {
		t.Errorf("attempts = %d, want 1", after.Attempts)
	}
}

func TestFakeSuccessCardOnSecondAttemptMarksOrderPaid(t *testing.T) {
	f := newFake(t, 3)
	order := createOrder(t, f, 75000)
	ctx := context.Background()

	first, err := f.AttemptPayment(ctx, order.ID, insufficientFundCard)
	if err != nil {
		t.Fatalf("first AttemptPayment: %v", err)
	}
	if first.Status != razorpay.PaymentStatusFailed {
		t.Fatalf("first attempt status = %q, want %q", first.Status, razorpay.PaymentStatusFailed)
	}

	second, err := f.AttemptPayment(ctx, order.ID, f.Cards().SuccessCard())
	if err != nil {
		t.Fatalf("second AttemptPayment: %v", err)
	}

	if second.Status != razorpay.PaymentStatusCaptured {
		t.Errorf("second attempt status = %q, want %q", second.Status, razorpay.PaymentStatusCaptured)
	}
	if second.ErrorCode != "" {
		t.Errorf("a captured payment carries error_code %q", second.ErrorCode)
	}

	after, err := f.FetchOrder(ctx, order.ID)
	if err != nil {
		t.Fatalf("FetchOrder: %v", err)
	}
	if after.Status != razorpay.OrderStatusPaid {
		t.Errorf("order status = %q, want %q", after.Status, razorpay.OrderStatusPaid)
	}
	if after.AmountPaid != 75000 {
		t.Errorf("amount_paid = %d paise, want 75000", after.AmountPaid)
	}
	if after.AmountDue != 0 {
		t.Errorf("amount_due = %d paise, want 0", after.AmountDue)
	}

	payments, err := f.ListPaymentsForOrder(ctx, order.ID)
	if err != nil {
		t.Fatalf("ListPaymentsForOrder: %v", err)
	}
	if len(payments) != 2 {
		t.Fatalf("got %d payments, want 2", len(payments))
	}
	if payments[0].ID != first.ID || payments[1].ID != second.ID {
		t.Errorf("payments are not in attempt order: got %q then %q, want %q then %q",
			payments[0].ID, payments[1].ID, first.ID, second.ID)
	}
}

func TestFakeIsDeterministicForSameSeed(t *testing.T) {
	ctx := context.Background()

	run := func(seed int64) ([]razorpay.Order, []razorpay.Payment) {
		t.Helper()

		f := newFake(t, seed)
		var orders []razorpay.Order
		var payments []razorpay.Payment

		for _, card := range []string{insufficientFundCard, "4100280000060003", "4100280000020007"} {
			order := createOrder(t, f, 120000)
			payment, err := f.AttemptPayment(ctx, order.ID, card)
			if err != nil {
				t.Fatalf("AttemptPayment(%s): %v", card, err)
			}
			fetched, err := f.FetchOrder(ctx, order.ID)
			if err != nil {
				t.Fatalf("FetchOrder: %v", err)
			}
			orders = append(orders, fetched)
			payments = append(payments, payment)
		}
		return orders, payments
	}

	ordersA, paymentsA := run(42)
	ordersB, paymentsB := run(42)

	if !reflect.DeepEqual(ordersA, ordersB) {
		t.Errorf("same seed produced different orders:\n%+v\n%+v", ordersA, ordersB)
	}
	if !reflect.DeepEqual(paymentsA, paymentsB) {
		t.Errorf("same seed produced different payments:\n%+v\n%+v", paymentsA, paymentsB)
	}

	ordersC, _ := run(43)
	if reflect.DeepEqual(ordersA, ordersC) {
		t.Error("seeds 42 and 43 produced identical orders, so the seed is not doing anything")
	}
}

func TestFakeRejectsAttemptOnAlreadyPaidOrder(t *testing.T) {
	f := newFake(t, 5)
	order := createOrder(t, f, 60000)
	ctx := context.Background()

	if _, err := f.AttemptPayment(ctx, order.ID, f.Cards().SuccessCard()); err != nil {
		t.Fatalf("AttemptPayment: %v", err)
	}

	_, err := f.AttemptPayment(ctx, order.ID, f.Cards().SuccessCard())

	if !errors.Is(err, razorpay.ErrOrderAlreadyPaid) {
		t.Fatalf("second charge on a paid order returned %v, want %v", err, razorpay.ErrOrderAlreadyPaid)
	}

	payments, err := f.ListPaymentsForOrder(ctx, order.ID)
	if err != nil {
		t.Fatalf("ListPaymentsForOrder: %v", err)
	}
	if len(payments) != 1 {
		t.Errorf("got %d payments on a paid order, want 1", len(payments))
	}
}
