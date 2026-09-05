package riskrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/audit"
	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/detect"
	"github.com/lopster568/rzp-recovery-agent/internal/intervene"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/lopster568/rzp-recovery-agent/internal/promise"
	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
	"github.com/lopster568/rzp-recovery-agent/internal/seed"
)

// Errors Run returns before it does anything.
var (
	ErrNoRecorder = errors.New("riskrun: needs an audit recorder")
	ErrNoResults  = errors.New("riskrun: needs somewhere to write the results")
	ErrNoAPI      = errors.New("riskrun: a live run needs a Razorpay client")
	ErrNoGateway  = errors.New("riskrun: the engine arm needs a Razorpay gateway")
)

// Options configures one run.
type Options struct {
	// Manifest is the seedbook ground truth. It supplies the disputed flag,
	// the last status the seeder saw, and the simulated ages.
	Manifest seed.Manifest
	// ManifestPath is recorded in the summary so a run says which file it read.
	ManifestPath string
	// RunTag names this run. Empty means one derived from the clock.
	RunTag string
	// Seed drives the arm assignment and nothing else.
	Seed int64

	// DryRun stops the run before any side-effecting call and reads the
	// manifest instead of Razorpay. It makes no network call of any kind.
	DryRun bool
	// SimulateAge measures every item the manifest knows against the age the
	// manifest says it was meant to have, rather than against Razorpay's own
	// timestamp. See simulatedAtRiskSince.
	SimulateAge bool
	// KillSwitch is R8's file half, already read by the caller.
	KillSwitch bool

	// DetectConfig bounds the sweep and sets the detector's own grace period.
	DetectConfig detect.Config
	// PolicyConfig is the gate's settings. The zero value is the standard
	// policy.
	PolicyConfig policy.Config
	// Clock is the instant every decision is made against. Nil means the wall
	// clock.
	Clock clock.Clock

	// API is what the detectors read through. Required unless DryRun.
	API DetectAPI
	// Gateway is what the engine arm calls. Required unless DryRun.
	Gateway intervene.Gateway
	// Recorder is the audit trail. Required: a run that could not record could
	// act without a record.
	Recorder *audit.Recorder
	// Escalations is where an escalation goes. Nil means an in-memory sink,
	// which is only useful for a dry run.
	Escalations intervene.EscalationSink
	// Promises is the promise ledger R15 reads and log_promise writes. Nil
	// means an empty one.
	Promises *promise.Store

	// Results receives one JSON line per item.
	Results io.Writer
	// Log receives the progress lines. Nil means they are dropped.
	Log io.Writer
}

