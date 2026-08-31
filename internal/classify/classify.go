package classify

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

// String returns the snake_case name of the class.
func (c Class) String() string { return "" }

// IsRetryEligible reports whether the class permits another attempt on the
// same instrument.
func (c Class) IsRetryEligible() bool { return false }

// MarshalJSON writes the class as its name.
func (c Class) MarshalJSON() ([]byte, error) { return nil, nil }

// UnmarshalJSON reads a class name.
func (c *Class) UnmarshalJSON(b []byte) error { return nil }

// ParseClass returns the class with the given name.
func ParseClass(name string) (Class, bool) { return Unclassified, false }

// Classify maps a failure to a class. It is total: anything it does not
// recognise returns Unclassified.
func Classify(f Failure) Class { return Unclassified }

// Reasons returns a copy of the error.reason table.
func Reasons() map[string]Class { return nil }

// ErrorClasses returns a copy of the error.code table.
func ErrorClasses() map[string]Class { return nil }

// ReasonsFor returns the error.reason strings that map to c, sorted.
func ReasonsFor(c Class) []string { return nil }
