package detect

import (
	"context"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
)

// The Razorpay reads each detector needs, and nothing wider.
//
// These are consumer interfaces: they are declared here, by the code that
// calls them, rather than exported by the package that implements them.
// *razorpay.Client satisfies all of them, and detect_test.go holds the
// compile-time assertion that says so. A test supplies a stub instead, which
// is why no detector holds a *razorpay.Client.
type (
	// OrderLister reads one page of orders, newest first.
	OrderLister interface {
		ListOrders(ctx context.Context, opts razorpay.ListOptions) ([]razorpay.Order, error)
	}

	// OrderPaymentLister reads every payment attempt on one order.
	OrderPaymentLister interface {
		ListPaymentsForOrder(ctx context.Context, orderID string) ([]razorpay.Payment, error)
	}

	// InvoiceLister reads one page of invoices.
	InvoiceLister interface {
		ListInvoices(ctx context.Context, opts razorpay.ListOptions) ([]razorpay.Invoice, error)
	}

	// OrderPaymentsAPI is the pair FailedPaymentDetector needs: the order list
	// to find candidate debts, and the payments behind each candidate to find
	// the failure. Razorpay has no endpoint that lists failed payments across
	// orders, so the walk is orders first and payments per order.
	OrderPaymentsAPI interface {
		OrderLister
		OrderPaymentLister
	}
)

// Sweep bounds, both configured choices rather than measurements.
const (
	// DefaultPageSize is how many records a detector asks for per list call.
	// Razorpay caps count at 100 and refuses a larger one, per ListOptions, so
	// this is the largest page that can be requested and the fewest round
	// trips a sweep can take.
	DefaultPageSize = 100
	// DefaultMaxPages caps a sweep at 20 pages, which is 2000 records at the
	// default page size. It exists so that an account with a long history
	// cannot turn one detector run into an unbounded walk of every order it
	// ever had. The number is a choice, not a limit anything reported: no
	// Razorpay list depth has been measured by this repository.
	DefaultMaxPages = 20
	// DefaultGrace is how long an issued invoice is left alone before
	// OverdueInvoiceDetector calls it overdue. Twenty four hours is a choice
	// made here, not a figure read off anything: Razorpay reports issued_at
	// and nothing about when a merchant expects to be paid.
	DefaultGrace = 24 * time.Hour
)

// Note keys a detector will read a contact out of.
//
// Razorpay's /v1/orders responses carry no customer email and no customer
// contact at all, confirmed against live test mode on 2026-09-05. An order is
// an amount and a status, and the contact only exists on a payment attempt, on
// an invoice, or on a payment link. So an order-sourced item has an empty
// Customer unless the merchant deliberately wrote the contact into the order's
// notes under one of these keys.
//
// The spelling matches the customer_details object on an invoice, so that the
// same key means the same thing in both places. A note under any other key is
// ignored rather than guessed at, and an item with no contact channel is
// escalated by the policy gate rather than notified. See
// riskitem.Customer.HasContactChannel.
const (
	NoteKeyCustomerName    = "customer_name"
	NoteKeyCustomerEmail   = "customer_email"
	NoteKeyCustomerContact = "customer_contact"
)

// Config bounds one sweep.
//
// The zero value is usable: every field falls back to the Default constant
// above, and Clock falls back to the wall clock.
type Config struct {
	// PageSize is the count each list call asks for. Zero means
	// DefaultPageSize. It is not clamped to Razorpay's cap of 100, for the
	// reason ListOptions gives: a silently shrunk page would make a caller
	// that asked for 500 believe it had seen everything.
	PageSize int
	// MaxPages is how many pages a sweep reads before it stops. Zero means
	// DefaultMaxPages. A sweep that stops here has not seen everything, and it
	// reports no error saying so, because a truncated page is indistinguishable
	// from a page that ended.
	MaxPages int
	// Grace is how long an issued invoice is left alone. Zero means
	// DefaultGrace. Read by OverdueInvoiceDetector only.
	Grace time.Duration
	// Clock supplies the instant an invoice's age is measured against. Nil
	// means clock.Real(). Read by OverdueInvoiceDetector only.
	Clock clock.Clock
}

