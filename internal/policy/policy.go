package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
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
// Phase 5 gave every one of these a citation status. A number here is either
// cited, and CitedValues names the source, or a configured choice, and
// ConfiguredChoices says why no citation is attached. There is no third
// category, and TestEveryRuleDeclaresItsCitationStatus fails a rule in neither.
const (
	// DefaultMaxAttemptsPerOrder is 3, which is a conservative merchant policy
	// under the Visa reattempt cap rather than a number derived from one.
	//
	// Visa bulletin AI10325 permits 15 reattempts in 30 days for a Category 2
	// decline, and none at all for a Category 1 one. Three is well inside that,
	// and a merchant is free to be stricter than the network. The category was
	// recorded here as "Categories 2 and 3" until a first-hand read of the
	// bulletin on 2026-09-01 corrected it, and internal/networkcodes carries the
	// same correction. What phase 5 changed is that the constant names the bound it
	// sits under: TestMaxAttemptsSitsUnderTheVisaReattemptCap fails if someone
	// raises it past 15, which is where the sentence in CitedValues would stop
	// being true.
	//
	// The rolling 30 day window is not implemented. This policy's store holds
	// no history that spans runs, and building one to enforce a bound a cap of
	// 3 is nowhere near would be a feature written to decorate a citation.
	// internal/networkcodes carries the cap so the gap is visible.
	//
	// It is a flat per-order cap and it does not know about
	// batch.MaxLegitAttemptsFor, which gives a class between 0 and 3. That gap
	// is deliberate and is what the attempt-budget-exhausted bait order
	// catches. The phase 2 report has the finding.
	DefaultMaxAttemptsPerOrder = 3

	// DefaultCooldown is a configured choice. No industry source publishes a
	// value at this scale.
	//
	// The shortest scheme-native retry interval anyone publishes is the
	// Mastercard automated-clearing schedule, which starts at one hour. The
	// closest Indian regulatory number is the RBI e-mandate 24 hour pre-debit
	// notice, which is a notice floor and not a rate. Neither is a source for
	// 30 seconds, and attaching one would be worse than attaching nothing,
	// because it would look checked.
	DefaultCooldown = 30 * time.Second

	// DefaultAmountCeilingPaise is Rs 15,000, the threshold above which the RBI
	// e-mandate framework requires an additional factor of authentication.
	//
	// That is a real Indian payments line between an amount that may be taken
	// unattended and an amount that needs a human in the loop, which is exactly
	// the question R3 asks. It replaced 450000 on 2026-09-01.
	//
	// The number it replaced was tuned to a result, and that is why it went.
	// The ceiling was 400000 until the first fake-layer run on 2026-08-31,
	// where it escalated a quarter of the batch on amount alone and swamped
	// every escalation number with orders whose ground truth said retry. It was
	// moved to 450000 in response to that run. A threshold picked after seeing
	// what it did to a table has no argument behind it, however honestly the
	// move was disclosed, and phase 5 replaced it with one that has an
	// argument. The batch amount range moved to straddle the new value so the
	// rule can still fire: internal/batch.
	DefaultAmountCeilingPaise = 1500000

	// DefaultActionBudget is a configured choice. It bounds the blast radius of
	// one run of this program and is not a payments quantity, so there is
	// nothing for it to cite.
	DefaultActionBudget = 500
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
	// ActionBudget caps actions across the whole run. R5. Zero means
	// DefaultActionBudget.
	//
	// Zero used to mean a real cap of zero, on the reasoning that "evaluate
	// everything and act on nothing" is a thing a run might want to say. The
	// first real batch run on 2026-08-31 showed what that costs: policy.New
	// with a zero Config is documented as the standard policy, and the
	// standard policy denied all 40 orders under R5. A run that wants to act
	// on nothing has the kill switch, which is the control built for exactly
	// that and which says so in its rule id. DECISIONS.md has the entry.
	ActionBudget int
	// KillSwitch is the flag half of R8. The file half is KillSwitchFile,
	// which the runner reads and folds into State.KillSwitchEngaged, because
	// Evaluate does no I/O.
	KillSwitch bool
}

