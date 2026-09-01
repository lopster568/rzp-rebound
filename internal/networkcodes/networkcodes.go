// The rules in this file come from the card networks. Every entry says which
// document it comes from and, where the document does not actually contain it,
// says that too.
//
// Sources, all read 2026-09-01:
//
//   - Visa, "Updates to Rules for Declined Transaction Resubmission and Use of
//     Authorization Response Codes", bulletin AI10325, dated 2020-09-03,
//     effective 2021-04-17.
//     https://usa.visa.com/dam/VCOM/global/support-legal/documents/updates-to-rules-for-declined-transaction-resubmission-and-use-of-authorization-response-codes.pdf
//
//     What the bulletin does establish: four decline categories; Category 1 is
//     a decline the issuer will never approve and which a merchant is not
//     permitted to reattempt; Category 2 may be reattempted up to 15 times in
//     30 days; response codes 03, 62, 78, and 93 move out of Category 1 into
//     Category 2 effective 2021-04-17; and code 14 sits in both Category 1 and
//     Category 3 and must never be reattempted with the same account number.
//
//     What it does not do is print the complete Category 1 code list. The
//     twelve codes in visaCategory1 below are a reconstruction of Visa's
//     member-gated table, published by processors. VisaCategory1IsReconstructed
//     is true and the source string says so, because citing the bulletin for a
//     list the bulletin does not contain would be the failure this whole phase
//     is about.
//
//   - Mastercard merchant advice codes. MAC 03 is "do not try again".
//
// The lists are per network on purpose. An ISO 8583 response code is only
// meaningful with the network that issued it, and "04" is a Visa Category 1
// code while it is nothing in particular as a Mastercard advice code. A caller
// that does not know the network gets no verdict here.
package networkcodes

import (
	"maps"
	"slices"
)

// Networks this package publishes a list for. A network not on this list
// returns no verdict rather than borrowing another network's.
const (
	Visa       = "visa"
	Mastercard = "mastercard"
)

// MastercardDoNotTryAgain is merchant advice code 03, "do not try again".
const MastercardDoNotTryAgain = "03"

// MastercardDoNotTryAgainIsFeeBearing reports that a resubmission after a MAC
// 03 decline is charged for.
//
// This is what makes MAC 03 more than advice. Mastercard assesses a fee for
// each authorization request resubmitted following a MAC 03 decline inside the
// window below, so ignoring the code costs money rather than only breaking a
// rule. It is the mirror image of the Visa side: Visa caps how many times you
// may reattempt, Mastercard charges you for reattempting what it told you not
// to.
const MastercardDoNotTryAgainIsFeeBearing = true

// MastercardResubmissionFeeWindowDays is the window that fee applies over.
const MastercardResubmissionFeeWindowDays = 30

// mastercardRetryScheduleHours is the resubmission ladder behind merchant
// advice codes 24 through 30, in hours.
//
//	24  1 hour     28  6 days
//	25  24 hours   29  8 days
//	26  2 days     30  10 days
//	27  4 days
//
// These are Mastercard-use-only codes, so a merchant does not choose one. The
// ladder is here because it is the only scheme-native retry timing anybody
// publishes, and this project needs something real to measure a configured
// interval against. R2-COOLDOWN is 30 seconds; the shortest rung here is one
// hour. policy.ConfiguredChoices says the interval is ours and this is the
// number that shows how far from any published one it sits.
var mastercardRetryScheduleHours = []int{1, 24, 48, 96, 144, 192, 240}

// MastercardShortestRetryIntervalHours is the first rung of that ladder.
const MastercardShortestRetryIntervalHours = 1

// MastercardRetryScheduleHours returns the published resubmission ladder behind
// merchant advice codes 24 through 30, in hours.
func MastercardRetryScheduleHours() []int { return slices.Clone(mastercardRetryScheduleHours) }

// The Visa reattempt bound from bulletin AI10325, carried as constants so a
// policy that describes itself as sitting under the cap can be checked against
// it rather than asserting it in a comment.
//
// The bulletin states the cap for Category 2. Category 1 permits no reattempt
// at all. This project first recorded it as Categories 2 and 3, from a summary
// rather than from the document, and a first-hand read on 2026-09-01 corrected
// it.
const (
	VisaCategory2ReattemptCap = 15
	VisaReattemptWindowDays   = 30
)

// VisaCategory1IsReconstructed reports that the Category 1 code list in this
// package does not come from the bulletin. It is true, and it is a constant
// rather than a comment so that a test can hold it.
const VisaCategory1IsReconstructed = true

// visaNeverRetrySameAccountNumber is the one code-level rule AI10325 states
// outright: 14, invalid account number, is in Category 1 and in Category 3, and
// must never be reattempted with the same account number.
//
// It is a separate list from visaCategory1 because it is a different claim with
// a different source. This one is in the bulletin.
var visaNeverRetrySameAccountNumber = []string{"14"}

// NeverRetrySameAccountNumber reports whether the network forbids reattempting
// this decline with the same account number, even where a reattempt would
// otherwise be allowed.
func NeverRetrySameAccountNumber(network, code string) bool {
	if network != Visa {
		return false
	}
	return slices.Contains(visaNeverRetrySameAccountNumber, code) || IsVisaCategory1(code)
}

