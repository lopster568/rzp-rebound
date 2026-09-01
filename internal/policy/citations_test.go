package policy_test

import (
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/networkcodes"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
)

// The phase 5 rule, tested rather than asserted: every default in this package
// is either a cited industry value or a configured choice that says so.

// TestAmountCeilingDefaultIsTheRBIEMandateThreshold pins the one default that
// moved in phase 5, and it moved for a reason the old value did not have.
//
// 450000 paise was chosen after a fake-layer run showed 400000 escalating a
// quarter of the batch, which is a threshold tuned to a result. 1500000 paise
// is Rs 15,000, the amount above which the RBI e-mandate framework requires an
// additional factor of authentication. That is a real Indian payments line
// between "unattended is fine" and "a human has to be in the loop", which is
// exactly the question R3 asks.
func TestAmountCeilingDefaultIsTheRBIEMandateThreshold(t *testing.T) {
	const rupees15000InPaise = 1500000

	if policy.DefaultAmountCeilingPaise != rupees15000InPaise {
		t.Errorf("DefaultAmountCeilingPaise = %d, want %d, the RBI e-mandate additional-factor threshold",
			policy.DefaultAmountCeilingPaise, rupees15000InPaise)
	}

	p := policy.New(policy.Config{}, nil)
	req := policy.Request{
		OrderID: "order_ceiling", Action: policy.ActionRetrySameInstrument,
		Class: classify.RetryEligible, AttemptNo: 1,
	}

	req.AmountPaise = rupees15000InPaise
	if got := p.Evaluate(policy.State{}, req); got.Verdict != policy.VerdictAllow {
		t.Errorf("at the threshold: verdict = %s under %s, want allow", got.Verdict, got.RuleID)
	}

	req.AmountPaise = rupees15000InPaise + 100
	got := p.Evaluate(policy.State{}, req)
	if got.Verdict != policy.VerdictEscalate || got.RuleID != policy.RuleAmountCeiling {
		t.Errorf("one rupee above the threshold: verdict = %s under %s, want escalate under %s",
			got.Verdict, got.RuleID, policy.RuleAmountCeiling)
	}
}

// TestMaxAttemptsSitsUnderTheVisaReattemptCap is what makes R1's relabelling
// checkable. The rule now describes itself as a conservative merchant policy
// under the Visa 15-in-30 cap, and that sentence is only true while the
// constant is at or under the cap.
func TestMaxAttemptsSitsUnderTheVisaReattemptCap(t *testing.T) {
	if policy.DefaultMaxAttemptsPerOrder > networkcodes.VisaCategory2ReattemptCap {
		t.Errorf("DefaultMaxAttemptsPerOrder = %d, above the published Visa Category 2 cap of %d per %d rolling days",
			policy.DefaultMaxAttemptsPerOrder,
			networkcodes.VisaCategory2ReattemptCap,
			networkcodes.VisaReattemptWindowDays)
	}
	if policy.DefaultMaxAttemptsPerOrder != 3 {
		t.Errorf("DefaultMaxAttemptsPerOrder = %d, want 3: phase 5 relabelled this number and did not move it",
			policy.DefaultMaxAttemptsPerOrder)
	}
}

// TestTheTwoIntervalDefaultsAreDeclaredConfiguredChoices is the honest half.
//
// There is no citable industry value at seconds scale for either interval. The
// shortest scheme-native retry interval anyone publishes is the Mastercard
// automated-clearing schedule, which starts at one hour, and the closest Indian
// regulatory number is the RBI e-mandate 24 hour pre-debit notice, which is a
// notice floor and not a rate. Attaching either to a 30 second constant would
// be worse than leaving it unlabelled, because it would look checked.
//
// So the two intervals are declared, by name, as configured choices, and this
// test fails if a future phase quietly promotes one to a cited value without
// removing it from the list.
func TestTheTwoIntervalDefaultsAreDeclaredConfiguredChoices(t *testing.T) {
	choices := policy.ConfiguredChoices()

	for _, rule := range []string{policy.RuleCooldown, policy.RuleNotifyRate} {
		reason, ok := choices[rule]
		if !ok {
			t.Errorf("%s is not declared a configured choice, and no industry source publishes an interval at this scale", rule)
			continue
		}
		if reason == "" {
			t.Errorf("%s is declared a configured choice with no reason on it", rule)
		}
	}
	if policy.DefaultCooldown != 30*time.Second {
		t.Errorf("DefaultCooldown = %s, want 30s: phase 5 relabelled this number and did not move it", policy.DefaultCooldown)
	}
}

// TestEveryRuleDeclaresItsCitationStatus is the exit criterion as a test. Every
// rule id is either cited, with a source, or a configured choice, with a
// reason. There is no third bucket and no rule may be in neither.
func TestEveryRuleDeclaresItsCitationStatus(t *testing.T) {
	cited := policy.CitedValues()
	choices := policy.ConfiguredChoices()

	for _, rule := range policy.RuleIDs() {
		source, isCited := cited[rule]
		reason, isChoice := choices[rule]
		switch {
		case isCited && isChoice:
			t.Errorf("%s is declared both cited and a configured choice", rule)
		case !isCited && !isChoice:
			t.Errorf("%s declares no citation status", rule)
		case isCited && source == "":
			t.Errorf("%s is declared cited with an empty source", rule)
		case isChoice && reason == "":
			t.Errorf("%s is declared a configured choice with an empty reason", rule)
		}
	}
	for _, rule := range []string{policy.RuleMaxAttempts, policy.RuleAmountCeiling, policy.RuleNeverRetryClass} {
		if _, ok := cited[rule]; !ok {
			t.Errorf("%s is not cited, and phase 5 gave it a source", rule)
		}
	}
}
