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
	"github.com/lopster568/rzp-recovery-agent/internal/quiet"
)

// Verdict is what the policy decided about one proposed action.
type Verdict string

// The three verdicts.
//
// Deny and Escalate are not the same refusal. Deny means this action is not
// allowed now, and the run carries on. Escalate means no automated action on
// this item is allowed at all and a person has to look at it, which is a
// different row in the report and a different number in the escalation
// precision and recall pair.
const (
	VerdictAllow    Verdict = "allow"
	VerdictDeny     Verdict = "deny"
	VerdictEscalate Verdict = "escalate"
)

// Rule ids. Every decision carries one, including an allow, so an audit row
// never has to be read as "no rule fired, presumably that was fine".
//
// The numbering is historical rather than ordinal: R1 through R9 kept the ids
// they had in the retry engine so that an old run's ledger and a new one's can
// be read side by side, and the rules the pivot added took the next free
// numbers. There is no R14. Write-off approval was drafted as one and then
// folded into R3, because "an amount above which a person decides" and "a
// write-off, which a person always decides" are the same control asking about
// two thresholds, and splitting them would have put the same escalation in two
// rows of the report.
const (
	RuleAllow             = "R0-DEFAULT-ALLOW"
	RuleMaxTouches        = "R1-MAX-TOUCHES"
	RuleCooldown          = "R2-COOLDOWN"
	RuleHumanApproval     = "R3-HUMAN-APPROVAL-CEILING"
	RuleNeverContact      = "R4-NEVER-CONTACT"
	RuleActionBudget      = "R5-ACTION-BUDGET"
	RuleNotifyRate        = "R6-NOTIFY-RATE"
	RuleUnknownFailClosed = "R7-UNKNOWN-FAIL-CLOSED"
	RuleKillSwitch        = "R8-KILL-SWITCH"
	RuleIdempotency       = "R9-IDEMPOTENCY"
	RuleNoContactChannel  = "R10-NO-CONTACT-CHANNEL"
	RuleNotYetDue         = "R11-NOT-YET-DUE"
	RuleQuietHours        = "R12-QUIET-HOURS"
	RuleDisputed          = "R13-DISPUTED-NEVER-CHASE"
	RulePromiseHold       = "R15-PROMISE-HOLD"
)

// Defaults.
//
// Every one of these is either a cited industry value, and CitedValues names
// the source, or a configured choice, and ConfiguredChoices says why no
// citation is attached. There is no third category, and
// TestEveryRuleDeclaresItsCitationStatus fails a rule in neither. The
// per-source cadence numbers live in sources.go and are configured choices to
// a number.
const (
	// DefaultMaxTouchesPerItem is the cap the table falls back to for a source
	// with no row, which is a source R7 escalates anyway. A real item's cap
	// comes from SourceParams.
	DefaultMaxTouchesPerItem = 3

	// DefaultCooldown is the fallback minimum interval between two contacts
	// about one item, used for a source with no row in the table.
	//
	// It is 24 hours, and the number it replaced was 30 seconds. Thirty
	// seconds was a retry rate: it bounded how fast the old engine
	// re-presented a card to a gateway, which is a machine-to-machine
	// interval. This engine's R2 bounds how often this merchant sends a
	// message about one debt, and there is no reading of that question under
	// which the answer is half a minute. The constant did not move, it changed
	// what it is about.
	DefaultCooldown = 24 * time.Hour

	// DefaultNotifyWindow is R6's global minimum interval between any two
	// notifications this run sends, to anyone.
	//
	// It is a send-rate bound on one run of this program rather than a
	// per-customer politeness rule, which is what R2 is. One second is enough
	// to stop a run that has just detected two hundred overdue invoices from
	// emitting two hundred sends in a burst, and it is not a claim about what
	// any customer experiences.
	DefaultNotifyWindow = time.Second

	// DefaultAmountCeilingPaise is 1500000 paise, Rs 15,000, above which R3
	// wants a person.
	//
	// The number is kept. The citation that used to be on it is gone, and its
	// removal is the point. It read "RBI e-mandate framework: Rs 15,000 is the
	// threshold above which an additional factor of authentication is
	// required", which is a true sentence about e-mandates and a category
	// error here. The e-mandate threshold separates an amount that may be
	// debited unattended under a registered mandate from one that may not.
	// Nothing this engine does is a debit, attended or otherwise: it sends
	// messages and mints links, and the customer authenticates every payment
	// themselves. Applied to a link, the threshold discriminates nothing.
	//
	// What survives is the operator's own question, which is a real one: above
	// what outstanding amount should a person look at the item before the
	// agent chases it. Rs 15,000 is a reasonable answer to that and it is
	// nobody's published one. docs/INDIA-CONSTRAINTS-AUDIT.md section 2 has
	// the finding that retired the citation.
	DefaultAmountCeilingPaise = 1500000

	// DefaultWriteOffFloorPaise is 10000 paise, Rs 100, the amount at or below
	// which a run may write an item off without a person.
	//
	// A write-off is the one action here that decides money will not be
	// collected, and it is terminal. R3 escalates it at any amount above this
	// floor, which is the great majority of items. The floor exists so that a
	// queue does not fill with rupee-scale rounding debris that no person will
	// ever be paid to look at. It is a configured choice and it is deliberately
	// small enough that getting it wrong costs a rupee.
	DefaultWriteOffFloorPaise = 10000

	// DefaultActionBudget is a configured choice. It bounds the blast radius of
	// one run of this program and is not a payments quantity, so there is
	// nothing for it to cite.
	DefaultActionBudget = 500
)

