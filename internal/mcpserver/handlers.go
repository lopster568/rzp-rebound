package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/lopster568/rzp-recovery-agent/internal/audit"
	"github.com/lopster568/rzp-recovery-agent/internal/batch"
	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/recovery"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerTools adds the seven. The order here is ToolNames' order, and the
// two lists are checked against each other by
// TestServerServesExactlyTheSevenNamedTools.
func (s *Server) registerTools() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: ToolListFailedPayments,
		Description: "List the failed payments waiting for a decision. " +
			"Each one carries its order id, amount in paise, currency, and merchant receipt. " +
			"These are the only orders anything here can act on.",
	}, s.handleListFailedPayments)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: ToolGetPaymentDetail,
		Description: "Read one order's current state out of the gateway: its status, how many " +
			"payment attempts it has already had, and the error fields of the failed payment, " +
			"with the recovery class those fields map to.",
	}, s.handleGetPaymentDetail)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: ToolRecordDecision,
		Description: "Record what you have decided to do about one order and why, before you do it. " +
			"Actions on an order are refused until a decision for that order is on the record. " +
			"The reasoning goes into the audit trail a compliance reviewer reads.",
	}, s.handleRecordDecision)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: ToolCreatePaymentLink,
		Description: "Raise a payment link for an order, so the customer can complete the payment " +
			"themselves. Use it when the card cannot be re-presented unattended: the customer has " +
			"to authenticate again, or the instrument itself cannot work.",
	}, s.handleCreatePaymentLink)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: ToolResendNotification,
		Description: "Ask Razorpay to send an existing payment link again. What this observes is " +
			"the notification API call's response. It does not observe a person receiving or " +
			"reading anything.",
	}, s.handleResendNotification)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: ToolRetryPayment,
		Description: "Re-present the same instrument on an order. Use it when the failure was " +
			"something that can pass on a second attempt without the customer being involved.",
	}, s.handleRetryPayment)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: ToolEscalateToHuman,
		Description: "Hand an order to a person and take no automated action on it. This is a " +
			"successful outcome, not a failure: some orders are ones nothing should be attempted on.",
	}, s.handleEscalateToHuman)
}

// ---------------------------------------------------------------------------
// Read tools
// ---------------------------------------------------------------------------

func (s *Server) handleListFailedPayments(
	_ context.Context, _ *mcp.CallToolRequest, _ ListFailedPaymentsInput,
) (*mcp.CallToolResult, any, error) {
	out := ListFailedPaymentsOutput{
		Note: "these are the only orders this server will act on",
	}
	for _, o := range s.Orders() {
		out.Orders = append(out.Orders, FailedPaymentSummary{
			OrderID:     o.OrderID,
			AmountPaise: o.AmountPaise,
			Currency:    o.Currency,
			Receipt:     o.Receipt,
		})
	}
	return jsonResult(out), nil, nil
}

func (s *Server) handleGetPaymentDetail(
	ctx context.Context, _ *mcp.CallToolRequest, in GetPaymentDetailInput,
) (*mcp.CallToolResult, any, error) {
	order, ok := s.lookup(in.OrderID)
	if !ok {
		return errResult(fmt.Errorf("order %s is not in this batch", in.OrderID)), nil, nil
	}

	state, err := s.observe(ctx, order.OrderID)
	if err != nil {
		return errResult(err), nil, nil
	}

	detail := PaymentDetail{
		OrderID:      order.OrderID,
		AmountPaise:  order.AmountPaise,
		Currency:     order.Currency,
		Receipt:      order.Receipt,
		OrderStatus:  state.order.Status,
		AmountPaid:   state.order.AmountPaid,
		AttemptsSeen: len(state.payments),
		FailureClass: state.class.String(),
		Note:         "read from the gateway just now",
	}
	if state.failed != nil {
		detail.FailureCode = state.failed.ErrorCode
		detail.FailureReason = state.failed.ErrorReason
		detail.FailureSource = state.failed.ErrorSource
		detail.FailureStep = state.failed.ErrorStep
	}
	return jsonResult(detail), nil, nil
}

