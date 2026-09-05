package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/audit"
	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The two media a notification can go out over. They are local constants
// because a medium never leaves this package: notify_item folds the medium
// into the action it asks for, and notify_email and notify_sms are two
// different members of the frozen lawful set rather than one action with an
// argument.
const (
	MediumEmail = "email"
	MediumSMS   = "sms"
)

// registerTools adds the eight. The order here is ToolNames' order, and the
// two lists are checked against each other by
// TestServerServesExactlyTheEightNamedTools.
func (s *Server) registerTools() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: ToolListRiskItems,
		Description: "List the revenue at risk waiting for a decision: failed payments, unpaid " +
			"orders, and overdue invoices, all as one kind of item. Each one carries what is still " +
			"due, how long it has been at risk, whether there is any way to contact the customer, " +
			"and what they can already pay against. These are the only items anything here can act on.",
	}, s.handleListRiskItems)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: ToolGetRiskItem,
		Description: "Read one item in full, with the evidence its detector saw: the error fields " +
			"of a failed payment, the attempt count on an order, the notification statuses on an " +
			"invoice. A field the source does not have is absent rather than zero. An email status " +
			"of sent means Razorpay accepted the send, not that a person read anything.",
	}, s.handleGetRiskItem)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: ToolRecordDecision,
		Description: "Record what you have decided to do about one item and why, before you do it. " +
			"Actions on an item are refused until a decision for that item is on the record. " +
			"The reasoning goes into the audit trail a compliance reviewer reads. The action you " +
			"name may be one no tool executes, such as cancel_write_off or do_nothing: those are " +
			"decisions, and recording one is the whole of doing them.",
	}, s.handleRecordDecision)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: ToolNotifyItem,
		Description: "Ask Razorpay to tell the customer about the way to pay this item already " +
			"carries, over email or sms. What this observes is the notification API call's " +
			"response. It does not observe a person receiving or reading anything. An item with no " +
			"contact channel is refused rather than guessed at, and an item with nothing to pay " +
			"against needs " + ToolCreatePaymentLink + " first.",
	}, s.handleNotifyItem)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: ToolCreatePaymentLink,
		Description: "Mint a payment link for an item that has nothing to pay against yet, so the " +
			"customer can pay by choosing to. Nothing here can take money from anyone: this raises " +
			"a link and stops. Sending it is a separate call. An item that already carries a link, " +
			"or an issued invoice with its own url, does not need one.",
	}, s.handleCreatePaymentLink)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: ToolResendLink,
		Description: "Send the link or invoice this item already carries, again. It creates " +
			"nothing. It reaches the same notification API " + ToolNotifyItem + " does, and what " +
			"separates the two is that this one is a repeat, which is the thing the rate limit " +
			"counts. It observes the API response and nothing about a person.",
	}, s.handleResendLink)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: ToolLogPromise,
		Description: "Record that the customer said they will pay, and when. It writes an audit " +
			"row and touches no Razorpay resource: no money moves, nothing is sent, and nobody is " +
			"held to it. Use days_hold to say how long this item should be left alone afterwards.",
	}, s.handleLogPromise)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: ToolEscalateItem,
		Description: "Hand the item to a person and take no automated action on it. This is a " +
			"successful outcome, not a failure: some debt is debt nothing automated should touch.",
	}, s.handleEscalateItem)
}

// ---------------------------------------------------------------------------
// Read tools
// ---------------------------------------------------------------------------

func (s *Server) handleListRiskItems(
	_ context.Context, _ *mcp.CallToolRequest, _ ListRiskItemsInput,
) (*mcp.CallToolResult, any, error) {
	now := s.clock.Now()
	out := ListRiskItemsOutput{
		Note: "these are the only risk items this server will act on",
	}
	for _, item := range s.Items() {
		out.Items = append(out.Items, RiskItemSummary{
			ItemID:         item.ID,
			Source:         string(item.Source),
			AmountDuePaise: item.AmountDuePaise,
			AmountPaise:    item.AmountPaise,
			Currency:       item.Currency,
			AgingDays:      agingDays(now, item.AtRiskSince),
			HasContact:     item.Customer.HasContactChannel(),
			HandleKind:     item.PayHandle.Kind,
		})
	}
	return jsonResult(out), nil, nil
}