// visaCategory1 is the never-reattempt set, sorted. Twelve codes.
//
// RECONSTRUCTED. AI10325 defines the category and does not enumerate it; this
// list is a processor reconstruction of Visa's member-gated table. See the
// package comment and VisaCategory1IsReconstructed.
//
//	04 pick up card                     41 lost card
//	07 pick up card, special condition  43 stolen card
//	12 invalid transaction              46 closed account
//	14 invalid account number           57 transaction not permitted to cardholder
//	15 no such issuer                   R0 stop payment order
//	                                    R1 revocation of authorization order
//	                                    R3 revocation of all authorizations order
var visaCategory1 = []string{"04", "07", "12", "14", "15", "41", "43", "46", "57", "R0", "R1", "R3"}

// movedOutOfVisaCategory1 is carried on purpose, and unlike the list above it
// is in the bulletin.
//
// AI10325 moves these four from Category 1 to Category 2, effective 2021-04-17.
// Payments blog posts written before that date still list all four as
// never-retry codes, so a reader arriving with one of those posts needs to find
// the correction rather than an unexplained gap.
//
//	03 invalid merchant   78 blocked, first use
//	62 restricted card    93 transaction cannot be completed, violation of law
//
// A code here is a reattempt this project's policy permits, and that is a
// change in the rules rather than a leniency of ours.
var movedOutOfVisaCategory1 = []string{"03", "62", "78", "93"}

// sources is every list in this package with where it was read from. The
// phase 5 rule: a constant is cited or it says it is a configured choice, and
// a decline code list has no business being the second one.
var sources = map[string]string{
	"visa-category-system":       "Visa bulletin AI10325, dated 2020-09-03, effective 2021-04-17, https://usa.visa.com/dam/VCOM/global/support-legal/documents/updates-to-rules-for-declined-transaction-resubmission-and-use-of-authorization-response-codes.pdf, read as a PDF 2026-09-01. It establishes the four categories and defines Category 1 as a decline the issuer will never approve, which a merchant may not reattempt.",
	"visa-category-1":            "RECONSTRUCTED, not primary. AI10325 defines Category 1 and does not enumerate it. The twelve codes are a processor reconstruction of Visa's member-gated table, of the kind Qualpay and other PSPs publish. Read 2026-09-01.",
	"visa-category-1-moved-out":  "AI10325 itself, which moves 03, 62, 78, and 93 from Category 1 to Category 2 effective 2021-04-17. Primary. Read 2026-09-01.",
	"visa-never-same-account":    "AI10325 itself: code 14 is in Category 1 and Category 3 and must never be reattempted with the same account number. Primary. Read 2026-09-01.",
	"mastercard-merchant-advice": "Mastercard merchant advice codes, MAC 03 do not try again, and a fee assessed for each authorization request resubmitted following a MAC 03 decline within 30 days. Verified through TabaPay's PSP documentation at developers.tabapay.com/docs/merchant-advice-code-mac, read 2026-09-01. A PSP restating a scheme rule, not the scheme's own publication.",
	"mastercard-retry-schedule":  "Merchant advice codes 24 through 30, Mastercard's own resubmission ladder: 1 hour, 24 hours, 2 days, 4 days, 6 days, 8 days, 10 days. Mastercard use only. Same TabaPay source, read 2026-09-01. It is the only scheme-native retry timing anyone publishes and it is what R2-COOLDOWN is measured against rather than derived from.",
	"visa-reattempt-cap":         "AI10325: Category 2 declines may be reattempted up to 15 times in 30 days. Primary. Read 2026-09-01. This project first recorded it as Categories 2 and 3, from a summary rather than the document.",
}

// VisaCategory1 returns the Visa Category 1 decline codes, sorted. A Category 1
// decline may not be reattempted at all.
func VisaCategory1() []string { return slices.Clone(visaCategory1) }

// IsVisaCategory1 reports whether code is on the Category 1 list.
func IsVisaCategory1(code string) bool { return slices.Contains(visaCategory1, code) }

// WasVisaCategory1BeforeApril2021 reports whether code was on the Category 1
// list before AI10325 moved it to Category 2, effective 2021-04-17.
func WasVisaCategory1BeforeApril2021(code string) bool {
	return slices.Contains(movedOutOfVisaCategory1, code)
}

// NeverRetry reports whether the network's own rules forbid a reattempt of a
// transaction declined with this code.
//
// An unrecognised network returns false, and that is not a fail-open. A code
// with no network behind it carries no instruction: "04" is Visa Category 1
// and it is not a Mastercard advice code, so answering for a network this
// package has no list for would be answering with Visa's rules under another
// name. The fail-closed default for a failure nothing recognises lives in
// internal/classify, which is where a class is decided.
func NeverRetry(network, code string) bool {
	switch network {
	case Visa:
		return IsVisaCategory1(code)
	case Mastercard:
		return code == MastercardDoNotTryAgain
	default:
		return false
	}
}

// Sources returns the source of every list in this package, keyed by list name.
func Sources() map[string]string { return maps.Clone(sources) }
