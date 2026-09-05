package policy_test

import (
	"slices"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
)

// The engine mirrors riskitem's two vocabularies rather than importing them, so
// that Evaluate can be driven from a table with no fixtures. The cost of
// mirroring is drift, and these two tests are what stop it. They are the reason
// the adapter file exists at all.

func TestPolicyAndRiskItemShareOneSourceVocabulary(t *testing.T) {
	mine := policy.Sources()
	theirs := []string{
		string(riskitem.SourceFailedPayment),
		string(riskitem.SourceUnpaidOrder),
		string(riskitem.SourceOverdueInvoice),
	}

	if !slices.Equal(slices.Sorted(slices.Values(mine)), slices.Sorted(slices.Values(theirs))) {
		t.Errorf("policy knows sources %v, riskitem has %v", mine, theirs)
	}
	for _, source := range theirs {
		if !policy.KnownSource(source) {
			t.Errorf("riskitem has source %q and the policy has no rules for it", source)
		}
	}
}

func TestPolicyAndRiskItemShareOneActionSurface(t *testing.T) {
	mine, theirs := policy.LawfulActions(), riskitem.LawfulActions()

	if !slices.Equal(slices.Sorted(slices.Values(mine)), slices.Sorted(slices.Values(theirs))) {
		t.Fatalf("policy knows actions %v, the frozen contract has %v", mine, theirs)
	}
	for _, action := range theirs {
		if !policy.IsLawfulAction(action) {
			t.Errorf("%q is in the frozen set and policy.IsLawfulAction says otherwise", action)
		}
	}

	// Every lawful action is in exactly one of the three predicate buckets, or
	// a rule that reads one of them silently does not apply to it. Writing an
	// item off is the one action in none of them: it is not a contact, not a
	// notification, and not safe, which is why R3 gates it on its own.
	for _, action := range theirs {
		buckets := 0
		for _, in := range []bool{policy.IsContactAction(action), policy.IsSafeAction(action)} {
			if in {
				buckets++
			}
		}
		switch {
		case action == riskitem.ActionCancelWriteOff && buckets != 0:
			t.Errorf("%q is classified as a contact or a safe action, and it is neither", action)
		case action != riskitem.ActionCancelWriteOff && buckets != 1:
			t.Errorf("%q is in %d of the contact and safe buckets, want exactly 1", action, buckets)
		}
		if policy.IsNotifyAction(action) && !policy.IsContactAction(action) {
			t.Errorf("%q sends a message and is not a contact action", action)
		}
	}
}

// TestRequestFromCopiesTheItemAndDerivesNothing is the adapter's contract. Every
// field it fills comes from the item or from the facts the caller supplies, and
// none of it is inferred.
func TestRequestFromCopiesTheItemAndDerivesNothing(t *testing.T) {
	atRisk := time.Date(2026, 9, 1, 9, 30, 0, 0, time.UTC)
	item := riskitem.RiskItem{
		ID:              riskitem.NewID(riskitem.SourceOverdueInvoice, "inv_probe"),
		Source:          riskitem.SourceOverdueInvoice,
		SourceID:        "inv_probe",
		RootOrderID:     "order_probe",
		Customer:        riskitem.Customer{Name: "A Customer", Email: "someone@example.test"},
		AmountPaise:     500000,
		AmountPaidPaise: 100000,
		AmountDuePaise:  400000,
		Currency:        "INR",
		AtRiskSince:     atRisk.Unix(),
		Signal:          riskitem.Signal{EmailStatus: "sent"},
	}
	facts := policy.Facts{
		TouchNo:          2,
		PromiseHoldUntil: atRisk.Add(72 * time.Hour),
		Disputed:         true,
		SourceStatus:     "issued",
	}

	req := policy.RequestFrom(item, riskitem.ActionResendLink, facts)

	for _, tc := range []struct {
		field     string
		got, want any
	}{
		{"RiskItemID", req.RiskItemID, item.ID},
		{"Source", req.Source, string(riskitem.SourceOverdueInvoice)},
		{"Action", req.Action, riskitem.ActionResendLink},
		{"AmountPaise", req.AmountPaise, int64(500000)},
		{"AmountDuePaise", req.AmountDuePaise, int64(400000)},
		{"HasEmail", req.HasEmail, true},
		{"HasContact", req.HasContact, false},
		{"SourceStatus", req.SourceStatus, "issued"},
		{"Disputed", req.Disputed, true},
		{"TouchNo", req.TouchNo, 2},
		{"Class", req.Class, classify.Unclassified},
		{"SignalPresent", req.SignalPresent, false},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %v, want %v", tc.field, tc.got, tc.want)
		}
	}
	if !req.AtRiskSince.Equal(atRisk) {
		t.Errorf("AtRiskSince = %s, want %s", req.AtRiskSince, atRisk)
	}
	if !req.PromiseHoldUntil.Equal(facts.PromiseHoldUntil) {
		t.Errorf("PromiseHoldUntil = %s, want %s", req.PromiseHoldUntil, facts.PromiseHoldUntil)
	}

	// The deprecated Request fields are untouched by the adapter. Nothing new
	// should be written through them.
	if req.OrderID != "" || req.AttemptNo != 0 {
		t.Errorf("the adapter filled the deprecated fields: OrderID=%q AttemptNo=%d", req.OrderID, req.AttemptNo)
	}

	// The class is the caller's to supply. A policy that classified would be
	// making the judgment it is supposed to be gating.
	classified := policy.RequestFromClassified(item, riskitem.ActionResendLink, classify.NeverRetry, facts)
	if classified.Class != classify.NeverRetry {
		t.Errorf("Class = %v, want the supplied NeverRetry", classified.Class)
	}
}

