package policy

import (
	"maps"
	"slices"
	"time"
)

// The three sources a risk item can come from.
//
// They are the same strings as riskitem.Source, and they are redeclared here
// rather than imported so that the engine in policy.go depends on nothing but
// the standard library, internal/classify, internal/clock, and internal/quiet.
// The adapter in riskitem.go is the one file in this package that imports the
// contract, and TestPolicyAndRiskItemShareOneSourceVocabulary checks the two
// sets agree.
const (
	SourceFailedPayment  = "failed_payment"
	SourceUnpaidOrder    = "unpaid_order"
	SourceOverdueInvoice = "overdue_invoice"
)

// The lawful action set, redeclared here for the same reason the sources are.
//
// There is no retry action, and there is no place to put one. Unattended
// re-presentment of a one-off Indian payment has no lawful counterpart on any
// rail: RBI's Authentication Directions require an additional factor on
// effectively every one-off domestic digital payment, and no
// merchant-initiated transaction concept exists outside a registered e-mandate.
// docs/INDIA-CONSTRAINTS-AUDIT.md section 1 has the sourcing, all of it
// labelled REPORTED. R7 escalates an action outside this set rather than
// refusing it quietly, so a caller that invents one is visible in the trail.
const (
	ActionNotifyEmail       = "notify_email"
	ActionNotifySMS         = "notify_sms"
	ActionCreatePaymentLink = "create_payment_link"
	ActionResendLink        = "resend_link"
	ActionLogPromise        = "log_promise"
	ActionEscalate          = "escalate"
	ActionCancelWriteOff    = "cancel_write_off"
	ActionDoNothing         = "do_nothing"
)

// Per-source defaults.
//
// Every number below is a configured choice and none of them is cited. They
// are merchant cadence, not regulation: nothing in the RBI, NPCI, or TRAI
// material this project read publishes how long to wait before chasing an
// unpaid order, or how many times. ConfiguredChoices says so per rule, and
// this block says so per number.
const (
	// GraceFailedPayment is zero. A payment Razorpay reports as failed is
	// already at risk at the instant it failed: there is no window in which
	// the customer might still be completing it, because the attempt is over.
	GraceFailedPayment = 0

	// GraceUnpaidOrder is the abandoned-cart window. An order created a minute
	// ago is a customer still at the checkout, not a debt, and chasing it is
	// both useless and rude.
	GraceUnpaidOrder = time.Hour

	// GraceOverdueInvoice stands in for payment terms. Razorpay's invoice
	// object does carry a due date, and when a detector reports one the caller
	// should hand it to the engine as AtRiskSince plus zero grace rather than
	// leaning on this. Three days is what an invoice with no terms on it gets.
	GraceOverdueInvoice = 72 * time.Hour

	// MaxTouchesFailedPayment, MaxTouchesUnpaidOrder, and
	// MaxTouchesOverdueInvoice are the lifetime outbound contact caps.
	//
	// An invoice gets one more than the other two because an issued invoice is
	// a debt the customer has already acknowledged, where an abandoned cart is
	// not. That is a judgment about which customers will not mind a fourth
	// message, and it is nobody's published number.
	MaxTouchesFailedPayment  = 3
	MaxTouchesUnpaidOrder    = 3
	MaxTouchesOverdueInvoice = 4

	// CooldownFailedPayment, CooldownUnpaidOrder, and CooldownOverdueInvoice
	// are the minimum interval between two contacts about one item.
	//
	// These are at the scale a person would recognise as a follow-up. The
	// number they replaced was 30 seconds, which was a retry rate: it bounded
	// how fast the old build re-presented a card, and re-presenting a card is
	// the thing this engine no longer does. Thirty seconds between two
	// messages to one customer about one debt is harassment, and carrying the
	// constant across the pivot would have made it that.
	CooldownFailedPayment  = 24 * time.Hour
	CooldownUnpaidOrder    = 24 * time.Hour
	CooldownOverdueInvoice = 48 * time.Hour
)

// SourceParams is everything the rules read that varies by source.
//
// It is a table rather than a switch inside Evaluate on purpose. A fourth
// source arrives as a row, the golden matrix grows by one source's worth of
// cells, and no rule body changes. A rule that branched on the source string
// would put the same decision in three places and let them drift.
type SourceParams struct {
	// Source is the source this row is for, so a row handed around on its own
	// still says what it describes.
	Source string
	// Grace is how long after AtRiskSince the item is not yet chaseable. R11.
	Grace time.Duration
	// MaxTouches is the lifetime cap on outbound contacts. R1.
	MaxTouches int
	// Cooldown is the minimum interval between two contacts. R2.
	Cooldown time.Duration
	// RequiresSignal says an item from this source must carry failure evidence
	// to be actionable. R7.
	//
	// It is true only for a failed payment, where the absence of a failure
	// reason means the detector could not read why the payment failed, and
	// acting on a failure nobody can name is exactly what R7 refuses. An
	// unpaid order has no failure to carry: nothing went wrong, the customer
	// walked away, and an empty Signal there is the truth rather than a gap.
	RequiresSignal bool
}