// Run detects, gates, and, in the engine arm, intervenes.
//
// It returns the summary even when it returns an error, because a run that
// stopped partway still saw real debt and, on the live path, may already have
// called Razorpay. The caller is expected to write the summary out regardless,
// for the reason seed.ExecutePlan gives for writing a half-finished manifest.
func Run(ctx context.Context, opts Options) (Summary, error) {
	if opts.Recorder == nil {
		return Summary{}, ErrNoRecorder
	}
	if opts.Results == nil {
		return Summary{}, ErrNoResults
	}
	if !opts.DryRun && opts.API == nil {
		return Summary{}, ErrNoAPI
	}
	if !opts.DryRun && opts.Gateway == nil {
		return Summary{}, ErrNoGateway
	}

	clk := opts.Clock
	if clk == nil {
		clk = clock.Real()
	}
	promises := opts.Promises
	if promises == nil {
		promises = promise.NewStore()
	}
	escalations := opts.Escalations
	if escalations == nil {
		escalations = intervene.NewMemorySink()
	}
	log := opts.Log
	if log == nil {
		log = io.Discard
	}

	runTag := opts.RunTag
	if runTag == "" {
		runTag = fmt.Sprintf("riskrun-%d", clk.Now().UTC().Unix())
	}
	mode := ModeLive
	api := opts.API
	if opts.DryRun {
		mode = ModeDryRun
		api = newManifestSource(opts.Manifest)
	}

	pol := policy.New(opts.PolicyConfig, clk)
	summary := newSummary()
	summary.RunTag = runTag
	summary.Mode = mode
	summary.Seed = opts.Seed
	summary.StartedAt = clk.Now().UTC()
	summary.ManifestPath = opts.ManifestPath
	summary.ManifestRunTag = opts.Manifest.RunTag
	summary.ManifestItems = len(opts.Manifest.Items)
	summary.KillSwitch = opts.KillSwitch
	summary.DetectGrace = detectGrace(opts.DetectConfig).String()
	summary.AgeSource = AgeSourceGateway
	if opts.SimulateAge {
		summary.AgeSource = AgeSourceManifest
	}
	summary.Policy = policySnapshot(pol)

	// Detection. The three detectors run in the order the dedupe wants them:
	// the overdue invoice first, because it is the only sighting that carries a
	// customer, a payable short URL, and the notification-status fields; the
	// failed payment next, because it at least carries the failure evidence;
	// the bare unpaid order last. Collapse keeps the first sighting of a debt,
	// so the order here is what decides which detector speaks for a debt two of
	// them can see.
	sightings, detectErr := sweepAll(ctx, api, opts.DetectConfig)
	for _, item := range sightings {
		summary.SightingsBySource[string(item.Source)]++
	}
	items := detect.Collapse(sightings)
	summary.CollapsedAway = len(sightings) - len(items)
	summary.ItemsTotal = len(items)

	index := newManifestIndex(opts.Manifest)
	if opts.SimulateAge {
		for i := range items {
			if at, ok := simulatedAtRiskSince(index, items[i]); ok {
				items[i].AtRiskSince = at
			}
		}
	}

	arms := AssignArms(items, opts.Seed)
	for _, item := range items {
		summary.ItemsBySource[string(item.Source)]++
		summary.ItemsByArm[arms[item.ID]]++
		summary.AmountDueBySource[string(item.Source)] += item.AmountDuePaise
		summary.AmountDueTotal += item.AmountDuePaise
	}

	var engine *intervene.Engine
	if !opts.DryRun {
		built, err := intervene.New(intervene.Options{
			Gateway:     opts.Gateway,
			Recorder:    opts.Recorder,
			Promises:    promiseAdapter{store: promises},
			Escalations: escalations,
			Clock:       clk,
		})
		if err != nil {
			return summary, errors.Join(detectErr, err)
		}
		engine = built
	}

	fmt.Fprintf(log, "run       %s\n", runTag)
	fmt.Fprintf(log, "mode      %s%s\n", mode, modeCaveat(mode))
	fmt.Fprintf(log, "manifest  %s (%d seeded item(s))\n", opts.ManifestPath, len(opts.Manifest.Items))
	fmt.Fprintf(log, "items     %d after the dedupe merged %d sighting(s)\n", len(items), summary.CollapsedAway)
	fmt.Fprintf(log, "age       %s\n\n", summary.AgeSource)

	encoder := json.NewEncoder(opts.Results)
	led := newLedger()
	var runErr error
	for _, item := range items {
		row, err := processItem(ctx, itemRun{
			opts:     opts,
			clock:    clk,
			policy:   pol,
			ledger:   led,
			index:    index,
			promises: promises,
			engine:   engine,
			arm:      arms[item.ID],
			mode:     mode,
			runTag:   runTag,
			summary:  &summary,
		}, item)
		if err != nil {
			// The row is still written. An item whose action errored is an item
			// the run acted on, and dropping it would report a smaller run than
			// the one that happened.
			runErr = errors.Join(runErr, err)
		}
		if err := encoder.Encode(row); err != nil {
			return summary, errors.Join(detectErr, runErr, fmt.Errorf("riskrun: write the result for %s: %w", item.ID, err))
		}
		if row.Error != "" {
			summary.Errors++
		}
		fmt.Fprintf(log, "  %-16s %-16s %-11s %-22s %-9s %s\n",
			row.Source, row.RiskItemID, row.Arm, row.ProposedAction, row.Verdict, row.RuleID)
	}

	summary.FinishedAt = clk.Now().UTC()
	return summary, errors.Join(detectErr, runErr)
}