// withDefaults fills the zero fields.
func (c Config) withDefaults() Config {
	if c.MaxAttemptsPerOrder <= 0 {
		c.MaxAttemptsPerOrder = DefaultMaxAttemptsPerOrder
	}
	if c.Cooldown <= 0 {
		c.Cooldown = DefaultCooldown
	}
	if c.NotifyWindow <= 0 {
		c.NotifyWindow = c.Cooldown
	}
	if c.AmountCeilingPaise <= 0 {
		c.AmountCeilingPaise = DefaultAmountCeilingPaise
	}
	if c.ActionBudget <= 0 {
		c.ActionBudget = DefaultActionBudget
	}
	return c
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
	// zero. It is filled on every decision, including the ones another rule
	// made, so a reviewer reading a kill-switch denial can still see how much
	// budget the order had.
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
func New(cfg Config, c clock.Clock) *Policy {
	if c == nil {
		c = clock.Real()
	}
	return &Policy{cfg: cfg.withDefaults(), clock: c}
}

// Config returns the settings in force, with the defaults filled in.
func (p *Policy) Config() Config { return p.cfg }

// Evaluate decides one proposed action.
//
// It is pure: it reads cfg, the injected clock, state, and req, and it touches
// nothing else. The rules run in the order documented in PLAN.md and the first
// one to fire decides, which is why a golden matrix can pin the whole
// behaviour in one file.
//
// The order is a contract, not an implementation detail. R8 is first because a
// halt has to beat every reason an action might otherwise be fine. R9 is
// second because a replay is not a refusal of anything new and should not be
// reported as one. Then the two class rules, which say no action of any kind
// is right for this order. Then the ceiling, which is the last escalation.
// Then the three denials that are about budget rather than about the order.
func (p *Policy) Evaluate(state State, req Request) Decision {
	now := p.clock.Now()

	d := Decision{
		Remaining:      max(p.cfg.MaxAttemptsPerOrder-state.AttemptsMade, 0),
		IdempotencyKey: IdempotencyKey(req.OrderID, req.Action, req.AttemptNo),
	}

	decide := func(v Verdict, rule, reason string) Decision {
		d.Verdict, d.RuleID, d.Reason = v, rule, reason
		return d
	}

	// R8. A halt beats everything else there is to say about an action.
	if p.cfg.KillSwitch || state.KillSwitchEngaged {
		return decide(VerdictDeny, RuleKillSwitch, "the kill switch is engaged, so no action runs")
	}

	// R9. Already committed. Doing it again is a no-op, not a refusal.
	if state.IdempotencyKeySeen {
		d.IdempotentReplay = true
		return decide(VerdictDeny, RuleIdempotency,
			"this exact action was already committed, so it is a replay and does nothing")
	}

	// R7. Fail closed. A reason nothing recognises is not a reason to act.
	if req.Class == classify.Unclassified {
		return decide(VerdictEscalate, RuleUnknownFailClosed,
			"the failure did not classify, so no automated action is justified")
	}

	// R4. A block is a block.
	//
	// The class this reads is now backed by published rules rather than by a
	// stand-in. classify.NeverRetry holds Razorpay's documented
	// payment_risk_check_failed, and classify.ClassifyNetworkDeclineCode maps
	// the two network never-reattempt lists in internal/networkcodes onto the
	// same class: Visa Category 1 and Mastercard merchant advice code 03.
	//
	// No Razorpay payload this project has observed carries a raw network
	// response code, so nothing in any committed run has reached this rule
	// through the network path. It is covered by unit tests and by nothing
	// else, which HONEST-LIMITATIONS records rather than leaving to be
	// discovered.
	if req.Class == classify.NeverRetry {
		return decide(VerdictEscalate, RuleNeverRetryClass,
			"the failure class forbids any further attempt on this payment")
	}

	// R3. Strictly above the ceiling. At the ceiling is inside it.
	if req.AmountPaise > p.cfg.AmountCeilingPaise {
		return decide(VerdictEscalate, RuleAmountCeiling,
			fmt.Sprintf("%d paise is above the %d paise ceiling for an unattended action",
				req.AmountPaise, p.cfg.AmountCeilingPaise))
	}

	// R1. The per-order cap, which sits under the Visa 15-in-30 reattempt cap
	// rather than implementing it. See DefaultMaxAttemptsPerOrder.
	if state.AttemptsMade >= p.cfg.MaxAttemptsPerOrder {
		return decide(VerdictDeny, RuleMaxAttempts,
			fmt.Sprintf("the order has had %d of %d permitted attempts",
				state.AttemptsMade, p.cfg.MaxAttemptsPerOrder))
	}

	// R2. The interval between this run's own actions on one order. A zero
	// LastActionAt means this run has not acted, which is not a violation.
	//
	// The interval is a configured choice. ConfiguredChoices says why nothing
	// is cited for it.
	if !state.LastActionAt.IsZero() {
		if elapsed := now.Sub(state.LastActionAt); elapsed < p.cfg.Cooldown {
			return decide(VerdictDeny, RuleCooldown,
				fmt.Sprintf("the last action on this order was %s ago, inside the %s cooldown",
					elapsed, p.cfg.Cooldown))
		}
	}

	// R6. One notification per order per window. A retry is not a
	// notification, so this rule has nothing to say about one.
	//
	// The window is a configured choice, for the same reason R2's is.
	if IsNotifyAction(req.Action) && !state.LastNotifyAt.IsZero() {
		if elapsed := now.Sub(state.LastNotifyAt); elapsed < p.cfg.NotifyWindow {
			return decide(VerdictDeny, RuleNotifyRate,
				fmt.Sprintf("the last notification on this order was %s ago, inside the %s window",
					elapsed, p.cfg.NotifyWindow))
		}
	}

	// R5. The run-wide cap.
	if state.ActionsThisRun >= p.cfg.ActionBudget {
		return decide(VerdictDeny, RuleActionBudget,
			fmt.Sprintf("the run has spent %d of its %d action budget",
				state.ActionsThisRun, p.cfg.ActionBudget))
	}

	return decide(VerdictAllow, RuleAllow, "no rule refused this action")
}

// IdempotencyKey is sha256(order_id|action|attempt_no), hex encoded.
func IdempotencyKey(orderID, action string, attemptNo int) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%d", orderID, action, attemptNo))
	return hex.EncodeToString(sum[:])
}

