package recovery

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/lopster568/rzp-recovery-agent/internal/audit"
	"github.com/lopster568/rzp-recovery-agent/internal/batch"
	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/poller"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
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
func DoNothing(_ context.Context, _ batch.AgentVisibleOrder, _ classify.Class) (ActionResult, error) {
	return ActionResult{Kind: ActionNone}, nil
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
func New(opts Options) (*Orchestrator, error) {
	if opts.Port == nil {
		return nil, ErrNoPort
	}
	if opts.Poller == nil {
		return nil, ErrNoPoller
	}
	if opts.Recorder == nil {
		return nil, ErrNoRecorder
	}

	o := &Orchestrator{
		port:     opts.Port,
		poller:   opts.Poller,
		recorder: opts.Recorder,
		action:   opts.Action,
		tracer:   opts.Tracer,
		clock:    opts.Clock,
	}
	if o.action == nil {
		o.action = DoNothing
	}
	if o.tracer == nil {
		o.tracer = noop.NewTracerProvider().Tracer(TracerName)
	}
	if o.clock == nil {
		o.clock = clock.Real()
	}
	return o, nil
}

// FailureFrom builds a classifier input from a payment. A nil payment gives
// the zero Failure, which classifies as Unclassified and is not retry
// eligible.
func FailureFrom(p *razorpay.Payment) classify.Failure {
	if p == nil {
		return classify.Failure{}
	}
	return classify.Failure{
		Code:   p.ErrorCode,
		Reason: p.ErrorReason,
		Source: p.ErrorSource,
		Step:   p.ErrorStep,
	}
}

// ProcessOrder runs one order through the cycle.
//
// The outcome comes from a FetchOrder after the action, never from the action
// result. A recovery rate built out of what an action claimed about itself
// would be measuring the agent's self-report, which is the number this project
// is trying not to produce.
func (o *Orchestrator) ProcessOrder(ctx context.Context, order batch.AgentVisibleOrder) (Outcome, error) {
	ctx, span := o.tracer.Start(ctx, "recovery.process_order")
	defer span.End()

	outcome := Outcome{OrderID: order.OrderID}

	polled, err := o.poller.PollUntilTerminal(ctx, order.OrderID)
	if err != nil {
		return outcome, fmt.Errorf("recovery: poll %s: %w", order.OrderID, err)
	}
	outcome.TimedOut = polled.TimedOut
	outcome.Class = classify.Classify(FailureFrom(polled.FailedPayment))

	if err := o.record(ctx, &outcome, audit.Event{
		OrderID: order.OrderID,
		Kind:    audit.KindClassified,
		Class:   outcome.Class.String(),
		Detail: map[string]string{
			"polled_order_status": polled.Order.Status,
			"attempts_seen":       strconv.Itoa(len(polled.Payments)),
			"poll_timed_out":      strconv.FormatBool(polled.TimedOut),
		},
	}); err != nil {
		return outcome, err
	}

	result, actionErr := o.action(ctx, order, outcome.Class)
	outcome.ActionKind = result.Kind
	if outcome.ActionKind == "" {
		outcome.ActionKind = ActionNone
	}
	outcome.ClaimedRecovered = result.ClaimedRecovered

	// A failed action still writes a row. Refusals and errors that leave no
	// trace are what make a containment count unprovable.
	//
	// An action that ran and returned an error is taken, not skipped. The
	// idiomatic Go failure is `return ActionResult{}, err`, which leaves Kind
	// empty and normalises to ActionNone above, so keying the row off Kind
	// alone would file every failed attempt as one that never happened and a
	// scoring pass counting attempts against refusals would undercount.
	kind := audit.KindActionTaken
	if outcome.ActionKind == ActionNone && actionErr == nil {
		kind = audit.KindActionSkipped
	}
	detail := map[string]string{"claimed_recovered": strconv.FormatBool(result.ClaimedRecovered)}
	for k, v := range result.Detail {
		detail[k] = v
	}
	if actionErr != nil {
		detail["action_error"] = actionErr.Error()
	}
	if err := o.record(ctx, &outcome, audit.Event{
		OrderID:        order.OrderID,
		Kind:           kind,
		Class:          outcome.Class.String(),
		ProposedAction: outcome.ActionKind,
		Detail:         detail,
	}); err != nil {
		return outcome, err
	}

	// The outcome. Read from the gateway, after the action, every time,
	// including when the action failed and including when it took no action at
	// all. One code path means there is no branch in which the claim is
	// believed.
	final, err := o.port.FetchOrder(ctx, order.OrderID)
	if err != nil {
		return outcome, fmt.Errorf("recovery: read back %s: %w", order.OrderID, err)
	}
	outcome.FinalOrderStatus = final.Status
	outcome.Recovered = final.Status == razorpay.OrderStatusPaid

	if err := o.record(ctx, &outcome, audit.Event{
		OrderID:        order.OrderID,
		Kind:           audit.KindOutcomeObserved,
		Class:          outcome.Class.String(),
		ProposedAction: outcome.ActionKind,
		Detail: map[string]string{
			"final_order_status": final.Status,
			"recovered":          strconv.FormatBool(outcome.Recovered),
			"claimed_recovered":  strconv.FormatBool(outcome.ClaimedRecovered),
			"amount_paid_paise":  strconv.FormatInt(final.AmountPaid, 10),
		},
	}); err != nil {
		return outcome, err
	}

	if actionErr != nil {
		return outcome, fmt.Errorf("recovery: action on %s: %w", order.OrderID, actionErr)
	}
	return outcome, nil
}

// record writes one audit row and keeps it on the outcome, so a caller has the
// trail without re-reading the ledger file.
func (o *Orchestrator) record(ctx context.Context, outcome *Outcome, ev audit.Event) error {
	rec, err := o.recorder.Record(ctx, ev)
	if err != nil {
		return fmt.Errorf("recovery: record %s for %s: %w", ev.Kind, ev.OrderID, err)
	}
	outcome.Events = append(outcome.Events, rec)
	return nil
}