// Config is the policy's settings. The zero value is filled from the defaults
// above, so a caller that wants the standard policy passes Config{}.
type Config struct {
	// MaxTouchesPerItem overrides the per-source contact cap for every source.
	// R1. Zero means the table in sources.go decides, which is the ordinary
	// case.
	MaxTouchesPerItem int
	// Cooldown overrides the per-source contact interval for every source. R2.
	// Zero means the table decides.
	Cooldown time.Duration
	// NotifyWindow is the run-wide minimum interval between any two
	// notifications. R6. Zero means DefaultNotifyWindow.
	NotifyWindow time.Duration
	// AmountCeilingPaise is the largest amount due the policy will chase
	// without a person. Strictly above it escalates. R3.
	AmountCeilingPaise int64
	// WriteOffFloorPaise is the amount due at or below which a write-off may
	// run unattended. R3. Anything above it escalates, which is the point:
	// write-off is a human decision unless the sum is trivial.
	//
	// Zero means DefaultWriteOffFloorPaise. A caller that wants every
	// write-off to need a person sets it negative, and every amount due,
	// including zero, is then above the floor.
	WriteOffFloorPaise int64
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
	// ContactWindow is the band of the day a notification may go out in. R12.
	// The zero Window means quiet.DefaultWindow, 09:00 to 21:00.
	ContactWindow quiet.Window
	// ContactLocation is the zone ContactWindow is read in. Nil means IST.
	ContactLocation *time.Location
	// KillSwitch is the flag half of R8. The file half is KillSwitchFile,
	// which the runner reads and folds into State.KillSwitchEngaged, because
	// Evaluate does no I/O.
	KillSwitch bool

	// MaxAttemptsPerOrder is a deprecated spelling of MaxTouchesPerItem.
	//
	// Deprecated: the pivot renamed the concept. An attempt was a
	// re-presentment of a payment, which this engine does not do; a touch is
	// an outbound contact. Set MaxTouchesPerItem. This field is read only when
	// MaxTouchesPerItem is zero, and it exists so that packages another work
	// package owns keep compiling across the pivot.
	MaxAttemptsPerOrder int
}

// withDefaults fills the zero fields and folds the deprecated ones in.
func (c Config) withDefaults() Config {
	if c.MaxTouchesPerItem <= 0 {
		c.MaxTouchesPerItem = c.MaxAttemptsPerOrder
	}
	if c.MaxTouchesPerItem < 0 {
		c.MaxTouchesPerItem = 0
	}
	c.MaxAttemptsPerOrder = c.MaxTouchesPerItem
	if c.NotifyWindow <= 0 {
		c.NotifyWindow = DefaultNotifyWindow
	}
	if c.AmountCeilingPaise <= 0 {
		c.AmountCeilingPaise = DefaultAmountCeilingPaise
	}
	if c.WriteOffFloorPaise == 0 {
		c.WriteOffFloorPaise = DefaultWriteOffFloorPaise
	}
	if c.ActionBudget <= 0 {
		c.ActionBudget = DefaultActionBudget
	}
	c.ContactWindow = c.ContactWindow.WithDefault()
	if c.ContactLocation == nil {
		c.ContactLocation = quiet.IST()
	}
	return c
}

