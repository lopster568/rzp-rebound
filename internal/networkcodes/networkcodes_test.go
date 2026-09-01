package networkcodes_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/networkcodes"
)

func TestVisaCategory1IsTheTwelveReconstructedCodes(t *testing.T) {
	want := []string{"04", "07", "12", "14", "15", "41", "43", "46", "57", "R0", "R1", "R3"}

	got := networkcodes.VisaCategory1()

	if !slices.Equal(got, want) {
		t.Errorf("VisaCategory1() = %v, want %v", got, want)
	}
	for _, code := range want {
		if !networkcodes.IsVisaCategory1(code) {
			t.Errorf("IsVisaCategory1(%q) is false, and the reconstructed table lists it", code)
		}
	}
}

// TestTheCategory1ListSaysItIsReconstructed is the correction that matters most
// in this package.
//
// Bulletin AI10325 establishes the four-category system, defines Category 1 as
// declines an issuer will never approve and which a merchant may not reattempt,
// and moves four codes out of Category 1. It does not print the complete
// Category 1 code list. The twelve codes above come from processor
// reconstructions of Visa's member-gated table, and a package that presented
// them as read off the bulletin would be citing a document that does not say
// what it is being cited for.
func TestTheCategory1ListSaysItIsReconstructed(t *testing.T) {
	if !networkcodes.VisaCategory1IsReconstructed {
		t.Error("VisaCategory1IsReconstructed is false, and the bulletin does not enumerate the list")
	}
	source := networkcodes.Sources()["visa-category-1"]
	if !strings.Contains(source, "reconstruct") {
		t.Errorf("the visa-category-1 source does not say the list is reconstructed: %q", source)
	}
	// The bulletin itself is a separate source, for the claims it does support.
	if _, ok := networkcodes.Sources()["visa-category-system"]; !ok {
		t.Error("Sources() has no entry for the bulletin itself, which is what the four-category system comes from")
	}
}

// TestCode14MayNeverBeReattemptedOnTheSameAccountNumber pins the one code the
// bulletin singles out. 14 sits in Category 1 and in Category 3, and the
// bulletin says it must never be reattempted with the same account number.
func TestCode14MayNeverBeReattemptedOnTheSameAccountNumber(t *testing.T) {
	if !networkcodes.IsVisaCategory1("14") {
		t.Error("visa 14 is not Category 1")
	}
	if !networkcodes.NeverRetrySameAccountNumber(networkcodes.Visa, "14") {
		t.Error("visa 14 may be reattempted on the same account number, and the bulletin says it may not")
	}
}

// TestCodesMovedOutOfCategory1AreNotCategory1 is the reason this package exists
// rather than a list pasted into a policy comment. Blog posts written before
// AI10325 took effect on 2021-04-17 still name these four as never-retry codes,
// and a reader who found one of those posts has to be able to find the
// correction here rather than concluding this list is short.
func TestCodesMovedOutOfCategory1AreNotCategory1(t *testing.T) {
	for _, code := range []string{"03", "62", "78", "93"} {
		if networkcodes.IsVisaCategory1(code) {
			t.Errorf("IsVisaCategory1(%q) is true, and AI10325 moved it to Category 2 effective 2021-04-17", code)
		}
		if !networkcodes.WasVisaCategory1BeforeApril2021(code) {
			t.Errorf("WasVisaCategory1BeforeApril2021(%q) is false, so the correction is not recorded anywhere", code)
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

// TestVisaReattemptCapIsFifteenInThirtyDaysForCategory2 pins the cap and the
// category it applies to. The bulletin states it for Category 2, and this
// project had it as Categories 2 and 3 until a first-hand read of the PDF on
// 2026-09-01 corrected it.
func TestVisaReattemptCapIsFifteenInThirtyDaysForCategory2(t *testing.T) {
	if networkcodes.VisaCategory2ReattemptCap != 15 {
		t.Errorf("VisaCategory2ReattemptCap = %d, want 15", networkcodes.VisaCategory2ReattemptCap)
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
	for _, want := range []string{"visa-category-system", "visa-category-1", "mastercard-merchant-advice", "visa-reattempt-cap"} {
		if _, ok := networkcodes.Sources()[want]; !ok {
			t.Errorf("Sources() has no entry for %s", want)
		}
	}
}

// TestMastercardRetryScheduleIsTheSevenStepLadder pins the only scheme-native
// retry timing anyone publishes.
//
// Merchant advice codes 24 through 30 are Mastercard's own resubmission
// schedule, and its first rung is one hour. That matters here because
// R2-COOLDOWN is 30 seconds and is declared a configured choice: this ladder is
// the nearest published thing to it, it is two orders of magnitude away, and
// carrying it makes the gap visible instead of asserted.
func TestMastercardRetryScheduleIsTheSevenStepLadder(t *testing.T) {
	want := []int{1, 24, 48, 96, 144, 192, 240}

	got := networkcodes.MastercardRetryScheduleHours()

	if !slices.Equal(got, want) {
		t.Errorf("MastercardRetryScheduleHours() = %v, want %v", got, want)
	}
	if len(got) == 0 || got[0] != networkcodes.MastercardShortestRetryIntervalHours {
		t.Errorf("MastercardShortestRetryIntervalHours = %d, and the ladder starts at %v",
			networkcodes.MastercardShortestRetryIntervalHours, got)
	}
	if _, ok := networkcodes.Sources()["mastercard-retry-schedule"]; !ok {
		t.Error("Sources() has no entry for the retry schedule")
	}
}

// TestMerchantAdviceCode03CarriesAResubmissionFee records the one thing that
// makes MAC 03 more than advice. Mastercard assesses a fee for each
// authorization request resubmitted after a MAC 03 decline inside 30 days, so
// ignoring it costs money rather than only breaking a rule.
func TestMerchantAdviceCode03CarriesAResubmissionFee(t *testing.T) {
	if !networkcodes.MastercardDoNotTryAgainIsFeeBearing {
		t.Error("MastercardDoNotTryAgainIsFeeBearing is false, and a resubmission after MAC 03 is charged for")
	}
	if networkcodes.MastercardResubmissionFeeWindowDays != 30 {
		t.Errorf("MastercardResubmissionFeeWindowDays = %d, want 30",
			networkcodes.MastercardResubmissionFeeWindowDays)
	}
}
