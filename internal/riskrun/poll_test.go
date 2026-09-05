package riskrun

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/seed"
)

// stubPoll answers the three fetches out of maps, and reports a miss as an
// error the way the live client does.
type stubPoll struct {
	invoices map[string]razorpay.Invoice
	orders   map[string]razorpay.Order
	links    map[string]razorpay.PaymentLinkDetail
	calls    int
}

func (s *stubPoll) FetchInvoice(_ context.Context, id string) (razorpay.Invoice, error) {
	s.calls++
	invoice, ok := s.invoices[id]
	if !ok {
		return razorpay.Invoice{}, errors.New("no such invoice")
	}
	return invoice, nil
}

func (s *stubPoll) FetchOrder(_ context.Context, id string) (razorpay.Order, error) {
	s.calls++
	order, ok := s.orders[id]
	if !ok {
		return razorpay.Order{}, errors.New("no such order")
	}
	return order, nil
}

func (s *stubPoll) FetchPaymentLink(_ context.Context, id string) (razorpay.PaymentLinkDetail, error) {
	s.calls++
	link, ok := s.links[id]
	if !ok {
		return razorpay.PaymentLinkDetail{}, errors.New("no such payment link")
	}
	return link, nil
}

func pollManifest() seed.Manifest {
	return seed.Manifest{
		RunTag: "poll",
		Items: []seed.Item{
			{Kind: seed.EntityInvoice, ID: "inv_1", OrderID: "order_1"},
			{Kind: seed.EntityOrder, ID: "order_2"},
		},
	}
}

func gateway(paid int64) *stubPoll {
	return &stubPoll{
		invoices: map[string]razorpay.Invoice{
			"inv_1": {
				ID: "inv_1", OrderID: "order_1", Status: "issued", Currency: "INR",
				AmountPaise: 100000, AmountPaid: paid, AmountDue: 100000 - paid,
				EmailStatus: "sent",
			},
		},
		orders: map[string]razorpay.Order{
			"order_1": {ID: "order_1", Status: "created", Currency: "INR", AmountPaise: 100000, AmountPaid: paid, AmountDue: 100000 - paid},
			"order_2": {ID: "order_2", Status: "created", Currency: "INR", AmountPaise: 50000, AmountDue: 50000},
		},
		links: map[string]razorpay.PaymentLinkDetail{
			"plink_1": {ID: "plink_1", Status: "created", Currency: "INR", AmountPaise: 50000},
		},
	}
}