// TestHasSignalReadsOnlyTheFailureFields is the arm of R7 that separates
// "nothing went wrong" from "something went wrong and nothing could read it".
//
// Method, Attempts, and the two notification-status fields are carried on
// ordinary items that never failed. Counting any of them would make every
// unpaid order look like it had failure evidence and would defeat the rule.
func TestHasSignalReadsOnlyTheFailureFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		signal riskitem.Signal
		want   bool
	}{
		{"an empty signal", riskitem.Signal{}, false},
		{"a failure reason", riskitem.Signal{FailureReason: "payment_timed_out"}, true},
		{"a failure code alone", riskitem.Signal{FailureCode: "BAD_REQUEST_ERROR"}, true},
		{"an instrument and an attempt count", riskitem.Signal{Method: "upi", Attempts: 1}, false},
		{"a notification status", riskitem.Signal{EmailStatus: "sent", SmsStatus: "sent"}, false},
		{"a failure step with no reason or code", riskitem.Signal{FailureStep: "payment_authorization"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := policy.HasSignal(tc.signal); got != tc.want {
				t.Errorf("HasSignal(%+v) = %t, want %t", tc.signal, got, tc.want)
			}
		})
	}
}

// TestAnItemWithNoAtRiskInstantArrivesWithAZeroTime covers the conversion the
// adapter does. riskitem carries Unix seconds and zero means "not reported",
// which must not become 1970 on the request or R11's fail-closed arm never
// fires.
func TestAnItemWithNoAtRiskInstantArrivesWithAZeroTime(t *testing.T) {
	item := riskitem.RiskItem{
		ID:          "ri_no_instant",
		Source:      riskitem.SourceUnpaidOrder,
		AmountPaise: 1000,
		Customer:    riskitem.Customer{Email: "someone@example.test"},
	}

	req := policy.RequestFrom(item, riskitem.ActionCreatePaymentLink, policy.Facts{TouchNo: 1})
	if !req.AtRiskSince.IsZero() {
		t.Fatalf("AtRiskSince = %s, want the zero time", req.AtRiskSince)
	}

	p := policy.New(testConfig(), clock.NewFake(start))
	got := p.Evaluate(policy.State{}, req)
	if got.Verdict != policy.VerdictDeny || got.RuleID != policy.RuleNotYetDue {
		t.Errorf("an unpaid order with no at-risk instant: got %s/%s, want %s/%s",
			got.Verdict, got.RuleID, policy.VerdictDeny, policy.RuleNotYetDue)
	}
}

// TestAnAdaptedItemFlowsThroughTheEngine is the end-to-end shape: a real item,
// a real action, a real verdict, with nothing hand-built in between.
func TestAnAdaptedItemFlowsThroughTheEngine(t *testing.T) {
	p := policy.New(testConfig(), clock.NewFake(start))

	item := riskitem.RiskItem{
		ID:             riskitem.NewID(riskitem.SourceOverdueInvoice, "inv_flow"),
		Source:         riskitem.SourceOverdueInvoice,
		SourceID:       "inv_flow",
		Customer:       riskitem.Customer{Email: "someone@example.test"},
		AmountPaise:    120000,
		AmountDuePaise: 120000,
		Currency:       "INR",
		AtRiskSince:    start.Add(-10 * 24 * time.Hour).Unix(),
	}

	req := policy.RequestFrom(item, riskitem.ActionNotifyEmail, policy.Facts{TouchNo: 1, SourceStatus: "issued"})
	if got := p.Evaluate(policy.State{}, req); got.Verdict != policy.VerdictAllow {
		t.Errorf("an ordinary overdue invoice: %s (%s), %s", got.Verdict, got.RuleID, got.Reason)
	}

	// Take the customer's email away and the same item escalates rather than
	// having a channel guessed for it.
	item.Customer = riskitem.Customer{}
	req = policy.RequestFrom(item, riskitem.ActionNotifyEmail, policy.Facts{TouchNo: 1, SourceStatus: "issued"})
	got := p.Evaluate(policy.State{}, req)
	if got.Verdict != policy.VerdictEscalate || got.RuleID != policy.RuleNoContactChannel {
		t.Errorf("an invoice with no contact detail: got %s/%s, want %s/%s",
			got.Verdict, got.RuleID, policy.VerdictEscalate, policy.RuleNoContactChannel)
	}
}