// State is everything the policy knows about the world, supplied by the caller
// from internal/store. Evaluate reads nothing else.
type State struct {
	// TouchesMade is how many outbound contacts this item has had over its
	// lifetime, including the ones made before this run started. R1 reads it.
	TouchesMade int
	// LastTouchAt is when this run last contacted the customer about this
	// item. Zero means it has not, which is not a cooldown violation. R2.
	LastTouchAt time.Time
	// LastNotifyAt is when this run last sent any notification, to anyone. R6
	// is a run-wide send rate, so this is not per item.
	LastNotifyAt time.Time
	// ActionsThisRun is the run-wide action count. R5 reads it.
	ActionsThisRun int
	// KillSwitchEngaged is the file half of R8.
	KillSwitchEngaged bool
	// IdempotencyKeySeen reports that this exact action has already been
	// committed. R9 reads it.
	IdempotencyKeySeen bool

	// AttemptsMade is a deprecated spelling of TouchesMade.
	//
	// Deprecated: set TouchesMade. Read only when TouchesMade is zero.
	AttemptsMade int
	// LastActionAt is a deprecated spelling of LastTouchAt.
	//
	// Deprecated: set LastTouchAt. Read only when LastTouchAt is zero.
	LastActionAt time.Time
}

// normalize folds the deprecated fields into the current ones.
func (s State) normalize() State {
	if s.TouchesMade == 0 {
		s.TouchesMade = s.AttemptsMade
	}
	if s.LastTouchAt.IsZero() {
		s.LastTouchAt = s.LastActionAt
	}
	return s
}

// Request is the action under consideration, against one risk item.
//
// It is a plain struct and it mirrors the fields of riskitem.RiskItem that the
// rules read, rather than embedding one. The engine below therefore has no
// opinion about where an item came from and can be driven from a table in a
// test with no fixtures. RequestFrom in riskitem.go is the adapter, and it is
// the only file in this package that imports the contract.
type Request struct {
	// RiskItemID is riskitem.RiskItem.ID. It is the first field of the
	// idempotency key.
	RiskItemID string
	// Source is one of the three source constants. R7 escalates anything else,
	// and SourceParams is keyed on it.
	Source string
	// Action is one of the lawful action constants. R7 escalates anything
	// else.
	Action string
	// Class is what the item's failure signal classified to, when it has one.
	// R7 reads Unclassified and R4 reads NeverRetry.
	Class classify.Class
	// SignalPresent reports that the item carried failure evidence at all.
	//
	// It is separate from Class because "no failure happened" and "a failure
	// happened and nothing recognised it" are different facts that both arrive
	// as classify.Unclassified. An abandoned cart is the first and is
	// ordinary; a failed payment whose reason nothing could read is the
	// second, and R7 escalates it.
	SignalPresent bool
	// AmountPaise is the full amount of the debt.
	AmountPaise int64
	// AmountDuePaise is what is still outstanding, which is what R3 weighs. A
	// partial payment makes it smaller than AmountPaise, and chasing a
	// customer for what they have already paid is the error that reading the
	// wrong one of these produces.
	//
	// Zero means nothing is outstanding. It does not mean "unset, use
	// AmountPaise", and it used to: normalize folded a zero up to the full
	// amount so that a caller filling only AmountPaise still reached R3. That
	// fold was the error this field's own comment warns about, written into
	// the engine. A fully collected item legitimately carries zero here, and
	// the fold silently reinflated it to the original debt, after which every
	// rule downstream weighed a balance that had already been paid and a
	// contact action against it could be allowed.
	//
	// So the field is authoritative and R4 escalates an item with nothing
	// outstanding. A caller that forgets to fill it gets an escalation naming
	// the reason rather than a chase for money that is not owed.
	AmountDuePaise int64
	// HasEmail and HasContact report which channels the item carries. R10
	// escalates rather than letting anything guess one.
	HasEmail   bool
	HasContact bool
	// SourceStatus is the Razorpay status of the resource behind the item,
	// such as cancelled or expired. R4 reads it.
	SourceStatus string
	// Disputed reports that somebody has contested this debt. R13 reads it.
	Disputed bool
	// AtRiskSince is when the debt started being at risk. R11 measures the
	// per-source grace period from it.
	AtRiskSince time.Time
	// PromiseHoldUntil is when an active promise to pay expires, from
	// promise.Store.ActiveHold. Zero means no promise is holding. R15.
	PromiseHoldUntil time.Time
	// TouchNo is which outbound contact this action would be, counting from 1.
	// It is part of the idempotency key.
	TouchNo int

	// OrderID is a deprecated spelling of RiskItemID.
	//
	// Deprecated: set RiskItemID. An item can be an invoice or a payment as
	// well as an order, so the old name was wrong for two of the three
	// sources. Read only when RiskItemID is empty.
	OrderID string
	// AttemptNo is a deprecated spelling of TouchNo.
	//
	// Deprecated: set TouchNo. Read only when TouchNo is zero.
	AttemptNo int
}

