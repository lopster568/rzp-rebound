package detect

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
)

// The live client satisfies every consumer interface in this package, and each
// detector satisfies the frozen Detector contract. These are compile-time
// assertions rather than tests: a method renamed on either side stops the
// build here instead of at the call site in whatever wires the engine up.
var (
	_ OrderLister        = (*razorpay.Client)(nil)
	_ OrderPaymentLister = (*razorpay.Client)(nil)
	_ InvoiceLister      = (*razorpay.Client)(nil)
	_ OrderPaymentsAPI   = (*razorpay.Client)(nil)

	_ riskitem.Detector = (*FailedPaymentDetector)(nil)
	_ riskitem.Detector = (*UnpaidOrderDetector)(nil)
	_ riskitem.Detector = (*OverdueInvoiceDetector)(nil)

	_ OrderPaymentsAPI = (*stubGateway)(nil)
	_ InvoiceLister    = (*stubGateway)(nil)
)

// The golden sightings this package is measured against live in the frozen
// riskitem package's testdata, not in a copy here. A copy would let a detector
// and the contract it implements drift apart while both tests stayed green.
const goldenDir = "../riskitem/testdata"

func loadGolden(t *testing.T, name string) riskitem.RiskItem {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(goldenDir, name))
	if err != nil {
		t.Fatalf("read the golden sighting %s: %v", name, err)
	}
	var item riskitem.RiskItem
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&item); err != nil {
		t.Fatalf("decode the golden sighting %s: %v", name, err)
	}
	return item
}

