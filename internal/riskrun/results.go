package riskrun

import "time"

// The two clocks an item's at-risk instant can come from. Every result row
// carries one, so a reading of a run can always tell a real age from a stated
// one.
const (
	// AgeSourceGateway is Razorpay's own timestamp: a payment's created_at, an
	// order's created_at, or an invoice's issued_at.
	AgeSourceGateway = "gateway"
	// AgeSourceManifest is the age the seedbook manifest says the item was
	// meant to have. See simulatedAtRiskSince for why an API that cannot
	// backdate an invoice leaves this as the only way to demo a book of aged
	// receivables.
	AgeSourceManifest = "manifest_simulated"
)

// The three modes a run can be in.
const (
	// ModeLive reads Razorpay and, in the engine arm, calls it.
	ModeLive = "live"
	// ModeDryRun reads the manifest instead of Razorpay and stops before the
	// intervention engine. It makes no network call of any kind.
	ModeDryRun = "dry-run"
	// ModeSimulated is the whole pipeline, the intervention engine included,
	// driven against a fixture book and a gateway that is memory rather than
	// Razorpay. It is not a dry run: actions execute, outcomes come back, and
	// the ledger carries intervention rows. What is on the far side of the
	// gateway interface is the only difference from ModeLive, and that
	// difference is the entire reason the label exists. See Options.Simulated.
	ModeSimulated = "simulated"
)