// TestPollReadsEveryManifestEntityAndTheLinksARunCreated. An invoice
// contributes two reads because the invoice and the order it minted are two
// different answers about one debt.
func TestPollReadsEveryManifestEntityAndTheLinksARunCreated(t *testing.T) {
	api := gateway(0)
	snapshot, err := Poll(context.Background(), api, PollOptions{
		Manifest:       pollManifest(),
		ManifestPath:   "seedbook.json",
		PaymentLinkIDs: []string{"plink_1"},
		Now:            time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}

	if snapshot.Totals.Entries != 4 {
		t.Errorf("%d entries, want 4: one invoice, the order it minted, one seeded order, one created link", snapshot.Totals.Entries)
	}
	if snapshot.Totals.Errors != 0 {
		t.Errorf("%d entries could not be read", snapshot.Totals.Errors)
	}
	if snapshot.Totals.Duplicates != 1 {
		t.Errorf("%d duplicate(s), want 1: the order inv_1 minted", snapshot.Totals.Duplicates)
	}
	if snapshot.ManifestRunTag != "poll" {
		t.Errorf("manifest run tag = %q", snapshot.ManifestRunTag)
	}

	var invoice SnapshotEntry
	for _, entry := range snapshot.Entries {
		if entry.Kind == EntryInvoice {
			invoice = entry
		}
	}
	if invoice.EmailStatus != "sent" {
		t.Errorf("the invoice entry dropped email_status: %+v", invoice)
	}
	// Read off the response, never subtracted. A partial payment makes the
	// arithmetic disagree with the gateway.
	if invoice.AmountDuePaise != 100000 {
		t.Errorf("amount due = %d, want the 100000 the gateway reported", invoice.AmountDuePaise)
	}
}

// TestPollKeepsAnUnreadableEntity. An entity that could not be read is not an
// entity that is settled, and dropping it would let a delta count it as paid.
func TestPollKeepsAnUnreadableEntity(t *testing.T) {
	api := gateway(0)
	delete(api.orders, "order_2")

	snapshot, err := Poll(context.Background(), api, PollOptions{Manifest: pollManifest()})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if snapshot.Totals.Entries != 3 || snapshot.Totals.Errors != 1 {
		t.Errorf("%d entries with %d error(s), want 3 and 1", snapshot.Totals.Entries, snapshot.Totals.Errors)
	}
	for _, entry := range snapshot.Entries {
		if entry.ID == "order_2" && entry.Error == "" {
			t.Error("the unreadable order came back with no error on it")
		}
	}
}

// TestDiffIsTheRiseInWhatTheGatewayReportsCollected.
func TestDiffIsTheRiseInWhatTheGatewayReportsCollected(t *testing.T) {
	before, err := Poll(context.Background(), gateway(0), PollOptions{
		Manifest: pollManifest(),
		Now:      time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}

	paidAPI := gateway(100000)
	paidAPI.invoices["inv_1"] = razorpay.Invoice{
		ID: "inv_1", OrderID: "order_1", Status: "paid", Currency: "INR",
		AmountPaise: 100000, AmountPaid: 100000, AmountDue: 0,
	}
	paidAPI.orders["order_1"] = razorpay.Order{
		ID: "order_1", Status: "paid", Currency: "INR",
		AmountPaise: 100000, AmountPaid: 100000, AmountDue: 0,
	}
	after, err := Poll(context.Background(), paidAPI, PollOptions{
		Manifest: pollManifest(),
		Now:      time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}

	delta := Diff(before, after)
	// The invoice and the order it minted both report the payment. It is one
	// debt, so it moves the sum once, on the invoice.
	if delta.RecoveredPaise != 100000 {
		t.Errorf("recovered = %d paise, want the 100000 the one debt is worth", delta.RecoveredPaise)
	}
	if delta.AmountDueChange != -100000 {
		t.Errorf("amount due change = %d, want -100000", delta.AmountDueChange)
	}
	if delta.EntriesCompared != 3 {
		t.Errorf("%d entries compared, want 3", delta.EntriesCompared)
	}
	if delta.EntriesDeduped != 1 {
		t.Errorf("%d deduped, want 1: the order inv_1 minted", delta.EntriesDeduped)
	}
	// Both ends of the debt are still visible. The order's flip to paid is the
	// transition a demo points at, and dropping the entry to fix the arithmetic
	// would have taken it away.
	if len(delta.StatusChanges) != 2 {
		t.Errorf("%d status change(s), want 2: %v", len(delta.StatusChanges), delta.StatusChanges)
	}
}

// TestSnapshotCountsOneDebtOnce is the arithmetic half, on the book shape that
// produced the defect: five issued invoices, each with the order it minted, and
// nothing else. The live run on 2026-09-05 read that book as INR 56676 gross
// against INR 28338 of actual debt, and Diff summed both ends, so a single paid
// invoice would have been reported at twice its value.
func TestSnapshotCountsOneDebtOnce(t *testing.T) {
	const invoices = 5
	const eachPaise = int64(566760 / invoices)

	manifest := seed.Manifest{RunTag: "double-count"}
	api := &stubPoll{
		invoices: map[string]razorpay.Invoice{},
		orders:   map[string]razorpay.Order{},
	}
	for i := range invoices {
		invoiceID := fmt.Sprintf("inv_%d", i)
		orderID := fmt.Sprintf("order_%d", i)
		manifest.Items = append(manifest.Items, seed.Item{
			Kind: seed.EntityInvoice, ID: invoiceID, OrderID: orderID,
		})
		api.invoices[invoiceID] = razorpay.Invoice{
			ID: invoiceID, OrderID: orderID, Status: "issued", Currency: "INR",
			AmountPaise: eachPaise, AmountDue: eachPaise,
		}
		api.orders[orderID] = razorpay.Order{
			ID: orderID, Status: "created", Currency: "INR",
			AmountPaise: eachPaise, AmountDue: eachPaise,
		}
	}

	snapshot, err := Poll(context.Background(), api, PollOptions{Manifest: manifest})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}

	// Both ends of every debt are read and both are in the file.
	if snapshot.Totals.Entries != invoices*2 {
		t.Errorf("%d entries, want %d: every invoice and the order it minted", snapshot.Totals.Entries, invoices*2)
	}
	if snapshot.Totals.Duplicates != invoices {
		t.Errorf("%d duplicate(s), want %d", snapshot.Totals.Duplicates, invoices)
	}
	if want := eachPaise * invoices; snapshot.Totals.AmountPaise != want {
		t.Errorf("gross = %d paise, want %d: the book is worth what it is worth, not twice that",
			snapshot.Totals.AmountPaise, want)
	}
	if want := eachPaise * invoices; snapshot.Totals.AmountDuePaise != want {
		t.Errorf("due = %d paise, want %d", snapshot.Totals.AmountDuePaise, want)
	}

	// Every order entry names the invoice its amounts were counted on, so the
	// file says why its own rows do not add up to its own totals.
	for _, entry := range snapshot.Entries {
		if entry.Kind != EntryOrder {
			continue
		}
		if entry.DuplicateOf == "" {
			t.Errorf("order %s is an invoice's minted order and carries no duplicate_of", entry.ID)
		}
		if entry.AmountPaise != eachPaise {
			t.Errorf("order %s lost the amount Razorpay reported for it: %d", entry.ID, entry.AmountPaise)
		}
	}

	// One invoice is paid. The delta is that one debt, once.
	paid := api.invoices["inv_0"]
	paid.Status, paid.AmountPaid, paid.AmountDue = "paid", eachPaise, 0
	api.invoices["inv_0"] = paid
	paidOrder := api.orders["order_0"]
	paidOrder.Status, paidOrder.AmountPaid, paidOrder.AmountDue = "paid", eachPaise, 0
	api.orders["order_0"] = paidOrder

	after, err := Poll(context.Background(), api, PollOptions{Manifest: manifest})
	if err != nil {
		t.Fatalf("Poll after the payment: %v", err)
	}
	delta := Diff(snapshot, after)
	if delta.RecoveredPaise != eachPaise {
		t.Errorf("recovered = %d paise for one paid invoice, want %d", delta.RecoveredPaise, eachPaise)
	}
	if delta.AmountDueChange != -eachPaise {
		t.Errorf("amount due change = %d, want %d", delta.AmountDueChange, -eachPaise)
	}
}

// TestSnapshotKeepsAnUnreadableInvoicesDebtOnItsOrder. The dedupe drops the
// order's amounts because the invoice already carries them. When the invoice
// could not be read it carries nothing, so the order has to stay countable or
// the debt disappears from the snapshot altogether.
func TestSnapshotKeepsAnUnreadableInvoicesDebtOnItsOrder(t *testing.T) {
	api := gateway(0)
	delete(api.invoices, "inv_1")

	snapshot, err := Poll(context.Background(), api, PollOptions{Manifest: pollManifest()})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if snapshot.Totals.Duplicates != 0 {
		t.Errorf("%d duplicate(s), want 0: the invoice was unreadable and counted nothing", snapshot.Totals.Duplicates)
	}
	// order_1 at 100000 plus order_2 at 50000. Losing the first is the failure
	// this test exists for.
	if snapshot.Totals.AmountPaise != 150000 {
		t.Errorf("gross = %d paise, want 150000", snapshot.Totals.AmountPaise)
	}
}

// TestPollReportsAManifestItemItCouldNotAskAbout. A seed run that stopped
// partway leaves an item with a customer id and no invoice id. Poll used to
// drop it inside add, which made a short snapshot look like a complete one.
func TestPollReportsAManifestItemItCouldNotAskAbout(t *testing.T) {
	manifest := pollManifest()
	manifest.Items = append(manifest.Items, seed.Item{
		Kind: seed.EntityInvoice, CustomerID: "cust_9", Incomplete: true,
	})

	snapshot, err := Poll(context.Background(), gateway(0), PollOptions{Manifest: manifest})
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if snapshot.Totals.Skipped != 1 || len(snapshot.Skipped) != 1 {
		t.Fatalf("skipped %d item(s) and listed %d, want 1 and 1", snapshot.Totals.Skipped, len(snapshot.Skipped))
	}
	skipped := snapshot.Skipped[0]
	if skipped.CustomerID != "cust_9" {
		t.Errorf("the skipped line lost the one id the item does carry: %+v", skipped)
	}
	if skipped.Reason != SkipNoID {
		t.Errorf("skipped reason = %q, want %q", skipped.Reason, SkipNoID)
	}
	if snapshot.Totals.Entries != 3 {
		t.Errorf("%d entries, want 3: the incomplete item is not one of them", snapshot.Totals.Entries)
	}
}

// TestDiffSkipsWhatEitherSnapshotCouldNotRead. A read that failed is not a zero.
func TestDiffSkipsWhatEitherSnapshotCouldNotRead(t *testing.T) {
	broken := gateway(0)
	delete(broken.orders, "order_2")
	before, err := Poll(context.Background(), broken, PollOptions{Manifest: pollManifest()})
	if err != nil {
		t.Fatal(err)
	}
	after, err := Poll(context.Background(), gateway(0), PollOptions{Manifest: pollManifest()})
	if err != nil {
		t.Fatal(err)
	}

	delta := Diff(before, after)
	if delta.EntriesCompared != 2 {
		t.Errorf("%d entries compared, want 2: the third was unreadable in the earlier snapshot", delta.EntriesCompared)
	}
	if delta.EntriesUnmatched != 1 {
		t.Errorf("%d unmatched, want 1", delta.EntriesUnmatched)
	}
	if delta.RecoveredPaise != 0 {
		t.Errorf("recovered = %d paise from two identical readable snapshots", delta.RecoveredPaise)
	}
}
