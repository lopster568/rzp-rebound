package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/audit"
	"github.com/lopster568/rzp-recovery-agent/internal/batch"
	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/notify"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/lopster568/rzp-recovery-agent/internal/poller"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/recovery"
	"github.com/lopster568/rzp-recovery-agent/internal/store"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// Layers, per ADR-0004. No table sums across them.
const (
	layerFake = "fake"
	layerLive = "live"
)

// liveMaxConcurrent caps requests in flight on the live layer.
//
// Two rather than the client's default of four. PRD Q5 is open: no 429 came
// back at 1.4 requests per second on 2026-08-31, which bounds nothing, and a
// batch run makes far more calls than that walk did. Two is the conservative
// setting until a deliberate ramp measures the limit.
const liveMaxConcurrent = 2

// runTracerName names the tracer the batch runner opens its own spans on.
const runTracerName = "github.com/lopster568/rzp-recovery-agent/cmd/rzp/run"

// materialised is one manifest order as it exists in a gateway.
//
// The manifest id and the gateway id are different on purpose. A manifest is a
// specification of a batch, and every arm materialises its own copy of it, so
// that one arm recovering an order cannot change what the next arm sees. On
// the live layer the gateway id is a real Razorpay order id.
type materialised struct {
	manifestID string
	visible    batch.AgentVisibleOrder
	attempts   int
}

// OutcomeRow is one order's result, one line of outcomes.jsonl. It is the file
// the Python scorer reads, and every field on it comes from the gateway or
// from the arm's own decision record, never from what an action claimed.
type OutcomeRow struct {
	RunID            string `json:"run_id"`
	Arm              string `json:"arm"`
	Layer            string `json:"layer"`
	BatchID          string `json:"batch_id"`
	ManifestOrderID  string `json:"manifest_order_id"`
	GatewayOrderID   string `json:"gateway_order_id"`
	Class            string `json:"class"`
	ActionKind       string `json:"action_kind"`
	FinalOrderStatus string `json:"final_order_status"`
	Recovered        bool   `json:"recovered"`
	ClaimedRecovered bool   `json:"claimed_recovered"`
	AmountPaidPaise  int64  `json:"amount_paid_paise"`
	AttemptsSeen     int    `json:"attempts_seen"`
	AttemptsAfter    int    `json:"attempts_after"`
	PolicyVerdict    string `json:"policy_verdict"`
	PolicyRule       string `json:"policy_rule"`
	Escalated        bool   `json:"escalated"`
	SideEffect       bool   `json:"side_effect"`
	TimedOut         bool   `json:"timed_out"`
	Error            string `json:"error"`
	// Observed reports that the final order state was read back from the
	// gateway. False makes the row unscorable, which the scorer counts and
	// keeps out of every denominator rather than dropping.
	Observed bool `json:"observed"`
	APICalls int  `json:"api_calls"`
}