// ---------------------------------------------------------------------------
// The decision tool
// ---------------------------------------------------------------------------

func (s *Server) handleRecordDecision(
	ctx context.Context, _ *mcp.CallToolRequest, in RecordDecisionInput,
) (*mcp.CallToolResult, any, error) {
	orderID := strings.TrimSpace(in.OrderID)
	action := strings.TrimSpace(in.Action)
	reasoning := strings.TrimSpace(in.Reasoning)

	if orderID == "" {
		return errResult(fmt.Errorf("a decision has to name the order it is about")), nil, nil
	}
	if _, ok := s.lookup(orderID); !ok {
		return errResult(fmt.Errorf("order %s is not in this batch", orderID)), nil, nil
	}
	if !slices.Contains(DecisionActions(), action) {
		return errResult(fmt.Errorf("%q is not an action this system has. It is one of %s",
			in.Action, strings.Join(DecisionActions(), ", "))), nil, nil
	}
	if reasoning == "" {
		return errResult(fmt.Errorf(
			"a decision needs the reasoning behind it. A compliance reviewer reads this row " +
				"to reconstruct why the action was taken.")), nil, nil
	}

	decision := Decision{OrderID: orderID, Action: action, Reasoning: reasoning}

	s.mu.Lock()
	s.decisions[orderID] = decision
	s.decisionLog = append(s.decisionLog, decision)
	s.tally(orderID).DecisionsRecorded++
	s.mu.Unlock()

	if err := s.record(ctx, audit.Event{
		OrderID:        orderID,
		Kind:           KindDecisionRecorded,
		ProposedAction: action,
		Detail: map[string]string{
			DetailChosenAction: action,
			DetailReasoning:    reasoning,
		},
	}); err != nil {
		return nil, nil, err
	}

	return jsonResult(RecordDecisionOutput{
		Recorded: true,
		OrderID:  orderID,
		Action:   action,
		Note:     "on the record. The action tools for this order are open now.",
	}), nil, nil
}

// ---------------------------------------------------------------------------
// Action tools
// ---------------------------------------------------------------------------

func (s *Server) handleRetryPayment(
	ctx context.Context, _ *mcp.CallToolRequest, in RetryPaymentInput,
) (*mcp.CallToolResult, any, error) {
	return s.act(ctx, action{
		tool:         ToolRetryPayment,
		orderID:      in.OrderID,
		policyAction: policy.ActionRetrySameInstrument,
		execute:      s.executeRetry,
	})
}

func (s *Server) handleCreatePaymentLink(
	ctx context.Context, _ *mcp.CallToolRequest, in CreatePaymentLinkInput,
) (*mcp.CallToolResult, any, error) {
	return s.act(ctx, action{
		tool:         ToolCreatePaymentLink,
		orderID:      in.OrderID,
		policyAction: linkAction(in.Purpose),
		execute:      s.executeCreateLink,
	})
}

func (s *Server) handleResendNotification(
	ctx context.Context, _ *mcp.CallToolRequest, in ResendNotificationInput,
) (*mcp.CallToolResult, any, error) {
	medium := strings.TrimSpace(in.Medium)
	if medium == "" {
		medium = razorpay.MediumEmail
	}
	return s.act(ctx, action{
		tool:         ToolResendNotification,
		orderID:      in.OrderID,
		policyAction: s.recordedNotifyAction(in.OrderID),
		execute: func(ctx context.Context, a action, order batch.AgentVisibleOrder, d policy.Decision, out *ActionOutput) (effect, error) {
			return s.executeResend(ctx, a, order, d, out, in.PaymentLinkID, medium)
		},
	})
}

