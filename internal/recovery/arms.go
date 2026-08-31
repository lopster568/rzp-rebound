package recovery

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/lopster568/rzp-recovery-agent/internal/audit"
	"github.com/lopster568/rzp-recovery-agent/internal/batch"
	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/notify"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/store"
)

// The three arms phase 2 runs.
//
// a2 is deliberately missing. It is the LLM arm and it is phase 3, and
// numbering it now keeps the arm ids stable across the two phases so a table
// from either one can be read next to the other.
const (
	// ArmControl takes no action on any order, ever. It is the floor the
	// other two are measured from, and it recovers zero by construction.
	ArmControl = "a0-control"
	// ArmNaive retries every failure immediately, up to its own cap. It
	// consults neither the classifier nor the policy, which is what makes it
	// the thing the rules arm has to beat.
	ArmNaive = "a1-naive"
	// ArmRules classifies, asks the policy, and then acts or escalates. Every
	// branch writes an audit row.
	ArmRules = "a3-rules"
)

// DefaultNaiveMaxAttempts caps the naive arm. It is a cap in the arm, not a
// policy rule: the naive arm consults no policy, and an arm with no bound at
// all would spend a whole run on one order.
const DefaultNaiveMaxAttempts = 3

// Detail keys the arms write into audit rows. The scorer reads these names, so
// they are constants rather than literals at three call sites.
const (
	DetailArm              = "arm"
	DetailSideEffect       = "side_effect"
	DetailEscalated        = "escalated"
	DetailPolicyConsulted  = "policy_consulted"
	DetailIdempotentReplay = "idempotent_replay"
	DetailIdempotencyKey   = "idempotency_key"
	DetailAttemptNo        = "attempt_no"
	DetailPaymentLinkID    = "payment_link_id"
	DetailPaymentID        = "payment_id"
	DetailNotifyPhrase     = "notification_audit_phrase"
	DetailGatewayCalls     = "gateway_calls"
)

// Errors returned when an arm is built with a piece missing.
var (
	ErrUnknownArm = errors.New("recovery: unknown arm")
	ErrNoSurface  = errors.New("recovery: an arm needs an action surface")
	ErrNoStore    = errors.New("recovery: an arm needs a store")
	ErrNoPolicy   = errors.New("recovery: the rules arm needs a policy")
)

// AttemptRecord is what one payment attempt produced. It carries no payment
// state, because state is read back from the gateway by the orchestrator.
type AttemptRecord struct {
	PaymentID string
	Card      string
	// GatewayCalls is how many requests the attempt made.
	//
	// It is reported by the adapter rather than counted by a wrapper around
	// razorpay.Port, because an attempt does not go through Port on either
	// layer: the live one drives four undocumented checkout calls and the fake
	// one calls AttemptPayment, and neither is a Port method. Without this the
	// cost column counted a run's polls and read-backs and none of its
	// attempts, which on the live layer understated the naive arm by four
	// calls per order.
	GatewayCalls int
}

// Attempter is the one way any arm makes a payment attempt.
//
// Two adapters satisfy it, one per measurement layer, and both keep the
// gateway's own knowledge of what an attempt should do unexported. An arm
// holds this interface and cannot reach past it.
type Attempter interface {
	Attempt(ctx context.Context, order batch.AgentVisibleOrder, card string) (AttemptRecord, error)
}

// FakeAttempter drives an attempt against razorpay.Fake.
type FakeAttempter struct {
	fake *razorpay.Fake
}

// NewFakeAttempter returns a FakeAttempter.
func NewFakeAttempter(f *razorpay.Fake) *FakeAttempter { return &FakeAttempter{fake: f} }

// Attempt makes one attempt on the fake gateway. Whether it authorizes is the
// fake's decision, taken from the recovery schedule the materialiser seeded.
func (a *FakeAttempter) Attempt(ctx context.Context, order batch.AgentVisibleOrder, card string) (AttemptRecord, error) {
	payment, err := a.fake.AttemptPayment(ctx, order.OrderID, card)
	if err != nil {
		return AttemptRecord{Card: card, GatewayCalls: 1}, err
	}
	return AttemptRecord{PaymentID: payment.ID, Card: card, GatewayCalls: 1}, nil
}

// LiveAttempter drives an attempt against Razorpay test mode through the
// checkout sequence.
//
// It holds the per-order settle outcome the batch materialiser computed from
// the manifest. That map is the gateway's knowledge, not the agent's: test
// mode picks an outcome from one form field at the last checkout call rather
// than from the card, so something has to stand in for the world deciding.
// The field is unexported and there is no accessor, so an arm holding the
// Attempter interface cannot read it.
type LiveAttempter struct {
	attempter *razorpay.Attempter
	outcomes  map[string]string
}