// itemRun is everything one item's pass through the pipeline needs. It is a
// struct rather than a dozen parameters because the loop above builds it once.
type itemRun struct {
	opts     Options
	clock    clock.Clock
	policy   *policy.Policy
	ledger   *ledger
	index    *manifestIndex
	promises *promise.Store
	engine   *intervene.Engine
	arm      string
	mode     string
	runTag   string
	summary  *Summary
}

// processItem is one item: classify, propose, evaluate, and then either execute
// or write down that nothing was executed and why.
func processItem(ctx context.Context, r itemRun, item riskitem.RiskItem) (ItemResult, error) {
	now := r.clock.Now()
	class := classFor(item)

	row := ItemResult{
		RunTag:          r.runTag,
		Arm:             r.arm,
		Mode:            r.mode,
		RiskItemID:      item.ID,
		Source:          string(item.Source),
		SourceID:        item.SourceID,
		RootOrderID:     item.RootOrderID,
		DedupeKey:       item.DedupeKey(),
		AmountPaise:     item.AmountPaise,
		AmountPaidPaise: item.AmountPaidPaise,
		// Carried, never derived. Razorpay reports what is outstanding and a
		// partial payment makes the subtraction disagree with the gateway, so
		// the one place this number can come from is the item.
		AmountDuePaise: item.AmountDuePaise,
		Currency:       item.Currency,
		AtRiskSince:    item.AtRiskSince,
		AgeSource:      ageSourceFor(r, item),
		// Empty when there was no signal to classify. An item nothing went
		// wrong with is not "unclassified": that word means a failure
		// happened and nothing recognised it, which is what R7 escalates, and
		// writing it on an abandoned cart would put the two facts in one
		// column.
		Class:         classLabel(item, class),
		SignalPresent: policy.HasSignal(item.Signal),
		HandleKind:    item.PayHandle.Kind,
		HasEmail:      item.Customer.Email != "",
		HasContact:    item.Customer.Contact != "",
	}

	if row.SignalPresent {
		if _, err := r.opts.Recorder.Record(ctx, classifiedEvent(item, class, r.arm)); err != nil {
			row.Error = err.Error()
			return row, err
		}
	}

	touchNo := r.ledger.touchNo(item.ID)
	facts := factsFor(r.index, r.promises, item, touchNo, now)
	row.Disputed = facts.Disputed
	row.SourceStatus = facts.SourceStatus
	row.TouchNo = touchNo

	proposed := ProposeAction(item)
	row.ProposedAction = proposed
	r.summary.ActionsProposed[proposed]++

	decision := r.policy.Evaluate(
		r.ledger.state(item.ID, policy.IdempotencyKey(item.ID, proposed, touchNo), r.opts.KillSwitch),
		policy.RequestFromClassified(item, proposed, class, facts),
	)
	row.Verdict = string(decision.Verdict)
	row.RuleID = decision.RuleID
	row.Reason = decision.Reason
	row.Remaining = decision.Remaining
	row.IdempotencyKey = policy.ShortKey(decision.IdempotencyKey)
	r.summary.countVerdict(decision.RuleID, string(decision.Verdict))

	if _, err := r.opts.Recorder.Record(ctx, evaluatedEvent(item, class, proposed, decision, r, row)); err != nil {
		row.Error = err.Error()
		return row, err
	}

	action := ""
	key := decision.IdempotencyKey
	reason := decision.Reason
	switch decision.Verdict {
	case policy.VerdictAllow:
		action = proposed
	case policy.VerdictEscalate:
		// The escalation is itself an action, so it goes through the gate. A
		// kill switch stops it, a replay of it is a replay, and the row saying
		// so is the difference between an item a person was handed and an item
		// the run merely decided to hand to a person.
		escalation := r.policy.Evaluate(
			r.ledger.state(item.ID, policy.IdempotencyKey(item.ID, riskitem.ActionEscalate, touchNo), r.opts.KillSwitch),
			policy.RequestFromClassified(item, riskitem.ActionEscalate, class, facts),
		)
		row.EscalationVerdict = string(escalation.Verdict)
		row.EscalationRule = escalation.RuleID
		r.summary.countEscalationVerdict(escalation.RuleID, string(escalation.Verdict))
		if _, err := r.opts.Recorder.Record(ctx, evaluatedEvent(item, class, riskitem.ActionEscalate, escalation, r, row)); err != nil {
			row.Error = err.Error()
			return row, err
		}
		if escalation.Allowed() {
			action = riskitem.ActionEscalate
			key = escalation.IdempotencyKey
			// reason stays the first decision's. What goes in the escalation
			// record is why the item is being handed to a person, which is the
			// rule that refused the action, not the escalation's own
			// R0-DEFAULT-ALLOW.
		}
	}

	// Nothing runs, for one of three reasons, and the row says which.
	//
	// Nothing is committed to the ledger either, so a control-arm item spends
	// no part of the run-wide action budget and moves neither the notify window
	// nor the item's touch count. That is what a control arm is: it decides and
	// does not act, so it has no blast radius to bound. It does mean the two
	// arms see slightly different run-wide state, which is the price of
	// measuring them in one pass over one account rather than in two runs whose
	// books would have moved in between.
	if action == "" || r.arm == ArmControl || r.opts.DryRun {
		if _, err := r.opts.Recorder.Record(ctx, skippedEvent(item, action, decision, r, row)); err != nil {
			row.Error = err.Error()
			return row, err
		}
		return row, nil
	}

	// From here there is a side effect. The key the policy computed travels on
	// the context because riskitem.Intervention.Apply is frozen at three
	// parameters, and so does the reason, which is the one thing an escalation
	// record and a promise note cannot derive from the item.
	actCtx := intervene.WithReason(intervene.WithIdempotencyKey(ctx, key), reason)
	outcome, applyErr := r.engine.Apply(actCtx, item, action)
	r.ledger.commit(item.ID, key, action, outcome.Accepted, r.clock.Now())

	row.ExecutedAction = action
	row.Executed = true
	row.Accepted = outcome.Accepted
	row.Observable = outcome.Observable
	row.HandleID = outcome.Handle.ID
	row.HandleURL = outcome.Handle.URL
	if outcome.Err != "" {
		row.Error = outcome.Err
	}

	r.summary.ActionsExecuted[action]++
	if outcome.Accepted {
		r.summary.ActionsAccepted[action]++
		if action == riskitem.ActionEscalate {
			r.summary.Escalations++
		}
	} else if outcome.Err != "" {
		r.summary.Refusals[outcome.Err]++
	}
	if outcome.Observable != "" {
		r.summary.Observables[outcome.Observable]++
	}

	if applyErr != nil {
		return row, fmt.Errorf("riskrun: apply %s to %s: %w", action, item.ID, applyErr)
	}
	return row, nil
}

