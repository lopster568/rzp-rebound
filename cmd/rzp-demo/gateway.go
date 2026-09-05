package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/lopster568/rzp-recovery-agent/internal/intervene"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/seed"
)

// simGateway is the whole of what this binary can reach when the intervention
// engine acts. It is a map and a mutex.
//
// It is not a fake Razorpay and it does not try to be. internal/razorpay.Fake
// is the repository's fake gateway and it deliberately does not implement the
// three invoice calls intervene.Gateway needs, because the retry engine it was
// written for never made them. Rather than widen a type the whole test suite
// depends on the day before a submission, this holds the five calls the
// intervention engine actually makes and answers them from the fixture book.
//
// The safety property this type carries is the one the demo is built on: it
// satisfies intervene.Gateway, it is the only value this binary ever puts in
// intervene.Options.Gateway, and it has no address, no credential, and no HTTP
// client anywhere inside it. razorpay.Client also satisfies that interface, and
// nothing in this package constructs one. cmd/rzp-demo/safety_test.go walks
// this package's own syntax tree and fails the build if that stops being true.
//
// What it models is what the seed run recorded: an issued invoice can be
// notified and cancelled, a minted payment link can be resent, and a
// notification moves the invoice's email_status or sms_status to sent, which is
// the field internal/intervene reads back to decide what it may claim. Nothing
// here models a payment arriving. A customer paying is the one event this
// simulation has no way to produce, and inventing one would put a recovery
// figure on a page that has no run behind it.
type simGateway struct {
	mu       sync.Mutex
	invoices map[string]*razorpay.Invoice
	links    map[string]*razorpay.PaymentLink
	// minted counts payment links, so an id is a function of the call order
	// rather than of a random source. Two runs over the same book produce the
	// same ids, which is what makes the page's output reproducible.
	minted int
	// calls is every call this gateway answered, in order, for the run panel.
	calls []string
}

var _ intervene.Gateway = (*simGateway)(nil)

// newSimGateway loads the fixture book into memory.
//
// Only invoices are loaded. An abandoned order has no gateway resource an
// intervention can act on: the action for one is create_payment_link, and the
// link it mints is recorded here when the call arrives.
func newSimGateway(m seed.Manifest) *simGateway {
	g := &simGateway{
		invoices: make(map[string]*razorpay.Invoice),
		links:    make(map[string]*razorpay.PaymentLink),
	}
	for _, item := range m.Items {
		if item.Kind != seed.EntityInvoice {
			continue
		}
		g.invoices[item.ID] = &razorpay.Invoice{
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
			AmountDue:   item.AmountPaise,
			IssuedAt:    item.SimulatedAtRiskSince,
			CreatedAt:   item.CreatedAt.Unix(),
		}
	}
	return g
}

// Calls returns what this gateway was asked to do, in order.
func (g *simGateway) Calls() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]string, len(g.calls))
	copy(out, g.calls)
	return out
}

func (g *simGateway) record(format string, args ...any) {
	g.calls = append(g.calls, fmt.Sprintf(format, args...))
}

