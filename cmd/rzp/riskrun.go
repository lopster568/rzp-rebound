package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/audit"
	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/config"
	"github.com/lopster568/rzp-recovery-agent/internal/detect"
	"github.com/lopster568/rzp-recovery-agent/internal/intervene"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/lopster568/rzp-recovery-agent/internal/promise"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/riskrun"
	"github.com/lopster568/rzp-recovery-agent/internal/seed"
)

// The three files a risk run leaves behind, plus the escalation queue.
const (
	riskLedgerFile      = "ledger.jsonl"
	riskResultsFile     = "results.jsonl"
	riskSummaryFile     = "summary.json"
	riskEscalationsFile = "escalations.jsonl"
)

// runRiskRun is the pipeline: detect, dedupe, gate, and, in the engine arm,
// intervene, over the ground truth a seedbook manifest records.
func runRiskRun(ctx context.Context, args []string) (runErr error) {
	fs := flag.NewFlagSet("risk-run", flag.ContinueOnError)
	manifestPath := fs.String("manifest", "seedbook.json", "the seedbook manifest this run is about")
	outDir := fs.String("out", "", "the directory the ledger, the results, and the summary go in (default: results/risk-runs/<run tag>)")
	runTag := fs.String("run-tag", "", "tag for this run (default: risk-<unix time>)")
	randSeed := fs.Int64("seed", 1234, "the seed the per-item arm assignment is drawn from")
	dryRun := fs.Bool("dry-run", false, "stop before any side-effecting call and read the manifest instead of Razorpay: no API call of any kind")
	replay := fs.Bool("replay", false, "an alias for -dry-run")
	simulateAge := fs.Bool("simulate-age", true,
		"measure an item's age against the manifest's simulated at-risk instant rather than Razorpay's own timestamp (nothing in the API can backdate an invoice)")
	detectGrace := fs.Duration("detect-grace", detect.DefaultGrace, "how long an issued invoice is left alone before the detector calls it overdue")
	killSwitchPath := fs.String("kill-switch-file", "", "a path whose existence halts every action")
	pageSize := fs.Int("page-size", detect.DefaultPageSize, "how many records each list call asks for")
	maxPages := fs.Int("max-pages", detect.DefaultMaxPages, "how many pages a sweep reads before it stops")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `usage: rzp risk-run [flags]

Runs the three detectors over the account, collapses the sightings that are one
debt, puts a proposed action for each through the policy gate, and executes what
the gate allowed. Items are split between two arms by a seeded shuffle
stratified by source: a0-control decides and executes nothing, a1-engine
executes.

-dry-run makes no API call of any kind. It replays the manifest through the real
detectors and the real gate and stops before the intervention engine.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	offline := *dryRun || *replay

	manifest, err := seed.ReadManifest(*manifestPath)
	if err != nil {
		return err
	}

	tag := *runTag
	if tag == "" {
		tag = fmt.Sprintf("risk-%d", time.Now().UTC().Unix())
	}
	dir := *outDir
	if dir == "" {
		dir = filepath.Join("results", "risk-runs", tag)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("make %s: %w", dir, err)
	}

	engaged, err := policy.KillSwitchFile(*killSwitchPath)
	if err != nil {
		return err
	}

	ledger, closeLedger, err := createFile(filepath.Join(dir, riskLedgerFile))
	if err != nil {
		return err
	}
	defer closeLedger(&runErr)
	results, closeResults, err := createFile(filepath.Join(dir, riskResultsFile))
	if err != nil {
		return err
	}
	defer closeResults(&runErr)
	escalationFile, closeEscalations, err := createFile(filepath.Join(dir, riskEscalationsFile))
	if err != nil {
		return err
	}
	defer closeEscalations(&runErr)

	runClock := clock.Real()
	recorder, err := audit.NewRecorder(audit.Options{Writer: ledger, Clock: runClock})
	if err != nil {
		return err
	}
	escalations, err := intervene.NewWriterSink(escalationFile)
	if err != nil {
		return err
	}

	opts := riskrun.Options{
		Manifest:     manifest,
		ManifestPath: *manifestPath,
		RunTag:       tag,
		Seed:         *randSeed,
		DryRun:       offline,
		SimulateAge:  *simulateAge,
		KillSwitch:   engaged,
		DetectConfig: detect.Config{
			PageSize: *pageSize,
			MaxPages: *maxPages,
			Grace:    *detectGrace,
			Clock:    runClock,
		},
		Clock:       runClock,
		Recorder:    recorder,
		Escalations: escalations,
		Promises:    promise.NewStore(),
		Results:     results,
		Log:         os.Stdout,
	}

	// The client is built only on the live path, so a dry run cannot reach
	// Razorpay even by accident: there is nothing here for it to reach it with,
	// and config.Load is not called, so a dry run needs no credentials at all.
	if !offline {
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if err := cfg.RequireLiveAccess(); err != nil {
			return err
		}
		client, err := razorpay.NewClient(razorpay.ClientOptions{
			KeyID:     cfg.RazorpayKeyID,
			KeySecret: cfg.RazorpayKeySecret,
		})
		if err != nil {
			return err
		}
		opts.API = client
		opts.Gateway = client
	}

	summary, runError := riskrun.Run(ctx, opts)

	// The summary is written whatever happened. A run that stopped partway has
	// still read real debt and, on the live path, may already have called
	// Razorpay, and a summary that is missing because the run failed is the one
	// a reviewer most needs.
	summaryPath := filepath.Join(dir, riskSummaryFile)
	if err := writeJSON(summaryPath, summary); err != nil {
		if runError != nil {
			return fmt.Errorf("write the summary: %w (the run itself also failed: %v)", err, runError)
		}
		return fmt.Errorf("write the summary: %w", err)
	}

	printRiskSummary(summary, dir)
	return runError
}

// runRiskPoll re-reads every manifest item and writes a snapshot.
//
// It is the other half of a recovered-paise figure. One snapshot says what the
// book looks like; two say what moved, and Diff is the subtraction. A snapshot
// writes nothing to Razorpay: every call it makes is a fetch.
func runRiskPoll(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("risk-poll", flag.ContinueOnError)
	manifestPath := fs.String("manifest", "seedbook.json", "the seedbook manifest to re-read")
	out := fs.String("out", "", "where the snapshot goes (default: snapshots/<manifest run tag>-<unix time>.json)")
	runDir := fs.String("run", "", "a risk-run output directory, so the payment links that run created are polled too")
	against := fs.String("against", "", "an earlier snapshot to diff this one against, which is what a recovered-paise figure is")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `usage: rzp risk-poll [flags]

Re-reads every entity a seedbook manifest created, plus any payment link a risk
run created, and writes what Razorpay reports about each: status, amount paid,
amount due. Every call is a fetch; nothing here writes to Razorpay.

Recovered paise is the difference between two of these, which is what -against
prints.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	manifest, err := seed.ReadManifest(*manifestPath)
	if err != nil {
		return err
	}

	links, err := paymentLinksFrom(*runDir)
	if err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.RequireLiveAccess(); err != nil {
		return err
	}
	client, err := razorpay.NewClient(razorpay.ClientOptions{
		KeyID:     cfg.RazorpayKeyID,
		KeySecret: cfg.RazorpayKeySecret,
	})
	if err != nil {
		return err
	}

	snapshot, err := riskrun.Poll(ctx, client, riskrun.PollOptions{
		Manifest:       manifest,
		ManifestPath:   *manifestPath,
		PaymentLinkIDs: links,
		Now:            time.Now(),
	})
	if err != nil {
		return err
	}

	path := *out
	if path == "" {
		name := manifest.RunTag
		if name == "" {
			name = "snapshot"
		}
		path = filepath.Join("snapshots", fmt.Sprintf("%s-%d.json", name, snapshot.TakenAt.Unix()))
	}
	if err := writeJSON(path, snapshot); err != nil {
		return err
	}

	fmt.Printf("snapshot  %s\n", path)
	fmt.Printf("taken at  %s\n", snapshot.TakenAt.Format(time.RFC3339))
	fmt.Printf("entities  %d read, %d could not be read\n", snapshot.Totals.Entries, snapshot.Totals.Errors)
	fmt.Printf("amount    %s gross, %s paid, %s due\n",
		formatPaise(snapshot.Totals.AmountPaise),
		formatPaise(snapshot.Totals.AmountPaidPaise),
		formatPaise(snapshot.Totals.AmountDuePaise))
	for _, entry := range snapshot.Entries {
		note := entry.Status
		if entry.Error != "" {
			note = "unreadable: " + entry.Error
		}
		fmt.Printf("  %-13s %-22s %-14s paid %s\n", entry.Kind, entry.ID, note, formatPaise(entry.AmountPaidPaise))
	}

	if *against == "" {
		return nil
	}
	earlier, err := readSnapshot(*against)
	if err != nil {
		return err
	}
	delta := riskrun.Diff(earlier, snapshot)
	fmt.Println()
	fmt.Printf("against   %s (%s)\n", *against, delta.FromTakenAt.Format(time.RFC3339))
	fmt.Printf("compared  %d entit(ies), %d unmatched or unreadable\n", delta.EntriesCompared, delta.EntriesUnmatched)
	fmt.Printf("collected %s more than the earlier snapshot reported\n", formatPaise(delta.RecoveredPaise))
	fmt.Printf("due       %s change\n", formatPaise(delta.AmountDueChange))
	for _, change := range delta.StatusChanges {
		fmt.Printf("  %s\n", change)
	}
	fmt.Println()
	fmt.Println("That figure is what Razorpay reports as collected between two reads. It is not")
	fmt.Println("a claim that this program caused any of it: a customer who paid for their own")
	fmt.Println("reasons moves the same number, which is what the control arm is for.")
	return nil
}