func (s *Server) handleGetRiskItem(
	_ context.Context, _ *mcp.CallToolRequest, in GetRiskItemInput,
) (*mcp.CallToolResult, any, error) {
	item, ok := s.lookup(in.ItemID)
	if !ok {
		return errResult(fmt.Errorf("risk item %s is not in this batch", in.ItemID)), nil, nil
	}

	detail := RiskItemDetail{
		ItemID:          item.ID,
		Source:          string(item.Source),
		SourceID:        item.SourceID,
		RootOrderID:     item.RootOrderID,
		AmountPaise:     item.AmountPaise,
		AmountPaidPaise: item.AmountPaidPaise,
		AmountDuePaise:  item.AmountDuePaise,
		Currency:        item.Currency,
		AtRiskSince:     item.AtRiskSince,
		AgingDays:       agingDays(s.clock.Now(), item.AtRiskSince),
		HasContact:      item.Customer.HasContactChannel(),
		Channels:        channelsOf(item),
		HandleKind:      item.PayHandle.Kind,
		HandleID:        item.PayHandle.ID,
		HandleURL:       item.PayHandle.URL,
		Signal: RiskSignal{
			FailureCode:   item.Signal.FailureCode,
			FailureReason: item.Signal.FailureReason,
			FailureSource: item.Signal.FailureSource,
			FailureStep:   item.Signal.FailureStep,
			Method:        item.Signal.Method,
			Attempts:      item.Signal.Attempts,
			EmailStatus:   item.Signal.EmailStatus,
			SmsStatus:     item.Signal.SmsStatus,
		},
		Note: "what the detector saw when it swept. Nothing here is re-read per call.",
	}
	return jsonResult(detail), nil, nil
}

// ---------------------------------------------------------------------------
// The decision tool
// ---------------------------------------------------------------------------

func (s *Server) handleRecordDecision(
	ctx context.Context, _ *mcp.CallToolRequest, in RecordDecisionInput,
) (*mcp.CallToolResult, any, error) {
	itemID := strings.TrimSpace(in.ItemID)
	action := strings.TrimSpace(in.Action)
	reasoning := strings.TrimSpace(in.Reasoning)

	if itemID == "" {
		return errResult(fmt.Errorf("a decision has to name the risk item it is about")), nil, nil
	}
	item, ok := s.lookup(itemID)
	if !ok {
		return errResult(fmt.Errorf("risk item %s is not in this batch", itemID)), nil, nil
	}
	// The lawful set is the vocabulary, and riskitem.IsLawfulAction is the one
	// place that decides what is in it. A retry, under any spelling, lands
	// here.
	if !slices.Contains(DecisionActions(), action) {
		return errResult(fmt.Errorf("%q is not an action this system has. It is one of %s",
			in.Action, strings.Join(DecisionActions(), ", "))), nil, nil
	}
	if reasoning == "" {
		return errResult(fmt.Errorf(
			"a decision needs the reasoning behind it. A compliance reviewer reads this row " +
				"to reconstruct why the action was taken.")), nil, nil
	}

	decision := Decision{ItemID: itemID, Action: action, Reasoning: reasoning}

	s.mu.Lock()
	s.decisions[itemID] = decision
	s.decisionLog = append(s.decisionLog, decision)
	s.tally(itemID).DecisionsRecorded++
	s.mu.Unlock()

	if err := s.record(ctx, audit.Event{
		OrderID:        itemID,
		Kind:           KindDecisionRecorded,
		ProposedAction: action,
		Detail: map[string]string{
			DetailChosenAction: action,
			DetailReasoning:    reasoning,
			DetailSource:       string(item.Source),
			DetailRootOrderID:  item.RootOrderID,
		},
	}); err != nil {
		return nil, nil, err
	}

	return jsonResult(RecordDecisionOutput{
		Recorded: true,
		ItemID:   itemID,
		Action:   action,
		Note:     "on the record. The action tools for this item are open now.",
	}), nil, nil
}

// ---------------------------------------------------------------------------
// Action tools
//
// Every one of them is the same three lines: validate what the tool's own
// arguments have to mean, name a member of the frozen lawful set, and hand it
// to act. None of them knows how its action is carried out, because none of
// them can: the Intervener is the only thing in the process that does.
// ---------------------------------------------------------------------------

func (s *Server) handleNotifyItem(
	ctx context.Context, _ *mcp.CallToolRequest, in NotifyItemInput,
) (*mcp.CallToolResult, any, error) {
	medium := strings.TrimSpace(strings.ToLower(in.Medium))
	if medium == "" {
		medium = MediumEmail
	}
	act, ok := notifyActionFor(medium)
	if !ok {
		return errResult(fmt.Errorf("medium %q is neither %s nor %s", in.Medium, MediumEmail, MediumSMS)), nil, nil
	}
	return s.act(ctx, action{tool: ToolNotifyItem, itemID: in.ItemID, action: act})
}