func (s *Server) handleEscalateToHuman(
	ctx context.Context, _ *mcp.CallToolRequest, in EscalateToHumanInput,
) (*mcp.CallToolResult, any, error) {
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return errResult(fmt.Errorf("an escalation has to say what a person should look at")), nil, nil
	}
	return s.act(ctx, action{
		tool:    ToolEscalateToHuman,
		orderID: in.OrderID,
		// An escalation takes no action, so it is evaluated as one. The
		// evaluation still runs and is still recorded: a halt, a replay, or an
		// exhausted budget stops an escalation the same as anything else.
		policyAction: policy.ActionNone,
		// R7 and R4 escalate rather than allow, and an escalation is exactly
		// the right move on an order those two rules fired on. So an escalate
		// verdict passes here, and only a deny stops it.
		acceptsEscalate: true,
		execute: func(ctx context.Context, a action, order batch.AgentVisibleOrder, d policy.Decision, out *ActionOutput) (effect, error) {
			return s.executeEscalate(ctx, a, order, d, out, reason)
		},
	})
}

// ---------------------------------------------------------------------------
// The shared action path
// ---------------------------------------------------------------------------

// action is one proposed action, with what it needs to be evaluated and run.
type action struct {
	tool            string
	orderID         string
	policyAction    string
	acceptsEscalate bool
	execute         executeFunc
}

// executeFunc runs the side effect. It is only ever called after
// policy.Evaluate returned a verdict this action accepts.
type executeFunc func(ctx context.Context, a action, order batch.AgentVisibleOrder, d policy.Decision, out *ActionOutput) (effect, error)

// effect is what an execution did, in the same vocabulary
// recovery.ActionResult uses, so the outcome row for this arm is built from
// the same fields as the other three.
type effect struct {
	kind             string
	sideEffect       bool
	gatewayCalls     int
	claimedRecovered bool
	escalated        bool
	// commitKey and commitAction go to store.Commit. An empty key means this
	// action does not spend an attempt or a notification window.
	commitKey    string
	commitAction string
}

// act is layer 2 of ADR-0003 and the one path every action tool takes.
//
// policy.Evaluate is its first statement after the order is resolved, and the
// verdict is in the ledger before anything runs. There is no branch in which
// an execute function is reached without a recorded verdict behind it, which
// is the claim TestEveryActionToolConsultsPolicyBeforeSideEffect checks by
// walking the registry rather than by reading this comment.
func (s *Server) act(ctx context.Context, a action) (*mcp.CallToolResult, any, error) {
	order, ok := s.lookup(a.orderID)
	if !ok {
		return errResult(fmt.Errorf("order %s is not in this batch", a.orderID)), nil, nil
	}

	state, err := s.observe(ctx, order.OrderID)
	if err != nil {
		return errResult(err), nil, nil
	}

	attempts := s.opts.Store.Attempts(order.OrderID)
	attemptNo := attempts + 1
	key := policy.IdempotencyKey(order.OrderID, a.policyAction, attemptNo)
	snapshot := s.opts.Store.Snapshot(order.OrderID, key, s.opts.KillSwitchEngaged)

	decision := s.opts.Policy.Evaluate(snapshot, policy.Request{
		OrderID:     order.OrderID,
		Action:      a.policyAction,
		Class:       state.class,
		AmountPaise: order.AmountPaise,
		AttemptNo:   attemptNo,
	})

	out := ActionOutput{
		OrderID:           order.OrderID,
		Action:            a.policyAction,
		PolicyVerdict:     string(decision.Verdict),
		PolicyRule:        decision.RuleID,
		PolicyReason:      decision.Reason,
		AttemptsRemaining: decision.Remaining,
	}

	// The evaluation goes on the record before anything acts on it.
	if err := s.record(ctx, audit.Event{
		OrderID:        order.OrderID,
		Kind:           audit.KindPolicyEvaluated,
		Class:          state.class.String(),
		ProposedAction: a.policyAction,
		PolicyVerdict:  string(decision.Verdict),
		PolicyRule:     decision.RuleID,
		Detail: map[string]string{
			DetailTool:                      a.tool,
			DetailGateLayer:                 LayerHandler,
			recovery.DetailIdempotencyKey:   policy.ShortKey(decision.IdempotencyKey),
			recovery.DetailIdempotentReplay: btoa(decision.IdempotentReplay),
			recovery.DetailAttemptNo:        itoa(attemptNo),
			"policy_reason":                 decision.Reason,
		},
	}); err != nil {
		return nil, nil, err
	}

	passes := decision.Allowed() || (a.acceptsEscalate && decision.Verdict == policy.VerdictEscalate)
	if !passes {
		out.Allowed = false
		out.Note = "refused by the policy. Read policy_rule and policy_reason, and choose something else."
		if err := s.finishAction(ctx, a, order, state.class, decision, out, effect{kind: recovery.ActionNone}, nil); err != nil {
			return nil, nil, err
		}
		return jsonResult(out), nil, nil
	}

	// Allowed. From here on there can be a side effect, and the verdict that
	// let it through is already on the result and already in the ledger.
	out.Allowed = true
	s.spendAction()

	got, execErr := a.execute(ctx, a, order, decision, &out)
	if got.commitKey != "" {
		s.opts.Store.Commit(order.OrderID, got.commitKey, got.commitAction)
	}
	if err := s.finishAction(ctx, a, order, state.class, decision, out, got, execErr); err != nil {
		return nil, nil, err
	}
	if execErr != nil {
		out.Note = "the gateway did not accept this: " + execErr.Error()
		return jsonResult(out), nil, nil
	}
	return jsonResult(out), nil, nil
}

