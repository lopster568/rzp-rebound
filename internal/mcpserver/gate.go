package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lopster568/rzp-recovery-agent/internal/audit"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// methodCallTool is the JSON-RPC method the gate cares about. The SDK does not
// export its constant, and hardcoding the wire name is correct here: it is the
// protocol's name, not the SDK's.
const methodCallTool = "tools/call"

// gateDecision is what layer 1 concluded about a tool call.
type gateDecision struct {
	verdict policy.Verdict
	rule    string
	reason  string
}

func (d gateDecision) allowed() bool { return d.verdict == policy.VerdictAllow }

// allowGate is what a call that nothing refused carries. It has a rule id for
// the same reason policy.Evaluate's allow does: an audit row should never have
// to be read as "no rule fired, presumably that was fine".
var allowGate = gateDecision{
	verdict: policy.VerdictAllow,
	rule:    policy.RuleAllow,
	reason:  "no middleware rule refused this call",
}

// gate is layer 1 of ADR-0003: receiving middleware around every tools/call.
//
// It knows the tool name and the risk item id and nothing about what the tool
// does. It opens the span, checks the kill switch, the tool allowlist, the
// item allowlist, the decision requirement, and the invocation's action
// budget, and it writes the tool_call row that puts the call on the record
// whether it was allowed or refused.
//
// Nothing in here names an action, a medium, a handle, or a source. That is
// the property that let the tool surface be replaced under it: the eight tools
// this server now serves are not the seven it served before, and this function
// did not have to know.
//
// It applies to every registered tool, including one written after this
// comment. That is what makes the containment claim survive a tool somebody
// forgets to gate by hand.
func (s *Server) gate(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method != methodCallTool {
			return next(ctx, method, req)
		}
		call, ok := req.(*mcp.CallToolRequest)
		if !ok || call.Params == nil {
			return next(ctx, method, req)
		}

		tool := call.Params.Name
		requested := itemIDFrom(call.Params.Arguments)

		ctx, span := s.tracer.Start(ctx, "mcp.tools/call", trace.WithAttributes(
			attribute.String(AttrTool, tool),
		))
		defer span.End()

		decision := s.evaluateGate(tool, requested)

		span.SetAttributes(
			attribute.String(AttrGateVerdict, string(decision.verdict)),
			attribute.String(AttrGateRule, decision.rule),
		)
		if requested != "" {
			span.SetAttributes(attribute.String(AttrItemID, requested))
		}
		if !decision.allowed() {
			span.SetStatus(codes.Error, decision.rule)
		}

		s.countToolCall(requested, decision)

		// The row goes down before the call is dispatched. A refusal that
		// leaves no row is what makes a containment count unprovable.
		// The verdict and the rule go on the row itself as well as into the
		// detail map, so a reader grepping the ledger for a rule id finds the
		// tool call that hit it and not only the policy evaluation behind it.
		//
		// It changes no metric. harness/scorer.py counts evaluations by kind
		// and both violation counts off the side-effect flag, and a tool_call
		// row deliberately carries no side-effect flag.
		if err := s.record(ctx, audit.Event{
			OrderID:        s.ledgerKey(requested),
			Kind:           KindToolCall,
			ProposedAction: tool,
			PolicyVerdict:  string(decision.verdict),
			PolicyRule:     decision.rule,
			Detail: map[string]string{
				DetailTool:        tool,
				DetailGateVerdict: string(decision.verdict),
				DetailGateRule:    decision.rule,
				DetailGateReason:  decision.reason,
				DetailGateLayer:   LayerMiddleware,
				DetailRequestedID: requested,
			},
		}); err != nil {
			return nil, err
		}

		if !decision.allowed() {
			// An unknown tool name is not a domain refusal the model should
			// reason about. It is a call for something that does not exist,
			// and the protocol has a shape for that. Everything else comes
			// back as a successful call carrying the refusal, so the model can
			// read why and choose something else.
			if decision.rule == RuleToolAllowlist {
				return nil, fmt.Errorf("mcpserver: %s: %s", decision.rule, decision.reason)
			}
			return refusalResult(requested, tool, decision), nil
		}

		return next(ctx, method, req)
	}
}