// TestDetectorNamesAreTheSourceStrings pins Name to the Source constant.
// The audit trail records both, and a row whose detector name and source
// disagree cannot be read as one event.
func TestDetectorNamesAreTheSourceStrings(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want riskitem.Source
	}{
		{"failed payment", NewFailedPaymentDetector(&stubGateway{}, Config{}).Name(), riskitem.SourceFailedPayment},
		{"unpaid order", NewUnpaidOrderDetector(&stubGateway{}, Config{}).Name(), riskitem.SourceUnpaidOrder},
		{"overdue invoice", NewOverdueInvoiceDetector(&stubGateway{}, Config{}).Name(), riskitem.SourceOverdueInvoice},
	}
	for _, tc := range cases {
		if tc.got != string(tc.want) {
			t.Errorf("%s detector Name = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

// TestCollapseMergesInvoiceAndItsMintedOrder is the reason Collapse exists.
//
// Issuing inv_TYEwC7POHGFZNa minted order_TYEwKA0KjwEW3t, so the invoice
// detector and the unpaid-order detector both report that one debt, under two
// source ids and therefore two RiskItem ids. Collapsing on DedupeKey is what
// stops the customer being contacted twice about the same money.
func TestCollapseMergesInvoiceAndItsMintedOrder(t *testing.T) {
	invoiceItem := loadGolden(t, "overdue_invoice.json")
	orderItem := loadGolden(t, "unpaid_order.json")
	orderItem.SourceID = invoiceItem.RootOrderID
	orderItem.RootOrderID = invoiceItem.RootOrderID
	orderItem.ID = riskitem.NewID(riskitem.SourceUnpaidOrder, orderItem.SourceID)

	if invoiceItem.ID == orderItem.ID {
		t.Fatal("the two sightings share a RiskItem id, so this test proves nothing")
	}
	if invoiceItem.DedupeKey() != orderItem.DedupeKey() {
		t.Fatalf("dedupe keys differ: invoice %q, order %q", invoiceItem.DedupeKey(), orderItem.DedupeKey())
	}

	got := Collapse([]riskitem.RiskItem{invoiceItem, orderItem})

	if len(got) != 1 {
		t.Fatalf("Collapse returned %d items, want 1", len(got))
	}
	if !reflect.DeepEqual(got[0], invoiceItem) {
		t.Errorf("Collapse kept %+v, want the first sighting %+v", got[0], invoiceItem)
	}
}

// TestCollapseKeepsUnrelatedItemsApart pins the fallback half of DedupeKey: an
// item with no root order behind it must never be merged with another one,
// however similar it looks.
func TestCollapseKeepsUnrelatedItemsApart(t *testing.T) {
	first := riskitem.RiskItem{Source: riskitem.SourceUnpaidOrder, SourceID: "order_A"}
	second := riskitem.RiskItem{Source: riskitem.SourceUnpaidOrder, SourceID: "order_B"}
	third := riskitem.RiskItem{Source: riskitem.SourceOverdueInvoice, SourceID: "inv_A"}

	got := Collapse([]riskitem.RiskItem{first, second, third})

	if len(got) != 3 {
		t.Fatalf("Collapse returned %d items, want 3", len(got))
	}
}

// TestCollapsePreservesOrderAndFirstSighting pins both halves of the rule the
// caller relies on to choose which detector speaks for a shared debt: the
// first sighting wins, and the rest of the queue keeps its order.
func TestCollapsePreservesOrderAndFirstSighting(t *testing.T) {
	shared := "order_TYEwKA0KjwEW3t"
	items := []riskitem.RiskItem{
		{Source: riskitem.SourceUnpaidOrder, SourceID: "order_first", RootOrderID: "order_first"},
		{Source: riskitem.SourceOverdueInvoice, SourceID: "inv_one", RootOrderID: shared, Currency: "INR"},
		{Source: riskitem.SourceUnpaidOrder, SourceID: shared, RootOrderID: shared, Currency: "USD"},
		{Source: riskitem.SourceUnpaidOrder, SourceID: "order_last", RootOrderID: "order_last"},
	}

	got := Collapse(items)

	wantIDs := []string{"order_first", "inv_one", "order_last"}
	if len(got) != len(wantIDs) {
		t.Fatalf("Collapse returned %d items, want %d", len(got), len(wantIDs))
	}
	for i, want := range wantIDs {
		if got[i].SourceID != want {
			t.Errorf("item %d is %q, want %q", i, got[i].SourceID, want)
		}
	}
	if got[1].Currency != "INR" {
		t.Errorf("the surviving shared item is the second sighting, currency %q", got[1].Currency)
	}
}

// TestCollapseReturnsANonNilSliceAndDoesNotAliasInput pins the two properties a
// caller that reuses its input buffer depends on.
func TestCollapseReturnsANonNilSliceAndDoesNotAliasInput(t *testing.T) {
	if got := Collapse(nil); got == nil {
		t.Error("Collapse(nil) returned nil, want an empty slice")
	}

	items := []riskitem.RiskItem{{Source: riskitem.SourceUnpaidOrder, SourceID: "order_A", Currency: "INR"}}
	got := Collapse(items)
	got[0].Currency = "USD"
	if items[0].Currency != "INR" {
		t.Error("Collapse shares backing memory with its input")
	}
}

// TestSweepWalksPagesAndStopsOnAShortPage pins the pagination the two list
// endpoints need. A short page is the only end-of-list signal Razorpay gives,
// and the skip on each call has to be the running total or records are read
// twice or missed.
func TestSweepWalksPagesAndStopsOnAShortPage(t *testing.T) {
	gateway := &stubGateway{orders: decodeOrders(t, probeOrderListMixed)}
	detector := NewUnpaidOrderDetector(gateway, Config{PageSize: 2})

	if _, err := detector.Detect(context.Background()); err != nil {
		t.Fatalf("Detect: %v", err)
	}

	want := []razorpay.ListOptions{
		{Count: 2, Skip: 0},
		{Count: 2, Skip: 2},
		{Count: 2, Skip: 4},
	}
	if !reflect.DeepEqual(gateway.orderOpts, want) {
		t.Errorf("list calls were %+v, want %+v", gateway.orderOpts, want)
	}
}

// TestSweepStopsAtMaxPages pins the cap. Four full pages are available and the
// sweep is allowed two, so it reads two and stops without an error: a
// truncated sweep is indistinguishable from a finished one to the caller, and
// pretending otherwise would need an end-of-list signal Razorpay does not send.
func TestSweepStopsAtMaxPages(t *testing.T) {
	gateway := &stubGateway{orders: decodeOrders(t, probeOrderListMixed)}
	detector := NewUnpaidOrderDetector(gateway, Config{PageSize: 1, MaxPages: 2})

	items, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(gateway.orderOpts) != 2 {
		t.Errorf("made %d list calls, want 2", len(gateway.orderOpts))
	}
	if len(items) != 2 {
		t.Errorf("returned %d items, want the 2 unpaid orders on the first two pages", len(items))
	}
}

// TestSweepReturnsWhatItReadAlongsideTheError pins the Detector contract's
// partial-sweep rule: the first page is real debt whether or not the second
// page answered.
func TestSweepReturnsWhatItReadAlongsideTheError(t *testing.T) {
	gateway := &stubGateway{orders: decodeOrders(t, probeOrderListMixed), orderErrOnCall: 2}
	detector := NewUnpaidOrderDetector(gateway, Config{PageSize: 2})

	items, err := detector.Detect(context.Background())

	if !errors.Is(err, errStub) {
		t.Fatalf("Detect error = %v, want the stub failure", err)
	}
	if len(items) != 2 {
		t.Fatalf("returned %d items with the error, want the 2 read off the first page", len(items))
	}
}

// TestDefaultConfigAsksForTheLargestPageRazorpayAllows pins the zero value.
func TestDefaultConfigAsksForTheLargestPageRazorpayAllows(t *testing.T) {
	gateway := &stubGateway{orders: decodeOrders(t, probeOrderListMixed)}
	detector := NewUnpaidOrderDetector(gateway, Config{})

	if _, err := detector.Detect(context.Background()); err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(gateway.orderOpts) != 1 {
		t.Fatalf("made %d list calls, want 1", len(gateway.orderOpts))
	}
	if gateway.orderOpts[0].Count != DefaultPageSize {
		t.Errorf("asked for count %d, want DefaultPageSize %d", gateway.orderOpts[0].Count, DefaultPageSize)
	}
}

// TestCustomerFromNotesReadsOnlyTheDocumentedKeys is the guard on the one path
// by which an order-sourced item can carry a contact at all. A note under any
// other spelling is not a contact this engine will notify.
func TestCustomerFromNotesReadsOnlyTheDocumentedKeys(t *testing.T) {
	documented := customerFromNotes(razorpay.Notes{
		NoteKeyCustomerName:    "Probe Customer",
		NoteKeyCustomerEmail:   "probe-2026-09-05@example.com",
		NoteKeyCustomerContact: "9000090000",
	})
	want := riskitem.Customer{Name: "Probe Customer", Email: "probe-2026-09-05@example.com", Contact: "9000090000"}
	if documented != want {
		t.Errorf("documented keys gave %+v, want %+v", documented, want)
	}

	other := customerFromNotes(razorpay.Notes{"email": "guessed@example.com", "phone": "9000090000"})
	if other.HasContactChannel() {
		t.Errorf("notes under undocumented keys produced a contact channel: %+v", other)
	}

	if got := customerFromNotes(nil); got != (riskitem.Customer{}) {
		t.Errorf("no notes gave %+v, want the zero Customer", got)
	}
}