func (s *Server) handleCreatePaymentLink(
	ctx context.Context, _ *mcp.CallToolRequest, in CreatePaymentLinkForItemInput,
) (*mcp.CallToolResult, any, error) {
	return s.act(ctx, action{
		tool:   ToolCreatePaymentLink,
		itemID: in.ItemID,
		action: riskitem.ActionCreatePaymentLink,
	})
}

func (s *Server) handleResendLink(
	ctx context.Context, _ *mcp.CallToolRequest, in ResendLinkForItemInput,
) (*mcp.CallToolResult, any, error) {
	return s.act(ctx, action{
		tool:   ToolResendLink,
		itemID: in.ItemID,
		action: riskitem.ActionResendLink,
	})
}

func (s *Server) handleLogPromise(
	ctx context.Context, _ *mcp.CallToolRequest, in LogPromiseInput,
) (*mcp.CallToolResult, any, error) {
	promisedAt, err := parsePromiseDate(in.PromisedAt)
	if err != nil {
		return errResult(err), nil, nil
	}
	if in.DaysHold < 0 {
		return errResult(fmt.Errorf("days_hold is %d. A hold cannot run backwards", in.DaysHold)), nil, nil
	}
	note := strings.TrimSpace(in.Note)
	if note == "" {
		return errResult(fmt.Errorf(
			"a promise needs the note that says what the customer actually said. " +
				"A row that records a promise and not its substance is not evidence of anything")), nil, nil
	}
	return s.act(ctx, action{
		tool:   ToolLogPromise,
		itemID: in.ItemID,
		action: riskitem.ActionLogPromise,
		extra: map[string]string{
			DetailPromisedAt:  promisedAt.Format(time.RFC3339),
			DetailDaysHold:    itoa(in.DaysHold),
			DetailPromiseNote: note,
		},
	})
}

func (s *Server) handleEscalateItem(
	ctx context.Context, _ *mcp.CallToolRequest, in EscalateItemInput,
) (*mcp.CallToolResult, any, error) {
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return errResult(fmt.Errorf("an escalation has to say what a person should look at")), nil, nil
	}
	return s.act(ctx, action{
		tool:   ToolEscalateItem,
		itemID: in.ItemID,
		action: riskitem.ActionEscalate,
		// R7 and R4 escalate rather than allow, and an escalation is exactly
		// the right move on an item those two rules fired on. So an escalate
		// verdict passes here, and only a deny stops it.
		acceptsEscalate:  true,
		escalationReason: reason,
	})
}

// ---------------------------------------------------------------------------
// The shared action path
// ---------------------------------------------------------------------------

// action is one proposed action: which tool asked, which item, and which
// member of the frozen lawful set it is.
type action struct {
	tool   string
	itemID string
	// action is a riskitem action constant. It is the string the Intervener
	// is handed and the string the audit row names.
	action           string
	acceptsEscalate  bool
	escalationReason string
	// extra is the tool's own audit detail, such as the terms of a promise.
	// It is merged into the action row and it never reaches the Intervener,
	// which takes an item and an action and nothing else.
	extra map[string]string
}

// applied is what one Apply did, in the vocabulary the audit row and the tally
// are built from.
type applied struct {
	outcome riskitem.Outcome
	err     error
	// sideEffect reports that a call to Razorpay was made. It is not the same
	// as Accepted: a request that reached the gateway and then failed is a
	// request that was made, and a refusal the applier decided on its own
	// reached nothing.
	sideEffect bool
}

