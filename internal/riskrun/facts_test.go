package riskrun

import (
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/promise"
	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
	"github.com/lopster568/rzp-recovery-agent/internal/seed"
)

var factsNow = time.Date(2026, 9, 5, 7, 0, 0, 0, time.UTC)

func disputedManifest() seed.Manifest {
	return seed.Manifest{
		RunTag: "facts",
		Items: []seed.Item{
			{
				Kind:    seed.EntityInvoice,
				ID:      "inv_disputed",
				OrderID: "order_from_disputed_invoice",
				Status:  "issued",
				Flags:   seed.Flags{Disputed: true},
			},
			{
				Kind:   seed.EntityOrder,
				ID:     "order_plain",
				Status: "created",
			},
			{
				Kind:    seed.EntityInvoice,
				ID:      "inv_cancelled",
				OrderID: "order_from_cancelled_invoice",
				Status:  "cancelled",
			},
		},
	}
}

// TestFactsCarryTheDisputedFlagFromTheManifest is the plumbing R13 needs.
//
// Razorpay has no disputed field anywhere, so the flag can only come from the
// record a person made, which for a seeded run is the manifest. Without this
// the rule is unreachable, which is what internal/mcpserver documents about the
// MCP path.
func TestFactsCarryTheDisputedFlagFromTheManifest(t *testing.T) {
	index := newManifestIndex(disputedManifest())
	promises := promise.NewStore()

	invoice := riskitem.RiskItem{
		ID:          "ri_invoice",
		Source:      riskitem.SourceOverdueInvoice,
		SourceID:    "inv_disputed",
		RootOrderID: "order_from_disputed_invoice",
	}
	facts := factsFor(index, promises, invoice, 1, factsNow)
	if !facts.Disputed {
		t.Error("the disputed invoice reached the gate with disputed false")
	}
	if facts.SourceStatus != "issued" {
		t.Errorf("source status = %q, want the status the seeder recorded", facts.SourceStatus)
	}

	plain := riskitem.RiskItem{
		ID:          "ri_order",
		Source:      riskitem.SourceUnpaidOrder,
		SourceID:    "order_plain",
		RootOrderID: "order_plain",
	}
	if factsFor(index, promises, plain, 1, factsNow).Disputed {
		t.Error("an order the manifest did not flag came back disputed")
	}
}

// TestFactsReachAFailedPaymentThroughItsRootOrder is why the index carries the
// order an invoice minted.
//
// A failed payment's own id is one the seeder never saw: the operator made it
// by failing a checkout in a browser. The only route back to the manifest is
// the order behind it, which is the order the invoice minted, and without that
// key the sighting the demo cares most about would carry no manifest facts at
// all.
func TestFactsReachAFailedPaymentThroughItsRootOrder(t *testing.T) {
	index := newManifestIndex(disputedManifest())

	payment := riskitem.RiskItem{
		ID:          "ri_payment",
		Source:      riskitem.SourceFailedPayment,
		SourceID:    "pay_nothing_seeded_this",
		RootOrderID: "order_from_disputed_invoice",
	}
	facts := factsFor(index, promise.NewStore(), payment, 1, factsNow)
	if !facts.Disputed {
		t.Error("a failed payment on a disputed invoice's order came back undisputed")
	}
}

// TestFactsCarryAnActivePromiseHold pins the R15 input: the hold is read at the
// instant the decision is being made, so a promise logged earlier in the same
// run holds the next action on that item.
func TestFactsCarryAnActivePromiseHold(t *testing.T) {
	promises := promise.NewStore()
	item := riskitem.RiskItem{ID: "ri_held", Source: riskitem.SourceOverdueInvoice, SourceID: "inv_disputed"}

	if got := factsFor(newManifestIndex(disputedManifest()), promises, item, 1, factsNow); !got.PromiseHoldUntil.IsZero() {
		t.Errorf("an item with no promise carries a hold until %s", got.PromiseHoldUntil)
	}

	record := promise.New(item.ID, factsNow, 72*time.Hour, "the customer said Friday")
	if err := promises.Log(record); err != nil {
		t.Fatal(err)
	}

	facts := factsFor(newManifestIndex(disputedManifest()), promises, item, 1, factsNow)
	if !facts.PromiseHoldUntil.Equal(record.HoldUntil()) {
		t.Errorf("hold until %s, want %s", facts.PromiseHoldUntil, record.HoldUntil())
	}
	// The hold is over at exactly HoldUntil, which promise.Record documents as
	// an exclusive end.
	after := factsFor(newManifestIndex(disputedManifest()), promises, item, 1, record.HoldUntil())
	if !after.PromiseHoldUntil.IsZero() {
		t.Error("the hold was still reported at the instant it expired")
	}
}

// TestUnknownItemsCarryNoManifestFacts. An account with debt from an earlier
// run has items this manifest never seeded, and the honest answer for one is
// that there is no record either way rather than a claim that nothing is
// disputed.
func TestUnknownItemsCarryNoManifestFacts(t *testing.T) {
	item := riskitem.RiskItem{
		ID:          "ri_unknown",
		Source:      riskitem.SourceUnpaidOrder,
		SourceID:    "order_from_some_other_run",
		RootOrderID: "order_from_some_other_run",
	}
	facts := factsFor(newManifestIndex(disputedManifest()), promise.NewStore(), item, 3, factsNow)

	if facts.Disputed {
		t.Error("an item the manifest does not know came back disputed")
	}
	if facts.SourceStatus != "" {
		t.Errorf("source status = %q, want empty for an item with no manifest row", facts.SourceStatus)
	}
	if facts.TouchNo != 3 {
		t.Errorf("touch no = %d, want the one the caller counted", facts.TouchNo)
	}
}

// TestSimulatedAgeOnlyAnswersForItemsTheManifestKnows.
func TestSimulatedAgeOnlyAnswersForItemsTheManifestKnows(t *testing.T) {
	manifest := disputedManifest()
	manifest.Items[0].SimulatedAtRiskSince = 1780812000
	index := newManifestIndex(manifest)

	known := riskitem.RiskItem{SourceID: "inv_disputed", RootOrderID: "order_from_disputed_invoice"}
	at, ok := simulatedAtRiskSince(index, known)
	if !ok || at != 1780812000 {
		t.Errorf("simulatedAtRiskSince = %d, %v, want the manifest's instant", at, ok)
	}

	unknown := riskitem.RiskItem{SourceID: "order_elsewhere"}
	if _, ok := simulatedAtRiskSince(index, unknown); ok {
		t.Error("an item the manifest does not know was given a simulated age")
	}

	// A manifest row with no simulated instant on it answers no rather than
	// answering zero, which would date the debt to 1970 and make it instantly
	// overdue.
	noAge := riskitem.RiskItem{SourceID: "order_plain"}
	if _, ok := simulatedAtRiskSince(index, noAge); ok {
		t.Error("a manifest row with no simulated instant was treated as having one")
	}
}
