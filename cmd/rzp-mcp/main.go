// Command rzp-mcp serves the recovery tools over MCP for one order.
//
// One process per order, started by the agent harness through the CLI's
// --mcp-config. On startup it materialises that order in the configured
// gateway with its seeded failure history, then serves the seven tools on
// stdin and stdout. When the client disconnects it reads the order back out of
// the gateway and appends one outcome row, so the recovery number for the
// agent arm comes from the same FetchOrder the other three arms are scored on.
//
// It holds the Razorpay credentials. The model holds tool names (FR-MCP-2).
//
// Nothing in this process may write to stdout. Stdout is the MCP transport,
// and one stray line on it is a protocol error the client reports as a
// connection failure. Progress and errors go to stderr, and the trace exporter
// is built only when an OTLP endpoint is configured, because the stdout
// exporter internal/telemetry falls back to would corrupt the session.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/audit"
	"github.com/lopster568/rzp-recovery-agent/internal/batch"
	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/config"
	"github.com/lopster568/rzp-recovery-agent/internal/mcpserver"
	"github.com/lopster568/rzp-recovery-agent/internal/notify"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/recovery"
	"github.com/lopster568/rzp-recovery-agent/internal/runner"
	"github.com/lopster568/rzp-recovery-agent/internal/store"
	"github.com/lopster568/rzp-recovery-agent/internal/telemetry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// ArmAgent is the arm id for the LLM arm. a2 was reserved for it in phase 2 so
// a table from either phase can be read next to the other.
const ArmAgent = "a2-agent"

// defaultCard is the instrument every retry re-presents. It is the same value
// cmd/rzp run defaults to, because an arm that re-presented a different card
// would be a different experiment.
const defaultCard = "4100280000080001"

// tracerName names the tracer this process opens spans on.
const tracerName = "github.com/lopster568/rzp-recovery-agent/cmd/rzp-mcp"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		// Anything from internal/razorpay has been through Client.Redact by
		// the time it is an error, which is the control that keeps a
		// credential off this line. It goes to stderr because stdout is the
		// MCP transport.
		fmt.Fprintf(os.Stderr, "rzp-mcp: %v\n", err)
		os.Exit(1)
	}
}

// options is what one invocation was told to do.
type options struct {
	batchPath      string
	orderID        string
	layer          string
	runDir         string
	arm            string
	killSwitchFile string
	card           string
	actionBudget   int
}