// act is layer 2 of ADR-0003 and the one path every action tool takes.
//
// The policy evaluation is its first statement after the item is resolved and
// the lock is held, and the verdict is in the ledger before anything runs.
// There is no branch in which the Intervener is reached without a recorded
// verdict behind it, which is the claim
// TestEveryActionToolConsultsPolicyBeforeSideEffect checks by walking the
// registry rather than by reading this comment.
//
// It does not re-read Razorpay. The item is what its detector saw on the
// sweep, and the only component in this process that talks to the gateway is
// the Intervener, which reports what it observed in the Outcome. A tool
// surface that re-read state would need a Port, and a Port is a second way to
// reach the gateway from here.
func (s *Server) act(ctx context.Context, a action) (*mcp.CallToolResult, any, error) {
	item, ok := s.lookup(a.itemID)
	if !ok {
		return errResult(fmt.Errorf("risk item %s is not in this batch", a.itemID)), nil, nil
	}

	// Held from before the snapshot to after the commit. Everything between is
	// one decision, and a second caller reading the state halfway through it
	// would read a state that is about to be false. See Server.actMu.
	s.actMu.Lock()
	defer s.actMu.Unlock()

	touches := s.touches(item.ID)
	touchNo := touches + 1
	key := policy.IdempotencyKey(item.ID, a.action, touchNo)
	snapshot := s.opts.Store.Snapshot(item.ID, key, s.opts.KillSwitchEngaged)
	// internal/store still counts a payment attempt, which this engine does
	// not make, and commits a notification by moving a window rather than by
	// counting a contact. So the count R1 reads comes from this invocation's
	// own tally of accepted contact actions. Seam: when the store counts
	// touches, delete this line and let the snapshot speak for itself.
	snapshot.TouchesMade = max(snapshot.TouchesMade, touches)

	facts := s.factsFor(ctx, item)
	if facts.TouchNo == 0 {
		facts.TouchNo = touchNo
	}
	request := policy.RequestFromClassified(item, a.action, classOf(item), facts)
	decision := s.opts.Policy.Evaluate(snapshot, request)

	out := ActionOutput{
		ItemID:        item.ID,
		Action:        a.action,
		PolicyVerdict: string(decision.Verdict),
		PolicyRule:    decision.RuleID,
		PolicyReason:  decision.Reason,
		Remaining:     decision.Remaining,
	}

	// The evaluation goes on the record before anything acts on it.
	if err := s.record(ctx, audit.Event{
		OrderID:        item.ID,
		Kind:           audit.KindPolicyEvaluated,
		Class:          classOf(item).String(),
		ProposedAction: a.action,
		PolicyVerdict:  string(decision.Verdict),
		PolicyRule:     decision.RuleID,
		Detail: map[string]string{
			DetailTool:           a.tool,
			DetailGateLayer:      LayerHandler,
			DetailSource:         string(item.Source),
			DetailIdempotencyKey: policy.ShortKey(decision.IdempotencyKey),
			DetailTouchNo:        itoa(touchNo),
			"idempotent_replay":  btoa(decision.IdempotentReplay),
			"policy_reason":      decision.Reason,
		},
	}); err != nil {
		return nil, nil, err
	}

	passes := decision.Allowed() || (a.acceptsEscalate && decision.Verdict == policy.VerdictEscalate)
	if !passes {
		out.Allowed = false
		out.Note = "refused by the policy. Read policy_rule and policy_reason, and choose something else."
		if err := s.finishAction(ctx, a, item, decision, touchNo, out, applied{}); err != nil {
			return nil, nil, err
		}
		return jsonResult(out), nil, nil
	}

	// Allowed. From here on the Intervener can be reached, and the verdict
	// that let it through is already on the result and already in the ledger.
	out.Allowed = true
	s.spendAction()

	if a.escalationReason != "" {
		// The reason goes on the action row rather than into a second
		// decision_recorded row. record_decision already wrote one for this
		// item, and two rows of the same kind for one decision reads as two
		// decisions.
		s.mu.Lock()
		s.tally(item.ID).escalationReason = a.escalationReason
		s.mu.Unlock()
	}

	outcome, applyErr := s.opts.Intervene.Apply(ctx, item, a.action)
	got := applied{
		outcome:    outcome,
		err:        applyErr,
		sideEffect: reachesGateway(a.action) && (outcome.Accepted || applyErr != nil),
	}

	out.Accepted = outcome.Accepted
	out.Observable = outcome.Observable
	out.HandleKind = outcome.Handle.Kind
	out.HandleID = outcome.Handle.ID
	out.HandleURL = outcome.Handle.URL
	switch {
	case applyErr != nil:
		out.Error = applyErr.Error()
		out.Note = "the call did not come back clean: " + applyErr.Error()
	case !outcome.Accepted:
		out.Error = outcome.Err
		out.Note = "the policy allowed this and the intervention engine refused it. Read error."
	}

	// A handle the applier just minted is part of this invocation's view of
	// the item. Without this, an item that had nothing to pay against still
	// has nothing to pay against after the link exists, and resend_link_for_item
	// could never find it.
	if outcome.Accepted && outcome.Handle.Kind != "" {
		s.mu.Lock()
		updated := s.allowed[item.ID]
		updated.PayHandle = outcome.Handle
		s.allowed[item.ID] = updated
		s.mu.Unlock()
	}

	// The commit spends the notification window. It is keyed off the call
	// having been made rather than off it having been accepted, for the same
	// reason the side-effect flag is: a message Razorpay took and then failed
	// on is not a free one.
	if commitsWindow(a.action) && got.sideEffect {
		s.opts.Store.Commit(item.ID, decision.IdempotencyKey, a.action)
	}
	if policy.IsContactAction(a.action) && got.sideEffect {
		s.spendTouch(item.ID)
	}

	if err := s.finishAction(ctx, a, item, decision, touchNo, out, got); err != nil {
		return nil, nil, err
	}
	return jsonResult(out), nil, nil
}

