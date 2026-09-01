package classify

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/lopster568/rzp-recovery-agent/internal/networkcodes"
)

// Class is what a payment failure means for recovery.
type Class int

const (
	// Unclassified is the zero value and the fail-closed default. A failure
	// nothing recognises lands here and is never retry eligible.
	Unclassified Class = iota
	// TransientRetryEligible is a failure on the gateway or network side that
	// a repeat of the same attempt can clear on its own.
	TransientRetryEligible
	// RetryEligible is a failure whose cause can change without the customer
	// doing anything different, such as an account balance.
	RetryEligible
	// ReauthRequired means the customer has to come back and authenticate
	// again. Repeating the charge without them is not an option.
	ReauthRequired
	// NewInstrumentRequired means this card cannot pay. Recovery needs a
	// different instrument.
	NewInstrumentRequired
	// NeverRetry means no further attempt on this payment is allowed.
	NeverRetry
)

var classNames = map[Class]string{
	Unclassified:           "unclassified",
	TransientRetryEligible: "transient_retry_eligible",
	RetryEligible:          "retry_eligible",
	ReauthRequired:         "reauth_required",
	NewInstrumentRequired:  "new_instrument_required",
	NeverRetry:             "never_retry",
}

// cardReasons is the documented live-mode failure reason list for card
// payments, read from https://razorpay.com/docs/errors/payments/cards/ on
// 2026-09-01. Fifteen reasons.
//
// The reason strings are Razorpay's and are cited. The class each one maps to
// is this project's judgment and is cited nowhere, which docs/EVIDENCE.md says
// in as many words. Adopting a documented vocabulary does not make the mapping
// documented.
var cardReasons = map[string]Class{
	// The gateway, the bank, or the clock. Not the customer, and not the
	// instrument. The same attempt can work on a repeat.
	"payment_timed_out":       TransientRetryEligible,
	"gateway_technical_error": TransientRetryEligible,
	"bank_technical_error":    TransientRetryEligible,

	// The card is fine and the customer is present. What blocked the charge
	// is a balance, and a balance changes.
	"insufficient_funds": RetryEligible,

	// The customer has to be back in the flow. A silent recharge is not an
	// option for any of these: one failed authentication, one was abandoned
	// partway, and one needs three digits only the cardholder has.
	"authentication_failed": ReauthRequired,
	"payment_cancelled":     ReauthRequired,
	"incorrect_cvv":         ReauthRequired,

	// This instrument cannot complete this payment, so repeating it repeats
	// the failure. Recovery needs a different one, which is a payment link and
	// not a silent retry.
	//
	// card_expired and debit_instrument_blocked sit here rather than under
	// never_retry deliberately. Both forbid another attempt on the same
	// instrument, which is what IsRetryEligible reports. What separates the two
	// classes is whether asking the customer for a different instrument is
	// allowed, and for an expired card it plainly is: that is recoverable
	// revenue behind one message. DECISIONS.md entry 2 has the reasoning.
	"card_declined":                     NewInstrumentRequired,
	"card_disabled_for_online_payments": NewInstrumentRequired,
	"card_not_enrolled":                 NewInstrumentRequired,
	"card_expired":                      NewInstrumentRequired,
	"debit_instrument_inactive":         NewInstrumentRequired,
	"debit_instrument_blocked":          NewInstrumentRequired,
	"transaction_limit_exceeded":        NewInstrumentRequired,

	// The one reason that forbids every action, not just a retry. Contacting a
	// customer a risk engine has flagged is itself an action, so this escalates
	// to a person under R4 rather than raising a payment link.
	ReasonPaymentRiskCheckFailed: NeverRetry,
}

// upiReasons is the documented live-mode failure reason list for UPI payments,
// read from https://razorpay.com/docs/errors/payments/upi/ on 2026-09-01.
// Eight reasons, three of which also appear on the card list.
var upiReasons = map[string]Class{
	// The bank, the beneficiary bank, or the VPA directory. A resolution
	// failure is a lookup that did not answer, which is not a statement about
	// the handle itself, so it is transient rather than a dead instrument.
	"bank_technical_error":  TransientRetryEligible,
	"credit_failed":         TransientRetryEligible,
	"vpa_resolution_failed": TransientRetryEligible,
	"payment_timed_out":     TransientRetryEligible,

	"insufficient_funds": RetryEligible,

	// The customer was asked and did not answer, or answered no. Either way
	// they have to be back in the flow.
	"payment_collect_request_expired": ReauthRequired,
	"payment_declined":                ReauthRequired,

	// The handle itself does not resolve to an account that can pay.
	"invalid_vpa": NewInstrumentRequired,
}