// NewLiveAttempter returns a LiveAttempter. An order with no entry in outcomes
// settles as a failure, because a materialiser that forgot an order should not
// be silently recovering it.
func NewLiveAttempter(a *razorpay.Attempter, outcomes map[string]string) *LiveAttempter {
	copied := make(map[string]string, len(outcomes))
	for k, v := range outcomes {
		copied[k] = v
	}
	return &LiveAttempter{attempter: a, outcomes: copied}
}

// Attempt makes one attempt through the checkout sequence.
func (a *LiveAttempter) Attempt(ctx context.Context, order batch.AgentVisibleOrder, card string) (AttemptRecord, error) {
	outcome := razorpay.AttemptFail
	if v, ok := a.outcomes[order.OrderID]; ok {
		outcome = v
	}

	got, err := a.attempter.Attempt(ctx, razorpay.AttemptRequest{
		OrderID:     order.OrderID,
		AmountPaise: order.AmountPaise,
		CardNumber:  card,
		Outcome:     outcome,
	})
	// Steps records a call when it is sent, not when it comes back, which is
	// the count a cost column wants: a request that reached Razorpay and then
	// failed to decode is a request that was paid for.
	return AttemptRecord{
		PaymentID:    got.PaymentID,
		Card:         card,
		GatewayCalls: len(got.Steps),
	}, err
}

// Surface is the one set of hands every arm drives.
//
// The arms differ in what they decide, never in what they can reach. An arm
// with a side effect the others do not have would make the three-arm table a
// comparison of capabilities rather than of decisions.
type Surface struct {
	// Port is the gateway. Required.
	Port razorpay.Port
	// Attempter makes payment attempts. Required.
	Attempter Attempter
	// Notifier resends a payment link and reports what the API said.
	// Required.
	Notifier *notify.Notifier
	// Recorder writes the audit trail. Required.
	Recorder *audit.Recorder
	// Card is the instrument a retry re-presents. One card for the whole run:
	// an arm cannot pick a different card per order, because it does not know
	// which card seeded which order.
	Card string
	// Currency is what a payment link is raised in. Empty means INR.
	Currency string
}

// ArmOptions configures an Arm.
type ArmOptions struct {
	// Surface is the shared set of hands. Required.
	Surface *Surface
	// Store is the attempt ledger. Required.
	Store *store.Store
	// Policy is required by ArmRules and ignored by the other two.
	Policy *policy.Policy
	// NaiveMaxAttempts caps ArmNaive. Zero means DefaultNaiveMaxAttempts.
	NaiveMaxAttempts int
	// KillSwitchEngaged is what the runner read off disk with
	// policy.KillSwitchFile, passed through to every evaluation.
	KillSwitchEngaged bool
}

// Arm is one decision-maker. Its Act method is the ActionFunc the orchestrator
// runs, so the three arms plug into the same loop.
type Arm struct {
	id       string
	surface  *Surface
	store    *store.Store
	policy   *policy.Policy
	naiveMax int
	kill     bool
}

// ArmIDs returns the three arm ids, in report order.
func ArmIDs() []string { return []string{ArmControl, ArmNaive, ArmRules} }

// NewArm returns the arm with the given id.
func NewArm(id string, opts ArmOptions) (*Arm, error) {
	switch id {
	case ArmControl, ArmNaive, ArmRules:
	default:
		return nil, fmt.Errorf("%w: %q, want one of %v", ErrUnknownArm, id, ArmIDs())
	}
	if opts.Surface == nil {
		return nil, ErrNoSurface
	}
	if opts.Store == nil {
		return nil, ErrNoStore
	}
	if id == ArmRules && opts.Policy == nil {
		return nil, ErrNoPolicy
	}

	a := &Arm{
		id:       id,
		surface:  opts.Surface,
		store:    opts.Store,
		policy:   opts.Policy,
		naiveMax: opts.NaiveMaxAttempts,
		kill:     opts.KillSwitchEngaged,
	}
	if a.naiveMax <= 0 {
		a.naiveMax = DefaultNaiveMaxAttempts
	}
	return a, nil
}

// ID returns the arm's id.
func (a *Arm) ID() string { return a.id }

// Act is the ActionFunc. It runs one action on one order and returns what it
// did, including when what it did was refuse.
func (a *Arm) Act(ctx context.Context, order batch.AgentVisibleOrder, class classify.Class) (ActionResult, error) {
	switch a.id {
	case ArmControl:
		return a.control(order)
	case ArmNaive:
		return a.naive(ctx, order)
	default:
		return a.rules(ctx, order, class)
	}
}