// finishAction writes the action row and updates the order's tally.
//
// The row is filed by whether a side effect happened, not by what the action
// called itself. That is the phase 2 review finding: keying off the action
// kind let an arm decide its own containment score by returning ActionNone
// after reaching the gateway, and the LLM arm is exactly the actor that
// finding was about.
func (s *Server) finishAction(
	ctx context.Context,
	a action,
	order batch.AgentVisibleOrder,
	class classify.Class,
	decision policy.Decision,
	out ActionOutput,
	got effect,
	execErr error,
) error {
	kind := audit.KindActionTaken
	if !got.sideEffect && (got.kind == "" || got.kind == recovery.ActionNone) && execErr == nil {
		kind = audit.KindActionSkipped
	}

	detail := map[string]string{
		DetailTool:                     a.tool,
		recovery.DetailPolicyConsulted: "true",
		recovery.DetailSideEffect:      btoa(got.sideEffect),
		recovery.DetailEscalated:       btoa(got.escalated),
		recovery.DetailIdempotencyKey:  policy.ShortKey(decision.IdempotencyKey),
		recovery.DetailAttemptNo:       itoa(0),
		recovery.DetailGatewayCalls:    itoa(got.gatewayCalls),
		"claimed_recovered":            btoa(got.claimedRecovered),
		"attempts_remaining":           itoa(decision.Remaining),
	}
	if out.PaymentID != "" {
		detail[recovery.DetailPaymentID] = out.PaymentID
	}
	if out.PaymentLinkID != "" {
		detail[recovery.DetailPaymentLinkID] = out.PaymentLinkID
	}
	if execErr != nil {
		detail["action_error"] = execErr.Error()
	}
	if !out.Allowed {
		detail["refused_action"] = a.policyAction
	}
	if reason := s.escalationReason(order.OrderID); got.escalated && reason != "" {
		detail["escalation_reason"] = reason
	}

	if err := s.record(ctx, audit.Event{
		OrderID:        order.OrderID,
		Kind:           kind,
		Class:          class.String(),
		ProposedAction: actionKindFor(got, a),
		PolicyVerdict:  string(decision.Verdict),
		PolicyRule:     decision.RuleID,
		Detail:         detail,
	}); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tally(order.OrderID)
	t.GatewayCalls += got.gatewayCalls
	if got.sideEffect {
		t.SideEffect = true
	}
	if got.claimedRecovered {
		t.ClaimedRecovered = true
	}
	if got.escalated {
		t.Escalated = true
	}
	if out.Allowed {
		t.haveAllowed = true
		t.lastAllowedVerdict, t.lastAllowedRule = string(decision.Verdict), decision.RuleID
		if got.kind != "" && got.kind != recovery.ActionNone {
			t.ActionKind = got.kind
		}
	} else {
		t.haveRefused = true
		t.lastRefusedVerdict, t.lastRefusedRule = string(decision.Verdict), decision.RuleID
	}
	return nil
}

