package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/audit"
	"github.com/lopster568/rzp-recovery-agent/internal/batch"
	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/notify"
	"github.com/lopster568/rzp-recovery-agent/internal/poller"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/recovery"
	"github.com/lopster568/rzp-recovery-agent/internal/runner"
	"go.opentelemetry.io/otel/trace"
)

// demoTracerName names the tracer the demo's own stages open spans on. The
// orchestrator has its own, so a trace shows which package produced which
// span.
const demoTracerName = "github.com/lopster568/rzp-recovery-agent/cmd/rzp"

// runDemo drives one order through the whole loop against Razorpay test mode:
// create, fail it, poll, classify, act, read the outcome back.
//
// Everything happens inside one root span, so one trace holds every stage, and
// every audit row carries that trace id.
func runDemo(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	card := fs.String("card", "4100280000080001", "card number for both attempts")
	secondOutcome := fs.String("second-attempt", razorpay.AttemptSucceed,
		"what the mock bank is told to do with the second attempt: S or F")
	ledgerPath := fs.String("ledger", "", "where the audit ledger goes (default: results/runs/demo-<unix>.jsonl)")
	amountPaise := fs.Int64("amount", 100000, "order amount in paise")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *secondOutcome != razorpay.AttemptSucceed && *secondOutcome != razorpay.AttemptFail {
		return fmt.Errorf("-second-attempt is %q, want %q or %q",
			*secondOutcome, razorpay.AttemptSucceed, razorpay.AttemptFail)
	}

	rig, err := runner.NewLiveRig(ctx, "", nil, 0)
	if err != nil {
		return err
	}
	defer func() { _ = rig.Close() }()

	ledger, path, err := openLedger(*ledgerPath)
	if err != nil {
		return err
	}
	defer func() { _ = ledger.Close() }()

	recorder, err := audit.NewRecorder(audit.Options{Writer: ledger})
	if err != nil {
		return err
	}

	tracer := rig.Telemetry.Tracer(demoTracerName)
	ctx, root := tracer.Start(ctx, "demo.recovery_loop")
	// root.End and the exporter flush both happen explicitly at the bottom,
	// before the trace link is printed. This defer is the safety net for the
	// error paths in between, and it is ordered correctly for them: defers run
	// last in first out, so this ends the root span and the rig.Close above
	// then drains the exporter. Both are safe to run twice, because Span.End
	// ignores a second call and Provider.Shutdown is behind a sync.Once.
	defer root.End()

	traceID := trace.SpanContextFromContext(ctx).TraceID().String()

	fmt.Println("rzp demo, against Razorpay TEST MODE")
	fmt.Println("  gateway  live test mode")
	fmt.Printf("  traces   %s\n", rig.Telemetry.ExporterKind)
	fmt.Printf("  ledger   %s\n", path)
	fmt.Println()

	// Stage 1. Create the order.
	order, err := rig.Client.CreateOrder(ctx, razorpay.CreateOrderRequest{
		AmountPaise: *amountPaise,
		Currency:    "INR",
		Receipt:     runner.Receipt("rcpt_demo"),
		Notes:       map[string]string{"purpose": "phase-1 demo"},
	})
	if err != nil {
		return fmt.Errorf("create the order: %w", err)
	}
	fmt.Printf("1. created  %s  %d paise  status=%s\n", order.ID, order.AmountPaise, order.Status)

	// Stage 2. Fail it, through the checkout sequence the spike found.
	first, err := rig.Attempter.Attempt(ctx, razorpay.AttemptRequest{
		OrderID:     order.ID,
		AmountPaise: *amountPaise,
		CardNumber:  *card,
		Outcome:     razorpay.AttemptFail,
	})
	if err != nil {
		return fmt.Errorf("drive the first attempt: %w", err)
	}
	fmt.Printf("2. attempt  %s  card %s  mock bank told: decline\n", first.PaymentID, maskCard(*card))
	fmt.Println("3. loop     poll, classify, act, then read the order back out of the gateway")

	// Stages 3 to 6 are the orchestrator's: poll, classify, act, read the
	// outcome back out of the gateway.
	p, err := poller.New(poller.Options{
		Port:     rig.Client,
		Interval: time.Second,
		MaxWait:  30 * time.Second,
	})
	if err != nil {
		return err
	}

	notifier, err := notify.New(notify.Options{Port: rig.Client})
	if err != nil {
		return err
	}

	action := recoveryAction{
		rig:           rig,
		recorder:      recorder,
		notifier:      notifier,
		amountPaise:   *amountPaise,
		card:          *card,
		secondOutcome: *secondOutcome,
	}

	orchestrator, err := recovery.New(recovery.Options{
		Port:     rig.Client,
		Poller:   p,
		Recorder: recorder,
		Tracer:   tracer,
		Action:   action.run,
	})
	if err != nil {
		return err
	}

	outcome, err := orchestrator.ProcessOrder(ctx, batch.AgentVisibleOrder{
		OrderID:     order.ID,
		AmountPaise: order.AmountPaise,
		Currency:    order.Currency,
		Receipt:     order.Receipt,
	})
	if err != nil {
		fmt.Printf("!  the cycle returned an error: %v\n", err)
	}

	fmt.Printf("4. class    the failure classified as %s\n", outcome.Class)
	fmt.Printf("5. acted    %s\n", outcome.ActionKind)
	fmt.Printf("6. outcome  order %s reads %s from the gateway, recovered=%v (the action claimed %v)\n",
		outcome.OrderID, outcome.FinalOrderStatus, outcome.Recovered, outcome.ClaimedRecovered)
	fmt.Println()

	// The rows come out of the ledger rather than out of Outcome.Events. The
	// orchestrator only keeps the rows it wrote itself, and the action writes
	// one of its own, so reading the file is the only way to print the trail
	// a scoring pass would actually see.
	rows, err := readLedger(path)
	if err != nil {
		return err
	}
	fmt.Printf("audit rows written: %d, every one carrying trace_id %s\n", len(rows), traceID)
	for _, row := range rows {
		if row.TraceID != traceID {
			return fmt.Errorf("ledger row %d carries trace_id %s, not the run's %s",
				row.Sequence, row.TraceID, traceID)
		}
		fmt.Printf("   %d %-24s %s\n", row.Sequence, row.Kind, row.Class)
	}
	fmt.Println()

	// The honesty line. It is the most important thing this command prints.
	fmt.Println("What this run is and is not evidence of:")
	fmt.Println("  The order state above was read back from Razorpay after the action, not")
	fmt.Println("  reported by the action. That part is real.")
	fmt.Printf("  The outcome of each attempt was chosen by this command and sent to the mock\n")
	fmt.Printf("  bank as a single form field (first attempt: decline, second attempt: %s).\n", describeOutcome(*secondOutcome))
	fmt.Println("  Test mode has no other mechanism, so no recovery rate from this layer is")
	fmt.Println("  evidence that the agent's decision was the reason a payment recovered.")
	fmt.Println("  See docs/RAZORPAY-TEST-MODE-NOTES.md and ADR-0004.")
	fmt.Println()

	// End the root span and drain the exporter before printing the link. The
	// deferred version of this would have printed a URL for spans that had not
	// reached Jaeger yet, so a reviewer clicking it immediately would get an
	// empty trace and reasonably conclude the run had not been traced.
	root.End()
	if err := rig.Close(); err != nil {
		return fmt.Errorf("flush the trace exporter: %w", err)
	}

	if url := rig.TraceURL(traceID); url != "" {
		fmt.Printf("trace: %s\n", url)
	} else {
		fmt.Println("trace: no trace id was recorded, so there is no link to print")
	}
	return nil
}