func parseFlags(args []string) (options, error) {
	var o options
	fs := flag.NewFlagSet("rzp-mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&o.batchPath, "batch", "", "path to a batch manifest written by rzp seed")
	fs.StringVar(&o.orderID, "order", "", "the manifest order id this invocation is for")
	fs.StringVar(&o.layer, "layer", runner.LayerFake, "which gateway: fake or live")
	fs.StringVar(&o.runDir, "run-dir", "", "the run directory the harness created")
	fs.StringVar(&o.arm, "arm", ArmAgent, "the arm id that goes in every audit row")
	fs.StringVar(&o.killSwitchFile, "kill-switch-file", "", "a path whose existence halts every action")
	fs.StringVar(&o.card, "card", defaultCard, "the instrument every retry re-presents")
	fs.IntVar(&o.actionBudget, "action-budget", 0, "action tool calls this invocation may make, zero means the default")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	if o.batchPath == "" {
		return o, errors.New("-batch is required: run rzp seed first")
	}
	if o.orderID == "" {
		return o, errors.New("-order is required: one invocation serves one order")
	}
	if o.runDir == "" {
		return o, errors.New("-run-dir is required")
	}
	if o.layer != runner.LayerFake && o.layer != runner.LayerLive {
		return o, fmt.Errorf("-layer is %q, want %q or %q", o.layer, runner.LayerFake, runner.LayerLive)
	}
	return o, nil
}

func run(ctx context.Context, args []string) (runErr error) {
	opts, err := parseFlags(args)
	if err != nil {
		return err
	}

	batchFile, err := runner.ReadBatchFile(opts.batchPath)
	if err != nil {
		return err
	}
	manifestOrder, ok := findOrder(batchFile, opts.orderID)
	if !ok {
		return fmt.Errorf("batch %s has no order %s", batchFile.BatchID, opts.orderID)
	}

	if err := requireExporterForLive(opts.layer); err != nil {
		return err
	}

	engaged, err := policy.KillSwitchFile(opts.killSwitchFile)
	if err != nil {
		return err
	}

	armDir := filepath.Join(opts.runDir, opts.arm)
	if err := os.MkdirAll(armDir, 0o755); err != nil {
		return fmt.Errorf("make %s: %w", armDir, err)
	}

	// Both files are opened for append, not truncate. One process serves one
	// order and the harness runs them one after another, so the arm's ledger
	// and outcomes are the concatenation of every invocation's rows. Truncating
	// would leave the run with the last order and nothing else.
	ledger, err := os.OpenFile(filepath.Join(armDir, "ledger.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open the ledger: %w", err)
	}
	defer func() {
		if err := ledger.Close(); err != nil && runErr == nil {
			runErr = fmt.Errorf("close the ledger: %w", err)
		}
	}()

	// The fake layer runs on a fake clock started at the same fixed instant
	// cmd/rzp run uses, so the two arms see the same time and R2 means the
	// same thing to both. The live layer runs on the wall clock, because real
	// time passes between real API calls whatever this process thinks.
	var runClock clock.Clock = clock.Real()
	if opts.layer == runner.LayerFake {
		runClock = clock.NewFake(time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC))
	}

	recorder, err := audit.NewRecorder(audit.Options{Writer: ledger, Clock: runClock})
	if err != nil {
		return err
	}

	rig, err := runner.NewGatewayRig(ctx, opts.layer, perOrderBatch(batchFile, opts.orderID), runClock)
	if err != nil {
		return err
	}
	defer rig.Close(ctx)

	materialised, err := rig.Materialise(ctx, []batch.Order{manifestOrder})
	if err != nil {
		return err
	}
	if len(materialised) != 1 {
		return fmt.Errorf("materialised %d orders for one manifest order", len(materialised))
	}
	order := materialised[0]

	tracer, shutdown, err := newTracer(ctx, rig, opts.layer)
	if err != nil {
		return err
	}
	defer shutdown()

	ledgerStore := store.New(runClock)
	ledgerStore.Observe(order.Visible.OrderID, order.Attempts)

	notifier, err := notify.New(notify.Options{Port: rig.Port, Clock: runClock})
	if err != nil {
		return err
	}

	server, err := mcpserver.New(mcpserver.Options{
		Surface: &recovery.Surface{
			Port:      rig.Port,
			Attempter: rig.Attempter(),
			Notifier:  notifier,
			Recorder:  recorder,
			Card:      opts.card,
			Currency:  "INR",
		},
		Store:             ledgerStore,
		Policy:            policy.New(policy.Config{}, runClock),
		Recorder:          recorder,
		Tracer:            tracer,
		Orders:            []batch.AgentVisibleOrder{order.Visible},
		KillSwitchEngaged: engaged,
		ActionBudget:      opts.actionBudget,
		Arm:               opts.arm,
	})
	if err != nil {
		return err
	}

	// One root span for the whole invocation, so a reviewer opening the trace
	// sees the classification, every tool call, and the outcome as one tree
	// rather than as a handful of unrelated traces. The MCP session's request
	// contexts descend from the one Run is given, so the middleware's per-call
	// spans hang off this one.
	ctx, invocation := tracer.Start(ctx, "mcp.invocation")
	invocation.SetAttributes(
		attribute.String("rzp.arm", opts.arm),
		attribute.String("rzp.layer", opts.layer),
		attribute.String("rzp.batch_id", batchFile.BatchID),
		attribute.String("rzp.manifest_order_id", order.ManifestID),
		attribute.String(mcpserver.AttrOrderID, order.Visible.OrderID),
	)
	defer invocation.End()

	// The class goes on the record before the agent sees anything, so the
	// ledger for this arm starts the same way the other three arms' ledgers
	// do and the scorer needs no separate path.
	beforeCalls := rig.Calls()
	class, attemptsSeen, err := observeAndRecord(ctx, rig, recorder, order, opts.arm, tracer)
	if err != nil {
		return err
	}

	// Serve. This blocks until the client closes stdin.
	serveErr := server.Run(ctx, &mcp.StdioTransport{})

	// The outcome. Read from the gateway, after the session, whatever the
	// agent did or said. One code path means there is no branch in which the
	// agent's claim is believed.
	tally := server.Tally(order.Visible.OrderID)
	row := runner.OutcomeRow{
		RunID:            filepath.Base(opts.runDir),
		Arm:              opts.arm,
		Layer:            opts.layer,
		BatchID:          batchFile.BatchID,
		ManifestOrderID:  order.ManifestID,
		GatewayOrderID:   order.Visible.OrderID,
		Class:            class.String(),
		ActionKind:       tally.ActionKind,
		ClaimedRecovered: tally.ClaimedRecovered,
		AttemptsSeen:     attemptsSeen,
		AttemptsAfter:    ledgerStore.Attempts(order.Visible.OrderID),
		PolicyVerdict:    tally.PolicyVerdict,
		PolicyRule:       tally.PolicyRule,
		Escalated:        tally.Escalated,
		SideEffect:       tally.SideEffect,
	}
	if serveErr != nil {
		row.Error = serveErr.Error()
	}

	// The read-back gets its own span, so the outcome_observed row joins to a
	// trace like every other row does. FR-AUD-3 is every row, not most rows.
	outcomeCtx, outcomeSpan := tracer.Start(ctx, "mcp.observe_outcome")
	final, ferr := rig.Port.FetchOrder(outcomeCtx, order.Visible.OrderID)
	if ferr != nil {
		// An unobserved row is unscorable, which the scorer counts and keeps
		// out of every denominator rather than folding into "not recovered".
		if row.Error == "" {
			row.Error = ferr.Error()
		}
	} else {
		row.FinalOrderStatus = final.Status
		row.Recovered = final.Status == razorpay.OrderStatusPaid
		row.AmountPaidPaise = final.AmountPaid
		row.Observed = true

		if _, err := recorder.Record(outcomeCtx, audit.Event{
			OrderID:        order.Visible.OrderID,
			Kind:           audit.KindOutcomeObserved,
			Class:          class.String(),
			ProposedAction: tally.ActionKind,
			PolicyVerdict:  tally.PolicyVerdict,
			PolicyRule:     tally.PolicyRule,
			Detail: map[string]string{
				recovery.DetailArm:   opts.arm,
				"final_order_status": final.Status,
				"recovered":          strconv.FormatBool(row.Recovered),
				"claimed_recovered":  strconv.FormatBool(row.ClaimedRecovered),
				"amount_paid_paise":  strconv.FormatInt(final.AmountPaid, 10),
				"tool_calls":         strconv.Itoa(tally.ToolCalls),
				"denied_tool_calls":  strconv.Itoa(tally.DeniedToolCalls),
				"decisions_recorded": strconv.Itoa(tally.DecisionsRecorded),
			},
		}); err != nil {
			outcomeSpan.End()
			return err
		}
	}
	outcomeSpan.SetAttributes(
		attribute.String("rzp.final_order_status", row.FinalOrderStatus),
		attribute.Bool("rzp.recovered", row.Recovered),
	)
	outcomeSpan.End()

	// Port calls plus the ones an attempt made outside Port, the same sum
	// cmd/rzp run writes. A payment attempt is four checkout calls on the live
	// layer and Port has no method for any of them.
	row.APICalls = rig.Calls() - beforeCalls + tally.GatewayCalls

	if err := appendOutcome(filepath.Join(armDir, "outcomes.jsonl"), row); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr,
		"rzp-mcp: %s %s tools=%d denied=%d action=%s status=%s\n",
		opts.arm, order.ManifestID, tally.ToolCalls, tally.DeniedToolCalls,
		row.ActionKind, row.FinalOrderStatus)

	return serveErr
}