// testModeCardTableReasons is the vocabulary the Razorpay test-card page
// carries, which is not the live-mode vocabulary.
//
// Two spellings differ and both are kept: the singular insufficient_fund, and
// card_number_invalid, which is not on the live-mode card list at all. Every
// batch manifest committed under results/batches/ carries them, the fake
// gateway seeds them, and dropping them to tidy a documentation list would make
// the published runs unreplayable.
//
// This table is separate from the two above so a reader can tell which
// vocabulary a reason belongs to. DocumentedReasons never returns it.
var testModeCardTableReasons = map[string]Class{
	"insufficient_fund":   RetryEligible,
	"card_number_invalid": NewInstrumentRequired,
}

// reasonTables is every table Classify consults when it has no method, in a
// fixed order. Agreement across them is checked rather than assumed: see
// ClassifyAcross.
var reasonTables = []map[string]Class{cardReasons, upiReasons, testModeCardTableReasons}

// documentedSources is the enumeration Razorpay publishes for error.source, at
// https://razorpay.com/docs/payments/payment-gateway/rainy-day/errors/payment-error-parameters/
// read 2026-09-01. Nine values.
//
// error.step has no published enumeration on the same page, which is why it is
// still a string on Failure and why TestErrorStepIsNotAnEnum asserts the
// absence.
var documentedSources = []Source{
	SourceBeneficiaryBank,
	SourceBusiness,
	SourceCustomer,
	SourceCustomerPSP,
	SourceGateway,
	SourceInternal,
	SourceIssuer,
	SourceIssuerBank,
	SourceNetwork,
}

// errorClasses maps error.code, the top-level Razorpay error class, to a
// class. It is only consulted when error.reason is empty.
var errorClasses = map[string]Class{
	// Something on the gateway side went wrong with no reason attached.
	"GATEWAY_ERROR": TransientRetryEligible,
	// The request itself was refused. Sending the same request again gets the
	// same refusal, so this is not a retry.
	"BAD_REQUEST_ERROR": NeverRetry,
}

// Method is the payment method a failure arrived on. Razorpay documents its
// live-mode failure reasons per method, and the lists are not the same list.
type Method string

// The methods this package holds a documented reason table for. MethodAny is
// the zero value and means the caller does not know, which is the common case:
// a classifier input built from a payment that carried no method field.
const (
	MethodAny  Method = ""
	MethodCard Method = "card"
	MethodUPI  Method = "upi"
)

// Source is error.source, the documented enumeration of where a failure came
// from.
type Source string

// The nine documented values of error.source.
const (
	SourceCustomer        Source = "customer"
	SourceBusiness        Source = "business"
	SourceInternal        Source = "internal"
	SourceGateway         Source = "gateway"
	SourceIssuerBank      Source = "issuer_bank"
	SourceCustomerPSP     Source = "customer_psp"
	SourceNetwork         Source = "network"
	SourceBeneficiaryBank Source = "beneficiary_bank"
	SourceIssuer          Source = "issuer"
)

// ReasonPaymentRiskCheckFailed is the documented reason for a payment a risk
// check blocked.
const ReasonPaymentRiskCheckFailed = "payment_risk_check_failed"

// DocumentedSources returns the nine documented values of error.source.
func DocumentedSources() []Source { return slices.Clone(documentedSources) }

// ParseSource returns the documented Source with this name. An undocumented
// name does not parse, so a value nobody published cannot be carried through
// the audit trail looking like one that was.
func ParseSource(name string) (Source, bool) {
	s := Source(name)
	if !s.Documented() {
		return "", false
	}
	return s, true
}

// Documented reports whether s is one of the nine documented values.
func (s Source) Documented() bool { return slices.Contains(documentedSources, s) }

// DocumentedReasons returns the documented live-mode reason table for a method.
//
// It returns a copy, and it never returns the test-mode card-table spellings:
// those are what test mode carries, not what Razorpay documents for live mode,
// and this function is what a caller uses to ask the second question.
func DocumentedReasons(m Method) map[string]Class {
	switch m {
	case MethodCard:
		return maps.Clone(cardReasons)
	case MethodUPI:
		return maps.Clone(upiReasons)
	default:
		return nil
	}
}

// ClassifyAcross looks a reason up in several tables and returns the class they
// agree on, or Unclassified when they do not.
//
// The disagreement rule is the fail-closed rule applied one level up. A reason
// no table holds is unclassified because nothing recognised it; a reason two
// tables classify differently is unclassified because the thing that recognised
// it does not agree with itself, and picking the first table's answer would be
// choosing an order of declaration over a decision.
func ClassifyAcross(tables []map[string]Class, reason string) Class {
	found := Unclassified
	for _, table := range tables {
		class, ok := table[reason]
		if !ok {
			continue
		}
		if found != Unclassified && found != class {
			return Unclassified
		}
		found = class
	}
	return found
}