// recoveryAction is the move the orchestrator makes on a failed order: raise a
// payment link, ask Razorpay to send it, and make a second attempt.
//
// Phase 2 puts policy.Evaluate in front of this, per ADR-0003. Phase 1 has no
// gate, and the action says so in its own audit detail rather than leaving a
// reader to assume there was one.
type recoveryAction struct {
	rig           *runner.LiveRig
	recorder      *audit.Recorder
	notifier      *notify.Notifier
	amountPaise   int64
	card          string
	secondOutcome string
}

func (a recoveryAction) run(ctx context.Context, order batch.AgentVisibleOrder, class classify.Class) (recovery.ActionResult, error) {
	result := recovery.ActionResult{
		Kind: recovery.ActionRetrySameInstrument,
		Detail: map[string]string{
			"policy_gate":      "none in phase 1, see ADR-0003",
			"classified_as":    class.String(),
			"retry_eligible":   strconv.FormatBool(class.IsRetryEligible()),
			"attempt_via":      "checkout sequence, see docs/RAZORPAY-TEST-MODE-NOTES.md",
			"outcome_selected": "by the operator, not by the gateway",
		},
	}

	// 1. Raise a payment link. Notification flags stay off: this creates the
	// link, and the resend call below is the one that asks Razorpay to send
	// it, which keeps the two observable events separate in the trail.
	link, err := a.rig.Client.CreatePaymentLink(ctx, razorpay.CreatePaymentLinkRequest{
		AmountPaise: a.amountPaise,
		Currency:    "INR",
		Description: "recovery for " + order.OrderID,
		ReferenceID: runner.Receipt("ref_demo"),
	})
	if err != nil {
		return result, fmt.Errorf("create a payment link for %s: %w", order.OrderID, err)
	}
	result.Detail["payment_link_id"] = link.ID
	fmt.Printf("   -> link    %s created\n", link.ID)

	// 2. Ask Razorpay to send it, and record what the API said. The phrase in
	// the row comes from a constant in internal/notify, and every one of those
	// is about an API call.
	sendReceipt, sendErr := a.notifier.SendPaymentLink(ctx, link.ID, razorpay.MediumEmail)
	result.Detail["notification_audit_phrase"] = sendReceipt.AuditPhrase
	result.Detail["notification_delivery_confirmed"] = strconv.FormatBool(sendReceipt.DeliveryConfirmed)
	if _, err := a.recorder.Record(ctx, audit.Event{
		OrderID:        order.OrderID,
		Kind:           audit.KindNotificationRequested,
		Class:          class.String(),
		ProposedAction: recovery.ActionRetrySameInstrument,
		Detail: map[string]string{
			"payment_link_id":    link.ID,
			"medium":             razorpay.MediumEmail,
			"audit_phrase":       sendReceipt.AuditPhrase,
			"api_call_succeeded": strconv.FormatBool(sendReceipt.APICallSucceeded),
			"delivery_confirmed": strconv.FormatBool(sendReceipt.DeliveryConfirmed),
		},
	}); err != nil {
		return result, err
	}
	if sendErr != nil {
		return result, fmt.Errorf("resend the payment link for %s: %w", order.OrderID, sendErr)
	}
	fmt.Printf("   -> notify  %s (delivery_confirmed=%v)\n", sendReceipt.AuditPhrase, sendReceipt.DeliveryConfirmed)

	// 3. The second attempt. This is what the 2026-08-31 spike unlocked: the
	// loop can close in test mode without a browser.
	second, err := a.rig.Attempter.Attempt(ctx, razorpay.AttemptRequest{
		OrderID:     order.OrderID,
		AmountPaise: a.amountPaise,
		CardNumber:  a.card,
		Outcome:     a.secondOutcome,
	})
	if err != nil {
		result.Detail["second_attempt_error"] = err.Error()
		return result, fmt.Errorf("drive the second attempt on %s: %w", order.OrderID, err)
	}
	result.Detail["second_payment_id"] = second.PaymentID
	result.Detail["second_attempt_told_bank"] = describeOutcome(a.secondOutcome)
	fmt.Printf("   -> retry   %s  mock bank told: %s\n", second.PaymentID, describeOutcome(a.secondOutcome))

	// ClaimedRecovered is what the action believes. The orchestrator records
	// it and then goes and reads the order, which is the only number that
	// counts.
	result.ClaimedRecovered = a.secondOutcome == razorpay.AttemptSucceed
	return result, nil
}

