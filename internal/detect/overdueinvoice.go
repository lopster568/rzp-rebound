package detect

import (
	"context"

	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
)

// OverdueInvoiceDetector finds issued invoices that have gone unpaid past a
// grace period.
//
// It is the detector whose items are worth the most to the rest of the engine,
// because an issued invoice carries three things an order does not: the
// customer's name, email, and contact in customer_details; a short URL that is
// already payable, so no link has to be minted; and email_status and
// sms_status, which are the only fields anywhere that separate a notification
// this account asked Razorpay to send from one it did not.
//
// It is also the detector that makes the dedupe key necessary. Issuing an
// invoice mints an order, confirmed on 2026-09-05 where inv_TYEwC7POHGFZNa
// issued and came back carrying order_TYEwKA0KjwEW3t. That order is at created
// with zero attempts, so UnpaidOrderDetector reports it too, and the two
// sightings are one debt. See Collapse.
type OverdueInvoiceDetector struct {
	api InvoiceLister
	cfg Config
}

// NewOverdueInvoiceDetector builds a detector that reads through api. Grace
// and the clock it is measured against are Config.Grace and Config.Clock.
func NewOverdueInvoiceDetector(api InvoiceLister, cfg Config) *OverdueInvoiceDetector {
	return &OverdueInvoiceDetector{api: api, cfg: cfg}
}

// Name is the audit-trail identifier, and it is the Source string.
func (d *OverdueInvoiceDetector) Name() string { return string(riskitem.SourceOverdueInvoice) }

// Detect returns one item per invoice that is issued or partially paid, still
// owes money, and was issued longer ago than the grace period.
//
// Both statuses are debts and only the size differs: partially_paid is what an
// invoice created with partial_payment reads once some of it has been
// collected, and Razorpay keeps reporting amount_due on it. Every other status
// is excluded and each for its own reason. A draft has no order and no short
// URL, so there is nothing to pay against and nothing to collapse onto. A paid
// invoice is not a debt. A cancelled or expired one is a debt the merchant has
// already closed, and chasing it would be this engine overriding a decision a
// person made.
//
// An invoice with no issued_at is skipped whatever its status says, because
// there is no instant to measure the grace period from and treating a zero as
// 1970 would make every such invoice instantly overdue.
func (d *OverdueInvoiceDetector) Detect(ctx context.Context) ([]riskitem.RiskItem, error) {
	invoices, sweepErr := sweep(ctx, d.cfg, d.api.ListInvoices)

	cutoff := d.cfg.now().Add(-d.cfg.grace()).Unix()

	var items []riskitem.RiskItem
	for _, invoice := range invoices {
		if !isOverdueStatus(invoice.Status) {
			continue
		}
		if invoice.AmountDue <= 0 || invoice.IssuedAt <= 0 {
			continue
		}
		if invoice.IssuedAt >= cutoff {
			continue
		}
		items = append(items, itemFromOverdueInvoice(invoice))
	}
	return items, sweepErr
}

// isOverdueStatus reports whether a debt can still be collected on an invoice
// in this status.
func isOverdueStatus(status string) bool {
	return status == razorpay.InvoiceStatusIssued || status == razorpay.InvoiceStatusPartial
}

// itemFromOverdueInvoice builds the sighting.
//
// RootOrderID is the order the invoice minted rather than the invoice id,
// which is what makes this item and the unpaid-order item for the same order
// collapse into one. It is empty only when Razorpay sent no order_id, and then
// DedupeKey falls back to the sighting and the item stands alone rather than
// merging with an unrelated one.
//
// PayHandle is the invoice itself. An issued invoice is already payable at its
// short URL, so the lawful action for it is a notification about the handle it
// has, never a second link minted beside it.
func itemFromOverdueInvoice(invoice razorpay.Invoice) riskitem.RiskItem {
	return riskitem.RiskItem{
		ID:          riskitem.NewID(riskitem.SourceOverdueInvoice, invoice.ID),
		Source:      riskitem.SourceOverdueInvoice,
		SourceID:    invoice.ID,
		RootOrderID: invoice.OrderID,
		Customer: riskitem.Customer{
			Name:    invoice.CustomerDetails.Name,
			Email:   invoice.CustomerDetails.Email,
			Contact: invoice.CustomerDetails.Contact,
		},
		AmountPaise:     invoice.AmountPaise,
		AmountPaidPaise: invoice.AmountPaid,
		AmountDuePaise:  invoice.AmountDue,
		Currency:        invoice.Currency,
		AtRiskSince:     invoice.IssuedAt,
		Signal: riskitem.Signal{
			EmailStatus: invoice.EmailStatus,
			SmsStatus:   invoice.SMSStatus,
		},
		PayHandle: riskitem.PayHandle{
			Kind: riskitem.HandleKindInvoice,
			URL:  invoice.ShortURL,
			ID:   invoice.ID,
		},
	}
}