// ProposeAction is the action a run puts to the gate for one item.
//
// It reads the item's shape and nothing else. An item with nothing to pay
// against needs a link before anything can be sent about it; an item that has a
// handle needs a message about the handle it has, over the channel it carries.
//
// It deliberately does not check whether there is a channel at all. An item
// with neither an email nor a phone number still arrives here as a proposed
// notification, so that R10 is the thing that refuses it and the refusal is in
// the trail under the rule that exists for it. Filtering here instead would
// make the run look clean and would leave the rule that says "nothing here may
// guess an address" with nothing to fire on.
func ProposeAction(item riskitem.RiskItem) string {
	if item.PayHandle.Kind == riskitem.HandleKindNone {
		return riskitem.ActionCreatePaymentLink
	}
	if item.Customer.Email != "" {
		return riskitem.ActionNotifyEmail
	}
	return riskitem.ActionNotifySMS
}

// sweepAll runs the three detectors and returns every sighting.
//
// A detector that read part of a page and then failed returns what it has along
// with the error, per the Detector contract, and this keeps both: the sightings
// go into the queue and the errors are joined and handed back, so the caller
// decides whether a partial sweep was worth acting on rather than having that
// decided here.
func sweepAll(ctx context.Context, api DetectAPI, cfg detect.Config) ([]riskitem.RiskItem, error) {
	detectors := []riskitem.Detector{
		detect.NewOverdueInvoiceDetector(api, cfg),
		detect.NewFailedPaymentDetector(api, cfg),
		detect.NewUnpaidOrderDetector(api, cfg),
	}

	var items []riskitem.RiskItem
	var errs error
	for _, detector := range detectors {
		found, err := detector.Detect(ctx)
		items = append(items, found...)
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("riskrun: the %s detector: %w", detector.Name(), err))
		}
	}
	return items, errs
}

