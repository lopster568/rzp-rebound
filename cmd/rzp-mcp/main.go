// Command rzp-mcp serves the risk-item tools over MCP for one item.
//
// One process per item, started by the agent harness through the CLI's
// --mcp-config. On startup it materialises the manifest order in the
// configured gateway with its seeded failure history, turns it into the one
// risk item the surface works, then serves the eight tools on stdin and
// stdout. When the client disconnects it reads the order back out of the
// gateway and appends one outcome row, so the number for the agent arm comes
// from the same FetchOrder the other arms are scored on.
//
// The batch manifest it runs on models a failed card payment and nothing else,
// which is one detector of three and no customer at all. riskItemFrom says
// what that costs. Seeding risk items rather than orders is the harness's
// side of the pivot and is not done here.
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
	"io"
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
	"github.com/lopster568/rzp-recovery-agent/internal/intervene"
	"github.com/lopster568/rzp-recovery-agent/internal/mcpserver"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/lopster568/rzp-recovery-agent/internal/promise"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/recovery"
	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
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

// defaultCard was the instrument every retry re-presented. Nothing here
// re-presents anything now: see docs/INDIA-CONSTRAINTS-AUDIT.md. The flag that
// carries it is kept, and ignored, because harness/claude_runner.py passes it
// and a command line that stops parsing is a run that does not start.
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
	// card is read by nothing. See defaultCard.
	card         string
	actionBudget int
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
	fs.StringVar(&o.card, "card", defaultCard, "accepted and ignored: this engine re-presents no instrument")
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

	tracer, shutdown, err := newTracer(ctx, rig, opts.layer, os.Stderr)
	if err != nil {
		return err
	}
	defer shutdown()

	// The store is deliberately not primed from the gateway. It counts
	// outbound contacts now, and the payment attempts this order arrived with
	// are not contacts: priming them would spend the item's contact cap on
	// failures that happened before this run existed.
	ledgerStore := store.New(runClock)

	escalations, err := os.OpenFile(filepath.Join(armDir, "escalations.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open the escalation queue: %w", err)
	}
	defer func() {
		if err := escalations.Close(); err != nil && runErr == nil {
			runErr = fmt.Errorf("close the escalation queue: %w", err)
		}
	}()
	sink, err := intervene.NewWriterSink(escalations)
	if err != nil {
		return err
	}

	// One promise ledger, written by the intervention engine and read by the
	// policy. Two of them would be the failure this wiring exists to avoid: an
	// agent logs a promise, the hold is written somewhere the policy cannot
	// see, and R15 lets it chase the customer it just agreed to leave alone.
	promises := promise.NewStore()

	engine, err := intervene.New(intervene.Options{
		Gateway:     &layerGateway{port: rig.Port, layer: opts.layer},
		Recorder:    recorder,
		Promises:    promiseLedger{inner: promises},
		Escalations: sink,
		Clock:       runClock,
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
	defer invocation.End()

	// The class goes on the record before the agent sees anything, so the
	// ledger for this arm starts the same way the other three arms' ledgers
	// do and the scorer needs no separate path.
	beforeCalls := rig.Calls()
	seen, err := observeAndRecord(ctx, rig, recorder, order, opts.arm, tracer)
	if err != nil {
		return err
	}
	class, attemptsSeen := seen.class, seen.attemptsSeen

	// The item is built from that same read, so the queue and the ledger agree
	// about what was on the order without asking Razorpay twice.
	item := riskItemFrom(order, seen)

	invocation.SetAttributes(
		attribute.String("rzp.arm", opts.arm),
		attribute.String("rzp.layer", opts.layer),
		attribute.String("rzp.batch_id", batchFile.BatchID),
		attribute.String("rzp.manifest_order_id", order.ManifestID),
		attribute.String("rzp.gateway_order_id", order.Visible.OrderID),
		attribute.String(mcpserver.AttrItemID, item.ID),
	)

	server, err := mcpserver.New(mcpserver.Options{
		Items:     []riskitem.RiskItem{item},
		Intervene: engine,
		Policy:    policy.New(policy.Config{}, runClock),
		Facts: itemFacts{
			promises:     promises,
			clock:        runClock,
			sourceStatus: seen.order.Status,
		},
		Store:             ledgerStore,
		Recorder:          recorder,
		Tracer:            tracer,
		Clock:             runClock,
		KillSwitchEngaged: engaged,
		ActionBudget:      opts.actionBudget,
		Arm:               opts.arm,
	})
	if err != nil {
		return err
	}

	// Serve. This blocks until the client closes stdin.
	serveErr := server.Run(ctx, &mcp.StdioTransport{})

	// The outcome. Read from the gateway, after the session, whatever the
	// agent did or said. One code path means there is no branch in which the
	// agent's claim is believed.
	tally := server.Tally(item.ID)
	row := runner.OutcomeRow{
		RunID:           filepath.Base(opts.runDir),
		Arm:             opts.arm,
		Layer:           opts.layer,
		BatchID:         batchFile.BatchID,
		ManifestOrderID: order.ManifestID,
		GatewayOrderID:  order.Visible.OrderID,
		Class:           class.String(),
		ActionKind:      tally.ActionKind,
		// ClaimedRecovered stays false and has nowhere else to come from. No
		// action this engine can take collects money, so there is no claim of
		// recovery for the gateway read below to disagree with. The column is
		// kept because the scorer reads it for every arm.
		ClaimedRecovered: false,
		AttemptsSeen:     attemptsSeen,
		// Nothing here puts a payment on an order, so the count cannot move.
		AttemptsAfter: attemptsSeen,
		PolicyVerdict: tally.PolicyVerdict,
		PolicyRule:    tally.PolicyRule,
		Escalated:     tally.Escalated,
		SideEffect:    tally.SideEffect,
	}
	if serveErr != nil {
		row.Error = serveErr.Error()
	}

	// The read-back runs on a context the client's exit cannot cancel. See
	// outcomeContext.
	readBack, cancelReadBack := outcomeContext(ctx)
	defer cancelReadBack()

	// It gets its own span, so the outcome_observed row joins to a trace like
	// every other row does. FR-AUD-3 is every row, not most rows.
	outcomeCtx, outcomeSpan := tracer.Start(readBack, "mcp.observe_outcome")
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

	// Every call this arm makes goes through the rig's gateway, so the rig's
	// own counter is the whole of it. The retry arms had to add the checkout
	// calls an attempt made outside Port; there are no attempts here.
	row.APICalls = rig.Calls() - beforeCalls

	if err := appendOutcome(filepath.Join(armDir, "outcomes.jsonl"), row); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr,
		"rzp-mcp: %s %s tools=%d denied=%d action=%s status=%s\n",
		opts.arm, order.ManifestID, tally.ToolCalls, tally.DeniedToolCalls,
		row.ActionKind, row.FinalOrderStatus)

	return serveErr
}