// ItemResult is one line of the results file: one risk item, what the gate
// decided about it, and what happened next.
//
// It is a flat row on purpose. A scoring pass reads it without knowing anything
// about this package, and every field on it is either copied from the item, the
// decision, or the outcome, or is a count this run kept.
type ItemResult struct {
	RunTag string `json:"run_tag"`
	Arm    string `json:"arm"`
	Mode   string `json:"mode"`

	RiskItemID  string `json:"risk_item_id"`
	Source      string `json:"source"`
	SourceID    string `json:"source_id"`
	RootOrderID string `json:"root_order_id,omitempty"`
	DedupeKey   string `json:"dedupe_key"`

	AmountPaise     int64  `json:"amount_paise"`
	AmountPaidPaise int64  `json:"amount_paid_paise"`
	AmountDuePaise  int64  `json:"amount_due_paise"`
	Currency        string `json:"currency,omitempty"`

	// AtRiskSince and AgeSource travel together. The instant means nothing
	// without the clock it was read off.
	AtRiskSince int64  `json:"at_risk_since"`
	AgeSource   string `json:"age_source"`

	Class         string `json:"class,omitempty"`
	SignalPresent bool   `json:"signal_present"`
	HandleKind    string `json:"handle_kind,omitempty"`
	HasEmail      bool   `json:"has_email"`
	HasContact    bool   `json:"has_contact"`
	Disputed      bool   `json:"disputed"`
	SourceStatus  string `json:"source_status,omitempty"`
	TouchNo       int    `json:"touch_no"`

	ProposedAction string `json:"proposed_action"`
	Verdict        string `json:"verdict"`
	RuleID         string `json:"rule_id"`
	Reason         string `json:"reason"`
	Remaining      int    `json:"remaining"`
	IdempotencyKey string `json:"idempotency_key"`

	// EscalationVerdict and EscalationRule are filled when the first decision
	// escalated and the escalation itself was put through the gate, which it
	// always is: handing an item to a person is an action like any other and
	// the kill switch stops it like any other.
	EscalationVerdict string `json:"escalation_verdict,omitempty"`
	EscalationRule    string `json:"escalation_rule,omitempty"`

	// ExecutedAction is what actually ran, empty when nothing did. Executed
	// says whether the intervention engine was called at all, which is false
	// for every item in the control arm and for every item in a dry run.
	ExecutedAction string `json:"executed_action,omitempty"`
	Executed       bool   `json:"executed"`
	// Accepted is riskitem.Outcome.Accepted: the call succeeded. It is not a
	// claim that a person read anything.
	Accepted   bool   `json:"accepted"`
	Observable string `json:"observable,omitempty"`
	HandleID   string `json:"handle_id,omitempty"`
	HandleURL  string `json:"handle_url,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Summary is the run-summary file: what was seen, what was decided, and what
// was done.
//
// Every count here is derived from the result rows, so the summary and the
// results file cannot disagree. Nothing in it is a rate: a rate needs a
// denominator that means something, and a run over a seeded book has one only
// after risk-poll has read the account twice.
type Summary struct {
	RunTag     string    `json:"run_tag"`
	Mode       string    `json:"mode"`
	Seed       int64     `json:"seed"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`

	ManifestPath   string `json:"manifest_path"`
	ManifestRunTag string `json:"manifest_run_tag,omitempty"`
	ManifestItems  int    `json:"manifest_items"`

	// AgeSource is the clock this run measured every item it could against.
	// It is a run-wide setting, and the per-item rows say which clock each item
	// actually got, because an item the manifest does not know keeps the
	// gateway's.
	AgeSource   string `json:"age_source"`
	DetectGrace string `json:"detect_grace"`
	// SweepSince is the created_at floor every one of the three sweeps ran
	// under, in Unix seconds, and SweepSinceSource says where that number came
	// from. Zero is a meaningful value and means an unscoped sweep over the
	// whole account, which is why the source is recorded beside it rather than
	// left to be inferred.
	//
	// Without this pair a reader cannot reproduce what the run looked at.
	// Everything else in this file describes what the run did with the items it
	// found; this is the only field that says which items it could find at all,
	// and the floor moves with the manifest.
	SweepSince       int64          `json:"sweep_since"`
	SweepSinceSource string         `json:"sweep_since_source"`
	KillSwitch       bool           `json:"kill_switch_engaged"`
	Policy           PolicySnapshot `json:"policy"`

	// SightingsBySource is what the detectors returned, before the dedupe.
	SightingsBySource map[string]int `json:"sightings_by_source"`
	// ItemsBySource is what was left after Collapse merged the sightings that
	// are one debt. The difference between the two is CollapsedAway.
	ItemsBySource map[string]int `json:"items_by_source"`
	CollapsedAway int            `json:"collapsed_away"`
	ItemsTotal    int            `json:"items_total"`
	ItemsByArm    map[string]int `json:"items_by_arm"`

	// VerdictsByRule is rule id to verdict to count, which is the shape a
	// report reads: a rule that both allowed and refused across a run shows as
	// two entries under one id rather than as one number that hides which.
	VerdictsByRule map[string]map[string]int `json:"verdicts_by_rule"`
	VerdictTotals  map[string]int            `json:"verdict_totals"`
	// EscalationVerdictsByRule is the same shape for the follow-up decision an
	// escalating verdict triggers, kept apart from the counts above so that
	// they stay one entry per item. Folding the two together made the allow
	// count larger than the number of items, because every escalation raises a
	// second, allowed, decision to escalate.
	EscalationVerdictsByRule map[string]map[string]int `json:"escalation_verdicts_by_rule"`
	EscalationVerdictTotals  map[string]int            `json:"escalation_verdict_totals"`

	ActionsProposed map[string]int `json:"actions_proposed"`
	ActionsExecuted map[string]int `json:"actions_executed"`
	// ActionsAccepted counts the executions whose call succeeded, by action.
	// It counts API calls that were accepted, not customers who were reached.
	ActionsAccepted map[string]int `json:"actions_accepted"`
	// Escalations is how many items were handed to a person and the sink took
	// the record. An escalation the gate decided on but the control arm did not
	// execute is in VerdictTotals and not here.
	Escalations int `json:"escalations"`
	// Observables counts riskitem.Outcome.Observable values, verbatim. Each is
	// a field and a value, such as email_status:sent, and each is the strongest
	// thing that was actually seen.
	Observables map[string]int `json:"observables"`
	// Refusals counts the reasons the intervention engine declined to act,
	// which are its own refusal strings and are not policy verdicts.
	Refusals map[string]int `json:"refusals,omitempty"`
	// Errors is how many rows carry an error, whatever its origin.
	Errors int `json:"errors"`

	// AmountDueBySource is the outstanding paise the run saw, per source. It is
	// the sum of what Razorpay reported as due, never a subtraction of paid
	// from gross.
	AmountDueBySource map[string]int64 `json:"amount_due_by_source"`
	AmountDueTotal    int64            `json:"amount_due_total"`
}

// SinceSourceUnrecorded is what Summary.SweepSinceSource holds when the caller
// did not say where its floor came from. It is a visible gap rather than a
// blank field a reader would take for an unscoped sweep.
const SinceSourceUnrecorded = "unrecorded"

// PolicySnapshot is the cadence the run actually ran under, recorded in its own
// summary.
//
// It is written down because the numbers move. Every one of them is either a
// configured choice or a cited value, policy.ConfiguredChoices and
// policy.CitedValues say which per rule, and a run scored six months later
// against today's constants would be scored against a policy it never saw. The
// durations are strings rather than the nanosecond integers a time.Duration
// marshals to, so the file is readable by the person the audit trail is for.
type PolicySnapshot struct {
	AmountCeilingPaise int64                    `json:"amount_ceiling_paise"`
	WriteOffFloorPaise int64                    `json:"write_off_floor_paise"`
	ActionBudget       int                      `json:"action_budget"`
	NotifyWindow       string                   `json:"notify_window"`
	ContactWindow      string                   `json:"contact_window"`
	SourceParams       map[string]SourceCadence `json:"source_params"`
}

// SourceCadence is one row of the per-source table, rendered for the summary.
type SourceCadence struct {
	Grace          string `json:"grace"`
	MaxTouches     int    `json:"max_touches"`
	Cooldown       string `json:"cooldown"`
	RequiresSignal bool   `json:"requires_signal"`
}

func newSummary() Summary {
	return Summary{
		SightingsBySource: make(map[string]int),
		ItemsBySource:     make(map[string]int),
		ItemsByArm:        make(map[string]int),
		VerdictsByRule:    make(map[string]map[string]int),
		VerdictTotals:     make(map[string]int),

		EscalationVerdictsByRule: make(map[string]map[string]int),
		EscalationVerdictTotals:  make(map[string]int),
		ActionsProposed:          make(map[string]int),
		ActionsExecuted:          make(map[string]int),
		ActionsAccepted:          make(map[string]int),
		Observables:              make(map[string]int),
		Refusals:                 make(map[string]int),
		AmountDueBySource:        make(map[string]int64),
	}
}

// countVerdict records the decision on the action a run proposed for an item.
// There is exactly one per item.
func (s *Summary) countVerdict(ruleID, verdict string) {
	count(s.VerdictsByRule, s.VerdictTotals, ruleID, verdict)
}

// countEscalationVerdict records the decision on the escalation an escalating
// verdict triggers. There is one per escalated item and none for the rest.
func (s *Summary) countEscalationVerdict(ruleID, verdict string) {
	count(s.EscalationVerdictsByRule, s.EscalationVerdictTotals, ruleID, verdict)
}

func count(byRule map[string]map[string]int, totals map[string]int, ruleID, verdict string) {
	byVerdict, ok := byRule[ruleID]
	if !ok {
		byVerdict = make(map[string]int)
		byRule[ruleID] = byVerdict
	}
	byVerdict[verdict]++
	totals[verdict]++
}