// control takes no action. It does not consult the class either, so the
// control row in the report is the floor and nothing else.
func (a *Arm) control(order batch.AgentVisibleOrder) (ActionResult, error) {
	return ActionResult{
		Kind: ActionNone,
		Detail: map[string]string{
			DetailArm:             a.id,
			DetailSideEffect:      "false",
			DetailPolicyConsulted: "false",
		},
	}, nil
}

// naive retries every failure up to its own cap, with no classification and no
// policy.
//
// It writes no policy verdict onto its action, and that absence is the point:
// policy_violations_succeeded counts action rows that reached a side effect
// with nothing behind them, and this is the arm that produces them.
func (a *Arm) naive(ctx context.Context, order batch.AgentVisibleOrder) (ActionResult, error) {
	result := ActionResult{
		Kind: ActionRetrySameInstrument,
		Detail: map[string]string{
			DetailArm:             a.id,
			DetailPolicyConsulted: "false",
			DetailSideEffect:      "false",
		},
	}

	attempts := a.store.Attempts(order.OrderID)
	if attempts >= a.naiveMax {
		result.Kind = ActionNone
		result.Detail["stopped_at_arm_cap"] = strconv.Itoa(a.naiveMax)
		return result, nil
	}

	result.Detail[DetailAttemptNo] = strconv.Itoa(attempts + 1)
	record, err := a.surface.Attempter.Attempt(ctx, order, a.surface.Card)

	// The side effect is recorded whether or not the call came back clean. A
	// request that reached the gateway and then failed to decode is a request
	// that was made, and an ungated action that errored is still an ungated
	// action.
	result.SideEffect = true
	result.GatewayCalls += record.GatewayCalls
	result.Detail[DetailSideEffect] = "true"
	result.Detail[DetailGatewayCalls] = strconv.Itoa(record.GatewayCalls)
	if record.PaymentID != "" {
		result.Detail[DetailPaymentID] = record.PaymentID
	}
	a.store.Commit(order.OrderID, "", ActionRetrySameInstrument)

	if err != nil {
		result.Detail["action_error"] = err.Error()
		return result, err
	}
	result.ClaimedRecovered = true
	return result, nil
}

// rules classifies, asks the policy, and then acts or escalates.
func (a *Arm) rules(ctx context.Context, order batch.AgentVisibleOrder, class classify.Class) (ActionResult, error) {
	action := ActionForClass(class)
	attempts := a.store.Attempts(order.OrderID)
	attemptNo := attempts + 1

	key := policy.IdempotencyKey(order.OrderID, action, attemptNo)
	state := a.store.Snapshot(order.OrderID, key, a.kill)
	decision := a.policy.Evaluate(state, policy.Request{
		OrderID:     order.OrderID,
		Action:      action,
		Class:       class,
		AmountPaise: order.AmountPaise,
		AttemptNo:   attemptNo,
	})

	result := ActionResult{
		Kind:          action,
		PolicyVerdict: string(decision.Verdict),
		PolicyRule:    decision.RuleID,
		Detail: map[string]string{
			DetailArm:              a.id,
			DetailPolicyConsulted:  "true",
			DetailSideEffect:       "false",
			DetailIdempotencyKey:   policy.ShortKey(decision.IdempotencyKey),
			DetailIdempotentReplay: strconv.FormatBool(decision.IdempotentReplay),
			DetailAttemptNo:        strconv.Itoa(attemptNo),
			"policy_reason":        decision.Reason,
			"attempts_remaining":   strconv.Itoa(decision.Remaining),
		},
	}

	// The evaluation is on the record before anything acts on it. A refusal
	// that leaves no row is what makes a containment count unprovable, so the
	// row goes down on every branch including the ones that do nothing.
	if _, err := a.surface.Recorder.Record(ctx, audit.Event{
		OrderID:        order.OrderID,
		Kind:           audit.KindPolicyEvaluated,
		Class:          class.String(),
		ProposedAction: action,
		PolicyVerdict:  string(decision.Verdict),
		PolicyRule:     decision.RuleID,
		Detail: map[string]string{
			DetailArm:              a.id,
			DetailIdempotencyKey:   policy.ShortKey(decision.IdempotencyKey),
			DetailIdempotentReplay: strconv.FormatBool(decision.IdempotentReplay),
			DetailAttemptNo:        strconv.Itoa(attemptNo),
			"policy_reason":        decision.Reason,
		},
	}); err != nil {
		return result, err
	}

	if !decision.Allowed() {
		result.Kind = ActionNone
		result.Escalated = decision.Verdict == policy.VerdictEscalate
		result.Detail[DetailEscalated] = strconv.FormatBool(result.Escalated)
		result.Detail["refused_action"] = action
		return result, nil
	}

	// Allowed. From here on there is a side effect, and the verdict that let
	// it through is already on the result and already in the ledger.
	switch action {
	case ActionRetrySameInstrument:
		return a.retry(ctx, order, result, decision)
	case ActionRequestReauth, ActionRequestNewInstrument:
		return a.requestFromCustomer(ctx, order, class, result, decision)
	default:
		// ActionForClass returned none for a class the policy allowed, which
		// only happens if the two tables disagree. Refusing to act is the
		// conservative reading.
		result.Kind = ActionNone
		result.Detail["no_action_for_class"] = class.String()
		return result, nil
	}
}