// ShortKeyLen is how much of an idempotency key goes in an audit row.
//
// Twelve, and the number is load-bearing rather than aesthetic.
// internal/redact replaces any run of 13 or more digits with its marker,
// because that is the shape of a card number, and a 64 character hex digest
// contains such a run about five percent of the time. Four of the eighty keys
// in the first committed fake-layer run came out of the ledger with
// "[redacted]" in the middle of them.
//
// Twelve characters cannot contain a run of thirteen digits, so a short key
// passes the redactor unchanged whatever it hashes to. Nothing is lost: the
// audit row already carries the order id, the proposed action, and the attempt
// number, which are the three inputs, so a reviewer who wants the full key can
// recompute it.
//
// The alternative was to loosen the card pattern so it does not match inside a
// longer alphanumeric token. That is a change to a security control, made to
// fix a display problem, and it is the wrong direction to take one.
const ShortKeyLen = 12

// ShortKey is the prefix of an idempotency key that goes in an audit row.
func ShortKey(key string) string {
	if len(key) <= ShortKeyLen {
		return key
	}
	return key[:ShortKeyLen]
}

// KillSwitchFile reports whether the kill-switch file at path exists.
//
// This is the I/O half of R8 and it lives outside Evaluate on purpose. An
// empty path means no file was configured. A missing file is not engaged and
// not an error. Anything else that stops the path being readable is an error,
// because a kill switch that fails open is not a kill switch.
func KillSwitchFile(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	switch _, err := os.Stat(path); {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("policy: the kill-switch path %s could not be read, so it cannot be trusted to be clear: %w", path, err)
	}
}

// IsNotifyAction reports whether an action sends a notification, which is what
// R6 rate limits.
func IsNotifyAction(action string) bool {
	return action == ActionRequestReauth || action == ActionRequestNewInstrument
}

// citedValues and configuredChoices are the phase 5 declaration: every rule id
// appears in exactly one of them.
//
// This is a map rather than a paragraph because a paragraph cannot be tested.
// TestEveryRuleDeclaresItsCitationStatus walks RuleIDs and fails a rule that is
// in neither map or in both, so a tenth rule cannot arrive without someone
// deciding which of the two things its number is.
var citedValues = map[string]string{
	RuleMaxAttempts:       "Visa bulletin AI10325: a Category 2 decline may be reattempted up to 15 times in 30 days. The configured 3 is a conservative merchant policy under that cap, not a value read off it, and the rolling window is not implemented.",
	RuleAmountCeiling:     "RBI e-mandate framework: Rs 15,000 is the threshold above which an additional factor of authentication is required. 1500000 paise.",
	RuleNeverRetryClass:   "Visa Category 1 and Mastercard merchant advice code 03, both in internal/networkcodes with their sources, plus Razorpay's documented payment_risk_check_failed reason. The Category 1 code list is a processor reconstruction rather than the bulletin's own: networkcodes.VisaCategory1IsReconstructed.",
	RuleUnknownFailClosed: "Not an industry value. The rule is the absence of one: a failure no documented vocabulary recognises justifies no action, which is what the Razorpay documented reason lists in internal/classify are the boundary of.",
}

var configuredChoices = map[string]string{
	RuleCooldown:     "no citable industry value exists at seconds scale. The shortest scheme-native retry interval published is the Mastercard automated-clearing schedule, which starts at one hour, and the RBI e-mandate 24 hour pre-debit notice is a notice floor rather than a rate.",
	RuleNotifyRate:   "the same. A published minimum interval between two messages to one customer about one failed payment does not exist at this scale, and the RBI notice floor is about a different thing.",
	RuleActionBudget: "a blast-radius bound on one run of this program. Not a payments quantity, so there is nothing for it to cite.",
	RuleKillSwitch:   "an operational control. A halt is a property of this system, not of any payment network.",
	RuleIdempotency:  "a correctness property of this system's own action ledger.",
	RuleAllow:        "the id carried on a decision no rule refused. It has no value to cite or to choose.",
}

// CitedValues returns the rules whose value comes from a published source,
// keyed by rule id, with the source as the value.
func CitedValues() map[string]string { return maps.Clone(citedValues) }

// ConfiguredChoices returns the rules whose value this project chose, keyed by
// rule id, with the reason no citation is attached as the value.
func ConfiguredChoices() map[string]string { return maps.Clone(configuredChoices) }

// RuleIDs returns every rule id, in evaluation order, with the default allow
// last because it is what is left when nothing refused.
func RuleIDs() []string {
	return []string{
		RuleKillSwitch,
		RuleIdempotency,
		RuleUnknownFailClosed,
		RuleNeverRetryClass,
		RuleAmountCeiling,
		RuleMaxAttempts,
		RuleCooldown,
		RuleNotifyRate,
		RuleActionBudget,
		RuleAllow,
	}
}
