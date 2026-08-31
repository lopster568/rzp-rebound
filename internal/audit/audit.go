package audit

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
)

// Redacted is what a key-shaped or card-shaped value becomes before either
// sink sees it.
const Redacted = "[redacted]"

// Event kinds. The set is closed and every kind is a constant, so a reader of
// the ledger has an enumerable vocabulary rather than whatever string a call
// site invented.
const (
	KindClassified            = "classified"
	KindPolicyEvaluated       = "policy_evaluated"
	KindActionTaken           = "action_taken"
	KindActionSkipped         = "action_skipped"
	KindNotificationRequested = "notification_requested"
	KindOutcomeObserved       = "outcome_observed"
)

// Span attribute keys. The ledger line and the span carry the same event, so
// these keys and the json tags on Record are two views of one shape. The live
// half writes them up in AUDIT-TRACE-SCHEMA.md once a real run has produced
// them.
const (
	AttrOrderID        = "rzp.order_id"
	AttrKind           = "rzp.audit.kind"
	AttrSequence       = "rzp.audit.sequence"
	AttrClass          = "rzp.failure_class"
	AttrProposedAction = "rzp.proposed_action"
	AttrPolicyVerdict  = "rzp.policy.verdict"
	AttrPolicyRule     = "rzp.policy.rule"
)

// ErrNoOrderID is returned when an event arrives with no order to attach it
// to. A row that cannot be joined to an order cannot be scored.
var ErrNoOrderID = errors.New("audit: event has no order id")

// ErrNoKind is returned when an event arrives with no kind.
var ErrNoKind = errors.New("audit: event has no kind")

// Event is one decision or observation the recovery loop wants on the record.
type Event struct {
	// OrderID is required. It is what joins a row to a batch manifest.
	OrderID string
	// Kind is required and comes from the constants above.
	Kind string
	// Class is the failure class, when the event has one.
	Class string
	// ProposedAction is the action under consideration, when the event has
	// one.
	ProposedAction string
	// PolicyVerdict and PolicyRule are filled by phase 2. A denied action
	// still writes a row, which is what turns a refusal into a countable
	// metric.
	PolicyVerdict string
	PolicyRule    string
	// Detail is free-form context. Values go through redaction, and a key on
	// the credential or card list is dropped whatever its value looks like.
	Detail map[string]string
}

// Record is the ledger line. One event produces exactly one of these and one
// set of span attributes, and TraceID is what joins the two.
type Record struct {
	Sequence       int               `json:"sequence"`
	OrderID        string            `json:"order_id"`
	Kind           string            `json:"kind"`
	Class          string            `json:"class,omitempty"`
	ProposedAction string            `json:"proposed_action,omitempty"`
	PolicyVerdict  string            `json:"policy_verdict,omitempty"`
	PolicyRule     string            `json:"policy_rule,omitempty"`
	TraceID        string            `json:"trace_id"`
	SpanID         string            `json:"span_id"`
	RecordedAt     string            `json:"recorded_at"`
	Detail         map[string]string `json:"detail,omitempty"`
}

// Options configures a Recorder.
type Options struct {
	// Writer receives one JSON line per event. Required.
	Writer io.Writer
	// Clock stamps each line. Nil means the wall clock.
	Clock clock.Clock
}

// Recorder writes one event to two sinks: attributes on the span that is
// active in the context, and a JSONL line carrying that span's trace id.
//
// Two sinks rather than one because they answer different questions. The trace
// shows a reviewer what happened inside one order's run, in order, with
// timings. The ledger is a flat file that a scoring pass can read without a
// trace backend. The trace id in the line is what lets a reviewer go from a
// row in the file to the span it came from, which is FR-AUD-3.
type Recorder struct {
	mu    sync.Mutex
	w     io.Writer
	clock clock.Clock
	seq   map[string]int
}

// NewRecorder returns a Recorder.
func NewRecorder(opts Options) (*Recorder, error) { return &Recorder{}, nil }

// Record writes ev to both sinks and returns the ledger line it wrote.
func (r *Recorder) Record(ctx context.Context, ev Event) (Record, error) { return Record{}, nil }

// RedactValue replaces anything shaped like a card number or a Razorpay key
// inside s.
func RedactValue(s string) string { return "" }

// IsRedactedKey reports whether a detail key names a credential or a card
// field, in which case the value never reaches either sink whatever it looks
// like.
func IsRedactedKey(key string) bool { return false }
