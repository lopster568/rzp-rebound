package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/redact"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// Redacted is what a key-shaped or card-shaped value becomes before either
// sink sees it. It is internal/redact's marker: two spellings of the same
// string would drift, and a ledger with two redaction markers in it reads as
// two different things having happened.
const Redacted = redact.Marker

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
	// KindInterventionApplied is one lawful action applied to one risk item,
	// accepted by whatever it called. internal/intervene writes it.
	KindInterventionApplied = "intervention_applied"
	// KindEscalationRaised is a risk item handed to a person through an
	// escalation sink. It is a refusal to act automatically, so it is its own
	// kind rather than an intervention that happened to be an escalation.
	KindEscalationRaised = "escalation_raised"
	// KindPromiseLogged is a promise to pay written to the promise ledger. No
	// gateway resource moved.
	KindPromiseLogged = "promise_logged"
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

// detailAttrPrefix namespaces free-form detail keys on a span, so a detail
// called "kind" cannot overwrite the event's own kind attribute.
const detailAttrPrefix = "rzp.detail."

// ErrNoOrderID is returned when an event arrives with no order to attach it
// to. A row that cannot be joined to an order cannot be scored.
var ErrNoOrderID = errors.New("audit: event has no order id")

// ErrNoKind is returned when an event arrives with no kind.
var ErrNoKind = errors.New("audit: event has no kind")

// ErrNoWriter is returned when a Recorder is built with no ledger to write to.
var ErrNoWriter = errors.New("audit: recorder needs a writer")

// redactedKeys are detail keys whose value never reaches either sink, whatever
// the value looks like. Matching on the key catches a credential that a
// value-shape check would miss.
var redactedKeys = map[string]bool{
	"api_key":             true,
	"authorization":       true,
	"card":                true,
	"card_number":         true,
	"contact":             true,
	"customer_contact":    true,
	"customer_email":      true,
	"customer_phone":      true,
	"cvv":                 true,
	"email":               true,
	"expiry":              true,
	"key":                 true,
	"key_id":              true,
	"key_secret":          true,
	"pan":                 true,
	"phone":               true,
	"razorpay_key_id":     true,
	"razorpay_key_secret": true,
	"secret":              true,
	"token":               true,
}

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
	//
	// A caller putting an error string in here has to have scrubbed it first.
	// A Razorpay key secret is a bare alphanumeric string, so no pattern on
	// this side can find one, and an error from internal/razorpay has already
	// been through Client.Redact by the time it is an error. That ordering is
	// the control. What happens here is a backstop for card-shaped and
	// key-shaped runs.
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
func NewRecorder(opts Options) (*Recorder, error) {
	if opts.Writer == nil {
		return nil, ErrNoWriter
	}

	c := opts.Clock
	if c == nil {
		c = clock.Real()
	}
	return &Recorder{w: opts.Writer, clock: c, seq: make(map[string]int)}, nil
}

// Record writes ev to both sinks and returns the ledger line it wrote.
func (r *Recorder) Record(ctx context.Context, ev Event) (Record, error) {
	if ev.OrderID == "" {
		return Record{}, ErrNoOrderID
	}
	if ev.Kind == "" {
		return Record{}, fmt.Errorf("%w: order %s", ErrNoKind, ev.OrderID)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.seq[ev.OrderID]++
	rec := Record{
		Sequence:       r.seq[ev.OrderID],
		OrderID:        ev.OrderID,
		Kind:           ev.Kind,
		Class:          RedactValue(ev.Class),
		ProposedAction: RedactValue(ev.ProposedAction),
		PolicyVerdict:  RedactValue(ev.PolicyVerdict),
		PolicyRule:     RedactValue(ev.PolicyRule),
		RecordedAt:     r.clock.Now().UTC().Format(time.RFC3339Nano),
		Detail:         redactDetail(ev.Detail),
	}

	span := trace.SpanFromContext(ctx)
	if sc := span.SpanContext(); sc.IsValid() {
		rec.TraceID = sc.TraceID().String()
		rec.SpanID = sc.SpanID().String()
	}

	encoded, err := json.Marshal(rec)
	if err != nil {
		// Undo the sequence number: a row that was not written did not happen,
		// and leaving a gap would make the next row look like a lost one.
		r.seq[ev.OrderID]--
		return Record{}, fmt.Errorf("audit: encode the row for %s: %w", ev.OrderID, err)
	}
	if _, err := r.w.Write(append(encoded, '\n')); err != nil {
		r.seq[ev.OrderID]--
		return Record{}, fmt.Errorf("audit: write the row for %s: %w", ev.OrderID, err)
	}

	span.SetAttributes(spanAttributes(rec)...)
	return rec, nil
}

// spanAttributes is the other sink. It carries the same values as the ledger
// line, so a reviewer reading the trace and a scoring pass reading the file
// cannot come away with different accounts of the same event.
func spanAttributes(rec Record) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(AttrOrderID, rec.OrderID),
		attribute.String(AttrKind, rec.Kind),
		attribute.Int(AttrSequence, rec.Sequence),
	}
	for key, value := range map[string]string{
		AttrClass:          rec.Class,
		AttrProposedAction: rec.ProposedAction,
		AttrPolicyVerdict:  rec.PolicyVerdict,
		AttrPolicyRule:     rec.PolicyRule,
	} {
		if value != "" {
			attrs = append(attrs, attribute.String(key, value))
		}
	}
	for key, value := range rec.Detail {
		attrs = append(attrs, attribute.String(detailAttrPrefix+key, value))
	}
	return attrs
}

func redactDetail(detail map[string]string) map[string]string {
	if len(detail) == 0 {
		return nil
	}

	out := make(map[string]string, len(detail))
	for key, value := range detail {
		if IsRedactedKey(key) {
			out[key] = Redacted
			continue
		}
		out[key] = RedactValue(value)
	}
	return out
}

// RedactValue replaces anything shaped like a card number or a Razorpay key
// inside s.
//
// It cannot find a bare key secret, which has no prefix and no shape to match.
// The packages that hold a credential scrub their own before a string gets
// here, and internal/redact says so at more length. Anything arriving in
// Event.Detail is expected to have been through its owner's redactor already;
// this is the second line, not the first.
func RedactValue(s string) string { return redact.Value(s) }

// IsRedactedKey reports whether a detail key names a credential or a card
// field, in which case the value never reaches either sink whatever it looks
// like.
func IsRedactedKey(key string) bool { return redactedKeys[strings.ToLower(strings.TrimSpace(key))] }
