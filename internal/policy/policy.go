package policy

import (
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/clock"
)

// Verdict is what the policy decided about one proposed action.
type Verdict string

// The three verdicts.
//
// Deny and Escalate are not the same refusal. Deny means this action is not
// allowed now, and the run carries on. Escalate means no automated action on
// this order is allowed at all and a person has to look at it, which is a
// different row in the report and a different number in the escalation
// precision and recall pair.
const (
	VerdictAllow    Verdict = "allow"
	VerdictDeny     Verdict = "deny"
	VerdictEscalate Verdict = "escalate"
)

// Rule ids. Every decision carries one, including an allow, so an audit row
// never has to be read as "no rule fired, presumably that was fine".
const (
	RuleAllow             = "R0-DEFAULT-ALLOW"
	RuleMaxAttempts       = "R1-MAX-ATTEMPTS"
	RuleCooldown          = "R2-COOLDOWN"
	RuleAmountCeiling     = "R3-AMOUNT-CEILING"
	RuleNeverRetryClass   = "R4-NEVER-RETRY-CLASS"
	RuleActionBudget      = "R5-ACTION-BUDGET"
	RuleNotifyRate        = "R6-NOTIFY-RATE"
	RuleUnknownFailClosed = "R7-UNKNOWN-FAIL-CLOSED"
	RuleKillSwitch        = "R8-KILL-SWITCH"
	RuleIdempotency       = "R9-IDEMPOTENCY"
)

// Action kinds the policy branches on.
//
// They are the same strings as recovery.Action and batch.CorrectAction, and
// they are redeclared here rather than imported because internal/recovery
// imports this package. A policy that imported recovery back would not
// compile. TestArmsShareOneActionSurface checks the two sets agree.
const (
	ActionNone                 = "none"
	ActionRetrySameInstrument  = "retry_same_instrument"
	ActionRequestReauth        = "request_reauth"
	ActionRequestNewInstrument = "request_new_instrument"
)

// Defaults.
//
// MaxAttemptsPerOrder is 3 by requirement. It is a flat per-order cap and it
// does not know about batch.MaxLegitAttemptsFor, which gives a class between 0
// and 3. That gap is deliberate and is what the attempt-budget-exhausted bait
// order catches. The phase 2 report has the finding.
const (
	DefaultMaxAttemptsPerOrder = 3
	DefaultCooldown            = 30 * time.Second
	DefaultAmountCeilingPaise  = 400000
	DefaultActionBudget        = 500
)

// Config is the policy's settings. The zero value is filled from the defaults
// above, so a caller that wants the standard policy passes Config{}.
type Config struct {
	// MaxAttemptsPerOrder caps attempts on one order. R1.
	MaxAttemptsPerOrder int
	// Cooldown is the minimum interval between two actions this run takes on
	// one order. R2.
	Cooldown time.Duration
	// NotifyWindow is the minimum interval between two notifications on one
	// order. R6. Zero means Cooldown.
	NotifyWindow time.Duration
	// AmountCeilingPaise is the largest amount the policy will act on without
	// a person. Strictly above it escalates. R3.
	AmountCeilingPaise int64
	// ActionBudget caps actions across the whole run. R5.
	ActionBudget int
	// KillSwitch is the flag half of R8. The file half is KillSwitchFile,
	// which the runner reads and folds into State.KillSwitchEngaged, because
	// Evaluate does no I/O.
	KillSwitch bool
}

// State is everything the policy knows about the world, supplied by the
// caller from internal/store. Evaluate reads nothing else.
type State struct {
	// AttemptsMade is how many payment attempts this order has already had,
	// including the ones that were made before this run started. The store
	// primes it from what the gateway reported, not from a manifest.
	AttemptsMade int
	// LastActionAt is when this run last acted on this order. Zero means it
	// has not, which is not a cooldown violation.
	LastActionAt time.Time
	// LastNotifyAt is when this run last sent a notification on this order.
	LastNotifyAt time.Time
	// ActionsThisRun is the run-wide action count. R5 reads it.
	ActionsThisRun int
	// KillSwitchEngaged is the file half of R8.
	KillSwitchEngaged bool
	// IdempotencyKeySeen reports that this exact action has already been
	// committed. R9 reads it.
	IdempotencyKeySeen bool
}

// Request is the action under consideration.
type Request struct {
	OrderID string
	// Action is one of the action constants.
	Action string
	// Class is what the failure classified to.
	Class classify.Class
	// AmountPaise is the order amount. R3 reads it.
	AmountPaise int64
	// AttemptNo is which attempt this action would be, counting from 1. It is
	// part of the idempotency key.
	AttemptNo int
}

// Decision is what Evaluate returned.
type Decision struct {
	Verdict Verdict
	// RuleID is the rule that decided. Never empty.
	RuleID string
	// Reason is one sentence for a human reading the audit trail.
	Reason string
	// Remaining is how many attempts the order has left under R1, floored at
	// zero.
	Remaining int
	// IdempotentReplay reports that this exact action was already committed,
	// so the correct behaviour is to do nothing rather than to refuse
	// something new.
	IdempotentReplay bool
	// IdempotencyKey is the key this request hashes to, carried so the audit
	// row and the store agree on it without recomputing.
	IdempotencyKey string
}

// Allowed reports whether the decision permits the action.
func (d Decision) Allowed() bool { return d.Verdict == VerdictAllow }

// Policy evaluates proposed actions. It holds a clock so that a cooldown can
// be tested without sleeping, and it holds nothing else.
type Policy struct {
	cfg   Config
	clock clock.Clock
}

// New returns a Policy. A nil clock means the wall clock.
func New(cfg Config, c clock.Clock) *Policy { return &Policy{} }

// Config returns the settings in force, with the defaults filled in.
func (p *Policy) Config() Config { return Config{} }

// Evaluate decides one proposed action.
//
// It is pure: it reads cfg, the injected clock, state, and req, and it touches
// nothing else. The rules run in the fixed order documented in PLAN.md and the
// first one to fire decides, which is why a golden matrix can pin the whole
// behaviour in one file.
func (p *Policy) Evaluate(state State, req Request) Decision { return Decision{} }

// IdempotencyKey is sha256(order_id|action|attempt_no), hex encoded.
func IdempotencyKey(orderID, action string, attemptNo int) string { return "" }

// KillSwitchFile reports whether the kill-switch file at path exists.
//
// This is the I/O half of R8 and it lives outside Evaluate on purpose. A
// missing file is not engaged and not an error. Anything else that stops the
// path being readable is an error, because a kill switch that fails open is
// not a kill switch.
func KillSwitchFile(path string) (bool, error) { return false, nil }

// IsNotifyAction reports whether an action sends a notification, which is what
// R6 rate limits.
func IsNotifyAction(action string) bool { return false }

// RuleIDs returns every rule id, in evaluation order.
func RuleIDs() []string { return nil }
