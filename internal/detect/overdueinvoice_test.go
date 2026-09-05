package detect

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
)

// goldenIssuedAt is when inv_TYEwC7POHGFZNa was issued, 2026-09-05 in Unix
// seconds. Every clock in this file is set relative to it.
const goldenIssuedAt = 1788586088

// atGrace returns a Config whose clock reads d after the golden invoice was
// issued, with a one day grace period.
func atGrace(d time.Duration) Config {
	return Config{
		Grace: 24 * time.Hour,
		Clock: clock.NewFake(time.Unix(goldenIssuedAt, 0).Add(d)),
	}
}

// TestOverdueInvoiceDetectorMatchesTheGoldenSighting is the whole-item pin,
// and it is the only detector whose golden includes a customer, a pay handle,
// and a notification status, because an invoice is the only source that
// carries them.
func TestOverdueInvoiceDetectorMatchesTheGoldenSighting(t *testing.T) {
	golden := loadGolden(t, "overdue_invoice.json")

	gateway := &stubGateway{invoices: decodeInvoices(t, probeInvoiceListMixed)}
	detector := NewOverdueInvoiceDetector(gateway, atGrace(48*time.Hour))

	items, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	var got riskitem.RiskItem
	found := false
	for _, item := range items {
		if item.SourceID == golden.SourceID {
			got, found = item, true
		}
	}
	if !found {
		t.Fatalf("Detect did not return %s; it returned %d items", golden.SourceID, len(items))
	}
	if !reflect.DeepEqual(got, golden) {
		t.Errorf("Detect built\n %+v\nwant\n %+v", got, golden)
	}
}

// TestOverdueInvoiceDetectorRootsTheItemOnTheMintedOrder is the dedupe jewel.
// Issuing the invoice minted order_TYEwKA0KjwEW3t, and rooting the item there
// rather than on the invoice id is what makes it the same debt as the
// unpaid-order sighting for that order.
func TestOverdueInvoiceDetectorRootsTheItemOnTheMintedOrder(t *testing.T) {
	invoiceGateway := &stubGateway{invoices: decodeInvoices(t, probeInvoiceListMixed)}
	invoiceItems, err := NewOverdueInvoiceDetector(invoiceGateway, atGrace(48*time.Hour)).Detect(context.Background())
	if err != nil {
		t.Fatalf("invoice Detect: %v", err)
	}

	orderGateway := &stubGateway{orders: decodeOrders(t, probeOrderListMixed)}
	orderItems, err := NewUnpaidOrderDetector(orderGateway, Config{}).Detect(context.Background())
	if err != nil {
		t.Fatalf("order Detect: %v", err)
	}

	collapsed := Collapse(append(invoiceItems, orderItems...))

	keys := make(map[string]int)
	for _, item := range collapsed {
		keys[item.DedupeKey()]++
	}
	if keys["order_TYEwKA0KjwEW3t"] != 1 {
		t.Errorf("order_TYEwKA0KjwEW3t appears %d times after the collapse, want 1", keys["order_TYEwKA0KjwEW3t"])
	}
	if len(collapsed) != len(invoiceItems)+len(orderItems)-1 {
		t.Errorf("collapsed %d items into %d, want exactly one merge", len(invoiceItems)+len(orderItems), len(collapsed))
	}
	for _, item := range collapsed {
		if item.DedupeKey() != "order_TYEwKA0KjwEW3t" {
			continue
		}
		if item.Source != riskitem.SourceOverdueInvoice {
			t.Errorf("the surviving item is a %s sighting, want the invoice, which is the one carrying a contact and a payable URL", item.Source)
		}
	}
}