// paymentLinksFrom reads the payment link ids a risk run created out of its
// results file. An empty run directory means there are none to read.
func paymentLinksFrom(runDir string) ([]string, error) {
	if runDir == "" {
		return nil, nil
	}
	path := filepath.Join(runDir, riskResultsFile)
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read the risk run at %s: %w", path, err)
	}
	defer file.Close()

	var links []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var row riskrun.ItemResult
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			return nil, fmt.Errorf("parse a result row in %s: %w", path, err)
		}
		if row.HandleID != "" && row.ExecutedAction == "create_payment_link" {
			links = append(links, row.HandleID)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return links, nil
}

func readSnapshot(path string) (riskrun.Snapshot, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return riskrun.Snapshot{}, fmt.Errorf("read the earlier snapshot %s: %w", path, err)
	}
	var snapshot riskrun.Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return riskrun.Snapshot{}, fmt.Errorf("parse the earlier snapshot %s: %w", path, err)
	}
	return snapshot, nil
}

// createFile opens one output file and returns a closer that reports a close
// failure through the command's own named error, so a truncated ledger is not
// silently shipped.
func createFile(path string) (*os.File, func(*error), error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("create %s: %w", path, err)
	}
	return file, func(runErr *error) {
		if err := file.Close(); err != nil && *runErr == nil {
			*runErr = fmt.Errorf("close %s: %w", path, err)
		}
	}, nil
}