// runRun runs one arm over one batch and writes its outcomes and its ledger.
func runRun(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	armID := fs.String("arm", recovery.ArmRules, "which arm: a0-control, a1-naive, or a3-rules")
	layer := fs.String("layer", layerFake, "which gateway: fake or live")
	batchPath := fs.String("batch", "", "path to a batch manifest written by rzp seed")
	runDir := fs.String("run-dir", "", "the run directory the harness created")
	sequencePath := fs.String("order-sequence", "", "a file of manifest order ids, one per line, in the order to run them")
	killSwitch := fs.String("kill-switch-file", "", "a path whose existence halts every action")
	card := fs.String("card", "4100280000080001", "the instrument every retry re-presents")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *batchPath == "" {
		return fmt.Errorf("-batch is required: run rzp seed first")
	}
	if *runDir == "" {
		return fmt.Errorf("-run-dir is required")
	}
	if *layer != layerFake && *layer != layerLive {
		return fmt.Errorf("-layer is %q, want %q or %q", *layer, layerFake, layerLive)
	}

	batchFile, err := readBatchFile(*batchPath)
	if err != nil {
		return err
	}
	ordered, err := orderSequence(batchFile, *sequencePath)
	if err != nil {
		return err
	}

	engaged, err := policy.KillSwitchFile(*killSwitch)
	if err != nil {
		return err
	}

	armDir := filepath.Join(*runDir, *armID)
	if err := os.MkdirAll(armDir, 0o755); err != nil {
		return fmt.Errorf("make %s: %w", armDir, err)
	}
	ledger, err := os.OpenFile(filepath.Join(armDir, "ledger.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create the ledger: %w", err)
	}
	defer func() { _ = ledger.Close() }()

	// The fake layer runs on a fake clock started at a fixed instant, so a
	// seed reproduces a run exactly. The live layer runs on the wall clock,
	// because real time passes between real API calls whatever this process
	// thinks.
	var runClock clock.Clock = clock.Real()
	if *layer == layerFake {
		runClock = clock.NewFake(time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC))
	}

	recorder, err := audit.NewRecorder(audit.Options{Writer: ledger, Clock: runClock})
	if err != nil {
		return err
	}

	rig, err := newGatewayRig(ctx, *layer, batchFile, runClock)
	if err != nil {
		return err
	}
	defer rig.close(ctx)

	orders, err := rig.materialise(ctx, ordered)
	if err != nil {
		return err
	}

	ledgerStore := store.New(runClock)
	for _, o := range orders {
		ledgerStore.Observe(o.visible.OrderID, o.attempts)
	}

	notifier, err := notify.New(notify.Options{Port: rig.port, Clock: runClock})
	if err != nil {
		return err
	}
	surface := &recovery.Surface{
		Port:      rig.port,
		Attempter: rig.attempter(),
		Notifier:  notifier,
		Recorder:  recorder,
		Card:      *card,
		Currency:  "INR",
	}

	arm, err := recovery.NewArm(*armID, recovery.ArmOptions{
		Surface:           surface,
		Store:             ledgerStore,
		Policy:            policy.New(policy.Config{}, runClock),
		KillSwitchEngaged: engaged,
	})
	if err != nil {
		return err
	}

	p, err := poller.New(poller.Options{
		Port:       rig.port,
		Clock:      runClock,
		Wait:       rig.wait,
		Interval:   rig.pollInterval,
		MaxBackoff: rig.pollInterval,
		MaxWait:    rig.pollMaxWait,
	})
	if err != nil {
		return err
	}
	orchestrator, err := recovery.New(recovery.Options{
		Port:     rig.port,
		Poller:   p,
		Recorder: recorder,
		Action:   arm.Act,
		Tracer:   rig.tracer,
		Clock:    runClock,
	})
	if err != nil {
		return err
	}

	runID := filepath.Base(strings.TrimRight(*runDir, string(os.PathSeparator)))
	outFile, err := os.OpenFile(filepath.Join(armDir, "outcomes.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create the outcomes file: %w", err)
	}
	defer func() { _ = outFile.Close() }()
	out := bufio.NewWriter(outFile)

	fmt.Printf("run      %s\n", runID)
	fmt.Printf("arm      %s\n", *armID)
	fmt.Printf("layer    %s%s\n", *layer, layerCaveat(*layer))
	fmt.Printf("batch    %s, %d orders\n", batchFile.BatchID, len(orders))
	fmt.Println()

	var acted, recovered, escalated, unobserved, armCalls, offPort int
	for _, o := range orders {
		before := rig.calls()
		outcome, procErr := orchestrator.ProcessOrder(ctx, o.visible)

		row := OutcomeRow{
			RunID:            runID,
			Arm:              *armID,
			Layer:            *layer,
			BatchID:          batchFile.BatchID,
			ManifestOrderID:  o.manifestID,
			GatewayOrderID:   o.visible.OrderID,
			Class:            outcome.Class.String(),
			ActionKind:       outcome.ActionKind,
			FinalOrderStatus: outcome.FinalOrderStatus,
			Recovered:        outcome.Recovered,
			ClaimedRecovered: outcome.ClaimedRecovered,
			AmountPaidPaise:  outcome.AmountPaidPaise,
			AttemptsSeen:     outcome.AttemptsSeen,
			AttemptsAfter:    ledgerStore.Attempts(o.visible.OrderID),
			PolicyVerdict:    outcome.PolicyVerdict,
			PolicyRule:       outcome.PolicyRule,
			Escalated:        outcome.Escalated,
			SideEffect:       outcome.SideEffect,
			TimedOut:         outcome.TimedOut,
			Observed:         outcome.FinalOrderStatus != "",
			// Port calls plus the ones an attempt made outside Port. The
			// second half is why this is not just the counting port's delta:
			// a payment attempt is four checkout calls on the live layer and
			// Port has no method for any of them.
			APICalls: rig.calls() - before + outcome.OffPortCalls,
		}
		if procErr != nil {
			// The error is redacted by internal/razorpay before it gets here,
			// and audit redaction is the backstop. An order whose cycle errored
			// still writes a row: a run that drops its failures reports a
			// recovery rate over the orders that happened to work.
			row.Error = procErr.Error()
		}
		if err := writeJSONLine(out, row); err != nil {
			return err
		}

		if row.ActionKind != recovery.ActionNone && row.ActionKind != "" {
			acted++
		}
		if row.Recovered {
			recovered++
		}
		if row.Escalated {
			escalated++
		}
		if !row.Observed {
			unobserved++
		}
		armCalls += row.APICalls
		offPort += outcome.OffPortCalls

		status := row.FinalOrderStatus
		if status == "" {
			status = "unobserved"
		}
		fmt.Printf("  %-18s %-24s %-22s %-9s %s\n",
			o.manifestID, row.Class, row.ActionKind, status, verdictLabel(row))
	}
	if err := out.Flush(); err != nil {
		return fmt.Errorf("flush the outcomes file: %w", err)
	}

	fmt.Println()
	// Two call counts, because they answer different questions. The arm's is
	// what the arm cost and is what the report's cost column carries. The
	// run's adds the materialisation, which is the harness building the world
	// before any arm sees it.
	//
	// The run total has to add the off-port calls back: the counting port
	// cannot see a checkout attempt, because none of those four calls is a
	// Port method.
	fmt.Printf("orders %d  actions %d  recovered %d  escalated %d  unobserved %d\n",
		len(orders), acted, recovered, escalated, unobserved)
	fmt.Printf("gateway calls: %d by the arm, %d for the whole run including materialisation\n",
		armCalls, rig.calls()+offPort)
	fmt.Printf("outcomes %s\n", filepath.Join(armDir, "outcomes.jsonl"))
	fmt.Printf("ledger   %s\n", filepath.Join(armDir, "ledger.jsonl"))
	return nil
}