// ClassifyNetworkDeclineCode maps a card-network decline code to a class.
//
// It speaks only for the networks' never-reattempt lists, which are the only
// code lists the networks publish as rules rather than as descriptions. Every
// other code returns Unclassified, including a plain do-not-honour: "05" is the
// most common decline there is and the network says nothing about whether it
// may be retried.
//
// No Razorpay payload this project has observed carries a raw network response
// code, so nothing in any committed run reaches this function. It is exercised
// by its unit test and by nothing else, which HONEST-LIMITATIONS records.
func ClassifyNetworkDeclineCode(network, code string) Class {
	if networkcodes.NeverRetry(network, code) {
		return NeverRetry
	}
	return Unclassified
}

// Failure is the error a Razorpay payment carries, in the fields the API
// returns it in.
type Failure struct {
	// Code is error.code, the top-level Razorpay error class.
	Code string
	// Reason is error.reason, the specific failure reason.
	Reason string
	// Source is error.source, where the failure came from.
	Source Source
	// Step is error.step, the point in the payment flow it happened at. It
	// stays a free string: Razorpay documents an enumeration for error.source
	// and publishes none for this field.
	Step string
	// Method is the payment method, which picks the documented reason table.
	Method Method
}

// Classify maps a failure to a class. It is total: anything it does not
// recognise returns Unclassified, which is not retry eligible.
//
// Reason wins over Code, because it is the specific field. A reason nothing
// recognises does not fall back to the coarser error class: the field carrying
// the detail is the one that was not understood, and treating the payment as a
// plain GATEWAY_ERROR at that point would hand back a retry the classifier has
// no basis for. Source and Step are carried for the audit trail; nothing
// branches on them.
//
// Method picks a documented table when it is known. A method whose table does
// not hold the reason falls through to the merged lookup rather than failing
// there, because the method field is not part of the reason vocabulary's
// contract: the fake gateway stamps every seeded payment "card" whatever reason
// it was seeded with, and a real merchant integration can carry a method string
// this package has no table for at all. What the fallback does not do is
// invent: ClassifyAcross returns Unclassified when the tables disagree.
func Classify(f Failure) Class {
	if f.Reason == "" {
		return errorClasses[f.Code]
	}
	if table := DocumentedReasons(f.Method); table != nil {
		if class, ok := table[f.Reason]; ok {
			return class
		}
	}
	return ClassifyAcross(reasonTables, f.Reason)
}

// String returns the snake_case name of the class, or an empty string for a
// value that is not one of the six.
func (c Class) String() string { return classNames[c] }

// IsRetryEligible reports whether the class permits another attempt on the
// same instrument. Only the two retry classes do.
func (c Class) IsRetryEligible() bool {
	return c == TransientRetryEligible || c == RetryEligible
}

// MarshalJSON writes the class as its name, so a manifest on disk is readable
// and does not turn into a bare integer that means nothing later.
func (c Class) MarshalJSON() ([]byte, error) {
	name := c.String()
	if name == "" {
		return nil, fmt.Errorf("classify: %d is not a class", int(c))
	}
	return json.Marshal(name)
}

// UnmarshalJSON reads a class name.
func (c *Class) UnmarshalJSON(b []byte) error {
	var name string
	if err := json.Unmarshal(b, &name); err != nil {
		return fmt.Errorf("classify: %w", err)
	}
	parsed, ok := ParseClass(name)
	if !ok {
		return fmt.Errorf("classify: %q is not a class", name)
	}
	*c = parsed
	return nil
}

// ParseClass returns the class with the given name.
func ParseClass(name string) (Class, bool) {
	for class, n := range classNames {
		if n == name {
			return class, true
		}
	}
	return Unclassified, false
}

// ReasonsFor returns every error.reason string in every table that maps to c,
// sorted and deduplicated.
func ReasonsFor(c Class) []string {
	seen := make(map[string]bool)
	for _, table := range reasonTables {
		for reason, class := range table {
			if class == c {
				seen[reason] = true
			}
		}
	}
	out := slices.Collect(maps.Keys(seen))
	slices.Sort(out)
	return out
}

// ReasonsForMethod returns the error.reason strings that map to c on one
// method's documented table, sorted.
//
// The batch seeder uses this rather than ReasonsFor, and it asks for cards.
// The fake gateway stamps every payment it seeds with method "card", so a batch
// seeded from the UPI list would carry a UPI reason on a card payment, which is
// a shape no gateway produces. The UPI table is therefore classified by this
// package and exercised by no run, which HONEST-LIMITATIONS records.
func ReasonsForMethod(m Method, c Class) []string {
	var out []string
	for reason, class := range DocumentedReasons(m) {
		if class == c {
			out = append(out, reason)
		}
	}
	slices.Sort(out)
	return out
}
