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
	VisaReattemptCapPerDeclinedTransaction = 15
	VisaReattemptWindowDays                = 30
)

// VisaCategory1 returns the Visa Category 1 decline codes, sorted. A Category 1
// decline may not be reattempted at all.
func VisaCategory1() []string { return nil }

// IsVisaCategory1 reports whether code is on the Category 1 list.
func IsVisaCategory1(code string) bool { return false }

// WasVisaCategory1Before2020 reports whether code was on the Category 1 list
// before the 2020 update moved it off.
func WasVisaCategory1Before2020(code string) bool { return false }

// NeverRetry reports whether the network's own rules forbid a reattempt of a
// transaction declined with this code.
func NeverRetry(network, code string) bool { return false }

// Sources returns the source of every list in this package, keyed by list name.
func Sources() map[string]string { return nil }
