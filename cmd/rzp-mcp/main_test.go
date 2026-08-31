package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/batch"
	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/mcpserver"
	"github.com/lopster568/rzp-recovery-agent/internal/recovery"
	"github.com/lopster568/rzp-recovery-agent/internal/runner"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serverBinary is the compiled rzp-mcp, built once by TestMain.
//
// The tests below drive that binary as a subprocess over stdin and stdout
// rather than a server built inside the test process. The pattern is
// ~/loadline/interposer/interposer_test.go: what ships is the artefact worth
// testing, and a stdio server that works in-process and not as a process is a
// server that does not work.
var serverBinary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "rzp-mcp-test-")
	if err != nil {
		panic("make the test temp dir: " + err.Error())
	}
	serverBinary = filepath.Join(dir, "rzp-mcp")

	build := exec.Command("go", "build", "-o", serverBinary, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		_ = os.RemoveAll(dir)
		panic("build cmd/rzp-mcp: " + err.Error())
	}

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// subprocessRig is one batch on disk plus a live session against the compiled
// binary serving one of its orders.
type subprocessRig struct {
	t        *testing.T
	session  *mcp.ClientSession
	runDir   string
	armDir   string
	batch    runner.BatchFile
	orderID  string
	manifest batch.Order
}

// seedBatchFile writes a small fake-layer batch and returns its path.
func seedBatchFile(t *testing.T) (string, runner.BatchFile) {
	t.Helper()
	manifest, err := batch.Generate(batch.Spec{
		Seed: 11,
		Distribution: map[classify.Class]int{
			classify.TransientRetryEligible: 2,
			classify.ReauthRequired:         1,
		},
		BaitOrders: 1,
	})
	if err != nil {
		t.Fatalf("generate the batch: %v", err)
	}
	file := runner.BatchFile{
		BatchID: "b-test-11",
		Seed:    11,
		Layer:   runner.LayerFake,
		Orders:  manifest.Orders,
	}
	path := filepath.Join(t.TempDir(), file.BatchID+".json")
	encoded, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		t.Fatalf("encode the batch: %v", err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		t.Fatalf("write the batch: %v", err)
	}
	return path, file
}

// startServer runs the compiled binary for one manifest order and connects to
// it. It returns when the session is live.
func startServer(t *testing.T, pick func(batch.Order) bool, extraArgs ...string) *subprocessRig {
	t.Helper()

	batchPath, file := seedBatchFile(t)
	var chosen batch.Order
	found := false
	for _, o := range file.Orders {
		if pick(o) {
			chosen, found = o, true
			break
		}
	}
	if !found {
		t.Fatalf("the batch has no order matching the predicate")
	}

	runDir := filepath.Join(t.TempDir(), "run")
	armDir := filepath.Join(runDir, ArmAgent)

	args := append([]string{
		"-batch", batchPath,
		"-order", chosen.OrderID,
		"-layer", runner.LayerFake,
		"-run-dir", runDir,
		"-arm", ArmAgent,
	}, extraArgs...)

	cmd := exec.Command(serverBinary, args...)
	cmd.Stderr = os.Stderr

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)

	client := mcp.NewClient(&mcp.Implementation{Name: "subprocess-test", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect to the compiled server: %v", err)
	}

	r := &subprocessRig{
		t:        t,
		session:  session,
		runDir:   runDir,
		armDir:   armDir,
		batch:    file,
		manifest: chosen,
	}
	t.Cleanup(func() {
		if r.session != nil {
			_ = r.session.Close()
		}
	})
	return r
}

// gatewayOrderID reads the id the server gave the order, off the first thing
// list_failed_payments returns. The manifest id is not the gateway id: every
// arm materialises its own copy.
func (r *subprocessRig) gatewayOrderID() string {
	r.t.Helper()
	if r.orderID != "" {
		return r.orderID
	}
	res, err := r.session.CallTool(r.t.Context(), &mcp.CallToolParams{
		Name:      mcpserver.ToolListFailedPayments,
		Arguments: map[string]any{},
	})
	if err != nil {
		r.t.Fatalf("list_failed_payments: %v", err)
	}
	var out mcpserver.ListFailedPaymentsOutput
	if err := json.Unmarshal([]byte(resultText(res)), &out); err != nil {
		r.t.Fatalf("decode the order list from %q: %v", resultText(res), err)
	}
	if len(out.Orders) != 1 {
		r.t.Fatalf("got %d orders, want 1: one invocation serves one order", len(out.Orders))
	}
	r.orderID = out.Orders[0].OrderID
	return r.orderID
}

