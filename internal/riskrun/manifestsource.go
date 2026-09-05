package riskrun

import (
	"context"

	"github.com/lopster568/rzp-recovery-agent/internal/detect"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/seed"
)

// DetectAPI is the slice of the Razorpay client the three detectors read
// through.
//
// It is the union of internal/detect's three consumer interfaces, declared here
// because a run needs one value that satisfies all of them. *razorpay.Client
// satisfies it, and so does the manifest source below, which is what lets a dry
// run drive the real detectors with no network underneath them.
type DetectAPI interface {
	detect.OrderLister
	detect.OrderPaymentLister
	detect.InvoiceLister
}

var _ DetectAPI = (*razorpay.Client)(nil)

// manifestSource answers the detectors' list calls out of a seedbook manifest.
//
// It is the dry-run gateway and it is not a fake Razorpay. It replays what the
// seed run recorded creating and nothing else: an issued invoice comes back
// issued, unpaid, and carrying the order it minted, and an abandoned order
// comes back at created with zero attempts. Nothing here models a status
// changing, a payment arriving, or an error.
//
// It answers no payments for any order, which is the manifest's own documented
// gap rather than a shortcut. A seed run cannot create a failed payment through
// the API at all: test-mode checkout is browser-only, which is why
// seed.Instructions exists to tell an operator which links to fail by hand. So
// a dry run exercises the overdue-invoice and unpaid-order detectors over real
// data and the failed-payment detector over an empty account, and a run that
// wants the third source has to be a live one.
type manifestSource struct {
	orders   []razorpay.Order
	invoices []razorpay.Invoice
}

var _ DetectAPI = (*manifestSource)(nil)

// newManifestSource builds the entities the detectors will read.
//
// An invoice contributes two of them: the invoice, and the order Razorpay mints
// when an invoice is issued. Both are what a live sweep would see, and it is
// the pair that makes the dedupe do any work, because the same debt then
// arrives from two detectors under two ids and Collapse has to merge them.
//
// The timestamps are the manifest's simulated ones where it has them. That is
// the same statement the age override makes at the policy layer and it is made
// here as well, so that a dry run's detector-level grace period sees the same
// age the gate does rather than treating every seeded item as minutes old.
func newManifestSource(m seed.Manifest) *manifestSource {
	src := &manifestSource{}
	for _, item := range m.Items {
		at := item.SimulatedAtRiskSince
		if at <= 0 {
			at = item.CreatedAt.Unix()
		}

		switch item.Kind {
		case seed.EntityInvoice:
			src.invoices = append(src.invoices, razorpay.Invoice{
				ID:         item.ID,
				CustomerID: item.CustomerID,
				CustomerDetails: razorpay.InvoiceCustomer{
					ID:      item.CustomerID,
					Name:    item.CustomerName,
					Email:   item.CustomerEmail,
					Contact: item.CustomerContact,
				},
				OrderID:     item.OrderID,
				Status:      item.Status,
				ShortURL:    item.ShortURL,
				Currency:    item.Currency,
				AmountPaise: item.AmountPaise,
				// Nothing has been collected. A seed run issues an invoice and
				// stops, so the whole amount is outstanding, and the manifest
				// records no partial payment for this to read.
				AmountPaid: 0,
				AmountDue:  item.AmountPaise,
				IssuedAt:   at,
				CreatedAt:  item.CreatedAt.Unix(),
			})
			if item.OrderID != "" {
				src.orders = append(src.orders, razorpay.Order{
					ID:          item.OrderID,
					AmountPaise: item.AmountPaise,
					AmountPaid:  0,
					AmountDue:   item.AmountPaise,
					Currency:    item.Currency,
					Status:      razorpay.OrderStatusCreated,
					Attempts:    0,
					CreatedAt:   at,
				})
			}
		case seed.EntityOrder:
			src.orders = append(src.orders, razorpay.Order{
				ID:          item.ID,
				AmountPaise: item.AmountPaise,
				AmountPaid:  0,
				AmountDue:   item.AmountPaise,
				Currency:    item.Currency,
				Status:      razorpay.OrderStatusCreated,
				Attempts:    0,
				CreatedAt:   at,
				Notes:       orderNotes(item),
			})
		}
	}
	return src
}

// orderNotes rebuilds the documented contact notes a seed run writes onto an
// abandoned order.
//
// The keys are internal/detect's own, which is the point: an order carries a
// contact only as a note the merchant chose to write, and this replays the ones
// seed.createOrderItem writes rather than inventing a channel. An item the seed
// run deliberately left with no contact gets no keys and stays uncontactable,
// which is the item R10 exists to escalate.
func orderNotes(item seed.Item) razorpay.Notes {
	notes := razorpay.Notes{}
	if item.CustomerName != "" {
		notes[detect.NoteKeyCustomerName] = item.CustomerName
	}
	if item.CustomerEmail != "" {
		notes[detect.NoteKeyCustomerEmail] = item.CustomerEmail
	}
	if item.CustomerContact != "" {
		notes[detect.NoteKeyCustomerContact] = item.CustomerContact
	}
	if len(notes) == 0 {
		return nil
	}
	return notes
}

// ListOrders answers one page, honouring Count and Skip the way the sweep in
// internal/detect expects: a short page ends the walk.
//
// ListOptions.From is deliberately ignored here, by both list methods. On the
// live path it is a created_at floor that keeps an account's older debt out of
// the queue, and the entities this source hands back carry the manifest's
// simulated at-risk instant as their created_at rather than a real one. Honouring
// the floor against a backdated timestamp would filter out exactly the aged book
// a dry run exists to replay. A dry run is therefore always an unscoped sweep,
// and it says so here rather than looking like it applied a bound it did not.
func (s *manifestSource) ListOrders(_ context.Context, opts razorpay.ListOptions) ([]razorpay.Order, error) {
	return page(s.orders, opts), nil
}

// ListInvoices answers one page of invoices.
func (s *manifestSource) ListInvoices(_ context.Context, opts razorpay.ListOptions) ([]razorpay.Invoice, error) {
	return page(s.invoices, opts), nil
}

// ListPaymentsForOrder answers no payments, for every order. See the type's
// doc comment: a seed run cannot create one.
func (s *manifestSource) ListPaymentsForOrder(_ context.Context, _ string) ([]razorpay.Payment, error) {
	return nil, nil
}

// page slices one page out of records. A Count of zero means the whole
// remainder, which is what Razorpay's own default page does when nothing asks
// for a size.
func page[T any](records []T, opts razorpay.ListOptions) []T {
	if opts.Skip >= len(records) {
		return nil
	}
	rest := records[opts.Skip:]
	if opts.Count > 0 && opts.Count < len(rest) {
		rest = rest[:opts.Count]
	}
	out := make([]T, len(rest))
	copy(out, rest)
	return out
}
