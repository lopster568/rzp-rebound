// Command seedbook seeds live Razorpay test-mode data for the Unified
// Revenue-at-Risk Engine demo: overdue invoices, abandoned orders, and a
// manifest recording the ground truth an eval scorer reads.
//
// It talks to Razorpay test mode only, the same way every other command
// under cmd/rzp does: RAZORPAY_KEY_ID and RAZORPAY_KEY_SECRET from the
// environment, nothing else. See internal/seed for what it seeds and does
// not: the one class it cannot create through the API at all is failed
// payments, and the manifest's own instructions block, printed at the end of
// a run, is what tells the operator how to make some by hand.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/config"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/seed"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:]); err != nil {
		// razorpay.Client.Redact has already scrubbed any credential out of an
		// error by the time it reaches here, the same guarantee cmd/rzp's own
		// main relies on.
		fmt.Fprintf(os.Stderr, "seedbook: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("seedbook", flag.ContinueOnError)
	profileName := fs.String("profile", "demo", "which seed profile to build")
	runTag := fs.String("run-tag", "",
		"tag stamped into every created resource's notes, so a second run is distinguishable from the first (default: seedbook-<unix time>)")
	out := fs.String("out", "seedbook.json", "where the manifest is written")
	dryRun := fs.Bool("dry-run", false, "print the plan and make no API calls")
	callBudget := fs.Int("call-budget", seed.DefaultCallBudget, "refuse to make more than this many Razorpay calls in one run")
	randSeed := fs.Int64("seed", 1234, "seed for the deterministic synthetic data: names, amounts, contacts")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `usage: seedbook [flags]

Seeds live Razorpay test-mode data for the risk-engine demo and writes a
manifest recording it. Reads RAZORPAY_KEY_ID and RAZORPAY_KEY_SECRET from the
environment; test-mode keys only.

flags:
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	profile, ok := seed.ProfileByName(*profileName)
	if !ok {
		return fmt.Errorf("no seed profile named %q, want %q", *profileName, "demo")
	}

	tag := *runTag
	if tag == "" {
		tag = fmt.Sprintf("seedbook-%d", time.Now().UTC().Unix())
	}

	plan := seed.GeneratePlan(profile, tag, *randSeed)

	if *dryRun {
		printPlan(plan, *callBudget)
		return nil
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

	fmt.Printf("seedbook: seeding against Razorpay TEST MODE, run tag %s, profile %s\n\n", tag, profile.Name)

	manifest, runErr := seed.ExecutePlan(ctx, client, plan, seed.RunOptions{CallBudget: *callBudget})
	manifest.Profile = profile.Name

	if writeErr := manifest.Write(*out); writeErr != nil {
		if runErr != nil {
			return fmt.Errorf("write the manifest: %w (the seed run itself also failed: %v)", writeErr, runErr)
		}
		return fmt.Errorf("write the manifest: %w", writeErr)
	}

	printSummary(manifest, *out)

	if runErr != nil {
		return fmt.Errorf("the seed run stopped early; the manifest at %s records what it created before that: %w", *out, runErr)
	}
	return nil
}

// printPlan is what -dry-run prints: what would be created, and nothing more.
// No API call is made anywhere on this path.
func printPlan(plan seed.Plan, callBudget int) {
	fmt.Printf("seedbook dry run, run tag %s (no API calls made)\n\n", plan.RunTag)

	fmt.Println("invoices:")
	fmt.Printf("  %-16s %-24s %-10s %-9s %-10s %-8s %-12s\n",
		"age_bucket", "customer", "amount", "disputed", "no_contact", "plan", "partial_pay")
	for _, inv := range plan.Invoices {
		fmt.Printf("  %-16s %-24s %-10s %-9v %-10v %-8v %-12v\n",
			inv.AgeBucket, inv.CustomerName, formatPaise(inv.AmountPaise),
			inv.Disputed, inv.CustomerContact == "", inv.PartialPlan, inv.PartialPayment)
	}

	fmt.Println("\norders (abandoned, left untouched):")
	for i, ord := range plan.Orders {
		fmt.Printf("  %d. %s\n", i+1, formatPaise(ord.AmountPaise))
	}

	calls := plan.CallEstimate()
	fmt.Printf("\nestimated API calls: %d (budget %d)\n", calls, callBudget)
	if calls > callBudget {
		fmt.Println("this plan would exceed the call budget; raise -call-budget or use a smaller profile")
	}
}

// printSummary is what a real run prints: what was actually created, where
// the manifest went, and the operator instructions for failed payments.
func printSummary(m seed.Manifest, outPath string) {
	fmt.Printf("run tag       %s\n", m.RunTag)
	fmt.Printf("profile       %s\n", m.Profile)
	fmt.Printf("gateway       %s\n", m.Gateway)
	fmt.Printf("calls used    %d / %d\n\n", m.CallsUsed, m.CallBudget)

	fmt.Println("seeded:")
	for _, item := range m.Items {
		fmt.Printf("  %-8s %-16s age=%-6s %-10s %s\n",
			item.Kind, item.ID, item.AgeBucket, formatPaise(item.AmountPaise), describeFlags(item.Flags))
	}
	fmt.Printf("\nmanifest      %s\n\n", outPath)

	if len(m.Instructions.Targets) == 0 && len(m.Instructions.TestCards) == 0 {
		return
	}
	fmt.Println(m.Instructions.Headline)
	if len(m.Instructions.Targets) > 0 {
		fmt.Println("\nfail these by hand, in a browser:")
		for _, t := range m.Instructions.Targets {
			fmt.Printf("  %-16s %s\n", t.ID, t.URL)
		}
	}
	if len(m.Instructions.TestCards) > 0 {
		fmt.Println("\nusing one of these documented test-mode cards (any future expiry, any CVV):")
		for _, c := range m.Instructions.TestCards {
			fmt.Printf("  %-20s %s\n", c.Number, c.ErrorCode)
		}
	}
}

func describeFlags(f seed.Flags) string {
	var parts []string
	if f.Disputed {
		parts = append(parts, "disputed")
	}
	if f.NoContact {
		parts = append(parts, "no_contact")
	}
	if f.PartialPlan {
		parts = append(parts, "partial_plan")
	}
	return strings.Join(parts, ",")
}

func formatPaise(p int64) string {
	return fmt.Sprintf("INR %.2f", float64(p)/100)
}