// pageSize is PageSize with the default filled in.
func (c Config) pageSize() int {
	if c.PageSize > 0 {
		return c.PageSize
	}
	return DefaultPageSize
}

// maxPages is MaxPages with the default filled in.
func (c Config) maxPages() int {
	if c.MaxPages > 0 {
		return c.MaxPages
	}
	return DefaultMaxPages
}

// grace is Grace with the default filled in.
func (c Config) grace() time.Duration {
	if c.Grace > 0 {
		return c.Grace
	}
	return DefaultGrace
}

// now reads the configured clock, or the wall clock when there is none.
func (c Config) now() time.Time {
	if c.Clock != nil {
		return c.Clock.Now()
	}
	return clock.Real().Now()
}

// sweep walks a paginated list endpoint and returns every record it read.
//
// It stops on a short page, which is the only signal Razorpay gives that a
// list has ended, at Config.MaxPages, or on the first error. An error is
// returned with the records read before it rather than instead of them,
// because a detector that read four pages and failed on the fifth has still
// seen four pages of real debt, and the Detector contract says the caller
// decides whether a partial sweep is worth acting on.
func sweep[T any](ctx context.Context, cfg Config, page func(context.Context, razorpay.ListOptions) ([]T, error)) ([]T, error) {
	size := cfg.pageSize()
	limit := cfg.maxPages()

	var out []T
	for read := 0; read < limit; read++ {
		items, err := page(ctx, razorpay.ListOptions{Count: size, Skip: read * size})
		if err != nil {
			return out, err
		}
		out = append(out, items...)
		if len(items) < size {
			return out, nil
		}
	}
	return out, nil
}

// customerFromNotes reads a contact out of a resource's notes, and only out of
// the documented keys.
//
// It derives nothing. An order with no such notes produces the zero Customer,
// which is the honest report that Razorpay told us nobody to contact.
func customerFromNotes(notes razorpay.Notes) riskitem.Customer {
	if len(notes) == 0 {
		return riskitem.Customer{}
	}
	return riskitem.Customer{
		Name:    notes[NoteKeyCustomerName],
		Email:   notes[NoteKeyCustomerEmail],
		Contact: notes[NoteKeyCustomerContact],
	}
}

// customerFromFailedPayment reads the contact off a payment, with the order's
// documented notes underneath it.
//
// The payment wins per field rather than as a whole record. A payment that
// carried an email and no phone number leaves the note's phone number in
// place, because the two fields are separate facts and dropping one because
// the other was overwritten would lose a channel that was reported.
//
// Nothing is derived here. A payment with neither field and an order with no
// documented notes produce the zero Customer, which is the honest report that
// Razorpay named nobody to contact, and the policy gate escalates it rather
// than letting anything guess an address.
func customerFromFailedPayment(payment razorpay.Payment, notes razorpay.Notes) riskitem.Customer {
	customer := customerFromNotes(notes)
	if payment.Email != "" {
		customer.Email = payment.Email
	}
	if payment.Contact != "" {
		customer.Contact = payment.Contact
	}
	return customer
}

// Collapse merges the sightings that are the same debt, first one wins.
//
// The queue collapses on riskitem.RiskItem.DedupeKey, not on ID: Razorpay
// mints an order when an invoice is issued, so one debt is reachable from
// OverdueInvoiceDetector under an inv_ id and from UnpaidOrderDetector under
// the order_ id it minted, and contacting the customer once per detector is
// contacting them twice about one debt.
//
// First sighting wins and input order is preserved, so the caller decides
// which detector speaks for a shared debt by the order it concatenates their
// output in. The overdue invoice is the one worth putting first: it carries a
// customer, a short URL that is already payable, and the notification state,
// none of which an order-sourced sighting has.
//
// The returned slice is always non-nil and never shares backing memory with
// items.
func Collapse(items []riskitem.RiskItem) []riskitem.RiskItem {
	out := make([]riskitem.RiskItem, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		key := item.DedupeKey()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}