// TestOverdueInvoiceDetectorHonoursTheGracePeriod pins the one configured
// number this package has. An invoice inside grace is not overdue, and the
// same invoice one second past it is.
func TestOverdueInvoiceDetectorHonoursTheGracePeriod(t *testing.T) {
	cases := []struct {
		name  string
		after time.Duration
		want  int
	}{
		{"at the moment it was issued", 0, 0},
		{"one second inside grace", 24*time.Hour - time.Second, 0},
		{"exactly at grace", 24 * time.Hour, 0},
		{"one second past grace", 24*time.Hour + time.Second, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gateway := &stubGateway{invoices: decodeInvoices(t, probeInvoiceListMixed)}
			detector := NewOverdueInvoiceDetector(gateway, atGrace(tc.after))

			items, err := detector.Detect(context.Background())
			if err != nil {
				t.Fatalf("Detect: %v", err)
			}
			got := 0
			for _, item := range items {
				if item.SourceID == "inv_TYEwC7POHGFZNa" {
					got++
				}
			}
			if got != tc.want {
				t.Errorf("the golden invoice came back %d times, want %d", got, tc.want)
			}
		})
	}
}

// TestOverdueInvoiceDetectorTakesOnlyCollectableStatuses pins the status
// filter and the reason each exclusion exists. Cancelled and expired are debts
// a person already closed, and reopening them would be this engine overriding
// that decision.
func TestOverdueInvoiceDetectorTakesOnlyCollectableStatuses(t *testing.T) {
	gateway := &stubGateway{invoices: decodeInvoices(t, probeInvoiceListMixed)}
	detector := NewOverdueInvoiceDetector(gateway, atGrace(48*time.Hour))

	items, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	want := []string{"inv_TYEwC7POHGFZNa", "inv_TYEwE3partial00"}
	got := make([]string, 0, len(items))
	for _, item := range items {
		got = append(got, item.SourceID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Detect returned %v, want %v: issued and partially_paid only", got, want)
	}
}

// TestOverdueInvoiceDetectorCarriesThePartialAmountsAndBothNotifyStatuses
// pins the partially paid case, which is the one where amount and amount_due
// disagree and where both notification fields have moved.
func TestOverdueInvoiceDetectorCarriesThePartialAmountsAndBothNotifyStatuses(t *testing.T) {
	gateway := &stubGateway{invoices: decodeInvoices(t, probeInvoiceListMixed)}
	detector := NewOverdueInvoiceDetector(gateway, atGrace(48*time.Hour))

	items, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	var got riskitem.RiskItem
	for _, item := range items {
		if item.SourceID == "inv_TYEwE3partial00" {
			got = item
		}
	}
	if got.SourceID == "" {
		t.Fatal("the partially paid invoice was not returned")
	}
	if got.AmountPaise != 80000 || got.AmountPaidPaise != 30000 || got.AmountDuePaise != 50000 {
		t.Errorf("amounts are %d/%d/%d, want 80000/30000/50000", got.AmountPaise, got.AmountPaidPaise, got.AmountDuePaise)
	}
	if got.Signal.EmailStatus != "sent" || got.Signal.SmsStatus != "sent" {
		t.Errorf("notify statuses are email %q sms %q, want both sent", got.Signal.EmailStatus, got.Signal.SmsStatus)
	}
	wantHandle := riskitem.PayHandle{
		Kind: riskitem.HandleKindInvoice,
		URL:  "https://rzp.io/rzp/PartLnk",
		ID:   "inv_TYEwE3partial00",
	}
	if got.PayHandle != wantHandle {
		t.Errorf("PayHandle = %+v, want %+v", got.PayHandle, wantHandle)
	}
}

// TestOverdueInvoiceDetectorSkipsAnInvoiceWithNoIssuedAt pins the guard
// against the worst arithmetic in this package. A null issued_at decodes to
// zero, and a zero read as an instant is 1970, which would make every such
// invoice permanently overdue by decades.
func TestOverdueInvoiceDetectorSkipsAnInvoiceWithNoIssuedAt(t *testing.T) {
	const noIssuedAt = `{
      "entity": "collection",
      "count": 1,
      "items": [
        {
          "id": "inv_TYEwNoIssued001",
          "entity": "invoice",
          "customer_id": "cust_TYEw5izKFR0iJr",
          "customer_details": {"id": "cust_TYEw5izKFR0iJr", "name": "Probe Customer", "email": "probe-2026-09-05@example.com", "contact": "9000090000", "gstin": null},
          "order_id": "order_TYEwNoIssuedOrd",
          "payment_id": null,
          "status": "issued",
          "issued_at": null,
          "paid_at": null,
          "cancelled_at": null,
          "expired_at": null,
          "sms_status": null,
          "email_status": null,
          "date": 1788586081,
          "partial_payment": false,
          "amount": 50000,
          "amount_paid": 0,
          "amount_due": 50000,
          "currency": "INR",
          "notes": [],
          "short_url": "https://rzp.io/rzp/NoIssue",
          "type": "invoice",
          "created_at": 1788586081
        }
      ]
    }`

	gateway := &stubGateway{invoices: decodeInvoices(t, noIssuedAt)}
	detector := NewOverdueInvoiceDetector(gateway, atGrace(48*time.Hour))

	items, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("Detect returned %d items, want 0 for an invoice with no issued_at", len(items))
	}
}