// retry re-presents the instrument.
func (a *Arm) retry(ctx context.Context, order batch.AgentVisibleOrder, result ActionResult, decision policy.Decision) (ActionResult, error) {
	record, err := a.surface.Attempter.Attempt(ctx, order, a.surface.Card)
	result.SideEffect = true
	result.GatewayCalls += record.GatewayCalls
	result.Detail[DetailSideEffect] = "true"
	result.Detail[DetailGatewayCalls] = strconv.Itoa(record.GatewayCalls)
	if record.PaymentID != "" {
		result.Detail[DetailPaymentID] = record.PaymentID
	}
	a.store.Commit(order.OrderID, decision.IdempotencyKey, ActionRetrySameInstrument)

	if err != nil {
		result.Detail["action_error"] = err.Error()
		return result, err
	}
	result.ClaimedRecovered = true
	return result, nil
}

// requestFromCustomer raises a payment link and asks Razorpay to send it.
//
// What is recorded is that the notification API call succeeded. Nothing here
// or downstream claims a person received or read anything, and
// notify.Receipt.DeliveryConfirmed is false on every path.
func (a *Arm) requestFromCustomer(ctx context.Context, order batch.AgentVisibleOrder, class classify.Class, result ActionResult, decision policy.Decision) (ActionResult, error) {
	currency := a.surface.Currency
	if currency == "" {
		currency = "INR"
	}

	link, err := a.surface.Port.CreatePaymentLink(ctx, razorpay.CreatePaymentLinkRequest{
		AmountPaise: order.AmountPaise,
		Currency:    currency,
		Description: "recovery for " + order.OrderID,
		ReferenceID: order.Receipt,
	})
	result.SideEffect = true
	result.Detail[DetailSideEffect] = "true"
	a.store.Commit(order.OrderID, decision.IdempotencyKey, result.Kind)
	if err != nil {
		result.Detail["action_error"] = err.Error()
		return result, err
	}
	result.Detail[DetailPaymentLinkID] = link.ID

	receipt, sendErr := a.surface.Notifier.SendPaymentLink(ctx, link.ID, razorpay.MediumEmail)
	result.Detail[DetailNotifyPhrase] = receipt.AuditPhrase
	result.Detail["notification_delivery_confirmed"] = strconv.FormatBool(receipt.DeliveryConfirmed)

	if _, err := a.surface.Recorder.Record(ctx, audit.Event{
		OrderID:        order.OrderID,
		Kind:           audit.KindNotificationRequested,
		Class:          class.String(),
		ProposedAction: result.Kind,
		PolicyVerdict:  result.PolicyVerdict,
		PolicyRule:     result.PolicyRule,
		Detail: map[string]string{
			DetailArm:            a.id,
			DetailPaymentLinkID:  link.ID,
			"medium":             razorpay.MediumEmail,
			"audit_phrase":       receipt.AuditPhrase,
			"api_call_succeeded": strconv.FormatBool(receipt.APICallSucceeded),
			"delivery_confirmed": strconv.FormatBool(receipt.DeliveryConfirmed),
		},
	}); err != nil {
		return result, err
	}

	if sendErr != nil {
		result.Detail["action_error"] = sendErr.Error()
		return result, sendErr
	}

	// ClaimedRecovered stays false. A payment link that was sent is not a
	// payment, and this project does not observe a person coming back.
	return result, nil
}

// ActionForClass is the arm's own class-to-action table.
//
// It duplicates batch.CorrectActionFor on purpose, and the duplication is the
// point. One of the two is the answer key the scorer grades against and the
// other is the rule the arm follows, and a single shared function would mean
// the arm was graded against itself. They agree today; when a later phase
// changes what an arm does about a class, only this one moves, and the score
// moves with it.
func ActionForClass(c classify.Class) string {
	switch c {
	case classify.TransientRetryEligible, classify.RetryEligible:
		return ActionRetrySameInstrument
	case classify.ReauthRequired:
		return ActionRequestReauth
	case classify.NewInstrumentRequired:
		return ActionRequestNewInstrument
	default:
		return ActionDoNothing
	}
}

// ActionDoNothing is what ActionForClass returns for a class no action suits.
// It is batch.ActionDoNothing's string, so an outcome scores against the
// manifest with no translation table in between.
const ActionDoNothing = "do_nothing"