// evaluateGate runs the five layer 1 rules in a fixed order.
//
// The order is the same shape as policy.Evaluate's: a halt beats every reason
// a call might otherwise be fine, so the kill switch is first. Then the two
// allowlists, which say this call names something that does not exist. Then
// the decision requirement, which is about the item. Then the budget, which is
// about the invocation.
func (s *Server) evaluateGate(tool, itemID string) gateDecision {
	if s.opts.KillSwitchEngaged {
		return gateDecision{policy.VerdictDeny, policy.RuleKillSwitch,
			"the kill switch is engaged, so no tool runs"}
	}

	if !IsKnownTool(tool) {
		return gateDecision{policy.VerdictDeny, RuleToolAllowlist,
			fmt.Sprintf("%q is not one of the tools this server serves", tool)}
	}

	// A read tool that names an item still has to name one that exists here.
	// The list tool names none, and that is not a violation.
	if itemID != "" && !s.isAllowed(itemID) {
		return gateDecision{policy.VerdictDeny, RuleItemAllowlist,
			fmt.Sprintf("risk item %s is not in this batch, so nothing here can act on it", itemID)}
	}

	if !IsActionTool(tool) {
		return allowGate
	}

	if itemID == "" {
		return gateDecision{policy.VerdictDeny, RuleItemAllowlist,
			"an action tool has to name the risk item it is acting on"}
	}

	if !s.hasDecision(itemID) {
		return gateDecision{policy.VerdictDeny, RuleDecisionFirst,
			"no decision is on the record for this risk item. Call record_decision with " +
				"the item id, the action you have chosen, and why, and then act."}
	}

	if spent, budget := s.actionSpend(); spent >= budget {
		return gateDecision{policy.VerdictDeny, policy.RuleActionBudget,
			fmt.Sprintf("this invocation has spent %d of its %d action budget", spent, budget)}
	}

	return allowGate
}

func (s *Server) isAllowed(itemID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.allowed[itemID]
	return ok
}

// hasDecision is M3: a decision for this item is on the record.
//
// It asks whether one exists, not whether it names the action about to run,
// and that is deliberate rather than an omission. Reaching a decided action
// takes more than one call: an item with nothing to pay against needs a link
// raised before anything can be sent about it, so a decision of notify_email
// is carried out by create_payment_link_for_item and then notify_item. A gate
// that demanded equality would refuse the first of those two and leave the
// decision unreachable.
//
// What the ledger does instead is carry both: the action row names the action
// that ran and the decision that was on the record when it did, so a reviewer
// reading one row sees a mismatch without joining anything.
func (s *Server) hasDecision(itemID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.decisions[itemID]
	return ok
}

// recordedDecision returns the decision on the record for an item.
func (s *Server) recordedDecision(itemID string) (Decision, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.decisions[itemID]
	return d, ok
}

func (s *Server) actionSpend() (spent, budget int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.actionsSpent, s.opts.ActionBudget
}

// spendAction charges one action tool call against the invocation budget. It
// is called by the handler after the gate let the call through, so a call the
// gate refused does not spend anything.
func (s *Server) spendAction() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.actionsSpent++
}

// countToolCall records the call on the invocation and on the item.
func (s *Server) countToolCall(itemID string, decision gateDecision) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.toolCalls++
	if itemID == "" || s.allowed[itemID].ID == "" {
		// A call naming no item, or one naming an item this invocation does
		// not have. Neither belongs on an item's tally: the first has no item
		// and the second names one whose row would then exist because somebody
		// asked about it.
		return
	}
	t := s.tally(itemID)
	t.ToolCalls++
	if !decision.allowed() {
		t.DeniedToolCalls++
	}
}

// itemIDFrom pulls item_id out of raw tool arguments.
//
// It decodes into a one-field struct rather than a map, so an argument
// document with a nested object or an unexpected type cannot make this panic
// or return something that is not a string. Anything it cannot read is an
// empty item id, which the allowlist then refuses for an action tool.
//
// One argument name across the whole surface is what keeps this function from
// having to know which tool it is looking at. Every tool that names an item
// names it in item_id, and a tool added later that invents its own spelling
// would be handing the middleware an empty id and refusing itself, which is
// the safe direction for that mistake to fall.
func itemIDFrom(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var args struct {
		ItemID string `json:"item_id"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return ""
	}
	return args.ItemID
}

// refusalResult is what a refused call comes back as: a successful tool call
// carrying an ActionOutput that says what was refused and which rule refused
// it.
//
// One shape for every refusal, read tools included. A model that gets a
// different document depending on which tool it called has to parse two things
// to learn one fact.
func refusalResult(itemID, tool string, decision gateDecision) *mcp.CallToolResult {
	return jsonResult(ActionOutput{
		ItemID:        itemID,
		Action:        tool,
		Allowed:       false,
		PolicyVerdict: string(decision.verdict),
		PolicyRule:    decision.rule,
		PolicyReason:  decision.reason,
		Note:          "refused by the server-side gate before the tool ran",
	})
}
