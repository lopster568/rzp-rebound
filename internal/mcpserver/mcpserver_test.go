package mcpserver_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/audit"
	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/mcpserver"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
	"github.com/lopster568/rzp-recovery-agent/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// runInstant is what the fake clock reads. Aging is measured against it, so
// every expected aging number in this file is arithmetic on this constant
// rather than on whatever today is.
var runInstant = time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)

// ---------------------------------------------------------------------------
// The two seams, stubbed
//
// The server reaches Razorpay through an Intervener and the policy through an
// Evaluator, and it holds nothing else that can act. So the whole suite runs
// on two stubs: what they record is exactly what the agent could reach, and a
// call that should never have happened fails the test at the moment it
// happens, with the action that made it.
// ---------------------------------------------------------------------------

// stubEvaluator is layer 2. It allows by default and a test can make it decide
// anything, including a decision that depends on the state the store handed
// it, which is what the concurrency test needs.
type stubEvaluator struct {
	mu     sync.Mutex
	decide func(policy.State, policy.Request) policy.Decision
	calls  []policy.Request
}

func newStubEvaluator() *stubEvaluator { return &stubEvaluator{} }

func (e *stubEvaluator) Evaluate(state policy.State, req policy.Request) policy.Decision {
	e.mu.Lock()
	decide := e.decide
	e.calls = append(e.calls, req)
	e.mu.Unlock()

	if decide != nil {
		return decide(state, req)
	}
	return policy.Decision{
		Verdict:        policy.VerdictAllow,
		RuleID:         policy.RuleAllow,
		Reason:         "the stub policy refused nothing",
		Remaining:      3,
		IdempotencyKey: policy.IdempotencyKey(req.OrderID, req.Action, req.AttemptNo),
	}
}

func (e *stubEvaluator) set(decide func(policy.State, policy.Request) policy.Decision) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.decide = decide
}

// deny makes every evaluation come back refused under one rule.
func (e *stubEvaluator) deny(rule, reason string) {
	e.set(func(_ policy.State, req policy.Request) policy.Decision {
		return policy.Decision{
			Verdict:        policy.VerdictDeny,
			RuleID:         rule,
			Reason:         reason,
			IdempotencyKey: policy.IdempotencyKey(req.OrderID, req.Action, req.AttemptNo),
		}
	})
}

// escalate makes every evaluation come back escalate, which is the verdict
// only escalate_item accepts.
func (e *stubEvaluator) escalate(rule, reason string) {
	e.set(func(_ policy.State, req policy.Request) policy.Decision {
		return policy.Decision{
			Verdict:        policy.VerdictEscalate,
			RuleID:         rule,
			Reason:         reason,
			IdempotencyKey: policy.IdempotencyKey(req.OrderID, req.Action, req.AttemptNo),
		}
	})
}

func (e *stubEvaluator) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.calls)
}

func (e *stubEvaluator) requests() []policy.Request {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]policy.Request(nil), e.calls...)
}

// applyCall is one thing the agent reached.
type applyCall struct {
	itemID string
	action string
}

// stubIntervener is the only way from the tool surface to a side effect, so it
// is where containment is observed.
//
// Two things fail the test from inside Apply rather than in an assertion
// afterwards. A call while forbidden is a side effect that got past the gate.
// A call with fewer policy evaluations behind it than applications in front of
// it is an action that reached the gateway without one, which is the claim
// ADR-0003 makes and the thing a new ungated tool would break.
type stubIntervener struct {
	t    *testing.T
	eval *stubEvaluator

	mu        sync.Mutex
	forbidden bool
	calls     []applyCall
	delay     time.Duration
	outcome   func(riskitem.RiskItem, string) (riskitem.Outcome, error)
}

func newStubIntervener(t *testing.T, eval *stubEvaluator) *stubIntervener {
	return &stubIntervener{t: t, eval: eval}
}

func (i *stubIntervener) Apply(_ context.Context, item riskitem.RiskItem, action string) (riskitem.Outcome, error) {
	i.mu.Lock()
	i.calls = append(i.calls, applyCall{itemID: item.ID, action: action})
	applied := len(i.calls)
	forbidden := i.forbidden
	delay := i.delay
	outcome := i.outcome
	i.mu.Unlock()

	if forbidden {
		i.t.Errorf("a side effect reached the intervention engine with no policy pass behind it: %s on %s",
			action, item.ID)
	}
	if evaluations := i.eval.count(); evaluations < applied {
		i.t.Errorf("%s on %s is application %d with only %d policy evaluations behind it",
			action, item.ID, applied, evaluations)
	}
	if !riskitem.IsLawfulAction(action) {
		i.t.Errorf("%q is not in the lawful action set and the server asked for it anyway", action)
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	if outcome != nil {
		return outcome(item, action)
	}
	return defaultOutcome(item, action)
}

// defaultOutcome is an engine that accepts everything it is lawfully asked
// for, and reports the strongest thing such an engine could actually observe.
func defaultOutcome(item riskitem.RiskItem, action string) (riskitem.Outcome, error) {
	out := riskitem.Outcome{Action: action, Accepted: true, At: runInstant}
	switch action {
	case riskitem.ActionNotifyEmail:
		out.Observable = "email_status:sent"
		out.Handle = item.PayHandle
	case riskitem.ActionNotifySMS:
		out.Observable = "sms_status:sent"
		out.Handle = item.PayHandle
	case riskitem.ActionResendLink:
		out.Observable = "email_status:sent"
		out.Handle = item.PayHandle
	case riskitem.ActionCreatePaymentLink:
		out.Observable = "plink_status:created"
		out.Handle = riskitem.PayHandle{
			Kind: riskitem.HandleKindPaymentLink,
			ID:   "plink_" + item.ID,
			URL:  "https://rzp.io/i/" + item.ID,
		}
	case riskitem.ActionLogPromise:
		out.Observable = "promise_row:written"
	case riskitem.ActionEscalate:
		out.Observable = "escalation_row:written"
	}
	return out, nil
}

func (i *stubIntervener) forbid(on bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.forbidden = on
}

func (i *stubIntervener) setDelay(d time.Duration) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.delay = d
}

func (i *stubIntervener) setOutcome(f func(riskitem.RiskItem, string) (riskitem.Outcome, error)) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.outcome = f
}

func (i *stubIntervener) count() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.calls)
}

func (i *stubIntervener) applied() []applyCall {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]applyCall(nil), i.calls...)
}

// stubFacts is the third seam: what the policy reads and the item does not
// carry.
type stubFacts struct {
	facts policy.Facts
}

func (f *stubFacts) FactsFor(_ context.Context, _ riskitem.RiskItem) policy.Facts { return f.facts }

var _ mcpserver.Intervener = (*stubIntervener)(nil)
var _ mcpserver.Evaluator = (*stubEvaluator)(nil)
var _ mcpserver.FactsProvider = (*stubFacts)(nil)

// ---------------------------------------------------------------------------
// The queue
// ---------------------------------------------------------------------------