// observation is what one read of the gateway saw. It is one struct rather
// than four return values because the risk item and the outcome row are both
// built from it, and they have to be built from the same read.
type observation struct {
	class        classify.Class
	attemptsSeen int
	order        razorpay.Order
	failed       *razorpay.Payment
}

// observeAndRecord reads the order and its payments once, classifies the
// failure, and puts the classification on the record. It is its own step so
// that the row exists even when the agent never calls a tool.
func observeAndRecord(
	ctx context.Context,
	rig *runner.GatewayRig,
	recorder *audit.Recorder,
	order runner.Materialised,
	arm string,
	tracer trace.Tracer,
) (observation, error) {
	ctx, span := tracer.Start(ctx, "mcp.classify")
	defer span.End()

	var seen observation

	payments, err := rig.Port.ListPaymentsForOrder(ctx, order.Visible.OrderID)
	if err != nil {
		return seen, err
	}
	seen.attemptsSeen = len(payments)
	for i := range payments {
		if payments[i].Status == razorpay.PaymentStatusFailed {
			seen.failed = &payments[i]
		}
	}
	seen.class = classify.Classify(recovery.FailureFrom(seen.failed))

	polled, err := rig.Port.FetchOrder(ctx, order.Visible.OrderID)
	if err != nil {
		return seen, err
	}
	seen.order = polled

	if _, err := recorder.Record(ctx, audit.Event{
		OrderID: order.Visible.OrderID,
		Kind:    audit.KindClassified,
		Class:   seen.class.String(),
		Detail: map[string]string{
			recovery.DetailArm:    arm,
			"polled_order_status": polled.Status,
			"attempts_seen":       strconv.Itoa(len(payments)),
			"poll_timed_out":      "false",
		},
	}); err != nil {
		return seen, err
	}
	return seen, nil
}