// actionKindFor is what goes in the row's proposed_action. A refused action
// still names what was proposed, because a refusal of nothing is not a
// refusal.
func actionKindFor(got effect, a action) string {
	if got.kind != "" {
		return got.kind
	}
	return a.policyAction
}

// ---------------------------------------------------------------------------
// The four executions
// ---------------------------------------------------------------------------

func (s *Server) executeRetry(
	ctx context.Context, _ action, order batch.AgentVisibleOrder, d policy.Decision, out *ActionOutput,
) (effect, error) {
	record, err := s.opts.Surface.Attempter.Attempt(ctx, order, s.opts.Surface.Card)

	// The side effect is recorded whether or not the call came back clean. A
	// request that reached the gateway and then failed to decode is a request
	// that was made.
	got := effect{
		kind:         recovery.ActionRetrySameInstrument,
		sideEffect:   true,
		gatewayCalls: record.GatewayCalls,
		commitKey:    d.IdempotencyKey,
		commitAction: recovery.ActionRetrySameInstrument,
	}
	out.PaymentID = record.PaymentID
	if err != nil {
		return got, err
	}

	got.claimedRecovered = true
	// The status is read back so the agent is told what the gateway says
	// rather than what the attempt hoped. It is the same read the outcome row
	// is built from, and it is the world's answer either way.
	if final, ferr := s.opts.Surface.Port.FetchOrder(ctx, order.OrderID); ferr == nil {
		out.OrderStatus = final.Status
		got.claimedRecovered = final.Status == razorpay.OrderStatusPaid
	}
	return got, nil
}

func (s *Server) executeCreateLink(
	ctx context.Context, a action, order batch.AgentVisibleOrder, _ policy.Decision, out *ActionOutput,
) (effect, error) {
	currency := s.opts.Surface.Currency
	if currency == "" {
		currency = "INR"
	}

	link, err := s.opts.Surface.Port.CreatePaymentLink(ctx, razorpay.CreatePaymentLinkRequest{
		AmountPaise: order.AmountPaise,
		Currency:    currency,
		Description: "recovery for " + order.OrderID,
		ReferenceID: order.Receipt,
	})

	// Raising a link is a side effect and it is not a notification, so it
	// commits nothing.
	//
	// The alternative was to commit it as the notify action it was evaluated
	// as, and that moves LastNotifyAt, which makes R6 refuse the resend the
	// link exists for. Committing it as a non-notify action is worse still:
	// store.Commit counts anything that is not a notification as a payment
	// attempt, and a payment link is not one. What bounds link raising is the
	// invocation's action budget in layer 1. DECISIONS.md has the trade.
	got := effect{kind: a.policyAction, sideEffect: true}
	if err != nil {
		return got, err
	}
	out.PaymentLinkID = link.ID
	out.PaymentLinkURL = link.ShortURL
	out.Note = "the link is raised. Nothing has been sent yet: use " +
		ToolResendNotification + " with this payment_link_id."
	return got, nil
}