// recordOutcome is folded into run, but the classification is its own step so
// that the row exists even when the agent never calls a tool.
func observeAndRecord(
	ctx context.Context,
	rig *runner.GatewayRig,
	recorder *audit.Recorder,
	order runner.Materialised,
	arm string,
	tracer trace.Tracer,
) (classify.Class, int, error) {
	ctx, span := tracer.Start(ctx, "mcp.classify")
	defer span.End()

	payments, err := rig.Port.ListPaymentsForOrder(ctx, order.Visible.OrderID)
	if err != nil {
		return classify.Unclassified, 0, err
	}
	var failed *razorpay.Payment
	for i := range payments {
		if payments[i].Status == razorpay.PaymentStatusFailed {
			failed = &payments[i]
		}
	}
	class := classify.Classify(recovery.FailureFrom(failed))

	polled, err := rig.Port.FetchOrder(ctx, order.Visible.OrderID)
	if err != nil {
		return class, len(payments), err
	}

	if _, err := recorder.Record(ctx, audit.Event{
		OrderID: order.Visible.OrderID,
		Kind:    audit.KindClassified,
		Class:   class.String(),
		Detail: map[string]string{
			recovery.DetailArm:    arm,
			"polled_order_status": polled.Status,
			"attempts_seen":       strconv.Itoa(len(payments)),
			"poll_timed_out":      "false",
		},
	}); err != nil {
		return class, len(payments), err
	}
	return class, len(payments), nil
}

