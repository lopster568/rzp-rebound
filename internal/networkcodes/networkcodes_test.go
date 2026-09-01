package networkcodes_test

import (
	"slices"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/networkcodes"
)

func TestVisaCategory1IsTheTwelvePublishedCodes(t *testing.T) {
	want := []string{"04", "07", "12", "14", "15", "41", "43", "46", "57", "R0", "R1", "R3"}

	got := networkcodes.VisaCategory1()

	if !slices.Equal(got, want) {
		t.Errorf("VisaCategory1() = %v, want %v", got, want)
	}
	for _, code := range want {
		if !networkcodes.IsVisaCategory1(code) {
			t.Errorf("IsVisaCategory1(%q) is false, and the bulletin lists it", code)
		}
	}
}

// TestCodesMovedOutOfCategory1In2020AreNotCategory1 is the reason this package
// exists rather than a list pasted into a policy comment. Blog posts written
// before the 2020 update still name these four as never-retry codes, and a
// reader who found one of those posts has to be able to find the correction
// here rather than concluding this list is short.
func TestCodesMovedOutOfCategory1In2020AreNotCategory1(t *testing.T) {
	for _, code := range []string{"03", "62", "78", "93"} {
		if networkcodes.IsVisaCategory1(code) {
			t.Errorf("IsVisaCategory1(%q) is true, and the 2020 update moved it out of Category 1", code)
		}
		if !networkcodes.WasVisaCategory1Before2020(code) {
			t.Errorf("WasVisaCategory1Before2020(%q) is false, so the correction is not recorded anywhere", code)
		}
	}
}

func TestMastercardMerchantAdviceCode03IsNeverRetry(t *testing.T) {
	if !networkcodes.NeverRetry(networkcodes.Mastercard, networkcodes.MastercardDoNotTryAgain) {
		t.Errorf("NeverRetry(%q, %q) is false, and the merchant advice code means do not try again",
			networkcodes.Mastercard, networkcodes.MastercardDoNotTryAgain)
	}
	// 21 is "payment cancellation", which is a different instruction.
	if networkcodes.NeverRetry(networkcodes.Mastercard, "21") {
		t.Error("mastercard advice code 21 came back never-retry, and only 03 carries that instruction")
	}
}

// TestNeverRetryIsFalseForAnUnknownNetworkAndCode keeps the predicate from
// treating a code string as evidence on its own. "04" is Visa Category 1 and
// it is not a Mastercard instruction, and a caller that does not know the
// network gets no verdict rather than Visa's.
func TestNeverRetryIsFalseForAnUnknownNetworkAndCode(t *testing.T) {
	if networkcodes.NeverRetry("", "04") {
		t.Error("an empty network with a Visa Category 1 code came back never-retry")
	}
	if networkcodes.NeverRetry("rupay", "04") {
		t.Error("a network this package publishes no list for came back never-retry")
	}
	if !networkcodes.NeverRetry(networkcodes.Visa, "04") {
		t.Error("Visa 04 is not never-retry, and it is the first code on the Category 1 list")
	}
}

func TestVisaReattemptCapIsFifteenInThirtyDays(t *testing.T) {
	if networkcodes.VisaReattemptCapPerDeclinedTransaction != 15 {
		t.Errorf("VisaReattemptCapPerDeclinedTransaction = %d, want 15",
			networkcodes.VisaReattemptCapPerDeclinedTransaction)
	}
	if networkcodes.VisaReattemptWindowDays != 30 {
		t.Errorf("VisaReattemptWindowDays = %d, want 30", networkcodes.VisaReattemptWindowDays)
	}
}

// TestEveryListNamesItsSource is the phase 5 rule applied to this package: a
// code list with no source is an invented list wearing a network's name.
func TestEveryListNamesItsSource(t *testing.T) {
	for name, source := range networkcodes.Sources() {
		if source == "" {
			t.Errorf("%s has no source", name)
		}
	}
	for _, want := range []string{"visa-category-1", "mastercard-merchant-advice", "visa-reattempt-cap"} {
		if _, ok := networkcodes.Sources()[want]; !ok {
			t.Errorf("Sources() has no entry for %s", want)
		}
	}
}
