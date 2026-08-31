package classify

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/lopster568/rzp-recovery-agent/internal/testcards"
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

// reasons maps error.reason, the specific failure reason, to a class. Every
// key except the risk block comes from testdata/error_codes.json.
var reasons = map[string]Class{
	// The gateway or the network, not the customer. The same attempt can
	// work on a repeat.
	"payment_timed_out":       TransientRetryEligible,
	"gateway_technical_error": TransientRetryEligible,

	// The card is fine and the customer is present. What blocked the charge
	// is a balance, and a balance changes.
	"insufficient_fund": RetryEligible,

	// The customer has to be back in the flow. A silent recharge is not an
	// option for either of these: one failed authentication, the other was
	// abandoned partway.
	"authentication_failed": ReauthRequired,
	"payment_cancelled":     ReauthRequired,

	// This card cannot complete this payment, so repeating it repeats the
	// failure. Recovery needs a different instrument.
	"card_declined":                     NewInstrumentRequired,
	"card_disabled_for_online_payments": NewInstrumentRequired,
	"card_number_invalid":               NewInstrumentRequired,

	// No documented Razorpay code, see internal/testcards. The behaviour is
	// what matters: a risk block is not retried.
	testcards.PendingRiskBlockCode: NeverRetry,
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

// Failure is the error a Razorpay payment carries, in the fields the API
// returns it in.
type Failure struct {
	// Code is error.code, the top-level Razorpay error class.
	Code string
	// Reason is error.reason, the specific failure reason.
	Reason string
	// Source is error.source, where the failure came from.
	Source string
	// Step is error.step, the point in the payment flow it happened at.
	Step string
}

// Classify maps a failure to a class. It is total: anything it does not
// recognise returns Unclassified, which is not retry eligible.
//
// Reason wins over Code, because it is the specific field. A reason nothing
// recognises does not fall back to the coarser error class: the field carrying
// the detail is the one that was not understood, and treating the payment as a
// plain GATEWAY_ERROR at that point would hand back a retry the classifier has
// no basis for. Source and Step are carried for the audit trail and for the
// phase 2 policy; nothing in phase 0 branches on them.
func Classify(f Failure) Class {
	if f.Reason != "" {
		return reasons[f.Reason]
	}
	return errorClasses[f.Code]
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

// Reasons returns a copy of the error.reason table.
func Reasons() map[string]Class { return maps.Clone(reasons) }

// ErrorClasses returns a copy of the error.code table.
func ErrorClasses() map[string]Class { return maps.Clone(errorClasses) }

// ReasonsFor returns the error.reason strings that map to c, sorted. The batch
// seeder uses it to pick a failure to seed for a requested class.
func ReasonsFor(c Class) []string {
	var out []string
	for reason, class := range reasons {
		if class == c {
			out = append(out, reason)
		}
	}
	slices.Sort(out)
	return out
}