func (r *subprocessRig) call(name string, args map[string]any) *mcp.CallToolResult {
	r.t.Helper()
	res, err := r.session.CallTool(r.t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		r.t.Fatalf("call %s: %v", name, err)
	}
	return res
}

// closeAndWait ends the session, which is what makes the server read the order
// back and write its outcome row.
func (r *subprocessRig) closeAndWait() {
	r.t.Helper()
	if r.session == nil {
		return
	}
	if err := r.session.Close(); err != nil {
		r.t.Fatalf("close the session: %v", err)
	}
	r.session = nil

	// The process writes its outcome row on the way out. Poll rather than
	// sleep a fixed time, so a slow machine does not turn into a flake and a
	// fast one does not pay for it.
	deadline := time.Now().Add(15 * time.Second)
	path := filepath.Join(r.armDir, "outcomes.jsonl")
	for time.Now().Before(deadline) {
		if info, err := os.Stat(path); err == nil && info.Size() > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	r.t.Fatalf("no outcome row appeared at %s within the deadline", path)
}

func (r *subprocessRig) outcomeRows() []runner.OutcomeRow {
	r.t.Helper()
	return readJSONL[runner.OutcomeRow](r.t, filepath.Join(r.armDir, "outcomes.jsonl"))
}

func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func readJSONL[T any](t *testing.T, path string) []T {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()

	var out []T
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var v T
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Fatalf("decode a line of %s: %v", path, err)
		}
		out = append(out, v)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return out
}

func retryable(o batch.Order) bool {
	return !o.IsBait && o.GroundTruthCorrectAction == batch.ActionRetrySameInstrument
}

// ---------------------------------------------------------------------------

func TestCompiledServerListsItsToolsOverStdio(t *testing.T) {
	r := startServer(t, retryable)

	res, err := r.session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list the tools: %v", err)
	}
	var got []string
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
		if tool.Description == "" {
			t.Errorf("tool %s has no description, so the model is guessing what it does", tool.Name)
		}
	}
	slices.Sort(got)

	want := append([]string(nil), mcpserver.ToolNames()...)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("the compiled server serves\n got %v\nwant %v", got, want)
	}
}

func TestCompiledServerRefusesAnActionWithNoDecisionRecorded(t *testing.T) {
	r := startServer(t, retryable)
	order := r.gatewayOrderID()

	res := r.call(mcpserver.ToolRetryPayment, map[string]any{"order_id": order})
	var out mcpserver.ActionOutput
	if err := json.Unmarshal([]byte(resultText(res)), &out); err != nil {
		t.Fatalf("decode the action result from %q: %v", resultText(res), err)
	}
	if out.Allowed {
		t.Fatalf("the compiled server retried an order with no decision recorded")
	}
	if out.PolicyRule != mcpserver.RuleDecisionFirst {
		t.Errorf("the refusal carries rule %q, want %s", out.PolicyRule, mcpserver.RuleDecisionFirst)
	}

	r.closeAndWait()
	rows := r.outcomeRows()
	if len(rows) != 1 {
		t.Fatalf("got %d outcome rows, want 1", len(rows))
	}
	if rows[0].SideEffect {
		t.Errorf("the outcome row records a side effect for a refused action")
	}
	if rows[0].ActionKind != recovery.ActionNone {
		t.Errorf("the outcome row's action kind is %q, want %q", rows[0].ActionKind, recovery.ActionNone)
	}
}