// classFor reads the item's failure evidence, when it has any.
//
// An item with no signal is not classified at all rather than classified as
// unclassified-because-nothing-recognised-it. The two are different facts and
// the policy gate reads them separately: SignalPresent is what separates an
// abandoned cart, where nothing went wrong, from a failed payment whose reason
// nothing could read, which is what R7 escalates.
func classFor(item riskitem.RiskItem) classify.Class {
	if !policy.HasSignal(item.Signal) {
		return classify.Unclassified
	}
	return classify.Classify(classify.Failure{
		Code:   item.Signal.FailureCode,
		Reason: item.Signal.FailureReason,
		Source: classify.Source(item.Signal.FailureSource),
		Step:   item.Signal.FailureStep,
		Method: classify.Method(item.Signal.Method),
	})
}

// classLabel is the class as it goes in a row, empty for an item that carried
// no failure evidence.
//
// classify.Unclassified is a real answer and it is not this one. It means a
// failure happened and no documented vocabulary recognised its reason, which is
// an arm of R7. An abandoned cart has no failure at all, and stamping the same
// word on it would make the two indistinguishable in every file a scorer reads.
func classLabel(item riskitem.RiskItem, class classify.Class) string {
	if !policy.HasSignal(item.Signal) {
		return ""
	}
	return class.String()
}

// ageSourceFor reports which clock this item's at-risk instant came off.
//
// It is per item rather than per run: a run measuring simulated ages still
// leaves an item the manifest does not know on Razorpay's own timestamp, and a
// row that claimed otherwise would overstate what the run knows.
func ageSourceFor(r itemRun, item riskitem.RiskItem) string {
	if !r.opts.SimulateAge {
		return AgeSourceGateway
	}
	if _, ok := simulatedAtRiskSince(r.index, item); ok {
		return AgeSourceManifest
	}
	return AgeSourceGateway
}

// detectGrace is the detector's own grace with the package default filled in,
// so the summary records the number that was in force rather than a zero.
func detectGrace(cfg detect.Config) time.Duration {
	if cfg.Grace > 0 {
		return cfg.Grace
	}
	return detect.DefaultGrace
}

// policySnapshot renders the cadence a run ran under.
func policySnapshot(pol *policy.Policy) PolicySnapshot {
	cfg := pol.Config()
	snapshot := PolicySnapshot{
		AmountCeilingPaise: cfg.AmountCeilingPaise,
		WriteOffFloorPaise: cfg.WriteOffFloorPaise,
		ActionBudget:       cfg.ActionBudget,
		NotifyWindow:       cfg.NotifyWindow.String(),
		ContactWindow:      cfg.ContactWindow.String(),
		SourceParams:       make(map[string]SourceCadence),
	}
	for _, source := range policy.Sources() {
		params := pol.ParamsFor(source)
		snapshot.SourceParams[source] = SourceCadence{
			Grace:          params.Grace.String(),
			MaxTouches:     params.MaxTouches,
			Cooldown:       params.Cooldown.String(),
			RequiresSignal: params.RequiresSignal,
		}
	}
	return snapshot
}

func modeCaveat(mode string) string {
	if mode == ModeDryRun {
		return "  (the manifest replayed through the real detectors and the real gate, no API call of any kind)"
	}
	return "  (Razorpay TEST MODE, see docs/RAZORPAY-TEST-MODE-NOTES.md)"
}

