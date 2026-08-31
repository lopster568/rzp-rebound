package notify

import (
	"context"
	"errors"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
)

// Audit phrases. Every string this package contributes to an audit trail comes
// from this block, so what the system can claim about a notification is a short
// list a reviewer can read in full rather than whatever a call site formatted.
//
// All three describe an API call. None of them describes a person, because an
// HTTP response is the only thing this system observes.
const (
	AuditPhraseAPICallSucceeded = "notification API call succeeded"
	AuditPhraseAPICallFailed    = "notification API call failed"
	AuditPhraseMediumRejected   = "notification medium rejected before any API call"
)

// ErrNoPort is returned when a Notifier is built with nothing to send through.
var ErrNoPort = errors.New("notify: needs a NotifierPort")

// NotifierPort is the slice of razorpay.Port this package needs. It is a
// separate interface so the notifier can run against razorpay.Fake, against
// the replay client, against razorpay.Client, and against Mock, with no
// credential involved in three of the four.
type NotifierPort interface {
	ResendPaymentLinkNotification(ctx context.Context, linkID, medium string) (razorpay.NotifyReceipt, error)
}

// Receipt is what one send attempt produced.
type Receipt struct {
	LinkID string
	Medium string
	// APICallSucceeded reports that the notification API call returned
	// success. That is the whole of what was observed.
	APICallSucceeded bool
	// DeliveryConfirmed is always false, and no code path in this package sets
	// it to anything else.
	//
	// The only observable is Razorpay's HTTP response to the resend call.
	// Whether a person received a message, opened it, or read it is not on
	// that response, and this project has no channel that would report it. The
	// field exists rather than being left out so that a reader of the audit
	// trail sees the answer stated instead of having to notice it is missing.
	// Phase 1 has no way to make it true and neither does any later phase in
	// the PRD.
	DeliveryConfirmed bool
	// AuditPhrase is one of the constants in this package.
	AuditPhrase string
	RequestedAt time.Time
}

// Options configures a Notifier.
type Options struct {
	// Port is what the resend call goes through. Required.
	Port NotifierPort
	// Clock stamps the receipt. Nil means the wall clock.
	Clock clock.Clock
}

// Notifier asks Razorpay to send a payment link again and reports what the API
// said.
type Notifier struct {
	port  NotifierPort
	clock clock.Clock
}

// New returns a Notifier.
func New(opts Options) (*Notifier, error) { return &Notifier{}, nil }

// SendPaymentLink resends a payment link over sms or email.
//
// The returned Receipt reports whether the API call succeeded. It reports
// nothing about a person, because nothing here observes one.
func (n *Notifier) SendPaymentLink(ctx context.Context, linkID, medium string) (Receipt, error) {
	return Receipt{}, nil
}

// AuditPhrases returns every phrase this package can put in an audit trail.
// TestNotifierNeverClaimsCustomerNotified walks it, so a new phrase added
// without thinking gets checked against the forbidden wording.
func AuditPhrases() []string { return nil }