// The three items are one per detector, and each one is the shape that makes a
// different branch reachable: a failed payment with an email and nothing to
// pay against, an issued invoice that is already payable, and an abandoned
// order with no way to contact anybody at all.
func failedPaymentItem() riskitem.RiskItem {
	return riskitem.RiskItem{
		ID:             riskitem.NewID(riskitem.SourceFailedPayment, "pay_failedone"),
		Source:         riskitem.SourceFailedPayment,
		SourceID:       "pay_failedone",
		RootOrderID:    "order_failedone",
		Customer:       riskitem.Customer{Name: "A Merchant Customer", Email: "customer@example.test"},
		AmountPaise:    420000,
		AmountDuePaise: 420000,
		Currency:       "INR",
		AtRiskSince:    runInstant.Add(-72 * time.Hour).Unix(),
		Signal:         riskitem.Signal{FailureCode: "BAD_REQUEST_ERROR", FailureReason: "payment_timed_out", FailureSource: "gateway", FailureStep: "payment_authorization", Method: "card", Attempts: 1},
	}
}

func overdueInvoiceItem() riskitem.RiskItem {
	return riskitem.RiskItem{
		ID:              riskitem.NewID(riskitem.SourceOverdueInvoice, "inv_overdueone"),
		Source:          riskitem.SourceOverdueInvoice,
		SourceID:        "inv_overdueone",
		RootOrderID:     "order_overdueone",
		Customer:        riskitem.Customer{Email: "invoiced@example.test", Contact: "+919000000000"},
		AmountPaise:     900000,
		AmountPaidPaise: 100000,
		AmountDuePaise:  800000,
		Currency:        "INR",
		AtRiskSince:     runInstant.Add(-240 * time.Hour).Unix(),
		Signal:          riskitem.Signal{EmailStatus: "sent"},
		PayHandle: riskitem.PayHandle{
			Kind: riskitem.HandleKindInvoice,
			ID:   "inv_overdueone",
			URL:  "https://rzp.io/i/invoiceone",
		},
	}
}

func unreachableOrderItem() riskitem.RiskItem {
	return riskitem.RiskItem{
		ID:             riskitem.NewID(riskitem.SourceUnpaidOrder, "order_abandoned"),
		Source:         riskitem.SourceUnpaidOrder,
		SourceID:       "order_abandoned",
		RootOrderID:    "order_abandoned",
		AmountPaise:    150000,
		AmountDuePaise: 150000,
		Currency:       "INR",
		AtRiskSince:    runInstant.Add(-24 * time.Hour).Unix(),
		Signal:         riskitem.Signal{Attempts: 0},
	}
}

// ---------------------------------------------------------------------------
// The rig
// ---------------------------------------------------------------------------

type rigOptions struct {
	killSwitch   bool
	actionBudget int
	items        []riskitem.RiskItem
	facts        mcpserver.FactsProvider
}

type testRig struct {
	t         *testing.T
	server    *mcpserver.Server
	session   *mcp.ClientSession
	ledger    *bytes.Buffer
	engine    *stubIntervener
	evaluator *stubEvaluator
	store     *store.Store
	spans     *tracetest.SpanRecorder
	items     []riskitem.RiskItem
}

func newRig(t *testing.T, opts rigOptions) *testRig {
	t.Helper()
	ctx := t.Context()

	items := opts.items
	if items == nil {
		items = []riskitem.RiskItem{failedPaymentItem(), overdueInvoiceItem(), unreachableOrderItem()}
	}

	runClock := clock.NewFake(runInstant)
	r := &testRig{t: t, ledger: &bytes.Buffer{}, items: items}

	recorder, err := audit.NewRecorder(audit.Options{Writer: r.ledger, Clock: runClock})
	if err != nil {
		t.Fatalf("build the recorder: %v", err)
	}

	r.spans = tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(r.spans))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })

	r.evaluator = newStubEvaluator()
	r.engine = newStubIntervener(t, r.evaluator)
	r.store = store.New(runClock)

	server, err := mcpserver.New(mcpserver.Options{
		Items:             items,
		Intervene:         r.engine,
		Policy:            r.evaluator,
		Facts:             opts.facts,
		Store:             r.store,
		Recorder:          recorder,
		Tracer:            provider.Tracer(mcpserver.TracerName),
		Clock:             runClock,
		KillSwitchEngaged: opts.killSwitch,
		ActionBudget:      opts.actionBudget,
		Arm:               "a2-agent",
	})
	if err != nil {
		t.Fatalf("build the mcp server: %v", err)
	}
	r.server = server

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.MCP().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("connect the server: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect the client: %v", err)
	}
	r.session = clientSession
	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Wait()
	})

	return r
}

// call invokes one tool. A transport error fails the test: a refusal is a
// successful call carrying a refusal, and a protocol error means something
// broke rather than something was refused.
func (r *testRig) call(name string, args map[string]any) *mcp.CallToolResult {
	r.t.Helper()
	res, err := r.session.CallTool(r.t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		r.t.Fatalf("call %s: %v", name, err)
	}
	return res
}

func (r *testRig) failedItem() string    { return r.items[0].ID }
func (r *testRig) invoiceItem() string   { return r.items[1].ID }
func (r *testRig) noContactItem() string { return r.items[2].ID }

// text returns the tool result's text content, which is what the model reads.
func text(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// decode unmarshals a tool result's text into T.
func decode[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	var out T
	body := text(t, res)
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode %T from %q: %v", out, body, err)
	}
	return out
}

// ledgerRows parses the audit ledger written so far.
func (r *testRig) ledgerRows() []audit.Record {
	r.t.Helper()
	var rows []audit.Record
	scanner := bufio.NewScanner(bytes.NewReader(r.ledger.Bytes()))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec audit.Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			r.t.Fatalf("decode a ledger row %q: %v", line, err)
		}
		rows = append(rows, rec)
	}
	return rows
}

func (r *testRig) rowsOfKind(kind string) []audit.Record {
	var out []audit.Record
	for _, row := range r.ledgerRows() {
		if row.Kind == kind {
			out = append(out, row)
		}
	}
	return out
}

// recordDecision states a decision so the action tools open up.
func (r *testRig) recordDecision(itemID, action string) *mcp.CallToolResult {
	r.t.Helper()
	return r.call(mcpserver.ToolRecordDecision, map[string]any{
		"item_id":   itemID,
		"action":    action,
		"reasoning": "the item calls for " + action + " and it is inside every limit I was given",
	})
}

// argumentsFor builds a valid-shaped argument map for one tool.
//
// A tool with no entry here fails the test that walks the registry. That is
// the mechanism by which adding an ungated tool turns the suite red without
// anybody remembering to add an assertion for it.
func argumentsFor(t *testing.T, tool, itemID string) map[string]any {
	t.Helper()
	switch tool {
	case mcpserver.ToolListRiskItems:
		return map[string]any{}
	case mcpserver.ToolGetRiskItem:
		return map[string]any{"item_id": itemID}
	case mcpserver.ToolRecordDecision:
		return map[string]any{
			"item_id":   itemID,
			"action":    riskitem.ActionNotifyEmail,
			"reasoning": "a reason long enough to be a reason",
		}
	case mcpserver.ToolNotifyItem:
		return map[string]any{"item_id": itemID, "medium": "email"}
	case mcpserver.ToolCreatePaymentLink:
		return map[string]any{"item_id": itemID}
	case mcpserver.ToolResendLink:
		return map[string]any{"item_id": itemID}
	case mcpserver.ToolLogPromise:
		return map[string]any{
			"item_id":     itemID,
			"promised_at": "2026-09-12",
			"days_hold":   7,
			"note":        "the customer said the invoice is with their finance team",
		}
	case mcpserver.ToolEscalateItem:
		return map[string]any{"item_id": itemID, "reason": "a person should look at this one"}
	default:
		t.Fatalf("tool %q is registered and this test has no arguments for it. "+
			"Every registered tool needs an entry here, so that a new tool cannot "+
			"be added without the containment sweep covering it.", tool)
		return nil
	}
}

