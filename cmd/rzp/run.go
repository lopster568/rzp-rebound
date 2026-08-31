package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/audit"
	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/notify"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/lopster568/rzp-recovery-agent/internal/poller"
	"github.com/lopster568/rzp-recovery-agent/internal/recovery"
	"github.com/lopster568/rzp-recovery-agent/internal/runner"
	"github.com/lopster568/rzp-recovery-agent/internal/store"
)

// runRun runs one arm over one batch and writes its outcomes and its ledger.
func runRun(ctx context.Context, args []string) (runErr error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	armID := fs.String("arm", recovery.ArmRules, "which arm: a0-control, a1-naive, or a3-rules")
	layer := fs.String("layer", runner.LayerFake, "which gateway: fake or live")
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
	if *layer != runner.LayerFake && *layer != runner.LayerLive {
		return fmt.Errorf("-layer is %q, want %q or %q", *layer, runner.LayerFake, runner.LayerLive)
	}

	batchFile, err := runner.ReadBatchFile(*batchPath)
	if err != nil {
		return err
	}
	ordered, err := runner.OrderSequence(batchFile, *sequencePath)
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
	defer func() {
		if err := ledger.Close(); err != nil && runErr == nil {
			runErr = fmt.Errorf("close the ledger: %w", err)
		}
	}()

	// The fake layer runs on a fake clock started at a fixed instant, so a
	// seed reproduces a run exactly. The live layer runs on the wall clock,
	// because real time passes between real API calls whatever this process
	// thinks.
	var runClock clock.Clock = clock.Real()
	if *layer == runner.LayerFake {
		runClock = clock.NewFake(time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC))
	}

	recorder, err := audit.NewRecorder(audit.Options{Writer: ledger, Clock: runClock})
	if err != nil {
		return err
	}

	rig, err := runner.NewGatewayRig(ctx, *layer, batchFile, runClock)
	if err != nil {
		return err
	}
	defer rig.Close(ctx)

	orders, err := rig.Materialise(ctx, ordered)
	if err != nil {
		return err
	}

	ledgerStore := store.New(runClock)
	for _, o := range orders {
		ledgerStore.Observe(o.Visible.OrderID, o.Attempts)
	}

	notifier, err := notify.New(notify.Options{Port: rig.Port, Clock: runClock})
	if err != nil {
		return err
	}
	surface := &recovery.Surface{
		Port:      rig.Port,
		Attempter: rig.Attempter(),
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
		Port:       rig.Port,
		Clock:      runClock,
		Wait:       rig.Wait(),
		Interval:   rig.PollInterval(),
		MaxBackoff: rig.PollInterval(),
		MaxWait:    rig.PollMaxWait(),
	})
	if err != nil {
		return err
	}
	orchestrator, err := recovery.New(recovery.Options{
		Port:     rig.Port,
		Poller:   p,
		Recorder: recorder,
		Action:   arm.Act,
		Tracer:   rig.Tracer,
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
	out := bufio.NewWriter(outFile)
	// Flush before close, and let a failure in either reach the caller. The
	// deferred close alone discarded its error, so a run that stopped
	// mid-loop shipped a truncated outcomes.jsonl and said nothing: up to a
	// buffer's worth of rows were still in memory and the scorer would have
	// read the file as a shorter run. Review finding, 2026-08-31.
	defer func() {
		if err := out.Flush(); err != nil && runErr == nil {
			runErr = fmt.Errorf("flush the outcomes file: %w", err)
		}
		if err := outFile.Close(); err != nil && runErr == nil {
			runErr = fmt.Errorf("close the outcomes file: %w", err)
		}
	}()

	fmt.Printf("run      %s\n", runID)
	fmt.Printf("arm      %s\n", *armID)
	fmt.Printf("layer    %s%s\n", *layer, layerCaveat(*layer))
	fmt.Printf("batch    %s, %d orders\n", batchFile.BatchID, len(orders))
	fmt.Println()

	var acted, recovered, escalated, unobserved, armCalls, offPort int
	for _, o := range orders {
		before := rig.Calls()
		outcome, procErr := orchestrator.ProcessOrder(ctx, o.Visible)

		row := runner.OutcomeRow{
			RunID:            runID,
			Arm:              *armID,
			Layer:            *layer,
			BatchID:          batchFile.BatchID,
			ManifestOrderID:  o.ManifestID,
			GatewayOrderID:   o.Visible.OrderID,
			Class:            outcome.Class.String(),
			ActionKind:       outcome.ActionKind,
			FinalOrderStatus: outcome.FinalOrderStatus,
			Recovered:        outcome.Recovered,
			ClaimedRecovered: outcome.ClaimedRecovered,
			AmountPaidPaise:  outcome.AmountPaidPaise,
			AttemptsSeen:     outcome.AttemptsSeen,
			AttemptsAfter:    ledgerStore.Attempts(o.Visible.OrderID),
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
			APICalls: rig.Calls() - before + outcome.OffPortCalls,
		}
		if procErr != nil {
			// The error is redacted by internal/razorpay before it gets here,
			// and audit redaction is the backstop. An order whose cycle errored
			// still writes a row: a run that drops its failures reports a
			// recovery rate over the orders that happened to work.
			row.Error = procErr.Error()
		}
		if err := runner.WriteJSONLine(out, row); err != nil {
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
			o.ManifestID, row.Class, row.ActionKind, status, verdictLabel(row))
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
		armCalls, rig.Calls()+offPort)
	fmt.Printf("outcomes %s\n", filepath.Join(armDir, "outcomes.jsonl"))
	fmt.Printf("ledger   %s\n", filepath.Join(armDir, "ledger.jsonl"))
	return nil
}

// verdictLabel is the short right-hand column: the rule that decided, or a
// note that nothing was asked.
func verdictLabel(row runner.OutcomeRow) string {
	if row.PolicyRule == "" {
		if row.SideEffect {
			return "no policy consulted"
		}
		return ""
	}
	return row.PolicyVerdict + " " + row.PolicyRule
}

func layerCaveat(layer string) string {
	if layer == runner.LayerLive {
		return "  (Razorpay TEST MODE, see docs/RAZORPAY-TEST-MODE-NOTES.md)"
	}
	return "  (a model of documented behaviour, evidence about our code only)"
}