// TestOverdueInvoiceDetectorUsesTheDefaultGraceAndTheWallClock pins the zero
// Config on the detector rather than only on the helpers: with no clock and no
// grace configured, an invoice issued two days before the wall clock reads is
// overdue and one issued a minute ago is not.
//
// The two records are the golden invoice with its issued_at moved, which is
// the only field the grace test reads. Everything else about them is the body
// Razorpay sent on 2026-09-05.
func TestOverdueInvoiceDetectorUsesTheDefaultGraceAndTheWallClock(t *testing.T) {
	invoices := decodeInvoices(t, probeInvoiceListMixed)
	var golden razorpay.Invoice
	for _, invoice := range invoices {
		if invoice.ID == "inv_TYEwC7POHGFZNa" {
			golden = invoice
		}
	}
	if golden.ID == "" {
		t.Fatal("the golden invoice is missing from the fixture")
	}

	now := time.Now()
	stale := golden
	stale.IssuedAt = now.Add(-2 * DefaultGrace).Unix()
	fresh := golden
	fresh.ID = "inv_TYEwFresh000001"
	fresh.OrderID = "order_TYEwFreshOrder0"
	fresh.IssuedAt = now.Add(-time.Minute).Unix()

	gateway := &stubGateway{invoices: []razorpay.Invoice{stale, fresh}}
	detector := NewOverdueInvoiceDetector(gateway, Config{})

	items, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Detect returned %d items, want only the one past DefaultGrace", len(items))
	}
	if items[0].SourceID != golden.ID {
		t.Errorf("SourceID = %q, want the stale invoice %q", items[0].SourceID, golden.ID)
	}
}

// TestZeroConfigFallsBackToTheDocumentedDefaults pins every fallback in one
// place, so that a default changed in Config and a default assumed in a
// detector cannot disagree.
func TestZeroConfigFallsBackToTheDocumentedDefaults(t *testing.T) {
	var zero Config

	if got := zero.pageSize(); got != DefaultPageSize {
		t.Errorf("pageSize = %d, want %d", got, DefaultPageSize)
	}
	if got := zero.maxPages(); got != DefaultMaxPages {
		t.Errorf("maxPages = %d, want %d", got, DefaultMaxPages)
	}
	if got := zero.grace(); got != DefaultGrace {
		t.Errorf("grace = %v, want %v", got, DefaultGrace)
	}
	if drift := time.Since(zero.now()); drift < 0 || drift > time.Minute {
		t.Errorf("now() is %v away from the wall clock, want the wall clock", drift)
	}

	set := Config{PageSize: 7, MaxPages: 3, Grace: time.Hour, Clock: clock.NewFake(time.Unix(goldenIssuedAt, 0))}
	if got := set.pageSize(); got != 7 {
		t.Errorf("pageSize = %d, want the configured 7", got)
	}
	if got := set.maxPages(); got != 3 {
		t.Errorf("maxPages = %d, want the configured 3", got)
	}
	if got := set.grace(); got != time.Hour {
		t.Errorf("grace = %v, want the configured hour", got)
	}
	if got := set.now(); !got.Equal(time.Unix(goldenIssuedAt, 0)) {
		t.Errorf("now = %v, want the configured clock", got)
	}
}