// finishAction writes the action row and updates the item's tally.
//
// The row is filed by what happened, never by what the action called itself.
// That is the phase 2 review finding: keying off the action kind let an arm
// decide its own containment score by naming a no-op after reaching the
// gateway, and the LLM arm is exactly the actor that finding was about. So
// taken and skipped are read off the side-effect flag and the applier's own
// verdict, both of which this package computes rather than receives.
func (s *Server) finishAction(
	ctx context.Context,
	a action,
	item riskitem.RiskItem,
	decision policy.Decision,
	touchNo int,
	out ActionOutput,
	got applied,
) error {
	// Taken when something happened: a call was made, or the applier accepted
	// an action that moves no gateway resource, such as a promise or an
	// escalation. Skipped when nothing did, which is what a refusal by either
	// layer is.
	kind := audit.KindActionTaken
	if !got.sideEffect && !got.outcome.Accepted && got.err == nil {
		kind = audit.KindActionSkipped
	}

	escalated := a.action == riskitem.ActionEscalate && got.outcome.Accepted

	detail := map[string]string{
		DetailTool:            a.tool,
		DetailSource:          string(item.Source),
		DetailPolicyConsulted: "true",
		DetailSideEffect:      btoa(got.sideEffect),
		DetailEscalated:       btoa(escalated),
		DetailAccepted:        btoa(got.outcome.Accepted),
		DetailIdempotencyKey:  policy.ShortKey(decision.IdempotencyKey),
		DetailTouchNo:         itoa(touchNo),
		"remaining":           itoa(decision.Remaining),
	}
	if item.RootOrderID != "" {
		detail[DetailRootOrderID] = item.RootOrderID
	}
	// The decision that was on the record when this ran. M3 requires one to
	// exist and does not require it to name this action, because reaching a
	// decided action can take two calls: see Server.hasDecision. Carrying it
	// here is what lets a reviewer see that an agent decided one thing and did
	// another, on the row itself rather than by joining two.
	if decided, ok := s.recordedDecision(item.ID); ok {
		detail[DetailChosenAction] = decided.Action
	}
	for k, v := range a.extra {
		detail[k] = v
	}
	if got.outcome.Observable != "" {
		detail[DetailObservable] = got.outcome.Observable
	}
	if got.outcome.Handle.Kind != "" {
		detail[DetailHandleKind] = got.outcome.Handle.Kind
		detail[DetailHandleID] = got.outcome.Handle.ID
	}
	if got.err != nil {
		detail["action_error"] = got.err.Error()
	} else if got.outcome.Err != "" {
		detail["refusal_reason"] = got.outcome.Err
	}
	if !out.Allowed {
		detail["refused_action"] = a.action
	}
	if reason := s.escalationReason(item.ID); escalated && reason != "" {
		detail[DetailEscalationReason] = reason
	}

	if err := s.record(ctx, audit.Event{
		OrderID:        item.ID,
		Kind:           kind,
		Class:          classOf(item).String(),
		ProposedAction: a.action,
		PolicyVerdict:  string(decision.Verdict),
		PolicyRule:     decision.RuleID,
		Detail:         detail,
	}); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tally(item.ID)
	if got.sideEffect {
		t.SideEffect = true
	}
	if escalated {
		t.Escalated = true
	}
	if out.Allowed {
		t.haveAllowed = true
		t.lastAllowedVerdict, t.lastAllowedRule = string(decision.Verdict), decision.RuleID
		if got.outcome.Accepted {
			t.ActionKind = a.action
		}
	} else {
		t.haveRefused = true
		t.lastRefusedVerdict, t.lastRefusedRule = string(decision.Verdict), decision.RuleID
	}
	return nil
}

