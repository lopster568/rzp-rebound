package policy_test

import (
	"strings"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/lopster568/rzp-recovery-agent/internal/quiet"
)

// The honesty rule, tested rather than asserted: every default in this package
// is either a cited industry value or a configured choice that says so.

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

	// And nothing may be declared for a rule that does not exist. A stale
	// entry left behind by a deleted rule is a citation nobody can check
	// against a decision, which is how the retry engine's dead Visa entries
	// survived four phases.
	for _, m := range []map[string]string{cited, choices} {
		for rule := range m {
			if !contains(policy.RuleIDs(), rule) {
				t.Errorf("%q has a citation status and is not a rule this engine has", rule)
			}
		}
	}
}

// TestTheRetiredCitationsAreGone is the pivot's citation work as a test.
//
// Three sources were cited in this package on 2026-09-01 and all three are
// wrong for what this engine now does. The Visa 15-in-30 reattempt cap and the
// Mastercard merchant advice code 03 list both bound merchant-initiated
// re-presentment of a card authorization, which has no lawful counterpart for a
// one-off Indian payment: RBI's Authentication Directions require an additional
// factor on effectively every one-off domestic digital payment, and this engine
// has no action that re-presents anything. The RBI e-mandate Rs 15,000
// threshold is a true statement about mandates attached to a rule about sending
// somebody a payment link, which is a category error.
//
// docs/INDIA-CONSTRAINTS-AUDIT.md sections 1 and 2 are the finding. This test
// fails if any of the three comes back into a declaration.
func TestTheRetiredCitationsAreGone(t *testing.T) {
	retired := []string{
		"visa", "ai10325", "category 1", "category 2",
		"mastercard", "merchant advice", "mac 03", "mac-03",
		"reattempt", "re-presentment", "represent",
		"e-mandate", "emandate", "additional factor",
	}

	for rule, source := range policy.CitedValues() {
		lower := strings.ToLower(source)
		for _, phrase := range retired {
			// The configured-choice reasons are allowed to name a retired
			// citation, because saying why a number is not cited any more is
			// the whole point of the entry. A cited value is not.
			if strings.Contains(lower, phrase) {
				t.Errorf("%s cites %q, which the pivot retired", rule, phrase)
			}
		}
	}
}

// TestTheAmountCeilingIsAConfiguredChoiceAndSaysSo pins the entry that mattered
// most.
//
// The number did not move. Rs 15,000 is still a reasonable answer to "above
// what outstanding amount should a person look at this first", which is a real
// operator question. What moved is that it no longer claims a regulator said
// so.
func TestTheAmountCeilingIsAConfiguredChoiceAndSaysSo(t *testing.T) {
	const rupees15000InPaise = 1500000

	if policy.DefaultAmountCeilingPaise != rupees15000InPaise {
		t.Errorf("DefaultAmountCeilingPaise = %d, want %d: the pivot relabelled this number and did not move it",
			policy.DefaultAmountCeilingPaise, rupees15000InPaise)
	}
	if _, ok := policy.CitedValues()[policy.RuleHumanApproval]; ok {
		t.Error("the human-approval ceiling is declared a cited value, and no regulator publishes it")
	}
	reason, ok := policy.ConfiguredChoices()[policy.RuleHumanApproval]
	if !ok {
		t.Fatal("the human-approval ceiling declares no citation status")
	}
	if !strings.Contains(strings.ToLower(reason), "operator-chosen") {
		t.Errorf("the ceiling's reason does not say it is operator-chosen: %q", reason)
	}
}

// TestQuietHoursIsConfiguredAndCitesNoRegulatedBand is the other entry the
// pivot had to get right on the first pass.
//
// It is tempting to cite TRAI here, because TRAI's DLT regime is real, does
// govern commercial SMS in India, and does restrict delivery hours for some
// traffic. No primary TRAI document was read by this project, and whether a
// payment reminder is promotional or transactional under it is unresolved.
// Naming a document is not the same as the document saying it, and a band
// presented as regulated when it is a merchant's own politeness rule is exactly
// the failure the honesty rules in CLAUDE.md were written after.
func TestQuietHoursIsConfiguredAndCitesNoRegulatedBand(t *testing.T) {
	if _, ok := policy.CitedValues()[policy.RuleQuietHours]; ok {
		t.Error("quiet hours is declared a cited value, and no primary source was read for the band")
	}
	reason, ok := policy.ConfiguredChoices()[policy.RuleQuietHours]
	if !ok {
		t.Fatal("quiet hours declares no citation status")
	}
	if !strings.Contains(reason, "NEEDS-VERIFICATION") {
		t.Errorf("the quiet-hours reason carries no NEEDS-VERIFICATION label: %q", reason)
	}
	if !strings.Contains(strings.ToLower(reason), "trai") {
		t.Errorf("the quiet-hours reason does not name the regime it is shaped by: %q", reason)
	}

	// The band itself is the default in internal/quiet, and it is 09:00 to
	// 21:00. This fails if somebody moves it to a number that looks read off a
	// regulation without the citation catching up.
	want := quiet.At(9, 0, 21, 0)
	if got := quiet.DefaultWindow(); got != want {
		t.Errorf("the default contact band is %s, want %s", got, want)
	}
}

// TestTheCadenceNumbersAreDeclaredConfiguredChoices is the honest half.
//
// No regulator or scheme publishes how often a merchant may message a customer
// about one debt, how many times in total, or how long to wait before starting.
// All four rules that hold such a number say so by name, and this fails if a
// future change quietly promotes one to a cited value.
func TestTheCadenceNumbersAreDeclaredConfiguredChoices(t *testing.T) {
	choices := policy.ConfiguredChoices()

	for _, rule := range []string{
		policy.RuleMaxTouches,
		policy.RuleCooldown,
		policy.RuleNotifyRate,
		policy.RuleNotYetDue,
		policy.RulePromiseHold,
		policy.RuleDisputed,
	} {
		reason, ok := choices[rule]
		if !ok {
			t.Errorf("%s is not declared a configured choice, and nothing published governs it", rule)
			continue
		}
		if reason == "" {
			t.Errorf("%s is declared a configured choice with no reason on it", rule)
		}
	}

	// The contact cooldown is at the scale of a follow-up rather than at the
	// scale of a retry. This is the one cadence number whose old value would
	// still compile and would still pass every other test in this package.
	if policy.DefaultCooldown != 24*time.Hour {
		t.Errorf("DefaultCooldown = %s, want 24h", policy.DefaultCooldown)
	}
	if policy.DefaultCooldown < time.Hour {
		t.Error("the contact cooldown is at machine scale, which is what it was when it bounded a retry")
	}
}

// TestOnlyTheRulesWithARealSourceAreCited is the count.
//
// Two, and both of them are Razorpay's own documented vocabulary rather than a
// regulation. That is a smaller number than the retry engine claimed and it is
// the honest one: every regulatory citation this package used to carry was
// about a mechanism the engine no longer has.
func TestOnlyTheRulesWithARealSourceAreCited(t *testing.T) {
	cited := policy.CitedValues()
	want := []string{policy.RuleNeverContact, policy.RuleUnknownFailClosed}

	if len(cited) != len(want) {
		t.Errorf("%d rules are cited, want %d: %v", len(cited), len(want), keys(cited))
	}
	for _, rule := range want {
		if _, ok := cited[rule]; !ok {
			t.Errorf("%s is not cited, and Razorpay's documented reason list is its source", rule)
		}
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
