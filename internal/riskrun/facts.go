package riskrun

import (
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/lopster568/rzp-recovery-agent/internal/promise"
	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
	"github.com/lopster568/rzp-recovery-agent/internal/seed"
)

// manifestIndex is the seedbook manifest keyed by every Razorpay id that can
// lead back to one of its items.
//
// An item is registered under its own id and, for an invoice, under the order
// that issuing it minted. That second key is what makes the index reachable
// from all three detectors: an issued invoice arrives as an overdue-invoice
// sighting under inv_, as an unpaid-order sighting under the order_ it minted,
// and, once somebody fails a checkout against it, as a failed-payment sighting
// whose SourceID is a pay_ id nothing seeded and whose RootOrderID is that same
// order. Without the order key the failed-payment sighting would carry no
// manifest facts at all, which is the sighting the demo cares most about.
type manifestIndex struct {
	byID map[string]seed.Item
}

func newManifestIndex(m seed.Manifest) *manifestIndex {
	idx := &manifestIndex{byID: make(map[string]seed.Item, len(m.Items)*2)}
	for _, item := range m.Items {
		if item.ID != "" {
			idx.byID[item.ID] = item
		}
		if item.OrderID != "" {
			idx.byID[item.OrderID] = item
		}
	}
	return idx
}

// lookup returns the manifest item behind a sighting.
//
// The sighting's own SourceID is tried first and the root order second, so an
// invoice sighting resolves to the invoice rather than to whatever else shares
// its order. A sighting the manifest does not know is not an error: an account
// with history from an earlier run has debts this manifest never seeded, and
// the honest answer for one is that there are no manifest facts about it.
func (idx *manifestIndex) lookup(item riskitem.RiskItem) (seed.Item, bool) {
	if found, ok := idx.byID[item.SourceID]; ok {
		return found, true
	}
	if item.RootOrderID != "" {
		if found, ok := idx.byID[item.RootOrderID]; ok {
			return found, true
		}
	}
	return seed.Item{}, false
}

// factsFor builds the policy facts an item cannot carry itself.
//
// Three of the four come from somewhere the detector cannot see. TouchNo is the
// run's own ledger. PromiseHoldUntil is the promise ledger, read at the instant
// the decision is being made rather than at the start of the run, so a promise
// this run just logged holds the next action on the same item. Disputed and
// SourceStatus are the manifest's.
//
// Disputed is the one that matters most and it is the one Razorpay cannot
// answer. There is no disputed field on an invoice, an order, or a payment: a
// contested debt is a thing a person recorded somewhere else, which is why
// internal/mcpserver documents that R13 cannot fire through it. The seedbook
// manifest is that somewhere else for a seeded run, so R13 fires here, on the
// items the seeder deliberately flagged. A run over an account this manifest
// did not seed gets false, which is not a claim that nothing is disputed: it is
// the honest report that this run has no record either way.
//
// SourceStatus is the last status the seed run observed, not a fresh read. It
// is enough for R4's terminal-status arm on an item the manifest cancelled or
// let expire, and it is stale by construction for anything that moved since. A
// caller that needs the current status re-reads it, which is what risk-poll is.
func factsFor(idx *manifestIndex, promises *promise.Store, item riskitem.RiskItem, touchNo int, now time.Time) policy.Facts {
	facts := policy.Facts{TouchNo: touchNo}

	if hold, ok := promises.ActiveHold(item.ID, now); ok {
		facts.PromiseHoldUntil = hold.HoldUntil()
	}
	if seeded, ok := idx.lookup(item); ok {
		facts.Disputed = seeded.Flags.Disputed
		facts.SourceStatus = seeded.Status
	}
	return facts
}

// simulatedAtRiskSince returns the instant the manifest says this debt should
// look as though it started, and whether the manifest had one.
//
// It exists because nothing in Razorpay's API can backdate an invoice. A
// seedbook run creates every item now and writes the age it meant the item to
// have into the manifest, per seed.AgeBucket. A gate reading Razorpay's own
// issued_at therefore sees a whole seeded book that is minutes old, and R11
// denies all of it as not yet due, which is a correct answer to the wrong
// question: the demo is about a book of aged receivables and the ages are the
// manifest's to state.
//
// It is on by default (the -simulate-age flag turns it off, since a seeded
// book is unusable without it) and every row a run writes says which clock it used, so
// a reading of a run can tell a real age from a stated one. Nothing is
// simulated for an item the manifest does not know.
func simulatedAtRiskSince(idx *manifestIndex, item riskitem.RiskItem) (int64, bool) {
	seeded, ok := idx.lookup(item)
	if !ok || seeded.SimulatedAtRiskSince <= 0 {
		return 0, false
	}
	return seeded.SimulatedAtRiskSince, true
}