// ---------------------------------------------------------------------------
// What an action costs
// ---------------------------------------------------------------------------

// reachesGateway reports whether an action makes a call to Razorpay. It is
// what the side-effect flag on an audit row is computed from, and
// harness/scorer.py counts both containment numbers off that flag.
//
// It is this package's own list rather than policy.IsContactAction, which
// happens to hold the same four actions today for a different reason: that one
// is about whether a customer hears from the merchant, and this one is about
// whether a request left the process. A future action that wakes nobody and
// still calls the gateway belongs here and not there.
func reachesGateway(action string) bool {
	switch action {
	case riskitem.ActionNotifyEmail, riskitem.ActionNotifySMS,
		riskitem.ActionResendLink, riskitem.ActionCreatePaymentLink:
		return true
	default:
		return false
	}
}

// commitsWindow reports whether an action spends the send window in the store,
// which is what R6 and R12 read.
//
// Raising a link is deliberately not one of them, and policy.IsNotifyAction
// agrees: minting a link sends nothing, and committing it would move
// LastNotifyAt and make the rate rule refuse the very send the link was raised
// for. What bounds link raising is R1, which counts it as a contact, and the
// invocation's action budget in layer 1.
func commitsWindow(action string) bool {
	return policy.IsNotifyAction(action)
}

// notifyActionFor turns a medium into the member of the lawful set that
// carries it.
func notifyActionFor(medium string) (string, bool) {
	switch medium {
	case MediumEmail:
		return riskitem.ActionNotifyEmail, true
	case MediumSMS:
		return riskitem.ActionNotifySMS, true
	default:
		return "", false
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// classOf is internal/classify's reading of the failure a detector saw.
//
// It is computed for the policy and it is not put on the wire: see the header
// of tools.go. An item whose source carries no failure at all, which is every
// unpaid order and every overdue invoice, classifies as unclassified, and what
// a policy does with that is the policy's business.
func classOf(item riskitem.RiskItem) classify.Class {
	return classify.Classify(classify.Failure{
		Code:   item.Signal.FailureCode,
		Reason: item.Signal.FailureReason,
		Source: classify.Source(item.Signal.FailureSource),
		Step:   item.Signal.FailureStep,
		Method: classify.Method(item.Signal.Method),
	})
}

// factsFor is what the policy needs and the item does not carry: an active
// promise hold, a dispute, the source resource's status. A server built with
// no Facts provider hands the policy a zero Facts, and the rules that read
// those fields do not fire.
func (s *Server) factsFor(ctx context.Context, item riskitem.RiskItem) policy.Facts {
	if s.opts.Facts == nil {
		return policy.Facts{}
	}
	return s.opts.Facts.FactsFor(ctx, item)
}

// channelsOf is which media a notification could use. It reports that an
// address exists and never what it is.
func channelsOf(item riskitem.RiskItem) []string {
	channels := []string{}
	if item.Customer.Email != "" {
		channels = append(channels, MediumEmail)
	}
	if item.Customer.Contact != "" {
		channels = append(channels, MediumSMS)
	}
	return channels
}

// promiseDateLayouts are what promised_at may be written as. The date-only
// form is first because it is the one a promise is actually made in.
var promiseDateLayouts = []string{"2006-01-02", time.RFC3339}

// parsePromiseDate reads promised_at, or says what it wanted.
func parsePromiseDate(raw string) (time.Time, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}, fmt.Errorf("a promise has to name the date the customer gave, as YYYY-MM-DD")
	}
	for _, layout := range promiseDateLayouts {
		if at, err := time.Parse(layout, value); err == nil {
			return at, nil
		}
	}
	return time.Time{}, fmt.Errorf("promised_at is %q, which is neither YYYY-MM-DD nor RFC3339", raw)
}

func (s *Server) lookup(itemID string) (riskitem.RiskItem, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.allowed[itemID]
	return item, ok
}

// jsonResult renders a value as the tool's text content.
//
// The text content is what the model reads, so it is the surface a leak test
// walks. StructuredContent is left unset: this package uses AddTool's typed
// form with an `any` output, so the SDK does not populate it, and one
// rendering of one value is one thing to check rather than two.
func jsonResult(v any) *mcp.CallToolResult {
	encoded, err := json.Marshal(v)
	if err != nil {
		return errResult(err)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
	}
}

// errResult is a tool-level failure the model can read and act on, rather than
// a protocol error it cannot.
func errResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
}