// riskItemFrom turns the one order this invocation serves into the one risk
// item the tool surface works.
//
// It stands in for a detector, and it is the pivot's seam in this command.
// internal/detect builds items from live Razorpay sweeps of three kinds; the
// batch manifest this eval harness runs on predates the risk item and models
// exactly one, a failed card payment. So the source is failed_payment, the
// signal is the error the gateway returned, and there is no customer on it,
// because a manifest order has none. That last part is not a gap to paper
// over: an item with no contact channel is one the intervention engine
// refuses to notify, which is the correct behaviour and is what the agent arm
// will see until the harness seeds items rather than orders.
func riskItemFrom(order runner.Materialised, seen observation) riskitem.RiskItem {
	item := riskitem.RiskItem{
		ID:              riskitem.NewID(riskitem.SourceFailedPayment, order.Visible.OrderID),
		Source:          riskitem.SourceFailedPayment,
		SourceID:        order.Visible.OrderID,
		RootOrderID:     order.Visible.OrderID,
		AmountPaise:     order.Visible.AmountPaise,
		AmountPaidPaise: seen.order.AmountPaid,
		// Carried, never derived. riskitem.RiskItem says the gateway reports
		// what is outstanding and that a partial payment can make the
		// arithmetic disagree with it, so amount minus paid is not a fallback,
		// it is a second opinion nobody asked for.
		AmountDuePaise: seen.order.AmountDue,
		Currency:       order.Visible.Currency,
		AtRiskSince:    seen.order.CreatedAt,
		Signal:         riskitem.Signal{Attempts: seen.attemptsSeen},
	}
	if seen.failed != nil {
		item.SourceID = seen.failed.ID
		item.ID = riskitem.NewID(riskitem.SourceFailedPayment, seen.failed.ID)
		item.Signal.FailureCode = seen.failed.ErrorCode
		item.Signal.FailureReason = seen.failed.ErrorReason
		item.Signal.FailureSource = seen.failed.ErrorSource
		item.Signal.FailureStep = seen.failed.ErrorStep
		item.Signal.Method = seen.failed.Method
	}
	return item
}

// promiseLedger is the conversion internal/intervene's PromiseRecord comment
// names, at the wiring site that comment says is the right place for it.
//
// intervene.PromiseRecord and promise.Record are the same four fields in the
// same order, deliberately, so that the intervention engine ships without
// importing the ledger. Nothing enforces that from either side. The conversion
// below is the assertion: reorder a field on either type and this line stops
// compiling, here, in the one file that imports both.
type promiseLedger struct{ inner *promise.Store }

func (l promiseLedger) Log(_ context.Context, rec intervene.PromiseRecord) error {
	return l.inner.Log(promise.Record(rec))
}

var _ intervene.PromiseLedger = promiseLedger{}

// itemFacts supplies the policy inputs a risk item does not carry.
//
// Two of the three are real here. A promise the agent logged this run is read
// back out of the same ledger the intervention engine wrote it to, which is
// what makes R15 able to refuse a chase after a hold; the source status is the
// one this invocation read at startup, which is what R4 reads to refuse
// chasing a cancelled order. The third is not: nothing in this process records
// a dispute, so Disputed stays false and R13 cannot fire. That is a gap in the
// harness rather than in the rule, and it is stated here rather than left to
// be discovered from an eval that never trips R13.
type itemFacts struct {
	promises     *promise.Store
	clock        clock.Clock
	sourceStatus string
}

var _ mcpserver.FactsProvider = itemFacts{}

func (f itemFacts) FactsFor(_ context.Context, item riskitem.RiskItem) policy.Facts {
	facts := policy.Facts{SourceStatus: f.sourceStatus}
	if hold, ok := f.promises.ActiveHold(item.ID, f.clock.Now()); ok {
		facts.PromiseHoldUntil = hold.HoldUntil()
	}
	return facts
}