// requireExporterForLive refuses to serve the live layer with no OTLP endpoint.
//
// It is a guard against a silent protocol corruption rather than a policy about
// telemetry. internal/telemetry falls back to the stdout exporter when no
// endpoint is configured, the live rig builds its provider through that
// function, and stdout is the MCP transport. A span printed there is not a lost
// trace, it is a malformed JSON-RPC frame, and the client reports it as a
// connection failure with nothing naming the cause.
//
// The fake layer needs no guard because newTracer below builds no provider at
// all when the endpoint is unset.
func requireExporterForLive(layer string) error {
	if layer != runner.LayerLive {
		return nil
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.OTLPEndpoint == "" {
		return fmt.Errorf(
			"the live layer needs %s set: with no endpoint the tracer falls back to "+
				"the stdout exporter, and stdout is the MCP transport. Run scripts/jaeger-up.sh, "+
				"which prints the value to export",
			config.EnvOTLPEndpoint)
	}
	return nil
}

// newTracer builds the span exporter, or a tracer that records nothing.
//
// The live layer already has a provider on the rig. The fake layer gets one
// only when an OTLP endpoint is configured, because internal/telemetry falls
// back to the stdout exporter and stdout is the MCP transport: a span printed
// there is a protocol error, not a trace.
func newTracer(ctx context.Context, rig *runner.GatewayRig, layer string) (trace.Tracer, func(), error) {
	if layer == runner.LayerLive {
		return rig.Tracer, func() {}, nil
	}

	cfg, err := config.Load()
	if err != nil || cfg.OTLPEndpoint == "" {
		// A config that will not load is not a reason to refuse to serve the
		// fake layer, which needs no credentials at all. It is a reason to
		// record no spans.
		return noop.NewTracerProvider().Tracer(tracerName), func() {}, nil
	}

	provider, err := telemetry.NewTracerProvider(ctx, telemetry.Config{
		ServiceName:  cfg.ServiceName,
		OTLPEndpoint: cfg.OTLPEndpoint,
		Insecure:     true,
	})
	if err != nil {
		return nil, nil, err
	}
	return provider.Tracer(tracerName), func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = provider.Shutdown(shutdownCtx)
	}, nil
}

// perOrderBatch returns the batch file with the gateway seed varied by order.
//
// Every invocation is its own process with its own in-memory fake, and the
// fake's rng is read for exactly one thing: generating ids. Two invocations of
// the same batch therefore handed their first order the same gateway id, and
// the arm's ledger came out with forty different manifest orders filed under
// one gateway id. The overall table row survives that, because it reads the
// whole ledger. The per-class rows do not: harness/aggregate.py selects a
// class's ledger rows by gateway id, so every class row would have picked up
// every other class's rows.
//
// Found by running the two-order smoke on 2026-09-01 and reading the ledger,
// not by reading the code. PROBLEMS.md has it.
//
// The offset is a hash of the order id, so it is deterministic: the same order
// in the same batch gets the same ids on every rerun. Nothing else about the
// fake moves, because nothing else about the fake reads the rng.
func perOrderBatch(file *runner.BatchFile, orderID string) *runner.BatchFile {
	varied := *file
	h := fnv.New64a()
	_, _ = h.Write([]byte(orderID))
	varied.Seed = file.Seed + int64(h.Sum64()&0x7fffffff)
	return &varied
}

func findOrder(file *runner.BatchFile, orderID string) (batch.Order, bool) {
	for _, o := range file.Orders {
		if o.OrderID == orderID {
			return o, true
		}
	}
	return batch.Order{}, false
}

// appendOutcome adds one row to the arm's outcomes file.
func appendOutcome(path string, row runner.OutcomeRow) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open the outcomes file: %w", err)
	}
	if err := runner.WriteJSONLine(f, row); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
