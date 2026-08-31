package recovery

import (
	"context"
	"errors"

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
func NewFakeAttempter(f *razorpay.Fake) *FakeAttempter { return &FakeAttempter{} }

// Attempt makes one attempt on the fake gateway.
func (a *FakeAttempter) Attempt(ctx context.Context, order batch.AgentVisibleOrder, card string) (AttemptRecord, error) {
	return AttemptRecord{}, nil
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
	fallback  string
}

// NewLiveAttempter returns a LiveAttempter. An order with no entry in outcomes
// settles as a failure, because a materialiser that forgot an order should not
// be silently recovering it.
func NewLiveAttempter(a *razorpay.Attempter, outcomes map[string]string) *LiveAttempter {
	return &LiveAttempter{}
}

// Attempt makes one attempt through the checkout sequence.
func (a *LiveAttempter) Attempt(ctx context.Context, order batch.AgentVisibleOrder, card string) (AttemptRecord, error) {
	return AttemptRecord{}, nil
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
func ArmIDs() []string { return nil }

// NewArm returns the arm with the given id.
func NewArm(id string, opts ArmOptions) (*Arm, error) { return &Arm{}, nil }

// ID returns the arm's id.
func (a *Arm) ID() string { return "" }

// Act is the ActionFunc. It runs one action on one order and returns what it
// did, including when what it did was refuse.
func (a *Arm) Act(ctx context.Context, order batch.AgentVisibleOrder, class classify.Class) (ActionResult, error) {
	return ActionResult{}, nil
}

// ActionForClass is the arm's own class-to-action table.
//
// It duplicates batch.CorrectActionFor on purpose, and the duplication is the
// point. One of the two is the answer key the scorer grades against and the
// other is the rule the arm follows, and a single shared function would mean
// the arm was graded against itself. They agree today; when a later phase
// changes what an arm does about a class, only this one moves, and the score
// moves with it.
func ActionForClass(c classify.Class) string { return "" }