// NotifyInvoice marks the invoice as having been sent over medium and reports
// that the call succeeded.
//
// Accepted true here means exactly what it means on the live path: the call was
// accepted. It is not a person having read anything, and the status this writes
// is the same field Razorpay writes, which is why internal/intervene's read-back
// finds something to read.
func (g *simGateway) NotifyInvoice(_ context.Context, invoiceID, medium string) (razorpay.NotifyReceipt, error) {
	if medium != razorpay.MediumEmail && medium != razorpay.MediumSMS {
		return razorpay.NotifyReceipt{}, fmt.Errorf("%w: %q", razorpay.ErrUnsupportedMedium, medium)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	invoice, ok := g.invoices[invoiceID]
	if !ok {
		return razorpay.NotifyReceipt{}, fmt.Errorf("simulated gateway: no invoice %s in the fixture book", invoiceID)
	}
	if medium == razorpay.MediumSMS {
		invoice.SMSStatus = razorpay.InvoiceNotifyStatusSent
	} else {
		invoice.EmailStatus = razorpay.InvoiceNotifyStatusSent
	}
	g.record("notify %s over %s", invoiceID, medium)

	return razorpay.NotifyReceipt{LinkID: invoiceID, Medium: medium, Accepted: true}, nil
}

// FetchInvoice is the verifying read internal/intervene makes after a notify.
func (g *simGateway) FetchInvoice(_ context.Context, invoiceID string) (razorpay.Invoice, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	invoice, ok := g.invoices[invoiceID]
	if !ok {
		return razorpay.Invoice{}, fmt.Errorf("simulated gateway: no invoice %s in the fixture book", invoiceID)
	}
	g.record("fetch %s", invoiceID)
	return *invoice, nil
}

// CancelInvoice closes an invoice. A paid one refuses, which is what Razorpay
// does and what the write-off path has to be able to see.
func (g *simGateway) CancelInvoice(_ context.Context, invoiceID string) (razorpay.Invoice, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	invoice, ok := g.invoices[invoiceID]
	if !ok {
		return razorpay.Invoice{}, fmt.Errorf("simulated gateway: no invoice %s in the fixture book", invoiceID)
	}
	if invoice.Status == razorpay.InvoiceStatusPaid {
		return razorpay.Invoice{}, fmt.Errorf("simulated gateway: invoice %s is paid and cannot be cancelled", invoiceID)
	}
	invoice.Status = razorpay.InvoiceStatusCancelled
	g.record("cancel %s", invoiceID)
	return *invoice, nil
}

// CreatePaymentLink mints a link for an item that has nothing to pay against.
//
// The host is pay.invalid, the same reserved name internal/razorpay.Fake uses,
// so a link on the page resolves nowhere and nobody who clicks one reaches a
// checkout. That is deliberate: this page is public and a plausible-looking
// payment URL on it would be the one thing here that could cost a stranger
// money.
func (g *simGateway) CreatePaymentLink(_ context.Context, req razorpay.CreatePaymentLinkRequest) (razorpay.PaymentLink, error) {
	if req.AmountPaise <= 0 {
		return razorpay.PaymentLink{}, fmt.Errorf("%w: got %d paise", razorpay.ErrAmountNotPositive, req.AmountPaise)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	g.minted++
	currency := req.Currency
	if currency == "" {
		currency = "INR"
	}
	link := &razorpay.PaymentLink{
		ID:          fmt.Sprintf("plink_SIM%011d", g.minted),
		Status:      razorpay.PaymentLinkStatusCreated,
		AmountPaise: req.AmountPaise,
		Currency:    currency,
		ReferenceID: req.ReferenceID,
	}
	link.ShortURL = "https://pay.invalid/" + link.ID
	g.links[link.ID] = link
	g.record("mint a link for %s, %s", req.ReferenceID, rupees(link.AmountPaise))
	return *link, nil
}

// ResendPaymentLinkNotification resends a link this run minted. There is no
// field to read back, so the strongest thing the engine can say afterwards is
// that the notification API call was accepted, which is what it does say.
func (g *simGateway) ResendPaymentLinkNotification(_ context.Context, linkID, medium string) (razorpay.NotifyReceipt, error) {
	if medium != razorpay.MediumEmail && medium != razorpay.MediumSMS {
		return razorpay.NotifyReceipt{}, fmt.Errorf("%w: %q", razorpay.ErrUnsupportedMedium, medium)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if _, ok := g.links[linkID]; !ok {
		return razorpay.NotifyReceipt{}, fmt.Errorf("%w: %s", razorpay.ErrPaymentLinkNotFound, linkID)
	}
	g.record("resend %s over %s", linkID, medium)
	return razorpay.NotifyReceipt{LinkID: linkID, Medium: medium, Accepted: true}, nil
}

// rupees renders paise the way cmd/rzp risk-run's formatPaise renders it, with
// no group separators, so a figure on the page and the same figure in a
// committed run log are the same string.
//
// The arithmetic is integer rather than the float division that spelling uses,
// which is the one deliberate difference: paise are exact and a float is not,
// and this figure goes on a public page.
func rupees(paise int64) string {
	sign := ""
	if paise < 0 {
		sign = "-"
		paise = -paise
	}
	return fmt.Sprintf("INR %s%d.%02d", sign, paise/100, paise%100)
}