// verdictLabel is the short right-hand column: the rule that decided, or a
// note that nothing was asked.
func verdictLabel(row OutcomeRow) string {
	if row.PolicyRule == "" {
		if row.SideEffect {
			return "no policy consulted"
		}
		return ""
	}
	return row.PolicyVerdict + " " + row.PolicyRule
}

func layerCaveat(layer string) string {
	if layer == layerLive {
		return "  (Razorpay TEST MODE, see docs/RAZORPAY-TEST-MODE-NOTES.md)"
	}
	return "  (a model of documented behaviour, evidence about our code only)"
}

// orderSequence returns the manifest orders in the order the harness asked
// for. An empty path means manifest order.
func orderSequence(file *BatchFile, path string) ([]batch.Order, error) {
	if path == "" {
		return file.Orders, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the order sequence at %s: %w", path, err)
	}
	byID := make(map[string]batch.Order, len(file.Orders))
	for _, o := range file.Orders {
		byID[o.OrderID] = o
	}

	var out []batch.Order
	seen := make(map[string]bool, len(file.Orders))
	for _, line := range strings.Split(string(raw), "\n") {
		id := strings.TrimSpace(line)
		if id == "" {
			continue
		}
		o, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("the order sequence names %s, which is not in batch %s", id, file.BatchID)
		}
		if seen[id] {
			return nil, fmt.Errorf("the order sequence names %s twice", id)
		}
		seen[id] = true
		out = append(out, o)
	}
	if len(out) != len(file.Orders) {
		return nil, fmt.Errorf("the order sequence has %d orders and batch %s has %d",
			len(out), file.BatchID, len(file.Orders))
	}
	return out, nil
}

func writeJSONLine(w *bufio.Writer, v any) error {
	encoded, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode an outcome row: %w", err)
	}
	if _, err := w.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("write an outcome row: %w", err)
	}
	return nil
}

// seededAttempts is how many failed attempts an order arrives with.
//
// One for an ordinary order: the failure that put it in the batch. For the
// attempt-budget-exhausted bait it is the budget its class justifies, already
// spent, which is what makes another attempt wrong even though the class says
// retry.
func seededAttempts(o batch.Order) int {
	if o.PriorAttempts > 0 {
		return o.PriorAttempts
	}
	return 1
}