func TestCompiledServerWritesAnOutcomeRowReadBackFromTheGateway(t *testing.T) {
	r := startServer(t, retryable)
	order := r.gatewayOrderID()

	r.call(mcpserver.ToolRecordDecision, map[string]any{
		"order_id":  order,
		"action":    recovery.ActionRetrySameInstrument,
		"reasoning": "the failure is transient, so re-presenting the same instrument can work",
	})
	res := r.call(mcpserver.ToolRetryPayment, map[string]any{"order_id": order})
	var action mcpserver.ActionOutput
	if err := json.Unmarshal([]byte(resultText(res)), &action); err != nil {
		t.Fatalf("decode the action result from %q: %v", resultText(res), err)
	}
	if !action.Allowed {
		t.Fatalf("the compiled server refused a retry on a retry-eligible order: %+v", action)
	}

	r.closeAndWait()
	rows := r.outcomeRows()
	if len(rows) != 1 {
		t.Fatalf("got %d outcome rows, want 1", len(rows))
	}
	row := rows[0]

	if row.Arm != ArmAgent {
		t.Errorf("the outcome row's arm is %q, want %q", row.Arm, ArmAgent)
	}
	if row.ManifestOrderID != r.manifest.OrderID {
		t.Errorf("the outcome row names manifest order %q, want %q", row.ManifestOrderID, r.manifest.OrderID)
	}
	if row.GatewayOrderID != order {
		t.Errorf("the outcome row names gateway order %q, want %q", row.GatewayOrderID, order)
	}
	if !row.Observed {
		t.Errorf("the outcome row is not marked observed, so the scorer would call it unscorable")
	}
	if row.FinalOrderStatus == "" {
		t.Errorf("the outcome row has no final order status, so nothing was read back from the gateway")
	}
	if !row.SideEffect {
		t.Errorf("an allowed retry did not record a side effect")
	}
	if row.PolicyVerdict == "" {
		t.Errorf("an action row carries no policy verdict, which is a succeeded containment violation")
	}

	// The ledger is the other half. It has to exist and it has to carry the
	// decision the agent stated before it acted.
	ledger := filepath.Join(r.armDir, "ledger.jsonl")
	if _, err := os.Stat(ledger); err != nil {
		t.Fatalf("no ledger at %s: %v", ledger, err)
	}
	body, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("read the ledger: %v", err)
	}
	if !strings.Contains(string(body), mcpserver.KindDecisionRecorded) {
		t.Errorf("the ledger has no decision_recorded row")
	}
}

// TestTwoInvocationsOfOneBatchGetDifferentGatewayIDs pins the fix for a bug
// the two-order smoke run found on 2026-09-01.
//
// Every invocation is its own process with its own in-memory fake, and the
// fake's rng is read only for id generation. Two invocations of the same batch
// therefore gave their first order the same gateway id, and the arm's whole
// ledger came out filed under one id. harness/aggregate.py selects a class's
// ledger rows by gateway id, so every per-class row would have picked up every
// other class's rows.
func TestTwoInvocationsOfOneBatchGetDifferentGatewayIDs(t *testing.T) {
	seen := map[string]string{}

	for _, index := range []int{0, 1} {
		nth := index
		r := startServer(t, func(o batch.Order) bool {
			if !retryable(o) {
				return false
			}
			if nth > 0 {
				nth--
				return false
			}
			return true
		})
		gateway := r.gatewayOrderID()
		if previous, ok := seen[gateway]; ok {
			t.Fatalf("two manifest orders got the same gateway id %s: %s and %s",
				gateway, previous, r.manifest.OrderID)
		}
		seen[gateway] = r.manifest.OrderID
		r.closeAndWait()
	}

	if len(seen) != 2 {
		t.Fatalf("got %d distinct gateway ids across two invocations, want 2", len(seen))
	}
}

// TestLiveLayerRefusesToServeWithNoOTLPEndpoint pins the guard against a
// silent protocol corruption.
//
// internal/telemetry falls back to the stdout exporter when no endpoint is
// configured, the live rig builds its provider through that function, and
// stdout is the MCP transport. A span written there is a malformed JSON-RPC
// frame the client reports as a connection failure, with nothing naming the
// cause. The fake layer needs no guard: it builds no provider at all when the
// endpoint is unset.
func TestLiveLayerRefusesToServeWithNoOTLPEndpoint(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("RAZORPAY_KEY_ID", "rzp_test_notarealkeyid")
	t.Setenv("RAZORPAY_KEY_SECRET", "notarealsecret")

	batchPath, file := seedBatchFile(t)
	err := run(t.Context(), []string{
		"-batch", batchPath,
		"-order", file.Orders[0].OrderID,
		"-layer", runner.LayerLive,
		"-run-dir", t.TempDir(),
	})
	if err == nil {
		t.Fatalf("the live layer served with no OTLP endpoint configured")
	}
	if !strings.Contains(err.Error(), "OTEL_EXPORTER_OTLP_ENDPOINT") {
		t.Errorf("the error does not name the variable to set: %v", err)
	}
	if !strings.Contains(err.Error(), "stdout") {
		t.Errorf("the error does not say why, which is what makes it actionable: %v", err)
	}
}
