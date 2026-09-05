package detect

import (
	"context"

	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
)

// UnpaidOrderDetector finds orders nobody ever tried to pay.
//
// The discriminator is the attempt counter, not the status literal. Razorpay
// documents an attempted status and the live account has never been observed
// holding an order in it: the 2026-09-05 walk found orders at created and at
// paid and nothing in between, because an order moves to paid the moment a
// payment captures and the window in which it reads attempted is the length of
// one checkout. An order at created with attempts above zero has been tried
// and failed, and it belongs to FailedPaymentDetector, which can say what the
// failure was. This detector takes only attempts of exactly zero, so that the
// two never both claim the same order for opposite reasons.
//
// Zero attempts is an abandonment rather than a failure. There is no error to
// classify, nothing to tell the customer about a decline, and the only lawful
// moves are a payment link and a nudge.
type UnpaidOrderDetector struct {
	api OrderLister
	cfg Config
}

// NewUnpaidOrderDetector builds a detector that reads through api.
func NewUnpaidOrderDetector(api OrderLister, cfg Config) *UnpaidOrderDetector {
	return &UnpaidOrderDetector{api: api, cfg: cfg}
}

// Name is the audit-trail identifier, and it is the Source string.
func (d *UnpaidOrderDetector) Name() string { return string(riskitem.SourceUnpaidOrder) }

// Detect returns one item per order at created with no attempts and money
// still due.
//
// Customer is empty on every item this detector produces unless the merchant
// wrote a contact into the order notes: /v1/orders carries no email and no
// contact field at all, confirmed on 2026-09-05. An empty Customer is the
// point rather than a gap. riskitem.Customer.HasContactChannel is false for
// it, the policy gate escalates it to a person, and nothing anywhere fills the
// address in from a similar order or a receipt.
func (d *UnpaidOrderDetector) Detect(ctx context.Context) ([]riskitem.RiskItem, error) {
	orders, sweepErr := sweep(ctx, d.cfg, d.api.ListOrders)

	var items []riskitem.RiskItem
	for _, order := range orders {
		if order.Status != razorpay.OrderStatusCreated {
			continue
		}
		if order.Attempts != 0 || order.AmountDue <= 0 {
			continue
		}
		items = append(items, itemFromUnpaidOrder(order))
	}
	return items, sweepErr
}

// itemFromUnpaidOrder builds the sighting. Every amount and the timestamp are
// the order's own, and RootOrderID is the order itself: an order-sourced item
// is its own root, which is what lets an invoice that minted this order
// collapse onto it.
func itemFromUnpaidOrder(order razorpay.Order) riskitem.RiskItem {
	return riskitem.RiskItem{
		ID:              riskitem.NewID(riskitem.SourceUnpaidOrder, order.ID),
		Source:          riskitem.SourceUnpaidOrder,
		SourceID:        order.ID,
		RootOrderID:     order.ID,
		Customer:        customerFromNotes(order.Notes),
		AmountPaise:     order.AmountPaise,
		AmountPaidPaise: order.AmountPaid,
		AmountDuePaise:  order.AmountDue,
		Currency:        order.Currency,
		AtRiskSince:     order.CreatedAt,
		Signal:          riskitem.Signal{Attempts: order.Attempts},
	}
}