func describeOutcome(outcome string) string {
	if outcome == razorpay.AttemptSucceed {
		return "authorize"
	}
	return "decline"
}

// maskCard keeps a card number out of stdout. Nothing this project prints
// needs the middle digits, and a terminal scrollback is a place data goes to
// live forever.
func maskCard(number string) string {
	if len(number) <= 4 {
		return "****"
	}
	return "****" + number[len(number)-4:]
}

// readLedger reads back the rows a run wrote, so the summary reflects the file
// a scoring pass reads rather than what one component remembers writing.
func readLedger(path string) ([]audit.Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read the ledger at %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var rows []audit.Record
	scanner := bufio.NewScanner(f)
	// A detail value can hold an error string, so a row is not bounded by the
	// 64KB default. Review finding, 2026-08-31.
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var row audit.Record
		if err := json.Unmarshal(line, &row); err != nil {
			return nil, fmt.Errorf("parse a ledger row in %s: %w", path, err)
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan %s: %w", path, err)
	}
	return rows, nil
}

// openLedger creates the JSONL file the audit recorder writes to. The default
// lives under results/runs/, which is gitignored: a run's output is evidence
// for whoever ran it, not a committed artefact.
func openLedger(path string) (*os.File, string, error) {
	if path == "" {
		path = filepath.Join("results", "runs", fmt.Sprintf("demo-%d.jsonl", time.Now().UTC().Unix()))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, "", fmt.Errorf("make %s: %w", filepath.Dir(path), err)
	}
	// O_EXCL rather than os.Create. A run whose ledger path already exists is
	// a run about to destroy the evidence from an earlier one, and the default
	// path carries a unix timestamp precisely so it never collides. Review
	// finding, 2026-08-31.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, "", fmt.Errorf("create %s (a ledger is never overwritten): %w", path, err)
	}
	return f, path, nil
}