// sourceParams is the table. Every known source has exactly one row.
var sourceParams = map[string]SourceParams{
	SourceFailedPayment: {
		Source:         SourceFailedPayment,
		Grace:          GraceFailedPayment,
		MaxTouches:     MaxTouchesFailedPayment,
		Cooldown:       CooldownFailedPayment,
		RequiresSignal: true,
	},
	SourceUnpaidOrder: {
		Source:     SourceUnpaidOrder,
		Grace:      GraceUnpaidOrder,
		MaxTouches: MaxTouchesUnpaidOrder,
		Cooldown:   CooldownUnpaidOrder,
	},
	SourceOverdueInvoice: {
		Source:     SourceOverdueInvoice,
		Grace:      GraceOverdueInvoice,
		MaxTouches: MaxTouchesOverdueInvoice,
		Cooldown:   CooldownOverdueInvoice,
	},
}

// Sources returns the sources the table knows, in evaluation-stable order.
func Sources() []string {
	return []string{SourceFailedPayment, SourceUnpaidOrder, SourceOverdueInvoice}
}

// SourceParamsTable returns a copy of the per-source table, so a run manifest
// can record the cadence it actually ran under.
func SourceParamsTable() map[string]SourceParams { return maps.Clone(sourceParams) }

// KnownSource reports whether source has a row in the table. R7 escalates
// anything else.
func KnownSource(source string) bool {
	_, ok := sourceParams[source]
	return ok
}

// lawfulActions is the closed action set. It mirrors riskitem.LawfulActions.
var lawfulActions = []string{
	ActionNotifyEmail,
	ActionNotifySMS,
	ActionCreatePaymentLink,
	ActionResendLink,
	ActionLogPromise,
	ActionEscalate,
	ActionCancelWriteOff,
	ActionDoNothing,
}

// LawfulActions returns the closed action set, in declaration order. The
// returned slice is a copy.
func LawfulActions() []string { return slices.Clone(lawfulActions) }

// IsLawfulAction reports whether action is one this engine may reason about.
// Anything else, including any spelling of a retry, is not.
func IsLawfulAction(action string) bool { return slices.Contains(lawfulActions, action) }

// IsContactAction reports whether the action sends a message to a customer or creates the
// thing a message is sent about.
//
// Creating a payment link is in here even though minting a link sends nothing,
// because a link is created to be sent and the rules that bound how often a
// message is sent to a customer would be trivially escapable if link
// creation sat outside them.
func IsContactAction(action string) bool {
	switch action {
	case ActionNotifyEmail, ActionNotifySMS, ActionCreatePaymentLink, ActionResendLink:
		return true
	default:
		return false
	}
}

// IsNotifyAction reports whether the action sends a message. R6 and R12 read
// it.
//
// It changed meaning at the pivot. It used to mean "asks the customer for a
// reauthentication or a new card", which were the two notify-shaped actions in
// the retry engine. It now means "a message goes out", which is the question
// quiet hours and a send rate actually ask. Creating a payment link is not in
// here: minting a link at 02:00 wakes nobody.
func IsNotifyAction(action string) bool {
	switch action {
	case ActionNotifyEmail, ActionNotifySMS, ActionResendLink:
		return true
	default:
		return false
	}
}

// IsSafeAction reports whether the action can be taken on an item this engine
// has decided it must not chase.
//
// Escalating, doing nothing, and recording what a customer said are the three
// things that are always still available. None of them touches money and none
// of them reaches a customer, so a rule that escalates a contact action has
// nothing to say about them. Without this, an engine whose fail-closed verdict
// is "escalate" could never approve an escalation, because every escalating
// rule would fire on the escalation itself.
//
// Writing an item off is deliberately not in here. It is terminal and it is
// about money, which is why R3 gates it at any amount.
func IsSafeAction(action string) bool {
	switch action {
	case ActionEscalate, ActionDoNothing, ActionLogPromise:
		return true
	default:
		return false
	}
}

// terminalSourceStatuses are the states a source resource can be in where
// there is nothing left to collect.
//
// Razorpay spells the first one with two l's on payment links and invoices.
// The American spelling is carried as well because a fixture, a hand-written
// test, or a future endpoint using it should not silently fall through into
// "this invoice is still live, go chase it".
var terminalSourceStatuses = []string{"cancelled", "canceled", "expired"}

// IsTerminalSourceStatus reports whether a source resource in this state is
// past chasing. R4 reads it.
func IsTerminalSourceStatus(status string) bool {
	return slices.Contains(terminalSourceStatuses, status)
}

// TerminalSourceStatuses returns the statuses R4 treats as past chasing.
func TerminalSourceStatuses() []string { return slices.Clone(terminalSourceStatuses) }
