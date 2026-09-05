package detect

import (
	"context"
	"errors"
	"fmt"

	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
)

// FailedPaymentDetector finds orders that are still owed money and have a
// failed payment behind them.
//
// The walk is orders first, then the payments on each candidate order, because
// Razorpay has no endpoint that lists failed payments across orders. Only
// orders with amount_due above zero are asked about: an order Razorpay reports
// as fully paid is not a debt, however many attempts failed on the way to
// being paid, and asking about it would spend one request per historical
// order for an answer nothing acts on. Confirmed against live test mode on
// 2026-09-05, where order_TWu8G6mQV0Drc9 carries a failed payment and an
// amount_due of 0 because a second card captured it thirty six seconds later.
type FailedPaymentDetector struct {
	api OrderPaymentsAPI
	cfg Config
}

// NewFailedPaymentDetector builds a detector that reads through api.
func NewFailedPaymentDetector(api OrderPaymentsAPI, cfg Config) *FailedPaymentDetector {
	return &FailedPaymentDetector{api: api, cfg: cfg}
}

// Name is the audit-trail identifier, and it is the Source string so that a
// row naming the detector and a row naming the source cannot drift apart.
func (d *FailedPaymentDetector) Name() string { return string(riskitem.SourceFailedPayment) }

// Detect returns one item per unpaid order that has a failed payment on it.
//
// The amounts and the attempt count are the order's own, not the payment's:
// the debt is what Razorpay says is still owed on the order right now, and a
// partial capture can make the payment's amount disagree with it. Only the
// failure evidence and AtRiskSince come off the payment, and they come off the
// newest failed one, because that is the attempt whose error the customer last
// saw.
//
// A page that reads and a payments call that then fails returns the items
// already built along with the error, per the Detector contract.
//
// One order's payments call failing does not end the walk and does not lose the
// sweep's own error. Both were happening: the first payments failure returned
// early, so every order after it went unread and its debt was invisible to the
// run, and it returned that one error in place of sweepErr, so a truncated
// sweep stopped being reported at all. Every failure is collected with
// errors.Join and the walk carries on, which is the partial-sweep contract
// detect.go's sweep documents, applied to the second call this detector makes.
func (d *FailedPaymentDetector) Detect(ctx context.Context) ([]riskitem.RiskItem, error) {
	orders, sweepErr := sweep(ctx, d.cfg, d.api.ListOrders)
	errs := sweepErr

	var items []riskitem.RiskItem
	for _, order := range orders {
		if order.AmountDue <= 0 {
			continue
		}
		payments, err := d.api.ListPaymentsForOrder(ctx, order.ID)
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("read the payments on %s: %w", order.ID, err))
			continue
		}
		failed, ok := newestFailed(payments)
		if !ok {
			continue
		}
		items = append(items, itemFromFailedPayment(order, failed))
	}
	return items, errs
}

// newestFailed returns the failed payment with the largest created_at.
//
// Ties keep the first in the order Razorpay listed them, so the result does
// not depend on how a tie is broken. Razorpay's created_at is a Unix second,
// so two attempts within the same second do tie.
func newestFailed(payments []razorpay.Payment) (razorpay.Payment, bool) {
	var newest razorpay.Payment
	found := false
	for _, payment := range payments {
		if payment.Status != razorpay.PaymentStatusFailed {
			continue
		}
		if !found || payment.CreatedAt > newest.CreatedAt {
			newest = payment
			found = true
		}
	}
	return newest, found
}

// itemFromFailedPayment builds the sighting.
//
// The contact comes off the payment, and the order's documented notes fill in
// what the payment did not carry. A /v1/orders response has no email and no
// contact field at all, so before razorpay.Payment decoded the two the notes
// were the only honest source and this detector's items were almost always
// contactless. The payment is the better one where it exists: it is the
// address and the number the payer themselves entered at the checkout that
// failed, where a note is whatever the merchant chose to write on the order.
// Neither side is derived, and a payment that carried neither still leaves an
// item with an empty Customer rather than a guessed one. See
// customerFromFailedPayment and customerFromNotes.
func itemFromFailedPayment(order razorpay.Order, payment razorpay.Payment) riskitem.RiskItem {
	return riskitem.RiskItem{
		ID:              riskitem.NewID(riskitem.SourceFailedPayment, payment.ID),
		Source:          riskitem.SourceFailedPayment,
		SourceID:        payment.ID,
		RootOrderID:     order.ID,
		Customer:        customerFromFailedPayment(payment, order.Notes),
		AmountPaise:     order.AmountPaise,
		AmountPaidPaise: order.AmountPaid,
		AmountDuePaise:  order.AmountDue,
		Currency:        order.Currency,
		AtRiskSince:     payment.CreatedAt,
		Signal: riskitem.Signal{
			FailureCode:   payment.ErrorCode,
			FailureReason: payment.ErrorReason,
			FailureSource: payment.ErrorSource,
			FailureStep:   payment.ErrorStep,
			Method:        payment.Method,
			Attempts:      order.Attempts,
		},
	}
}