// registeredTools lists the tools through the server's own registry, over a
// live session, so the set walked is exactly the set the model sees.
func (r *testRig) registeredTools() []string {
	r.t.Helper()
	res, err := r.session.ListTools(r.t.Context(), nil)
	if err != nil {
		r.t.Fatalf("list the tools: %v", err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

// ---------------------------------------------------------------------------
// Layer (a): containment
// ---------------------------------------------------------------------------

func TestEveryActionToolConsultsPolicyBeforeSideEffect(t *testing.T) {
	// Sweep 1: the kill switch is engaged, so every action must be refused in
	// layer 1 before any handler runs.
	t.Run("refused in the middleware", func(t *testing.T) {
		r := newRig(t, rigOptions{killSwitch: true})
		item := r.failedItem()

		tools := r.registeredTools()
		if len(tools) == 0 {
			t.Fatalf("the server registered no tools, so this sweep proves nothing")
		}

		r.engine.forbid(true)
		for _, tool := range tools {
			r.call(tool, argumentsFor(t, tool, item))
		}
		if got := r.engine.count(); got != 0 {
			t.Errorf("with the kill switch engaged, %d action(s) reached the intervention engine", got)
		}
		if got := r.evaluator.count(); got != 0 {
			t.Errorf("with the kill switch engaged, %d policy evaluation(s) happened in a handler", got)
		}
	})

	// Sweep 2: the kill switch is clear and a decision is on the record, so
	// layer 1 lets every call through and layer 2 is the only thing standing
	// between the tool and the intervention engine.
	t.Run("refused by the policy in the handler", func(t *testing.T) {
		r := newRig(t, rigOptions{})
		item := r.failedItem()
		r.recordDecision(item, riskitem.ActionNotifyEmail)
		r.evaluator.deny(policy.RuleHumanApproval, "the stub policy refused everything")

		r.engine.forbid(true)
		for _, tool := range r.registeredTools() {
			if !mcpserver.IsActionTool(tool) {
				continue
			}
			res := r.call(tool, argumentsFor(t, tool, item))
			out := decode[mcpserver.ActionOutput](t, res)
			if out.PolicyRule == "" {
				t.Errorf("%s carries no rule id, so what it did is not countable", tool)
			}
			if out.Allowed {
				t.Errorf("%s was allowed while the policy denied everything", tool)
			}
		}
		if got := r.engine.count(); got != 0 {
			t.Errorf("%d action(s) reached the intervention engine past a denying policy", got)
		}
	})

	// Sweep 2b: an escalate verdict is the one refusal escalate_item is
	// allowed through, because handing an item to a person is the right move
	// on an item the policy escalated. Every other action tool still stops.
	t.Run("only escalate_item passes an escalate verdict", func(t *testing.T) {
		r := newRig(t, rigOptions{})
		item := r.failedItem()
		r.recordDecision(item, riskitem.ActionEscalate)
		r.evaluator.escalate(policy.RuleUnknownFailClosed, "nothing automated is justified here")

		for _, tool := range r.registeredTools() {
			if !mcpserver.IsActionTool(tool) {
				continue
			}
			out := decode[mcpserver.ActionOutput](t, r.call(tool, argumentsFor(t, tool, item)))
			want := tool == mcpserver.ToolEscalateItem
			if out.Allowed != want {
				t.Errorf("%s allowed=%v on an escalate verdict, want %v: %+v", tool, out.Allowed, want, out)
			}
		}
		applied := r.engine.applied()
		if len(applied) != 1 || applied[0].action != riskitem.ActionEscalate {
			t.Errorf("the intervention engine saw %+v, want one escalate", applied)
		}
	})

	// Sweep 3: every action row that carries a side effect carries a verdict.
	// This is the claim harness/scorer.py computes policy_violations_succeeded
	// from, asserted here at the level ADR-0003 states it.
	t.Run("every action row carries a verdict", func(t *testing.T) {
		r := newRig(t, rigOptions{})
		item := r.failedItem()
		r.recordDecision(item, riskitem.ActionNotifyEmail)
		r.call(mcpserver.ToolNotifyItem, map[string]any{"item_id": item, "medium": "email"})

		rows := r.rowsOfKind(audit.KindActionTaken)
		if len(rows) == 0 {
			t.Fatalf("no action_taken row was written for an allowed notification")
		}
		for _, row := range rows {
			if row.Detail[mcpserver.DetailSideEffect] != "true" {
				continue
			}
			if row.PolicyVerdict == "" {
				t.Errorf("an action row with a side effect carries no policy verdict: %+v", row)
			}
		}
	})
}

func TestMiddlewareOpensSpanForEveryToolCall(t *testing.T) {
	r := newRig(t, rigOptions{})
	item := r.failedItem()
	r.recordDecision(item, riskitem.ActionNotifyEmail)

	calls := []struct {
		tool string
		args map[string]any
	}{
		{mcpserver.ToolListRiskItems, map[string]any{}},
		{mcpserver.ToolGetRiskItem, map[string]any{"item_id": item}},
		{mcpserver.ToolNotifyItem, map[string]any{"item_id": item, "medium": "email"}},
	}
	before := len(r.spans.Ended())
	for _, c := range calls {
		r.call(c.tool, c.args)
	}

	var toolSpans []sdktrace.ReadOnlySpan
	for _, span := range r.spans.Ended() {
		for _, attr := range span.Attributes() {
			if string(attr.Key) == mcpserver.AttrTool {
				toolSpans = append(toolSpans, span)
				break
			}
		}
	}
	// One for record_decision in the setup above, plus one per call here.
	if want := len(calls) + before; len(toolSpans) != want {
		t.Fatalf("got %d spans carrying a tool name, want one per tool call (%d)", len(toolSpans), want)
	}

	seen := map[string]bool{}
	for _, span := range toolSpans {
		var name, verdict string
		for _, attr := range span.Attributes() {
			switch string(attr.Key) {
			case mcpserver.AttrTool:
				name = attr.Value.AsString()
			case mcpserver.AttrGateVerdict:
				verdict = attr.Value.AsString()
			}
		}
		if name == "" {
			t.Errorf("a tool span carries no tool name")
		}
		if verdict == "" {
			t.Errorf("the span for %s carries no gate verdict, so a refused call and an allowed one look alike in the trace", name)
		}
		seen[name] = true
	}
	for _, c := range calls {
		if !seen[c.tool] {
			t.Errorf("no span was recorded for %s", c.tool)
		}
	}
}

func TestKillSwitchDeniesAllToolsImmediately(t *testing.T) {
	r := newRig(t, rigOptions{killSwitch: true})
	item := r.failedItem()

	r.engine.forbid(true)

	for _, tool := range r.registeredTools() {
		res := r.call(tool, argumentsFor(t, tool, item))
		body := text(t, res)
		if !strings.Contains(body, policy.RuleKillSwitch) {
			t.Errorf("%s did not refuse with %s: %s", tool, policy.RuleKillSwitch, body)
		}
	}

	for _, row := range r.rowsOfKind(mcpserver.KindToolCall) {
		if row.Detail[mcpserver.DetailGateRule] != policy.RuleKillSwitch {
			t.Errorf("a tool_call row under the kill switch carries rule %q, want %s",
				row.Detail[mcpserver.DetailGateRule], policy.RuleKillSwitch)
		}
	}
}

func TestActionToolsRefuseUntilDecisionRecorded(t *testing.T) {
	r := newRig(t, rigOptions{})
	item := r.failedItem()

	r.engine.forbid(true)
	for _, tool := range mcpserver.ActionTools() {
		res := r.call(tool, argumentsFor(t, tool, item))
		out := decode[mcpserver.ActionOutput](t, res)
		if out.Allowed {
			t.Errorf("%s acted on an item with no decision recorded", tool)
		}
		if out.PolicyRule != mcpserver.RuleDecisionFirst {
			t.Errorf("%s refused with rule %q, want %s", tool, out.PolicyRule, mcpserver.RuleDecisionFirst)
		}
	}
	if got := r.engine.count(); got != 0 {
		t.Errorf("%d action(s) happened before any decision was recorded", got)
	}

	// The same call, once a decision exists.
	r.engine.forbid(false)
	r.recordDecision(item, riskitem.ActionNotifyEmail)
	res := r.call(mcpserver.ToolNotifyItem, map[string]any{"item_id": item, "medium": "email"})
	out := decode[mcpserver.ActionOutput](t, res)
	if !out.Allowed {
		t.Errorf("notify_item was refused after a decision was recorded: %+v", out)
	}
	if r.engine.count() != 1 {
		t.Errorf("got %d applications after one allowed notification, want 1", r.engine.count())
	}
}

func TestItemAllowlistDeniesAnItemOutsideTheBatch(t *testing.T) {
	r := newRig(t, rigOptions{})
	const outside = "ri_notinthisbatch"

	r.engine.forbid(true)

	for _, tool := range mcpserver.ActionTools() {
		res := r.call(tool, argumentsFor(t, tool, outside))
		out := decode[mcpserver.ActionOutput](t, res)
		if out.Allowed {
			t.Errorf("%s acted on an item outside the batch", tool)
		}
		if out.PolicyRule != mcpserver.RuleItemAllowlist {
			t.Errorf("%s refused an outside item with rule %q, want %s",
				tool, out.PolicyRule, mcpserver.RuleItemAllowlist)
		}
	}
	if got := r.engine.count(); got != 0 {
		t.Errorf("%d action(s) reached an item outside the batch", got)
	}
}

func TestActionBudgetDeniesPastTheInvocationCap(t *testing.T) {
	const budget = 2
	r := newRig(t, rigOptions{actionBudget: budget})
	item := r.failedItem()
	r.recordDecision(item, riskitem.ActionEscalate)

	allowed := 0
	for range budget + 2 {
		res := r.call(mcpserver.ToolEscalateItem, map[string]any{
			"item_id": item,
			"reason":  "spending the budget",
		})
		if decode[mcpserver.ActionOutput](t, res).Allowed {
			allowed++
		}
	}
	if allowed != budget {
		t.Errorf("%d action tool calls were allowed against a budget of %d", allowed, budget)
	}

	res := r.call(mcpserver.ToolNotifyItem, map[string]any{"item_id": item, "medium": "email"})
	out := decode[mcpserver.ActionOutput](t, res)
	if out.Allowed {
		t.Errorf("a notification was allowed past the invocation budget")
	}
	if out.PolicyRule != policy.RuleActionBudget {
		t.Errorf("the budget refusal carries rule %q, want %s", out.PolicyRule, policy.RuleActionBudget)
	}
}

func TestUnknownToolNameIsRefusedByTheAllowlist(t *testing.T) {
	r := newRig(t, rigOptions{})

	// The SDK answers an unregistered name itself, so this asserts the
	// allowlist over the set that is registered: every registered name passes
	// the allowlist, and the allowlist is the eight.
	for _, tool := range r.registeredTools() {
		if !slices.Contains(mcpserver.ToolNames(), tool) {
			t.Errorf("tool %q is registered and is not on the allowlist", tool)
		}
	}

	_, err := r.session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "transfer_funds",
		Arguments: map[string]any{},
	})
	if err == nil {
		t.Errorf("a call to an unregistered tool succeeded")
	}

	for _, row := range r.rowsOfKind(mcpserver.KindToolCall) {
		if row.Detail[mcpserver.DetailTool] == "transfer_funds" &&
			row.Detail[mcpserver.DetailGateRule] != mcpserver.RuleToolAllowlist {
			t.Errorf("an unregistered tool reached the ledger without the allowlist rule: %+v", row)
		}
	}
}

func TestEveryToolCallStampsTheAuditTrailWithItsTraceID(t *testing.T) {
	r := newRig(t, rigOptions{})
	item := r.failedItem()
	r.recordDecision(item, riskitem.ActionNotifyEmail)
	r.call(mcpserver.ToolNotifyItem, map[string]any{"item_id": item, "medium": "email"})

	rows := r.ledgerRows()
	if len(rows) == 0 {
		t.Fatalf("no ledger rows were written")
	}
	traceIDs := map[string]bool{}
	for _, row := range rows {
		if row.TraceID == "" {
			t.Errorf("a %s row for %s carries no trace id", row.Kind, row.OrderID)
			continue
		}
		traceIDs[row.TraceID] = true
	}

	spanTraces := map[string]bool{}
	for _, span := range r.spans.Ended() {
		spanTraces[span.SpanContext().TraceID().String()] = true
	}
	for id := range traceIDs {
		if !spanTraces[id] {
			t.Errorf("ledger trace id %s matches no recorded span, so a reviewer cannot go from the row to the trace", id)
		}
	}
}

// TestNoToolResponseCarriesACustomerAddress is what replaced the phase 3
// ground-truth leak sweep, and it is the same shape of test about a different
// secret.
//
// The queue holds a customer's email address and phone number because an
// intervention engine needs them. The model never does: it chooses a medium,
// and a channel it cannot see is a channel it cannot exfiltrate, mistype into
// a note, or put in a reasoning string that ends up in the ledger. So the
// bytes every tool returns are searched for both values, for every item.
func TestNoToolResponseCarriesACustomerAddress(t *testing.T) {
	r := newRig(t, rigOptions{actionBudget: 500})

	var secrets []string
	for _, item := range r.items {
		if item.Customer.Email != "" {
			secrets = append(secrets, item.Customer.Email)
		}
		if item.Customer.Contact != "" {
			secrets = append(secrets, item.Customer.Contact)
		}
		if item.Customer.Name != "" {
			secrets = append(secrets, item.Customer.Name)
		}
	}
	if len(secrets) == 0 {
		t.Fatalf("no item in the rig carries contact detail, so this test proves nothing")
	}

	for _, item := range r.items {
		r.recordDecision(item.ID, riskitem.ActionNotifyEmail)
		for _, tool := range r.registeredTools() {
			body := text(t, r.call(tool, argumentsFor(t, tool, item.ID)))
			for _, secret := range secrets {
				if strings.Contains(body, secret) {
					t.Errorf("%s put %q on the wire for item %s: %s", tool, secret, item.ID, body)
				}
			}
		}
	}

	// And the ledger, which a reviewer reads and which is not a place for a
	// customer's address either.
	for _, row := range r.ledgerRows() {
		for _, value := range row.Detail {
			for _, secret := range secrets {
				if strings.Contains(value, secret) {
					t.Errorf("a %s row carries %q in its detail", row.Kind, secret)
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Layer (b): the tools
// ---------------------------------------------------------------------------

func TestServerServesExactlyTheEightNamedTools(t *testing.T) {
	r := newRig(t, rigOptions{})

	want := append([]string(nil), mcpserver.ToolNames()...)
	slices.Sort(want)
	got := r.registeredTools()

	if !slices.Equal(got, want) {
		t.Errorf("registered tools\n got %v\nwant %v", got, want)
	}
	if len(mcpserver.ReadTools())+len(mcpserver.ActionTools())+1 != len(want) {
		t.Errorf("the read tools, the action tools, and record_decision do not add up to the surface")
	}
}

// TestThereIsNoRetryOfAnyKind is the one test in this file that asserts an
// absence.
//
// Re-presenting a one-off Indian payment without the customer is not lawful on
// any rail (docs/INDIA-CONSTRAINTS-AUDIT.md), so the engine deleted the concept
// rather than gating it. This checks the three places a retry could come back:
// the tool surface, the decision vocabulary, and the action set the decision
// vocabulary is drawn from.
func TestThereIsNoRetryOfAnyKind(t *testing.T) {
	r := newRig(t, rigOptions{})
	item := r.failedItem()

	for _, tool := range r.registeredTools() {
		if strings.Contains(tool, "retry") {
			t.Errorf("tool %q is registered", tool)
		}
	}
	for _, action := range mcpserver.DecisionActions() {
		if strings.Contains(action, "retry") {
			t.Errorf("record_decision accepts %q", action)
		}
	}

	// And a model that asks for one by name gets the tool allowlist rather
	// than a handler.
	if _, err := r.session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "retry_payment",
		Arguments: map[string]any{"item_id": item},
	}); err == nil {
		t.Errorf("a call to retry_payment succeeded")
	}

	// Naming it as a decision is a tool-level error, not an accepted row.
	res := r.recordDecision(item, "retry_same_instrument")
	if !res.IsError {
		t.Errorf("record_decision accepted a retry: %s", text(t, res))
	}
}

func TestRecordDecisionRequiresItemActionAndReasoning(t *testing.T) {
	r := newRig(t, rigOptions{})
	item := r.failedItem()

	cases := []struct {
		name string
		args map[string]any
	}{
		{"no item", map[string]any{"item_id": "", "action": riskitem.ActionNotifyEmail, "reasoning": "because"}},
		{"unknown action", map[string]any{"item_id": item, "action": "wire_the_money_somewhere", "reasoning": "because"}},
		{"no reasoning", map[string]any{"item_id": item, "action": riskitem.ActionNotifyEmail, "reasoning": "   "}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := r.call(mcpserver.ToolRecordDecision, c.args)
			if !res.IsError {
				t.Errorf("a decision with %s was accepted: %s", c.name, text(t, res))
			}
		})
	}

	// The two lawful actions no tool executes are still decisions a model can
	// state, and stating one is the whole of doing it.
	for _, action := range []string{riskitem.ActionCancelWriteOff, riskitem.ActionDoNothing} {
		res := r.recordDecision(item, action)
		if res.IsError {
			t.Errorf("record_decision refused %s, which is in the lawful set: %s", action, text(t, res))
		}
	}

	res := r.recordDecision(item, riskitem.ActionNotifyEmail)
	if res.IsError {
		t.Errorf("a complete decision was refused: %s", text(t, res))
	}
	out := decode[mcpserver.RecordDecisionOutput](t, res)
	if !out.Recorded {
		t.Errorf("a complete decision came back not recorded: %+v", out)
	}
}

func TestRecordDecisionWritesReasoningToTheAuditTrail(t *testing.T) {
	r := newRig(t, rigOptions{})
	item := r.failedItem()
	const reasoning = "the invoice is ten days overdue and the customer has an email on file, so one message is the smallest thing that can work"

	r.call(mcpserver.ToolRecordDecision, map[string]any{
		"item_id":   item,
		"action":    riskitem.ActionNotifyEmail,
		"reasoning": reasoning,
	})

	rows := r.rowsOfKind(mcpserver.KindDecisionRecorded)
	if len(rows) != 1 {
		t.Fatalf("got %d decision_recorded rows, want 1", len(rows))
	}
	row := rows[0]
	if row.OrderID != item {
		t.Errorf("the decision row names item %q, want %q", row.OrderID, item)
	}
	if row.Detail[mcpserver.DetailReasoning] != reasoning {
		t.Errorf("the decision row carries reasoning %q, want %q",
			row.Detail[mcpserver.DetailReasoning], reasoning)
	}
	if row.Detail[mcpserver.DetailChosenAction] != riskitem.ActionNotifyEmail {
		t.Errorf("the decision row carries action %q, want %q",
			row.Detail[mcpserver.DetailChosenAction], riskitem.ActionNotifyEmail)
	}
	if row.Detail[mcpserver.DetailSource] != string(riskitem.SourceFailedPayment) {
		t.Errorf("the decision row carries source %q, want %q",
			row.Detail[mcpserver.DetailSource], riskitem.SourceFailedPayment)
	}
	if row.TraceID == "" {
		t.Errorf("the decision row carries no trace id, so it cannot be joined to a span")
	}
}

func TestReadToolsNeedNoDecisionAndReachNoSideEffect(t *testing.T) {
	r := newRig(t, rigOptions{})
	r.engine.forbid(true)

	listed := r.call(mcpserver.ToolListRiskItems, map[string]any{})
	if listed.IsError {
		t.Errorf("list_risk_items was refused with no decision recorded: %s", text(t, listed))
	}
	list := decode[mcpserver.ListRiskItemsOutput](t, listed)
	if len(list.Items) != len(r.items) {
		t.Fatalf("got %d items, want %d", len(list.Items), len(r.items))
	}

	// The summary is the thing a model triages on, so every field it triages
	// on has to be there and be right.
	byID := map[string]mcpserver.RiskItemSummary{}
	for _, summary := range list.Items {
		byID[summary.ItemID] = summary
	}
	invoice := byID[r.invoiceItem()]
	if invoice.Source != string(riskitem.SourceOverdueInvoice) {
		t.Errorf("the invoice item lists source %q", invoice.Source)
	}
	if invoice.AmountDuePaise != 800000 || invoice.AmountPaise != 900000 {
		t.Errorf("the invoice item lists %d due of %d", invoice.AmountDuePaise, invoice.AmountPaise)
	}
	if invoice.AgingDays != 10 {
		t.Errorf("the invoice item has aged %d days, want 10", invoice.AgingDays)
	}
	if !invoice.HasContact {
		t.Errorf("the invoice item reports no contact channel and it has two")
	}
	if invoice.HandleKind != riskitem.HandleKindInvoice {
		t.Errorf("the invoice item lists handle kind %q, want %q", invoice.HandleKind, riskitem.HandleKindInvoice)
	}
	if unreachable := byID[r.noContactItem()]; unreachable.HasContact {
		t.Errorf("the abandoned order reports a contact channel and it has none")
	}

	detailed := r.call(mcpserver.ToolGetRiskItem, map[string]any{"item_id": r.failedItem()})
	if detailed.IsError {
		t.Errorf("get_risk_item was refused with no decision recorded: %s", text(t, detailed))
	}
	detail := decode[mcpserver.RiskItemDetail](t, detailed)
	if detail.ItemID != r.failedItem() {
		t.Errorf("get_risk_item returned item %q, want %q", detail.ItemID, r.failedItem())
	}
	if detail.Signal.FailureReason != "payment_timed_out" {
		t.Errorf("get_risk_item returned failure reason %q", detail.Signal.FailureReason)
	}
	if !slices.Equal(detail.Channels, []string{"email"}) {
		t.Errorf("the failed payment lists channels %v, want [email]", detail.Channels)
	}

	if got := r.engine.count(); got != 0 {
		t.Errorf("the read tools reached the intervention engine %d time(s)", got)
	}
}

func TestNotifyItemAsksForTheMediumItWasGiven(t *testing.T) {
	r := newRig(t, rigOptions{actionBudget: 10})
	item := r.invoiceItem()
	r.recordDecision(item, riskitem.ActionNotifySMS)

	cases := []struct {
		medium string
		want   string
	}{
		{"email", riskitem.ActionNotifyEmail},
		{"sms", riskitem.ActionNotifySMS},
		{"SMS", riskitem.ActionNotifySMS},
		{"", riskitem.ActionNotifyEmail},
	}
	for _, c := range cases {
		args := map[string]any{"item_id": item}
		if c.medium != "" {
			args["medium"] = c.medium
		}
		out := decode[mcpserver.ActionOutput](t, r.call(mcpserver.ToolNotifyItem, args))
		if out.Action != c.want {
			t.Errorf("medium %q asked for action %q, want %q", c.medium, out.Action, c.want)
		}
	}

	res := r.call(mcpserver.ToolNotifyItem, map[string]any{"item_id": item, "medium": "carrier_pigeon"})
	if !res.IsError {
		t.Errorf("an unknown medium was accepted: %s", text(t, res))
	}

	applied := r.engine.applied()
	if len(applied) != len(cases) {
		t.Fatalf("the engine saw %d applications, want %d", len(applied), len(cases))
	}
	for i, c := range cases {
		if applied[i].action != c.want {
			t.Errorf("application %d was %q, want %q", i, applied[i].action, c.want)
		}
	}
}

func TestCreatePaymentLinkPutsTheHandleOnTheItem(t *testing.T) {
	r := newRig(t, rigOptions{actionBudget: 10})
	item := r.failedItem()
	r.recordDecision(item, riskitem.ActionCreatePaymentLink)

	before := decode[mcpserver.RiskItemDetail](t, r.call(mcpserver.ToolGetRiskItem, map[string]any{"item_id": item}))
	if before.HandleKind != riskitem.HandleKindNone {
		t.Fatalf("the failed payment already carries a handle, so this test proves nothing")
	}

	out := decode[mcpserver.ActionOutput](t, r.call(mcpserver.ToolCreatePaymentLink, map[string]any{"item_id": item}))
	if !out.Allowed || !out.Accepted {
		t.Fatalf("create_payment_link_for_item did not go through: %+v", out)
	}
	if out.HandleID == "" || out.HandleURL == "" {
		t.Fatalf("the link came back with no id or url: %+v", out)
	}

	// The item now carries what the engine minted, which is what makes the
	// resend reachable at all.
	after := decode[mcpserver.RiskItemDetail](t, r.call(mcpserver.ToolGetRiskItem, map[string]any{"item_id": item}))
	if after.HandleKind != riskitem.HandleKindPaymentLink || after.HandleID != out.HandleID {
		t.Errorf("the item carries handle %+v after the link was raised", after)
	}

	resend := decode[mcpserver.ActionOutput](t, r.call(mcpserver.ToolResendLink, map[string]any{"item_id": item}))
	if !resend.Allowed || resend.Action != riskitem.ActionResendLink {
		t.Errorf("resend_link_for_item did not go through on an item that now has a link: %+v", resend)
	}
}

func TestLogPromiseRecordsItsTermsAndReachesNoGateway(t *testing.T) {
	r := newRig(t, rigOptions{})
	item := r.invoiceItem()
	r.recordDecision(item, riskitem.ActionLogPromise)

	const note = "their finance team pays on the twelfth of every month"
	out := decode[mcpserver.ActionOutput](t, r.call(mcpserver.ToolLogPromise, map[string]any{
		"item_id":     item,
		"promised_at": "2026-09-12",
		"days_hold":   7,
		"note":        note,
	}))
	if !out.Allowed || !out.Accepted {
		t.Fatalf("log_promise did not go through: %+v", out)
	}

	rows := r.rowsOfKind(audit.KindActionTaken)
	if len(rows) != 1 {
		t.Fatalf("got %d action rows for one promise, want 1", len(rows))
	}
	row := rows[0]
	if row.Detail[mcpserver.DetailSideEffect] != "false" {
		t.Errorf("a promise was recorded as a gateway side effect")
	}
	if row.Detail[mcpserver.DetailDaysHold] != "7" {
		t.Errorf("the promise row carries days_hold %q, want 7", row.Detail[mcpserver.DetailDaysHold])
	}
	if row.Detail[mcpserver.DetailPromiseNote] != note {
		t.Errorf("the promise row carries note %q, want %q", row.Detail[mcpserver.DetailPromiseNote], note)
	}
	if !strings.HasPrefix(row.Detail[mcpserver.DetailPromisedAt], "2026-09-12") {
		t.Errorf("the promise row carries promised_at %q", row.Detail[mcpserver.DetailPromisedAt])
	}

	// The terms are the row. A promise with no date, no note, or a hold that
	// runs backwards is not one.
	for _, bad := range []map[string]any{
		{"item_id": item, "promised_at": "", "note": note},
		{"item_id": item, "promised_at": "next tuesday", "note": note},
		{"item_id": item, "promised_at": "2026-09-12", "note": "  "},
		{"item_id": item, "promised_at": "2026-09-12", "days_hold": -3, "note": note},
	} {
		if res := r.call(mcpserver.ToolLogPromise, bad); !res.IsError {
			t.Errorf("log_promise accepted %v: %s", bad, text(t, res))
		}
	}
}

func TestEscalateItemIsRecordedAsAnEscalationNotAFailure(t *testing.T) {
	r := newRig(t, rigOptions{})
	item := r.noContactItem()
	r.recordDecision(item, riskitem.ActionEscalate)

	const reason = "there is no way to reach this customer and the order was never attempted"
	out := decode[mcpserver.ActionOutput](t, r.call(mcpserver.ToolEscalateItem, map[string]any{
		"item_id": item,
		"reason":  reason,
	}))
	if !out.Allowed {
		t.Fatalf("escalate_item was refused: %+v", out)
	}

	tally := r.server.Tally(item)
	if !tally.Escalated {
		t.Errorf("the tally does not record the escalation: %+v", tally)
	}
	if tally.SideEffect {
		t.Errorf("an escalation was recorded as a gateway side effect: %+v", tally)
	}
	if tally.ActionKind != riskitem.ActionEscalate {
		t.Errorf("the tally's action kind is %q, want %q", tally.ActionKind, riskitem.ActionEscalate)
	}

	rows := r.rowsOfKind(audit.KindActionTaken)
	if len(rows) != 1 {
		t.Fatalf("got %d action rows for one escalation, want 1", len(rows))
	}
	if rows[0].Detail[mcpserver.DetailEscalationReason] != reason {
		t.Errorf("the escalation row carries reason %q, want %q",
			rows[0].Detail[mcpserver.DetailEscalationReason], reason)
	}

	if res := r.call(mcpserver.ToolEscalateItem, map[string]any{"item_id": item, "reason": "  "}); !res.IsError {
		t.Errorf("an escalation with no reason was accepted: %s", text(t, res))
	}
}

// TestAnEngineRefusalIsAnAllowedCallThatDidNothing pins the gap between the
// two layers and the third thing, which is the engine's own judgment.
//
// The frozen contract says an Intervention refuses rather than guesses when it
// is asked to notify an item with no contact channel. The policy has nothing
// to say about that and should not: it is not a rule, it is the absence of an
// address. So the call is allowed, nothing is sent, and the row says both.
func TestAnEngineRefusalIsAnAllowedCallThatDidNothing(t *testing.T) {
	r := newRig(t, rigOptions{})
	item := r.noContactItem()
	r.recordDecision(item, riskitem.ActionNotifyEmail)

	r.engine.setOutcome(func(_ riskitem.RiskItem, action string) (riskitem.Outcome, error) {
		return riskitem.Outcome{
			Action:   action,
			Accepted: false,
			Err:      "the item has no contact channel, so there is nowhere to send anything",
			At:       runInstant,
		}, nil
	})

	out := decode[mcpserver.ActionOutput](t, r.call(mcpserver.ToolNotifyItem, map[string]any{
		"item_id": item,
		"medium":  "email",
	}))
	if !out.Allowed {
		t.Errorf("the gate refused a call the policy allowed: %+v", out)
	}
	if out.Accepted {
		t.Errorf("a refused notification came back accepted: %+v", out)
	}
	if out.Error == "" {
		t.Errorf("a refused notification came back with no reason: %+v", out)
	}

	rows := r.rowsOfKind(audit.KindActionSkipped)
	if len(rows) != 1 {
		t.Fatalf("got %d action_skipped rows, want 1", len(rows))
	}
	if rows[0].Detail[mcpserver.DetailSideEffect] != "false" {
		t.Errorf("a refused notification was recorded as a side effect")
	}
	if rows[0].Detail[mcpserver.DetailAccepted] != "false" {
		t.Errorf("a refused notification was recorded as accepted")
	}

	tally := r.server.Tally(item)
	if tally.ActionKind != riskitem.ActionDoNothing {
		t.Errorf("the tally names action %q for an item nothing happened to, want %q",
			tally.ActionKind, riskitem.ActionDoNothing)
	}
}

// TestTheActionRowCarriesTheDecisionThatWasOnTheRecord pins what M3 does and
// what it does not do.
//
// It requires a decision to exist, not to name the action about to run, and
// the two-step link flow is why: a decision of notify_email is carried out by
// raising a link and then sending it, and a gate that demanded equality would
// refuse the first call and leave the decision unreachable. What stops that
// from becoming a hole a compliance reviewer cannot see is the row: the action
// that ran and the decision that was on the record are both on it, so an agent
// that decides one thing and does another says so in the ledger.
func TestTheActionRowCarriesTheDecisionThatWasOnTheRecord(t *testing.T) {
	r := newRig(t, rigOptions{actionBudget: 10})
	item := r.failedItem()
	r.recordDecision(item, riskitem.ActionNotifyEmail)

	// The link the decided notification needs. It is not the decided action,
	// and it is allowed.
	out := decode[mcpserver.ActionOutput](t, r.call(mcpserver.ToolCreatePaymentLink, map[string]any{"item_id": item}))
	if !out.Allowed {
		t.Fatalf("the step a decided notification needs was refused: %+v", out)
	}

	rows := r.rowsOfKind(audit.KindActionTaken)
	if len(rows) != 1 {
		t.Fatalf("got %d action rows, want 1", len(rows))
	}
	row := rows[0]
	if row.ProposedAction != riskitem.ActionCreatePaymentLink {
		t.Errorf("the row names action %q, want the action that ran", row.ProposedAction)
	}
	if row.Detail[mcpserver.DetailChosenAction] != riskitem.ActionNotifyEmail {
		t.Errorf("the row carries decision %q, want the one on the record (%q)",
			row.Detail[mcpserver.DetailChosenAction], riskitem.ActionNotifyEmail)
	}
}

// TestThePolicySeesTheItemAndNotTheTool checks what crosses the evaluator
// seam. The policy is handed the item id, an action, an amount, and a class,
// and nothing about which tool asked.
func TestThePolicySeesTheItemAndNotTheTool(t *testing.T) {
	r := newRig(t, rigOptions{})
	item := r.invoiceItem()
	r.recordDecision(item, riskitem.ActionNotifyEmail)
	r.call(mcpserver.ToolNotifyItem, map[string]any{"item_id": item, "medium": "email"})

	reqs := r.evaluator.requests()
	if len(reqs) != 1 {
		t.Fatalf("the policy was consulted %d times for one action, want 1", len(reqs))
	}
	req := reqs[0]
	if req.RiskItemID != item {
		t.Errorf("the policy was asked about %q, want the risk item id %q", req.RiskItemID, item)
	}
	if req.Source != string(riskitem.SourceOverdueInvoice) {
		t.Errorf("the policy was told source %q", req.Source)
	}
	// The action crossing the seam is the frozen one, not a tool name and not
	// a translation of one.
	if req.Action != riskitem.ActionNotifyEmail {
		t.Errorf("the policy was asked about action %q, want %q", req.Action, riskitem.ActionNotifyEmail)
	}
	// Both amounts cross, because the rule that weighs them wants what is
	// still due and a reviewer wants to see the whole debt next to it.
	if req.AmountDuePaise != 800000 || req.AmountPaise != 900000 {
		t.Errorf("the policy was handed %d due of %d", req.AmountDuePaise, req.AmountPaise)
	}
	if !req.HasEmail || !req.HasContact {
		t.Errorf("the policy was told this item has channels email=%v sms=%v", req.HasEmail, req.HasContact)
	}
	if req.TouchNo != 1 {
		t.Errorf("the first contact on an item was touch %d", req.TouchNo)
	}
	if req.AtRiskSince.IsZero() {
		t.Errorf("the policy was handed no aging, so the grace-period rule has nothing to read")
	}
}

// TestTheTouchCountRisesWithEveryContact is the input R1 caps.
//
// It is asserted here because the count does not come from the store: see the
// note in act. A contact that the policy refused, and one the intervention
// engine declined, both cost nothing, because neither reached a customer.
func TestTheTouchCountRisesWithEveryContact(t *testing.T) {
	r := newRig(t, rigOptions{actionBudget: 10})
	item := r.invoiceItem()
	r.recordDecision(item, riskitem.ActionNotifyEmail)

	for range 3 {
		r.call(mcpserver.ToolNotifyItem, map[string]any{"item_id": item, "medium": "email"})
	}
	// A promise reaches nobody, so it is not a touch.
	r.call(mcpserver.ToolLogPromise, argumentsFor(t, mcpserver.ToolLogPromise, item))

	// And one the engine declines is not one either.
	r.engine.setOutcome(func(_ riskitem.RiskItem, action string) (riskitem.Outcome, error) {
		return riskitem.Outcome{Action: action, Accepted: false, Err: "declined", At: runInstant}, nil
	})
	r.call(mcpserver.ToolNotifyItem, map[string]any{"item_id": item, "medium": "email"})

	var touches []int
	for _, req := range r.evaluator.requests() {
		touches = append(touches, req.TouchNo)
	}
	if want := []int{1, 2, 3, 4, 4}; !slices.Equal(touches, want) {
		t.Errorf("the policy saw touch numbers %v, want %v", touches, want)
	}
}

// TestTheFactsProviderReachesThePolicy pins the third seam: what the item does
// not carry still gets to the rules that read it.
func TestTheFactsProviderReachesThePolicy(t *testing.T) {
	hold := runInstant.Add(48 * time.Hour)
	r := newRig(t, rigOptions{facts: &stubFacts{facts: policy.Facts{
		PromiseHoldUntil: hold,
		Disputed:         true,
		SourceStatus:     "cancelled",
	}}})
	item := r.invoiceItem()
	r.recordDecision(item, riskitem.ActionNotifyEmail)
	r.call(mcpserver.ToolNotifyItem, map[string]any{"item_id": item, "medium": "email"})

	reqs := r.evaluator.requests()
	if len(reqs) != 1 {
		t.Fatalf("the policy was consulted %d times, want 1", len(reqs))
	}
	req := reqs[0]
	if !req.PromiseHoldUntil.Equal(hold) {
		t.Errorf("the policy was handed hold %v, want %v", req.PromiseHoldUntil, hold)
	}
	if !req.Disputed {
		t.Errorf("the policy was not told the item is disputed")
	}
	if req.SourceStatus != "cancelled" {
		t.Errorf("the policy was handed source status %q", req.SourceStatus)
	}
	// The provider said nothing about the touch number, so the server filled
	// it in rather than handing the policy a zero.
	if req.TouchNo != 1 {
		t.Errorf("the policy was handed touch %d, want 1", req.TouchNo)
	}
}

// ---------------------------------------------------------------------------
// Layer (c): the parallel-call race
// ---------------------------------------------------------------------------

func TestConcurrentActionToolCallsCannotBothSpendTheNotifyWindow(t *testing.T) {
	// The race internal/store's doc comment describes, reachable for the first
	// time on the MCP surface: a client can issue tool calls in parallel, and
	// snapshot, evaluate, and commit are three separate lock acquisitions.
	//
	// The policy here is the shape of R6 and nothing else: one notification per
	// item per window, read off the LastNotifyAt the store handed it. Eight
	// callers ask at once. Without the lock in Server.act they all snapshot a
	// zero LastNotifyAt, all pass, and all eight reach the intervention engine.
	r := newRig(t, rigOptions{actionBudget: 100})
	item := r.invoiceItem()
	r.recordDecision(item, riskitem.ActionNotifyEmail)

	r.evaluator.set(func(state policy.State, req policy.Request) policy.Decision {
		d := policy.Decision{IdempotencyKey: policy.IdempotencyKey(req.OrderID, req.Action, req.AttemptNo)}
		if !state.LastNotifyAt.IsZero() {
			d.Verdict, d.RuleID = policy.VerdictDeny, policy.RuleNotifyRate
			d.Reason = "this item has already been notified inside the window"
			return d
		}
		d.Verdict, d.RuleID = policy.VerdictAllow, policy.RuleAllow
		d.Reason = "no notification has gone out on this item yet"
		return d
	})

	// A delay inside the engine widens the window between the snapshot and the
	// commit, so the callers actually overlap inside it rather than merely
	// being launched together. Without it this test is a probabilistic
	// detector, and a test that usually passes against the bug it exists for is
	// a test nobody can act on.
	r.engine.setDelay(25 * time.Millisecond)

	const callers = 8
	start := make(chan struct{})
	var ready, done sync.WaitGroup
	ready.Add(callers)
	done.Add(callers)
	for range callers {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			// Not r.call: that fatals from a non-test goroutine, which is not
			// allowed. A transport error is picked up by the assertions below
			// instead.
			_, _ = r.session.CallTool(t.Context(), &mcp.CallToolParams{
				Name:      mcpserver.ToolNotifyItem,
				Arguments: map[string]any{"item_id": item, "medium": "email"},
			})
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()

	if got := r.engine.count(); got != 1 {
		t.Errorf("%d notifications reached the intervention engine from %d concurrent callers, want 1",
			got, callers)
	}
	if got := r.evaluator.count(); got != callers {
		t.Errorf("the policy was consulted %d times for %d calls, want one evaluation each", got, callers)
	}

	// And the seven that were refused are on the record as refusals, which is
	// what makes the containment count computable rather than inferred.
	refused := 0
	for _, row := range r.rowsOfKind(audit.KindActionSkipped) {
		if row.PolicyRule == policy.RuleNotifyRate {
			refused++
		}
	}
	if refused != callers-1 {
		t.Errorf("%d refusals are on the record, want %d", refused, callers-1)
	}
}

// TestTheActionPathHoldsOneLockAcrossTheWholeDecision is the same property in
// the small: the lock is not per store call, it spans the sequence.
//
// It is here because the barrier test above can only ever observe the outcome
// of the race, and a future refactor that split the lock would keep that test
// green whenever the timing was kind. This one fails on the structure: while
// one action is inside the engine, a second caller cannot have reached its own
// evaluation.
func TestTheActionPathHoldsOneLockAcrossTheWholeDecision(t *testing.T) {
	r := newRig(t, rigOptions{actionBudget: 10})
	item := r.invoiceItem()
	r.recordDecision(item, riskitem.ActionNotifyEmail)

	var mu sync.Mutex
	var inside bool
	var overlaps int
	r.engine.setOutcome(func(it riskitem.RiskItem, action string) (riskitem.Outcome, error) {
		mu.Lock()
		if inside {
			overlaps++
		}
		inside = true
		evaluations := r.evaluator.count()
		mu.Unlock()

		time.Sleep(10 * time.Millisecond)

		mu.Lock()
		if r.evaluator.count() != evaluations {
			overlaps++
		}
		inside = false
		mu.Unlock()
		return defaultOutcome(it, action)
	})

	const callers = 4
	var done sync.WaitGroup
	done.Add(callers)
	for range callers {
		go func() {
			defer done.Done()
			_, _ = r.session.CallTool(t.Context(), &mcp.CallToolParams{
				Name:      mcpserver.ToolLogPromise,
				Arguments: argumentsFor(t, mcpserver.ToolLogPromise, item),
			})
		}()
	}
	done.Wait()

	if overlaps != 0 {
		t.Errorf("%d action(s) evaluated the policy while another action was still running", overlaps)
	}
}