// recoversOnRetry is the gateway's answer to whether re-presenting the same
// instrument can work.
//
// It reads the manifest, and it is read by the gateway rather than by an arm.
// An order is recoverable by a retry when its ground truth says it is
// recoverable and the correct action is to retry. The two classes that need
// the customer back are not: the correct action there is to raise a payment
// link, and this project observes an API call and never a person, so nothing
// in it can model one coming back.
func recoversOnRetry(o batch.Order) bool {
	return o.GroundTruthRecoverable && o.GroundTruthCorrectAction == batch.ActionRetrySameInstrument
}

// gatewayRig is one layer's gateway plus everything built on it.
type gatewayRig struct {
	layer        string
	port         razorpay.Port
	fake         *razorpay.Fake
	live         *liveRig
	tracer       trace.Tracer
	clock        clock.Clock
	wait         poller.WaitFunc
	pollInterval time.Duration
	pollMaxWait  time.Duration
	apiCalls     *int
	// outcomes is the per-order settle outcome for the live layer, computed
	// from the manifest at materialisation time. It never reaches an arm: it
	// is handed to recovery.NewLiveAttempter, which keeps it unexported.
	outcomes map[string]string
}

func newGatewayRig(ctx context.Context, layer string, file *BatchFile, c clock.Clock) (*gatewayRig, error) {
	calls := 0
	rig := &gatewayRig{
		layer:    layer,
		clock:    c,
		apiCalls: &calls,
		outcomes: make(map[string]string),
		tracer:   noop.NewTracerProvider().Tracer(runTracerName),
	}

	if layer == layerFake {
		fake, err := razorpay.NewFake(razorpay.FakeOptions{Seed: file.Seed, Clock: c})
		if err != nil {
			return nil, err
		}
		rig.fake = fake
		rig.port = &countingPort{inner: fake, calls: &calls}
		// The fake settles instantly, so a poll never has to wait. The wait
		// still advances the clock: nothing else moves a fake clock, and a
		// poll run whose elapsed time never grows cannot reach its deadline.
		fakeClock, _ := c.(*clock.FakeClock)
		rig.wait = func(_ context.Context, d time.Duration) error {
			if fakeClock != nil {
				fakeClock.Advance(d)
			}
			return nil
		}
		rig.pollInterval = time.Millisecond
		rig.pollMaxWait = 3 * time.Millisecond
		return rig, nil
	}

	live, err := newLiveRig(ctx, "", nil, liveMaxConcurrent)
	if err != nil {
		return nil, err
	}
	rig.live = live
	rig.port = &countingPort{inner: live.client, calls: &calls}
	rig.tracer = live.telemetry.Tracer(runTracerName)
	// The order is already at attempted when the run starts: materialise
	// drove its failure and that settled synchronously. So the poll is a read
	// of current state rather than a wait for one, and three reads is enough
	// to see it. A longer budget would spend a minute per arm waiting for a
	// transition that has already happened.
	rig.pollInterval = 500 * time.Millisecond
	rig.pollMaxWait = 1200 * time.Millisecond
	return rig, nil
}

func (r *gatewayRig) close(ctx context.Context) {
	if r.live != nil {
		_ = r.live.Close()
	}
	_ = ctx
}

func (r *gatewayRig) calls() int { return *r.apiCalls }

// attempter returns the layer's adapter. Both keep the gateway's own settle
// schedule unexported, so an arm holding the Attempter interface cannot read
// how the world is going to decide.
//
// It has to be built after materialise, because materialise is what fills the
// live layer's outcome map.
func (r *gatewayRig) attempter() recovery.Attempter {
	if r.layer == layerFake {
		return recovery.NewFakeAttempter(r.fake)
	}
	return recovery.NewLiveAttempter(r.live.attempter, r.outcomes)
}