// normalize folds the deprecated fields in and fills AmountDuePaise.
func (r Request) normalize() Request {
	if r.RiskItemID == "" {
		r.RiskItemID = r.OrderID
	}
	if r.TouchNo == 0 {
		r.TouchNo = r.AttemptNo
	}
	return r
}

// Decision is what Evaluate returned.
type Decision struct {
	Verdict Verdict
	// RuleID is the rule that decided. Never empty.
	RuleID string
	// Reason is one sentence for a human reading the audit trail.
	Reason string
	// Remaining is how many contacts the item has left under R1, floored at
	// zero. It is filled on every decision, including the ones another rule
	// made, so a reviewer reading a kill-switch denial can still see how much
	// budget the item had.
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

// Policy evaluates proposed actions. It holds a clock so that a cooldown and a
// quiet-hours band can be tested without sleeping, and it holds nothing else.
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

// ParamsFor returns the cadence in force for one source, with any Config-wide
// override applied.
//
// A source with no row gets the fallback defaults. That is not a way to run an
// unknown source past the rules: R7 escalates one before any rule that reads
// this table, and the fallback exists so that Decision.Remaining on that
// escalation is a number rather than a zero that reads as "no contacts left".
func (p *Policy) ParamsFor(source string) SourceParams {
	params, ok := sourceParams[source]
	if !ok {
		params = SourceParams{Source: source, MaxTouches: DefaultMaxTouchesPerItem, Cooldown: DefaultCooldown}
	}
	if p.cfg.MaxTouchesPerItem > 0 {
		params.MaxTouches = p.cfg.MaxTouchesPerItem
	}
	if p.cfg.Cooldown > 0 {
		params.Cooldown = p.cfg.Cooldown
	}
	return params
}

// Evaluate decides one proposed action.
//
// It is pure: it reads cfg, the injected clock, state, and req, and it touches
// nothing else. The rules run in the order below and the first one to fire
// decides, which is why a golden matrix can pin the whole behaviour in one
// file.
//
// The order is a contract, not an implementation detail.
//
//	R8  kill switch      a halt beats every reason an action might be fine
//	R9  idempotency      a replay is not a refusal of anything new
//	R7  fail closed      an item or an action nothing recognises
//	R11 not yet due      this is not a debt yet, so nothing else applies
//	R10 no channel       there is nowhere to send, and nothing may guess one
//	R4  never contact    the gateway, the resource, or a zero balance says no
//	R15 promise hold     we said we would wait
//	R12 quiet hours      it is the middle of the night
//	R13 disputed         somebody contests the debt
//	R3  human approval   the amount, or a write-off, wants a person
//	R1  max touches      the item has had its lifetime contacts
//	R2  cooldown         the last contact was too recent
//	R6  notify rate      the run is sending too fast
//	R5  action budget    the run has spent its blast radius
//	R0  allow            nothing refused
//
// Most rules below skip an action IsSafeAction reports true for. Escalating,
// doing nothing, and logging a promise are what is left when this engine has
// decided not to chase an item, so a rule that refused them would leave no
// verdict able to say "hand this to a person". R8 and R9 are the exceptions and
// have to be: a halt stops everything, and a replay of an escalation is still a
// replay. R7 is the third, because an action nothing recognises is not safe
// merely by being unrecognised.
//
// Two rules narrow it further to IsContactAction, R10 and R4, because they are
// about reaching a customer and a write-off is neither safe nor a contact.
func (p *Policy) Evaluate(state State, req Request) Decision {
	now := p.clock.Now()
	state, req = state.normalize(), req.normalize()
	params := p.ParamsFor(req.Source)
	safe := IsSafeAction(req.Action)

	d := Decision{
		Remaining:      max(params.MaxTouches-state.TouchesMade, 0),
		IdempotencyKey: IdempotencyKey(req.RiskItemID, req.Action, req.TouchNo),
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

	// R7. Fail closed, in four ways.
	//
	// An unrecognised source and an unlawful action are both a caller handing
	// this engine something it has no rule for, and the honest answer to that
	// is a person. The action arm is what keeps the retry vocabulary out: a
	// caller that has not been ported yet reaches this line rather than
	// falling through to an allow.
	//
	// The two signal arms are the old R7 split in half. A failed payment is
	// required to carry failure evidence, because a payment that failed for no
	// readable reason is exactly the case the rule was written for. Every
	// other source is allowed to carry none, because an abandoned cart has no
	// failure to report and treating its empty Signal as an unreadable one
	// would escalate the whole unpaid-order queue.
	switch {
	case !KnownSource(req.Source):
		return decide(VerdictEscalate, RuleUnknownFailClosed,
			fmt.Sprintf("the source %q is not one this engine has rules for", req.Source))
	case !IsLawfulAction(req.Action):
		return decide(VerdictEscalate, RuleUnknownFailClosed,
			fmt.Sprintf("the action %q is not in the lawful set, so no rule here governs it", req.Action))
	case params.RequiresSignal && !req.SignalPresent:
		return decide(VerdictEscalate, RuleUnknownFailClosed,
			"the payment is reported as failed and carries no failure signal, so nothing can be said about it")
	case req.SignalPresent && req.Class == classify.Unclassified:
		return decide(VerdictEscalate, RuleUnknownFailClosed,
			"the failure did not classify, so no automated action is justified")
	}

	// R11. A debt that is not due yet is not a debt to chase.
	//
	// The grace period is per source and comes from the table. A zero
	// AtRiskSince with a non-zero grace is a denial rather than a pass: the
	// detector reports the instant, and without it there is no way to show the
	// window has gone by. A source whose grace is zero is due the moment it is
	// seen, so the missing timestamp cannot change the answer there.
	if !safe && params.Grace > 0 {
		switch {
		case req.AtRiskSince.IsZero():
			return decide(VerdictDeny, RuleNotYetDue,
				fmt.Sprintf("the item carries no at-risk instant, so the %s grace for a %s cannot be shown to have passed",
					params.Grace, req.Source))
		case now.Sub(req.AtRiskSince) < params.Grace:
			return decide(VerdictDeny, RuleNotYetDue,
				fmt.Sprintf("the item has been at risk for %s, inside the %s grace for a %s",
					now.Sub(req.AtRiskSince), params.Grace, req.Source))
		}
	}

	// R10. Nowhere to send, and nothing here invents an address.
	//
	// The general arm is the one the frozen contract states: an item with
	// neither an email nor a phone number cannot be contacted at all. The two
	// specific arms are the same rule at channel resolution, because an email
	// notification on an item that carries only a phone number is a caller
	// about to pick whichever channel it has, which is the guess this rule
	// exists to refuse.
	if IsContactAction(req.Action) {
		switch {
		case !req.HasEmail && !req.HasContact:
			return decide(VerdictEscalate, RuleNoContactChannel,
				"the item carries no email address and no phone number, and nothing here may guess one")
		case req.Action == ActionNotifyEmail && !req.HasEmail:
			return decide(VerdictEscalate, RuleNoContactChannel,
				"an email notification was proposed for an item that carries no email address")
		case req.Action == ActionNotifySMS && !req.HasContact:
			return decide(VerdictEscalate, RuleNoContactChannel,
				"an SMS notification was proposed for an item that carries no phone number")
		}
	}

	// R4. A block is a block.
	//
	// The class arm is Razorpay's documented payment_risk_check_failed, which
	// internal/classify maps to NeverRetry along with the reason list it was
	// read from. A payment the gateway's own risk check refused is not a
	// payment to chase a customer about, whatever the debt is worth.
	//
	// The status arm is a resource with nothing left to collect on it: a
	// cancelled invoice or an expired payment link. Chasing one asks a
	// customer to pay against a handle that will not take the money.
	//
	// The Visa Category 1 and Mastercard merchant advice code 03 half of the
	// old R4 is gone. Both lists bound merchant-initiated re-presentment of a
	// card authorization, which is a mechanism with no lawful counterpart for
	// a one-off Indian payment and which this engine no longer has an action
	// for. No committed run ever reached it: no Razorpay payload this project
	// has observed carries a raw network response code.
	// The guard is IsContactAction rather than the !safe the neighbouring rules
	// use, because this rule is about contacting somebody and the actions it
	// should not reach are not all safe ones. Writing an item off is the case:
	// it is deliberately outside IsSafeAction, so a !safe guard caught it here,
	// and the result was that a cancelled invoice with fifty paise outstanding
	// escalated under a rule whose own reason says there is nothing left to
	// collect, rather than being closed by the write-off floor that exists to
	// keep rounding debris out of somebody's queue. A write-off is still gated:
	// R3 escalates one at any amount above that floor.
	if IsContactAction(req.Action) {
		switch {
		case req.Class == classify.NeverRetry:
			return decide(VerdictEscalate, RuleNeverContact,
				"the gateway's risk check refused this payment, so the customer is not to be chased about it")
		case IsTerminalSourceStatus(req.SourceStatus):
			return decide(VerdictEscalate, RuleNeverContact,
				fmt.Sprintf("the source resource is %s, so there is nothing left to collect against it", req.SourceStatus))
		case req.AmountDuePaise <= 0:
			// Nothing is owed, so there is nobody to chase. This is also where
			// a caller that left AmountDuePaise unset lands, which is the
			// point: the alternative was to read a zero as "use the gross
			// amount", and that chases people for money they have paid.
			return decide(VerdictEscalate, RuleNeverContact,
				"nothing is outstanding on this item, so there is nothing to contact anybody about")
		}
	}

	// R15. The merchant said it would wait, so it waits.
	//
	// Logging a further promise is still allowed, which is how a renegotiation
	// gets recorded, and so is escalating, because a hold is not a reason a
	// person may not look.
	//
	// The guard is !safe rather than IsContactAction so that it also covers a
	// write-off. Closing an item as not collectable inside a window the
	// merchant said it would wait out is not a contact, but it is the same
	// broken promise from the customer's side, and it destroys the evidence of
	// whether they kept theirs.
	if !safe && !req.PromiseHoldUntil.IsZero() && now.Before(req.PromiseHoldUntil) {
		return decide(VerdictDeny, RulePromiseHold,
			fmt.Sprintf("the customer promised to pay by %s and that hold has not expired",
				req.PromiseHoldUntil.UTC().Format(time.RFC3339)))
	}

	// R12. Outside the allowed contact band, nothing goes out.
	//
	// Only notifications. Minting a payment link at 02:00 wakes nobody, and
	// denying it would mean the link the morning's message needs does not
	// exist yet.
	if IsNotifyAction(req.Action) && !p.cfg.ContactWindow.Contains(now, p.cfg.ContactLocation) {
		return decide(VerdictDeny, RuleQuietHours,
			fmt.Sprintf("%s is outside the %s contact window", now.In(p.cfg.ContactLocation).Format("15:04 MST"), p.cfg.ContactWindow))
	}

	// R13. A contested debt is a conversation, not a queue item.
	if !safe && req.Disputed {
		return decide(VerdictEscalate, RuleDisputed,
			"the debt is disputed, and a disputed debt is never chased automatically")
	}

	// R3. The two thresholds above which a person decides.
	//
	// The write-off arm is first and it is the strict one. Writing an item off
	// says the money will not be collected and it is terminal, so it needs a
	// person at any amount above a floor small enough that the floor exists
	// only to keep rounding debris out of somebody's queue.
	//
	// The ceiling arm is the operator's own line, and it reads AmountDuePaise
	// rather than AmountPaise: a Rs 40,000 invoice with Rs 39,000 already paid
	// is a Rs 1,000 debt, and escalating it on the gross figure would put a
	// person in front of an item there is nothing to decide about.
	if req.Action == ActionCancelWriteOff && req.AmountDuePaise > p.cfg.WriteOffFloorPaise {
		return decide(VerdictEscalate, RuleHumanApproval,
			fmt.Sprintf("writing off %d paise is above the %d paise a run may write off unattended, and a write-off is terminal",
				req.AmountDuePaise, p.cfg.WriteOffFloorPaise))
	}
	if !safe && req.AmountDuePaise > p.cfg.AmountCeilingPaise {
		return decide(VerdictEscalate, RuleHumanApproval,
			fmt.Sprintf("%d paise is above the %d paise ceiling for an unattended action",
				req.AmountDuePaise, p.cfg.AmountCeilingPaise))
	}

	// R1. The lifetime contact cap, per source.
	//
	// It escalates rather than denying. An item that has had every message the
	// merchant is willing to send and is still unpaid is not an item to try
	// again later, it is an item somebody has to decide about.
	if IsContactAction(req.Action) && state.TouchesMade >= params.MaxTouches {
		return decide(VerdictEscalate, RuleMaxTouches,
			fmt.Sprintf("the item has had %d of the %d contacts a %s gets",
				state.TouchesMade, params.MaxTouches, req.Source))
	}

	// R2. The interval between two contacts about one item. A zero LastTouchAt
	// means this run has not contacted anyone about it, which is not a
	// violation.
	if IsContactAction(req.Action) && !state.LastTouchAt.IsZero() {
		if elapsed := now.Sub(state.LastTouchAt); elapsed < params.Cooldown {
			return decide(VerdictDeny, RuleCooldown,
				fmt.Sprintf("the last contact about this item was %s ago, inside the %s cooldown for a %s",
					elapsed, params.Cooldown, req.Source))
		}
	}

	// R6. The run-wide send rate. This is not about one customer: LastNotifyAt
	// is the last notification this run sent to anybody.
	if IsNotifyAction(req.Action) && !state.LastNotifyAt.IsZero() {
		if elapsed := now.Sub(state.LastNotifyAt); elapsed < p.cfg.NotifyWindow {
			return decide(VerdictDeny, RuleNotifyRate,
				fmt.Sprintf("this run sent a notification %s ago, inside its %s send window",
					elapsed, p.cfg.NotifyWindow))
		}
	}

	// R5. The run-wide cap.
	//
	// It bounds side effects, so it skips the actions that have none outside
	// this program. Escalating, doing nothing, and recording what a customer
	// said reach no Razorpay resource, and a spent budget refusing an
	// escalation would be the worst refusal in the file: Deny and Escalate are
	// different rows in the report and feed the escalation precision and
	// recall pair, so an item that needed a person would leave the run as a
	// denial and never appear in the number that counts them.
	//
	// This is the one place the rule changed rather than being kept. The retry
	// engine's R5 fired on everything, which was defensible when the fail-open
	// direction was "take no action" and there was no escalate-shaped action to
	// lose. It is not defensible now.
	if !safe && state.ActionsThisRun >= p.cfg.ActionBudget {
		return decide(VerdictDeny, RuleActionBudget,
			fmt.Sprintf("the run has spent %d of its %d action budget",
				state.ActionsThisRun, p.cfg.ActionBudget))
	}

	return decide(VerdictAllow, RuleAllow, "no rule refused this action")
}

// IdempotencyKey is sha256(risk_item_id|action|touch_no), hex encoded.
func IdempotencyKey(riskItemID, action string, touchNo int) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%d", riskItemID, action, touchNo))
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
// audit row already carries the item id, the proposed action, and the touch
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

// citedValues and configuredChoices are the declaration: every rule id appears
// in exactly one of them.
//
// This is a map rather than a paragraph because a paragraph cannot be tested.
// TestEveryRuleDeclaresItsCitationStatus walks RuleIDs and fails a rule that is
// in neither map or in both, so a new rule cannot arrive without someone
// deciding which of the two things its number is.
//
// The pivot emptied most of this file. The Visa 15-in-30 reattempt cap, the
// Mastercard merchant advice code 03 list, and the RBI e-mandate Rs 15,000
// threshold were all cited here on 2026-09-01 and all three are gone. The
// first two bound merchant-initiated re-presentment of a card authorization,
// which has no lawful counterpart for a one-off Indian payment and which this
// engine has no action for. The third is a true statement about e-mandates
// applied to a rule about sending someone a payment link, which is a category
// error. docs/INDIA-CONSTRAINTS-AUDIT.md sections 1 and 2 are the finding.
//
// What is left is one rule with a real source on it. That is a smaller number
// than the retry engine claimed and it is the honest one.
var citedValues = map[string]string{
	RuleNeverContact:      "Razorpay's documented payment_risk_check_failed reason, carried in internal/classify with the doc page and date it was read from. A payment the gateway's own risk check refused is not one to chase. The terminal-status half, a cancelled or expired source resource, is Razorpay's own documented status vocabulary for invoices and payment links.",
	RuleUnknownFailClosed: "Not an industry value. The rule is the absence of one: a failure no documented vocabulary recognises justifies no action, which is what the Razorpay documented reason lists in internal/classify are the boundary of. The same argument covers a source and an action this engine has no rule for.",
}

var configuredChoices = map[string]string{
	RuleMaxTouches:       "how many times a merchant is willing to message one customer about one debt. No regulator or scheme publishes this. The per-source spread in sources.go, three for a failed payment or an abandoned cart and four for an issued invoice, is a judgment about which customers have already acknowledged the debt, and it is nobody's number but this project's.",
	RuleCooldown:         "the interval between two of those messages, and the same absence of a source. It is at the scale of a day because that is the scale a person would recognise as a follow-up. The value it replaced was 30 seconds, which was a machine-to-machine retry rate for a mechanism this engine no longer has.",
	RuleNotifyRate:       "a run-wide send rate, so that one sweep of a large receivables ledger does not emit its whole queue in a burst. It is a property of this program rather than of any customer relationship, and there is nothing for it to cite.",
	RuleHumanApproval:    "operator-chosen, at both thresholds. Rs 15,000 was carried across the pivot as a number and not as a citation: it used to cite the RBI e-mandate additional-factor threshold, which is a real Indian line between an amount that may be debited unattended under a mandate and one that may not, and which discriminates nothing here because this engine debits nothing. The write-off floor is a queue-hygiene number and is small enough that getting it wrong costs a rupee.",
	RuleQuietHours:       "TRAI-shaped and NEEDS-VERIFICATION. India's commercial-communication regime for SMS runs through TRAI's DLT registration and does restrict delivery hours for some traffic, but no primary TRAI document was read by this project, and whether a payment reminder counts as promotional or transactional under it is unresolved. The 09:00 to 21:00 band in internal/quiet is therefore a merchant's own politeness rule. Do not quote it as a regulated window.",
	RuleDisputed:         "a merchant's own decision that a contested debt goes to a person rather than into an automated cadence. It is a customer-relations rule, not a payments one, and nothing published governs it.",
	RulePromiseHold:      "the length of a hold is whatever the customer and the merchant agreed, which is why it arrives on the request rather than as a constant here. That a promise holds off contact at all is this project's choice.",
	RuleNoContactChannel: "a correctness property of this system rather than a value. The frozen contract says an item with no channel must never have one guessed, and this is that sentence as a rule.",
	RuleNotYetDue:        "merchant cadence. The one-hour abandoned-cart window and the three-day invoice grace in sources.go are guesses at when a customer stops being mid-checkout and starts being overdue, and no source was found for either. An invoice that carries real payment terms should be handed to the engine with those terms rather than falling back on the constant.",
	RuleActionBudget:     "a blast-radius bound on one run of this program. Not a payments quantity, so there is nothing for it to cite.",
	RuleKillSwitch:       "an operational control. A halt is a property of this system, not of any payment network.",
	RuleIdempotency:      "a correctness property of this system's own action ledger.",
	RuleAllow:            "the id carried on a decision no rule refused. It has no value to cite or to choose.",
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
		RuleNotYetDue,
		RuleNoContactChannel,
		RuleNeverContact,
		RulePromiseHold,
		RuleQuietHours,
		RuleDisputed,
		RuleHumanApproval,
		RuleMaxTouches,
		RuleCooldown,
		RuleNotifyRate,
		RuleActionBudget,
		RuleAllow,
	}
}
