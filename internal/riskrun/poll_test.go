package riskrun

import (
	"context"
	"errors"
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
	// The invoice and the order it minted both report the payment, so one
	// customer paying one debt moves the sum twice. That is what the two reads
	// say, and a reader of the snapshot pair can see both rows.
	if delta.RecoveredPaise != 200000 {
		t.Errorf("recovered = %d paise, want 200000 across the invoice and its order", delta.RecoveredPaise)
	}
	if delta.AmountDueChange != -200000 {
		t.Errorf("amount due change = %d, want -200000", delta.AmountDueChange)
	}
	if delta.EntriesCompared != 3 {
		t.Errorf("%d entries compared, want 3", delta.EntriesCompared)
	}
	if len(delta.StatusChanges) != 2 {
		t.Errorf("%d status change(s), want 2: %v", len(delta.StatusChanges), delta.StatusChanges)
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