// materialise creates this arm's own copy of every manifest order in the
// gateway, with its seeded failure history already on it.
//
// Each arm gets its own copy on purpose. Three arms sharing one set of orders
// would mean the first arm to recover one changes what the next two see, and
// the three-arm table would be measuring the running order rather than the
// arms.
func (r *gatewayRig) materialise(ctx context.Context, orders []batch.Order) ([]materialised, error) {
	out := make([]materialised, 0, len(orders))

	for _, o := range orders {
		attempts := seededAttempts(o)

		var gatewayID string
		var visible batch.AgentVisibleOrder

		if r.layer == layerFake {
			// The materialisation calls go straight at the fake rather than
			// through the counting port, so they are counted here. Both
			// layers have to count them the same way or the run total means
			// one thing on one layer and another on the other.
			//
			// SeedRecoversAfter below is deliberately not counted. It
			// configures the gateway; it does not talk to one.
			created, err := r.fake.CreateOrder(ctx, razorpay.CreateOrderRequest{
				AmountPaise: o.AmountPaise,
				Currency:    o.Currency,
				Receipt:     o.Receipt,
			})
			*r.apiCalls++
			if err != nil {
				return nil, fmt.Errorf("materialise %s: %w", o.OrderID, err)
			}
			gatewayID = created.ID
			for range attempts {
				if _, err := r.fake.SeedFailedPayment(ctx, gatewayID, o.SeededErrorCode); err != nil {
					return nil, fmt.Errorf("seed the failure on %s: %w", o.OrderID, err)
				}
				*r.apiCalls++
			}
			if recoversOnRetry(o) {
				r.fake.SeedRecoversAfter(gatewayID, attempts)
			}
			visible = batch.AgentVisibleOrder{
				OrderID:     gatewayID,
				AmountPaise: created.AmountPaise,
				Currency:    created.Currency,
				Receipt:     created.Receipt,
			}
		} else {
			created, err := r.live.client.CreateOrder(ctx, razorpay.CreateOrderRequest{
				AmountPaise: o.AmountPaise,
				Currency:    o.Currency,
				Receipt:     receipt("rcpt_batch"),
				Notes:       map[string]string{"purpose": "phase-2 batch"},
			})
			*r.apiCalls++
			if err != nil {
				return nil, fmt.Errorf("materialise %s: %w", o.OrderID, err)
			}
			gatewayID = created.ID

			card := o.SeededCard
			if card == "" {
				// The risk-block reason has no card behind it (PRD Q2). The
				// card does not choose the outcome in test mode anyway: the
				// last checkout call does, and it is being told to fail.
				card = "4100280000080001"
			}
			for range attempts {
				if _, err := r.live.attempter.Attempt(ctx, razorpay.AttemptRequest{
					OrderID:     gatewayID,
					AmountPaise: created.AmountPaise,
					CardNumber:  card,
					Outcome:     razorpay.AttemptFail,
				}); err != nil {
					return nil, fmt.Errorf("seed the failure on %s: %w", o.OrderID, err)
				}
				*r.apiCalls += 4
			}

			r.outcomes[gatewayID] = razorpay.AttemptFail
			if recoversOnRetry(o) {
				r.outcomes[gatewayID] = razorpay.AttemptSucceed
			}
			visible = batch.AgentVisibleOrder{
				OrderID:     gatewayID,
				AmountPaise: created.AmountPaise,
				Currency:    created.Currency,
				Receipt:     created.Receipt,
			}
		}

		out = append(out, materialised{manifestID: o.OrderID, visible: visible, attempts: attempts})
	}
	return out, nil
}

// countingPort counts every call an arm makes through razorpay.Port, so the
// cost column in the report is a count of requests rather than an estimate.
//
// It wraps the port rather than the transport because the checkout sequence
// does not go through Port at all: those four calls per attempt are added by
// the attempter adapters, which is why Add is exported on this type.
type countingPort struct {
	inner razorpay.Port
	calls *int
}

func (c *countingPort) add(n int) { *c.calls += n }

func (c *countingPort) CreateOrder(ctx context.Context, req razorpay.CreateOrderRequest) (razorpay.Order, error) {
	c.add(1)
	return c.inner.CreateOrder(ctx, req)
}

func (c *countingPort) FetchOrder(ctx context.Context, orderID string) (razorpay.Order, error) {
	c.add(1)
	return c.inner.FetchOrder(ctx, orderID)
}

func (c *countingPort) ListPaymentsForOrder(ctx context.Context, orderID string) ([]razorpay.Payment, error) {
	c.add(1)
	return c.inner.ListPaymentsForOrder(ctx, orderID)
}

func (c *countingPort) FetchPayment(ctx context.Context, paymentID string) (razorpay.Payment, error) {
	c.add(1)
	return c.inner.FetchPayment(ctx, paymentID)
}

func (c *countingPort) CreatePaymentLink(ctx context.Context, req razorpay.CreatePaymentLinkRequest) (razorpay.PaymentLink, error) {
	c.add(1)
	return c.inner.CreatePaymentLink(ctx, req)
}

func (c *countingPort) ResendPaymentLinkNotification(ctx context.Context, linkID, medium string) (razorpay.NotifyReceipt, error) {
	c.add(1)
	return c.inner.ResendPaymentLinkNotification(ctx, linkID, medium)
}

var _ razorpay.Port = (*countingPort)(nil)
