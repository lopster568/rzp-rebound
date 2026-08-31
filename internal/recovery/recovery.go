package recovery

import (
	"context"
	"errors"

	"github.com/lopster568/rzp-recovery-agent/internal/audit"
	"github.com/lopster568/rzp-recovery-agent/internal/batch"
	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/poller"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"go.opentelemetry.io/otel/trace"
)

// TracerName names the tracer the orchestrator opens spans on.
const TracerName = "github.com/lopster568/rzp-recovery-agent/internal/recovery"

// Action kinds. They match batch.CorrectAction so an outcome can be scored
// against the ground-truth manifest without a translation table in between.
const (
	ActionNone                 = "none"
	ActionRetrySameInstrument  = "retry_same_instrument"
	ActionRequestReauth        = "request_reauth"
	ActionRequestNewInstrument = "request_new_instrument"
)

// Errors returned when an Orchestrator is built with a piece missing.
var (
	ErrNoPort     = errors.New("recovery: needs a razorpay.Port")
	ErrNoPoller   = errors.New("recovery: needs a poller")
	ErrNoRecorder = errors.New("recovery: needs an audit recorder")
)

// ActionResult is what an action reported about itself.
type ActionResult struct {
	// Kind is one of the action constants.
	Kind string
	// ClaimedRecovered is what the action says happened. The orchestrator
	// records it and does not believe it: the outcome comes from reading the
	// order back out of the gateway. An action that reports success and a
	// gateway that still says attempted is exactly the disagreement this
	// project exists to measure, so the claim is kept rather than dropped.
	ClaimedRecovered bool
	// Detail goes into the audit row.
	Detail map[string]string
}

// ActionFunc is the one move the orchestrator makes on an order.
//
// Phase 1 ships DoNothing. Phase 2 puts policy.Evaluate in front of whatever
// is wired here, per ADR-0003, and no action reaches a side effect without a
// verdict behind it.
type ActionFunc func(ctx context.Context, order batch.AgentVisibleOrder, class classify.Class) (ActionResult, error)

// DoNothing is the phase 1 action: it takes none.
func DoNothing(ctx context.Context, order batch.AgentVisibleOrder, class classify.Class) (ActionResult, error) {
	return ActionResult{}, nil
}

// Options configures an Orchestrator.
type Options struct {
	// Port is the gateway. Required, and it is what the outcome is read back
	// from.
	Port razorpay.Port
	// Poller reads the order until it settles. Required.
	Poller *poller.Poller
	// Recorder writes the audit trail. Required.
	Recorder *audit.Recorder
	// Action is the move to make per order. Nil means DoNothing.
	Action ActionFunc
	// Tracer opens one span per order, which is what the audit recorder's span
	// sink writes onto. Nil means a tracer that records nothing.
	Tracer trace.Tracer
	// Clock is passed through to anything that needs it. Nil means the wall
	// clock.
	Clock clock.Clock
}

// Orchestrator runs one order through the recovery cycle: poll, classify,
// act, read the outcome back.
type Orchestrator struct {
	port     razorpay.Port
	poller   *poller.Poller
	recorder *audit.Recorder
	action   ActionFunc
	tracer   trace.Tracer
	clock    clock.Clock
}

// Outcome is what one order's cycle produced.
type Outcome struct {
	OrderID string
	// Class is what the failure classified to, or Unclassified when the order
	// had no failed payment to classify.
	Class classify.Class
	// ActionKind is the action that ran.
	ActionKind string
	// FinalOrderStatus is the order status read back from the gateway after
	// the action, not the status the action reported.
	FinalOrderStatus string
	// Recovered is FinalOrderStatus reading paid. It comes from the gateway.
	Recovered bool
	// ClaimedRecovered is what the action said about itself, kept so a
	// disagreement with Recovered is visible rather than silently resolved.
	ClaimedRecovered bool
	// TimedOut reports that the poll ran out of time before the order settled.
	TimedOut bool
	// Events are the audit rows this cycle wrote, in order.
	Events []audit.Record
}

// New returns an Orchestrator.
func New(opts Options) (*Orchestrator, error) { return &Orchestrator{}, nil }

// FailureFrom builds a classifier input from a payment. A nil payment gives
// the zero Failure, which classifies as Unclassified and is not retry
// eligible.
func FailureFrom(p *razorpay.Payment) classify.Failure { return classify.Failure{} }

// ProcessOrder runs one order through the cycle.
//
// The outcome comes from a FetchOrder after the action, never from the action
// result. A recovery rate built out of what an action claimed about itself
// would be measuring the agent's self-report, which is the number this project
// is trying not to produce.
func (o *Orchestrator) ProcessOrder(ctx context.Context, order batch.AgentVisibleOrder) (Outcome, error) {
	return Outcome{}, nil
}