// promiseAdapter is the seam internal/intervene's PromiseRecord doc comment
// specifies, written at the one site that imports both packages.
//
// The conversion is what pins the two field sets together: a field reordered or
// renamed on either side stops this file compiling, which is the assertion the
// intervention engine cannot make about a ledger it deliberately does not
// import. promise.Store.Log takes no context, so this drops it.
type promiseAdapter struct{ store *promise.Store }

var _ intervene.PromiseLedger = promiseAdapter{}

func (a promiseAdapter) Log(_ context.Context, rec intervene.PromiseRecord) error {
	return a.store.Log(promise.Record(rec))
}

// classifiedEvent is the row for an item that carried failure evidence.
func classifiedEvent(item riskitem.RiskItem, class classify.Class, arm string) audit.Event {
	return audit.Event{
		OrderID: item.DedupeKey(),
		Kind:    audit.KindClassified,
		Class:   class.String(),
		Detail: map[string]string{
			"risk_item_id":   item.ID,
			"source":         string(item.Source),
			"arm":            arm,
			"failure_reason": item.Signal.FailureReason,
			"failure_code":   item.Signal.FailureCode,
		},
	}
}

// evaluatedEvent is the row for one decision. It goes down before anything acts
// on it, so a refusal leaves a row the same way an allow does.
func evaluatedEvent(item riskitem.RiskItem, class classify.Class, action string, decision policy.Decision, r itemRun, row ItemResult) audit.Event {
	return audit.Event{
		OrderID:        item.DedupeKey(),
		Kind:           audit.KindPolicyEvaluated,
		Class:          classLabel(item, class),
		ProposedAction: action,
		PolicyVerdict:  string(decision.Verdict),
		PolicyRule:     decision.RuleID,
		Detail: map[string]string{
			"risk_item_id":      item.ID,
			"source":            string(item.Source),
			"arm":               r.arm,
			"mode":              r.mode,
			"age_source":        row.AgeSource,
			"amount_due_paise":  strconv.FormatInt(item.AmountDuePaise, 10),
			"disputed":          strconv.FormatBool(row.Disputed),
			"touch_no":          strconv.Itoa(row.TouchNo),
			"idempotency_key":   policy.ShortKey(decision.IdempotencyKey),
			"idempotent_replay": strconv.FormatBool(decision.IdempotentReplay),
			"policy_reason":     decision.Reason,
			"remaining":         strconv.Itoa(decision.Remaining),
		},
	}
}

// skippedEvent is the row for an item nothing ran on, and it says which of the
// three reasons it was.
//
// A refusal, a control-arm item, and a dry-run item all end here and they are
// not the same thing. The control arm decided and chose not to act; the dry run
// stopped before it could; a refusal is the gate saying no. A single skipped
// row with no reason on it would flatten the three, and the control arm's whole
// value is that its skipped rows carry a verdict.
func skippedEvent(item riskitem.RiskItem, action string, decision policy.Decision, r itemRun, row ItemResult) audit.Event {
	why := "the policy refused every action for this item"
	switch {
	case r.opts.DryRun:
		why = "dry run: the decision was made and nothing was executed"
	case r.arm == ArmControl:
		why = "control arm: the decision was made and nothing was executed"
	}

	detail := map[string]string{
		"risk_item_id":  item.ID,
		"source":        string(item.Source),
		"arm":           r.arm,
		"mode":          r.mode,
		"not_executed":  why,
		"policy_reason": decision.Reason,
	}
	if action != "" {
		detail["would_have_run"] = action
	}
	if row.EscalationRule != "" {
		detail["escalation_rule"] = row.EscalationRule
		detail["escalation_verdict"] = row.EscalationVerdict
	}

	return audit.Event{
		OrderID:        item.DedupeKey(),
		Kind:           audit.KindActionSkipped,
		ProposedAction: row.ProposedAction,
		PolicyVerdict:  row.Verdict,
		PolicyRule:     row.RuleID,
		Detail:         detail,
	}
}