// layerGateway is intervene.Gateway over whatever gateway this layer runs.
//
// razorpay.Client is an intervene.Gateway on its own. razorpay.Port, which is
// what the fake layer exposes, has the two payment-link methods and none of
// the three invoice ones, because the fake has no invoices yet. Rather than
// pretend, the invoice methods refuse with the layer named: an invoice action
// on the fake layer is a thing this build cannot do, and an engine that
// reported it as done would be worse than one that says so.
type layerGateway struct {
	port  razorpay.Port
	layer string
}

var _ intervene.Gateway = (*layerGateway)(nil)

func (g *layerGateway) unsupported(what string) error {
	return fmt.Errorf("rzp-mcp: the %s layer has no invoice support, so %s cannot run here",
		g.layer, what)
}

func (g *layerGateway) NotifyInvoice(_ context.Context, _, _ string) (razorpay.NotifyReceipt, error) {
	return razorpay.NotifyReceipt{}, g.unsupported("an invoice notification")
}

func (g *layerGateway) FetchInvoice(_ context.Context, _ string) (razorpay.Invoice, error) {
	return razorpay.Invoice{}, g.unsupported("an invoice read")
}

func (g *layerGateway) CancelInvoice(_ context.Context, _ string) (razorpay.Invoice, error) {
	return razorpay.Invoice{}, g.unsupported("an invoice cancellation")
}

func (g *layerGateway) CreatePaymentLink(ctx context.Context, req razorpay.CreatePaymentLinkRequest) (razorpay.PaymentLink, error) {
	return g.port.CreatePaymentLink(ctx, req)
}

func (g *layerGateway) ResendPaymentLinkNotification(ctx context.Context, linkID, medium string) (razorpay.NotifyReceipt, error) {
	return g.port.ResendPaymentLinkNotification(ctx, linkID, medium)
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
//
// The no-op branch says so on warn. The phase 5 fake run served 404 audit rows
// with no trace id because the harness shell had no endpoint exported at
// config-generation time, and nothing anywhere said a flagship property had
// been dropped. Serving without spans is allowed; doing it silently is not.
func newTracer(ctx context.Context, rig *runner.GatewayRig, layer string, warn io.Writer) (trace.Tracer, func(), error) {
	if layer == runner.LayerLive {
		return rig.Tracer, func() {}, nil
	}

	cfg, err := config.Load()
	if err != nil || cfg.OTLPEndpoint == "" {
		// A config that will not load is not a reason to refuse to serve the
		// fake layer, which needs no credentials at all. It is a reason to
		// record no spans, and to say so: every audit row this invocation
		// writes will carry no trace id.
		fmt.Fprintf(warn, "rzp-mcp: serving with a no-op tracer: %s is unset, "+
			"so audit rows from this invocation carry no trace id\n",
			config.EnvOTLPEndpoint)
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

// OutcomeReadBackTimeout bounds the gateway read that produces the outcome
// row. It is generous because it is the last call of the process and there is
// nothing to be gained by giving up on it early.
const OutcomeReadBackTimeout = 30 * time.Second

// outcomeContext returns a context for the gateway read that produces the
// outcome row, detached from the one the session ran on.
//
// The process is started by the CLI, and when the CLI exits it takes the
// process group with it: the SIGTERM that arrives cancels the context
// signal.NotifyContext built. Everything up to that point should stop, which
// is what the signal is for. The read-back should not.
//
// It cost the first live run its whole agent arm. On the fake layer nothing
// showed: razorpay.Fake ignores the context it is handed, so FetchOrder
// succeeded and every row came back observed. On the live layer the HTTP
// client honours it, so all 8 read-backs failed with "context canceled", all 8
// rows came back with observed false, and harness/scorer.py correctly called
// every one of them unscorable. The arm had done its work and the harness
// threw the answer away.
//
// A cancelled read-back is the worst shape of failure this eval has, because
// it does not look like a failure. It looks like an arm with no data.
func outcomeContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), OutcomeReadBackTimeout)
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
