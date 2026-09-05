package riskitem_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
)

// This file is the compile-time contract for every downstream work package. It
// wires the whole shape once, against stubs that live here and nowhere else:
// three detectors, one queue that collapses on DedupeKey, one policy decision,
// one intervention, one audit trail.
//
// It proves that the interfaces compose. It tests no business logic, and it
// must not grow any: the real detectors, the real policy gate and the real
// intervention engine are tested in the packages that own them. What breaks
// here is a change to the contract itself.

// fixtureDetector is a Detector that replays golden fixtures.
type fixtureDetector struct {
	name  string
	items []riskitem.RiskItem
	err   error
}

func (d fixtureDetector) Name() string { return d.name }

func (d fixtureDetector) Detect(context.Context) ([]riskitem.RiskItem, error) {
	return d.items, d.err
}

// stubIntervention is an Intervention that makes no call and records what it
// was asked to do. It enforces the two refusals the contract requires of every
// real implementation: an unlawful action, and a notify action on an item with
// no contact channel.
type stubIntervention struct {
	at      time.Time
	applied []string
}

func (s *stubIntervention) Apply(_ context.Context, item riskitem.RiskItem, action string) (riskitem.Outcome, error) {
	s.applied = append(s.applied, action)
	out := riskitem.Outcome{Action: action, At: s.at}

	if !riskitem.IsLawfulAction(action) {
		out.Err = "action is not in the lawful set"
		return out, nil
	}

	switch action {
	case riskitem.ActionNotifyEmail, riskitem.ActionNotifySMS, riskitem.ActionResendLink:
		if !item.Customer.HasContactChannel() {
			out.Err = "item has no contact channel"
			return out, nil
		}
		out.Accepted = true
		out.Observable = "email_status:sent"
		out.Handle = item.PayHandle
	case riskitem.ActionCreatePaymentLink:
		out.Accepted = true
		out.Observable = "plink_status:created"
		out.Handle = riskitem.PayHandle{
			Kind: riskitem.HandleKindPaymentLink,
			URL:  "https://rzp.io/rzp/W2ToDkL",
			ID:   "plink_TYEx2CoiwQvYow",
		}
	case riskitem.ActionEscalate:
		out.Accepted = true
		out.Observable = "escalated:queued"
	default:
		out.Accepted = true
	}
	return out, nil
}

// decide is the stub policy gate. The real one lives in internal/policy and
// reads far more than this. The only thing this has to get right is the shape:
// one item in, one action out of the lawful set.
func decide(item riskitem.RiskItem) string {
	if !item.Customer.HasContactChannel() {
		return riskitem.ActionEscalate
	}
	if item.PayHandle.Kind == riskitem.HandleKindNone {
		return riskitem.ActionCreatePaymentLink
	}
	return riskitem.ActionResendLink
}

// collapse is the queue. One debt is one entry, keyed on DedupeKey, and the
// first sighting wins so the sweep is deterministic. Order is preserved so an
// audit trail reads in the order the detectors ran.
func collapse(batches ...[]riskitem.RiskItem) []riskitem.RiskItem {
	seen := make(map[string]bool)
	var queue []riskitem.RiskItem
	for _, batch := range batches {
		for _, item := range batch {
			key := item.DedupeKey()
			if seen[key] {
				continue
			}
			seen[key] = true
			queue = append(queue, item)
		}
	}
	return queue
}

// The three stubs satisfy the contract at compile time. A change to either
// interface fails to build here before it fails anywhere downstream.
var (
	_ riskitem.Detector     = fixtureDetector{}
	_ riskitem.Intervention = (*stubIntervention)(nil)
)

