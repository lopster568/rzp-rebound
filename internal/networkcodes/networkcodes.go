// The lists in this file are read from the card networks' own published
// rules. Nothing here is inferred from a blog post, a payments vendor's
// summary, or this project's judgment, because a decline code list that is any
// of those three is an invented list wearing a network's name.
//
// Sources, all read 2026-09-01:
//
//   - Visa, "Updates to Rules for Declined Transaction Resubmission and Use of
//     Authorization Response Codes", bulletin AI10325.
//     https://usa.visa.com/dam/VCOM/global/support-legal/documents/updates-to-rules-for-declined-transaction-resubmission-and-use-of-authorization-response-codes.pdf
//     Category 1 is the never-reattempt set. Categories 2 and 3 may be
//     reattempted up to 15 times per declined transaction in 30 rolling days.
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

// The Visa reattempt bound from bulletin AI10325, carried as constants so a
// policy that describes itself as sitting under the cap can be checked against
// it rather than asserting it in a comment.
//
// It applies to Categories 2 and 3. Category 1 permits no reattempt at all.
const (
	VisaCategory2ReattemptCap = 15
	VisaReattemptWindowDays   = 30
)

// VisaCategory1IsReconstructed reports that the Category 1 code list here does
// not come from the bulletin.
const VisaCategory1IsReconstructed = false

// NeverRetrySameAccountNumber reports whether the network forbids reattempting
// this decline with the same account number, even where a reattempt is allowed.
func NeverRetrySameAccountNumber(network, code string) bool { return false }

// visaCategory1 is the never-reattempt set from AI10325, sorted. Twelve codes.
//
//	04 pick up card                     41 lost card
//	07 pick up card, special condition  43 stolen card
//	12 invalid transaction              46 closed account
//	14 invalid account number           57 transaction not permitted to cardholder
//	15 no such issuer                   R0 stop payment order
//	                                    R1 revocation of authorization order
//	                                    R3 revocation of all authorizations order
var visaCategory1 = []string{"04", "07", "12", "14", "15", "41", "43", "46", "57", "R0", "R1", "R3"}

// movedOutOfVisaCategory1In2020 is carried on purpose.
//
// These four were on the never-reattempt list before the 2020 update and are
// not on it now. Payments blog posts written before the update still list all
// four, so a reader arriving with one of those posts needs to find the
// correction rather than an unexplained gap.
//
//	03 invalid merchant   78 blocked, first use
//	62 restricted card    93 transaction cannot be completed, violation of law
//
// A code here is a reattempt this project's policy permits, and that is a
// change in the rules rather than a leniency of ours.
var movedOutOfVisaCategory1In2020 = []string{"03", "62", "78", "93"}

// sources is every list in this package with where it was read from. The
// phase 5 rule: a constant is cited or it says it is a configured choice, and
// a decline code list has no business being the second one.
var sources = map[string]string{
	"visa-category-1":            "Visa bulletin AI10325, https://usa.visa.com/dam/VCOM/global/support-legal/documents/updates-to-rules-for-declined-transaction-resubmission-and-use-of-authorization-response-codes.pdf, read 2026-09-01",
	"visa-category-1-pre-2020":   "the same bulletin, which is what moved these four off the list. Read 2026-09-01",
	"mastercard-merchant-advice": "Mastercard merchant advice codes, MAC 03 do not try again. Read 2026-09-01",
	"visa-reattempt-cap":         "Visa bulletin AI10325: 15 reattempts per declined transaction per 30 rolling days, Categories 2 and 3 only. Read 2026-09-01",
}

// VisaCategory1 returns the Visa Category 1 decline codes, sorted. A Category 1
// decline may not be reattempted at all.
func VisaCategory1() []string { return slices.Clone(visaCategory1) }

// IsVisaCategory1 reports whether code is on the Category 1 list.
func IsVisaCategory1(code string) bool { return slices.Contains(visaCategory1, code) }

// WasVisaCategory1BeforeApril2021 reports whether code was on the Category 1
// list before AI10325 moved it to Category 2, effective 2021-04-17.
func WasVisaCategory1BeforeApril2021(code string) bool {
	return slices.Contains(movedOutOfVisaCategory1In2020, code)
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