// printRiskSummary is the terminal view of the summary file. Every number here
// is read off the Summary, so the screen and the file cannot disagree.
func printRiskSummary(s riskrun.Summary, dir string) {
	fmt.Println()
	fmt.Printf("items      %d, from %d sighting(s) with %d merged by the dedupe\n",
		s.ItemsTotal, s.ItemsTotal+s.CollapsedAway, s.CollapsedAway)
	for _, source := range slices.Sorted(maps.Keys(s.ItemsBySource)) {
		fmt.Printf("  %-18s %d, %s outstanding\n", source, s.ItemsBySource[source], formatPaise(s.AmountDueBySource[source]))
	}

	fmt.Println("arms")
	for _, arm := range riskrun.Arms() {
		fmt.Printf("  %-18s %d\n", arm, s.ItemsByArm[arm])
	}

	fmt.Println("verdicts by rule, one per item")
	printVerdicts(s.VerdictsByRule)
	if len(s.EscalationVerdictsByRule) > 0 {
		// The second decision each escalating verdict raises. It is printed
		// apart from the first so that the counts above stay one per item: an
		// escalation is itself an action and goes through the gate, so folding
		// the two together would report more allows than there are items.
		fmt.Println("verdicts on the escalations those refusals raised")
		printVerdicts(s.EscalationVerdictsByRule)
	}

	if len(s.ActionsAccepted) > 0 {
		fmt.Println("actions accepted (the API call succeeded, not that anybody read anything)")
		for _, action := range slices.Sorted(maps.Keys(s.ActionsAccepted)) {
			fmt.Printf("  %-24s %d\n", action, s.ActionsAccepted[action])
		}
	}
	if len(s.Observables) > 0 {
		fmt.Println("observables")
		for _, observable := range slices.Sorted(maps.Keys(s.Observables)) {
			fmt.Printf("  %-28s %d\n", observable, s.Observables[observable])
		}
	}
	if len(s.Refusals) > 0 {
		fmt.Println("refusals from the intervention engine")
		for _, refusal := range slices.Sorted(maps.Keys(s.Refusals)) {
			fmt.Printf("  %d  %s\n", s.Refusals[refusal], refusal)
		}
	}
	fmt.Printf("escalations %d\n", s.Escalations)
	if s.Errors > 0 {
		fmt.Printf("errors      %d row(s) carry one\n", s.Errors)
	}

	fmt.Println()
	fmt.Printf("ledger      %s\n", filepath.Join(dir, riskLedgerFile))
	fmt.Printf("results     %s\n", filepath.Join(dir, riskResultsFile))
	fmt.Printf("escalations %s\n", filepath.Join(dir, riskEscalationsFile))
	fmt.Printf("summary     %s\n", filepath.Join(dir, riskSummaryFile))
}

// printVerdicts prints one rule-and-verdict table, sorted so two runs are
// diffable.
func printVerdicts(byRule map[string]map[string]int) {
	for _, rule := range slices.Sorted(maps.Keys(byRule)) {
		byVerdict := byRule[rule]
		for _, verdict := range slices.Sorted(maps.Keys(byVerdict)) {
			fmt.Printf("  %-9s %-28s %d\n", verdict, rule, byVerdict[verdict])
		}
	}
}

// formatPaise renders paise as rupees. It is display only; every stored number
// stays in paise.
func formatPaise(p int64) string { return fmt.Sprintf("INR %.2f", float64(p)/100) }