func TestContractComposesFromDetectorToAuditTrail(t *testing.T) {
	ctx := context.Background()
	failed, _ := loadFixture(t, failedPaymentFixture)
	unpaid, _ := loadFixture(t, unpaidOrderFixture)
	invoice, _ := loadFixture(t, overdueInvoiceFixture)

	// The order the invoice minted, seen by the unpaid-order detector as
	// well. It is the same debt as invoice, reached the other way.
	mintedOrder := riskitem.RiskItem{
		ID:              riskitem.NewID(riskitem.SourceUnpaidOrder, invoice.RootOrderID),
		Source:          riskitem.SourceUnpaidOrder,
		SourceID:        invoice.RootOrderID,
		RootOrderID:     invoice.RootOrderID,
		Customer:        invoice.Customer,
		AmountPaise:     invoice.AmountPaise,
		AmountDuePaise:  invoice.AmountDuePaise,
		Currency:        invoice.Currency,
		AtRiskSince:     invoice.AtRiskSince,
		Signal:          riskitem.Signal{Attempts: 0},
		PayHandle:       riskitem.PayHandle{},
		AmountPaidPaise: invoice.AmountPaidPaise,
	}

	detectors := []riskitem.Detector{
		fixtureDetector{name: "failed-payments", items: []riskitem.RiskItem{failed}},
		fixtureDetector{name: "unpaid-orders", items: []riskitem.RiskItem{unpaid, mintedOrder}},
		fixtureDetector{name: "overdue-invoices", items: []riskitem.RiskItem{invoice}},
	}

	var batches [][]riskitem.RiskItem
	for _, detector := range detectors {
		if detector.Name() == "" {
			t.Fatal("a detector reported an empty name, and the audit trail keys on it")
		}
		items, err := detector.Detect(ctx)
		if err != nil {
			t.Fatalf("%s.Detect: %v", detector.Name(), err)
		}
		batches = append(batches, items)
	}

	queue := collapse(batches...)

	// Four sightings, three debts: the minted order and the invoice are one.
	if len(queue) != 3 {
		t.Fatalf("queue holds %d items, want 3", len(queue))
	}
	for _, item := range queue {
		if item.ID == "" || item.Source == "" || item.SourceID == "" {
			t.Errorf("queue holds an item with no identity: %+v", item)
		}
	}

	engine := &stubIntervention{at: time.Unix(1788586217, 0).UTC()}

	var trail []riskitem.Outcome
	for _, item := range queue {
		action := decide(item)
		if !riskitem.IsLawfulAction(action) {
			t.Fatalf("the policy stub returned %q, which is not in the lawful set", action)
		}
		outcome, err := engine.Apply(ctx, item, action)
		if err != nil {
			t.Fatalf("Apply(%s, %s): %v", item.ID, action, err)
		}
		if outcome.Action != action {
			t.Errorf("Outcome.Action = %q, want %q", outcome.Action, action)
		}
		if outcome.At.IsZero() {
			t.Errorf("Outcome for %s has no timestamp", item.ID)
		}
		trail = append(trail, outcome)
	}

	if len(trail) != len(queue) {
		t.Fatalf("the audit trail has %d rows for %d items", len(trail), len(queue))
	}

	// The unpaid order carried no customer, so the only lawful move on it was
	// to hand it to a person. Nothing guessed an address.
	var escalated int
	for i, outcome := range trail {
		if queue[i].Customer.HasContactChannel() {
			continue
		}
		escalated++
		if outcome.Action != riskitem.ActionEscalate {
			t.Errorf("item %s has no contact channel and the action was %q", queue[i].ID, outcome.Action)
		}
	}
	if escalated != 1 {
		t.Errorf("%d items had no contact channel, want 1", escalated)
	}
}

// TestApplyRefusesAnUnlawfulActionWithAnAuditRow pins the shape of a refusal.
// An implementation returns an Outcome for every call, including one it will
// not make, so the audit trail is never missing a row for a decision.
func TestApplyRefusesAnUnlawfulActionWithAnAuditRow(t *testing.T) {
	item, _ := loadFixture(t, failedPaymentFixture)
	engine := &stubIntervention{at: time.Unix(1788586217, 0).UTC()}

	outcome, err := engine.Apply(context.Background(), item, "retry_same_instrument")
	if err != nil {
		t.Fatalf("Apply returned an error for a refusal: %v", err)
	}
	if outcome.Accepted {
		t.Error("Outcome.Accepted is true for an action outside the lawful set")
	}
	if outcome.Err == "" {
		t.Error("a refusal carried no reason")
	}
	if outcome.Action != "retry_same_instrument" {
		t.Errorf("Outcome.Action = %q, and the audit row has to name what was refused", outcome.Action)
	}
}

// TestOutcomeRoundTripsIntoTheAuditTrail checks that an Outcome survives JSON,
// which is the form the audit trail is written in.
func TestOutcomeRoundTripsIntoTheAuditTrail(t *testing.T) {
	item, _ := loadFixture(t, overdueInvoiceFixture)
	engine := &stubIntervention{at: time.Unix(1788586217, 0).UTC()}

	outcome, err := engine.Apply(context.Background(), item, riskitem.ActionResendLink)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !outcome.Accepted || outcome.Observable == "" {
		t.Fatalf("Outcome = %+v, want an accepted call with an observable", outcome)
	}

	encoded, err := json.Marshal(outcome)
	if err != nil {
		t.Fatalf("encode outcome: %v", err)
	}
	var decoded riskitem.Outcome
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode outcome: %v", err)
	}
	if !decoded.At.Equal(outcome.At) {
		t.Errorf("At = %v, want %v", decoded.At, outcome.At)
	}
	decoded.At = outcome.At
	if decoded != outcome {
		t.Errorf("Outcome does not round-trip\n before: %+v\n  after: %+v", outcome, decoded)
	}
}