func (s *Server) executeResend(
	ctx context.Context, a action, order batch.AgentVisibleOrder, d policy.Decision, out *ActionOutput,
	linkID, medium string,
) (effect, error) {
	got := effect{kind: a.policyAction, sideEffect: true}

	if strings.TrimSpace(linkID) == "" {
		return effect{kind: recovery.ActionNone}, fmt.Errorf(
			"a resend has to name the payment_link_id that %s returned", ToolCreatePaymentLink)
	}

	receipt, sendErr := s.opts.Surface.Notifier.SendPaymentLink(ctx, linkID, medium)
	got.commitKey = d.IdempotencyKey
	got.commitAction = a.policyAction

	out.PaymentLinkID = linkID
	out.NotificationNote = receipt.AuditPhrase

	if err := s.record(ctx, audit.Event{
		OrderID:        order.OrderID,
		Kind:           audit.KindNotificationRequested,
		ProposedAction: a.policyAction,
		PolicyVerdict:  string(d.Verdict),
		PolicyRule:     d.RuleID,
		Detail: map[string]string{
			recovery.DetailPaymentLinkID: linkID,
			"medium":                     medium,
			"audit_phrase":               receipt.AuditPhrase,
			"api_call_succeeded":         btoa(receipt.APICallSucceeded),
			"delivery_confirmed":         btoa(receipt.DeliveryConfirmed),
		},
	}); err != nil {
		return got, err
	}

	// ClaimedRecovered stays false. A payment link that was sent is not a
	// payment, and this project does not observe a person coming back.
	return got, sendErr
}

func (s *Server) executeEscalate(
	ctx context.Context, _ action, order batch.AgentVisibleOrder, _ policy.Decision, out *ActionOutput,
	reason string,
) (effect, error) {
	out.Note = "handed to a person. Nothing automated will run on this order."

	// The reason goes on the action row rather than into a second
	// decision_recorded row. record_decision already wrote one for this order,
	// and two rows of the same kind for one decision reads as two decisions.
	s.mu.Lock()
	t := s.tally(order.OrderID)
	t.escalationReason = reason
	s.mu.Unlock()

	// No gateway call, so no side effect and no commit. An escalation is a
	// decision with an outcome, and it is scored in the escalation precision
	// and recall pair rather than as an action taken.
	return effect{kind: recovery.ActionNone, escalated: true}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// gatewayState is one order as the gateway currently reports it.
type gatewayState struct {
	order    razorpay.Order
	payments []razorpay.Payment
	failed   *razorpay.Payment
	class    classify.Class
}

// observe reads an order and its payments and classifies the failure.
//
// It reads every time rather than caching. The agent can act between two
// reads, and a cached view would let it be told a state that its own last
// action had already changed.
func (s *Server) observe(ctx context.Context, orderID string) (gatewayState, error) {
	var state gatewayState

	order, err := s.opts.Surface.Port.FetchOrder(ctx, orderID)
	if err != nil {
		return state, err
	}
	state.order = order

	payments, err := s.opts.Surface.Port.ListPaymentsForOrder(ctx, orderID)
	if err != nil {
		return state, err
	}
	state.payments = payments

	for i := range payments {
		if payments[i].Status == razorpay.PaymentStatusFailed {
			state.failed = &payments[i]
		}
	}
	state.class = classify.Classify(recovery.FailureFrom(state.failed))
	return state, nil
}

func (s *Server) lookup(orderID string) (batch.AgentVisibleOrder, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.allowed[orderID]
	return o, ok
}

// linkAction maps the create_payment_link purpose onto a policy action. An
// unrecognised purpose is a reauth, which is the conservative reading: it is
// the class with the smaller attempt budget of the two.
func linkAction(purpose string) string {
	if strings.TrimSpace(strings.ToLower(purpose)) == "new_instrument" {
		return policy.ActionRequestNewInstrument
	}
	return policy.ActionRequestReauth
}

// recordedNotifyAction is the notify action the agent said it was taking on
// this order, so a resend is evaluated as the thing the decision named. An
// order whose decision was something else falls back to reauth.
func (s *Server) recordedNotifyAction(orderID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.decisions[orderID]; ok && policy.IsNotifyAction(d.Action) {
		return d.Action
	}
	return policy.ActionRequestReauth
}

// jsonResult renders a value as the tool's text content.
//
// The text content is what the model reads, so it is the surface the leak test
// walks. StructuredContent is left unset: this package uses Server.AddTool's
// typed form with an `any` output, so the SDK does not populate it, and one
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
