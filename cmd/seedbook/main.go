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
	"errors"
	"flag"
	"fmt"
	"io/fs"
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
	pace := fs.Duration("pace", seed.DefaultPace,
		"wait this long between creation calls; it is politeness rather than the rate-limit fix, so a value small enough to be irrelevant, such as 1ns, is how a run takes the pacing off")
	burstWait := fs.Duration("burst-wait", seed.DefaultBurstWait,
		"wait this long and make the same call again when test mode answers 429 to an invoice creation; the limit is a burst quota of about five creations, not a rate, and it cleared in 45 to 60 seconds on 2026-09-05")
	force := fs.Bool("force", false,
		"seed a new book even though -out already holds a manifest, overwriting it; without this a run refuses rather than orphaning what the earlier manifest names")
	fs.Usage = func() {
		fmt.Fprint(fs.Output(), `usage: seedbook [flags]

Seeds live Razorpay test-mode data for the risk-engine demo and writes a
manifest recording it. Reads RAZORPAY_KEY_ID and RAZORPAY_KEY_SECRET from the
environment; test-mode keys only.

Creation calls are paced, and a POST /v1/invoices that answers 429 anyway is
waited out and made again inside this same run. Test mode enforces a burst quota
of about five invoice creations rather than a rate, observed three times on
2026-09-05, and it cleared in 45 to 60 seconds. The demo profile creates eight
invoices, so expect a wait or two and a run of a couple of minutes.

If -out already holds an unfinished manifest, this continues that run under its
own tag instead of seeding a second book beside it. A finished one is refused.
-force overwrites either.

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
	tagSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "run-tag" {
			tagSet = true
		}
	})

	if *dryRun {
		printPlan(seed.GeneratePlan(profile, tag, *randSeed), *callBudget)
		return nil
	}

	plan, resume, note, err := planFor(profile, tag, tagSet, *randSeed, *out, *force)
	if err != nil {
		return err
	}
	if note != "" {
		fmt.Println(note)
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

	invoicesLeft, ordersLeft, err := plan.Remaining(resume)
	if err != nil {
		return err
	}
	calls := invoicesLeft*3 + ordersLeft
	fmt.Printf("seedbook: seeding against Razorpay TEST MODE, run tag %s, profile %s\n", plan.RunTag, profile.Name)
	// The pace and what it costs, said out loud, because a seeder that looks
	// hung is the next thing an operator would interrupt. The burst wait is
	// named on the same line for the same reason: a run that goes quiet for a
	// whole minute has to have said in advance that it might.
	fmt.Printf("seedbook: %d call(s) to make, paced %s apart, so about %s\n",
		calls, *pace, (time.Duration(max(calls-1, 0)) * *pace).Round(time.Second))
	fmt.Printf("seedbook: plus %s per invoice burst quota hit, and this profile creates %d invoice(s) against a quota of about five\n\n",
		*burstWait, invoicesLeft)

	manifest, runErr := seed.ExecutePlan(ctx, client, plan, seed.RunOptions{
		CallBudget: *callBudget,
		Pace:       *pace,
		BurstWait:  *burstWait,
		Resume:     resume,
		Log:        os.Stdout,
	})
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

// planFor decides which run tag this invocation seeds under and which earlier
// manifest, if any, it continues, and returns the line to print about that
// decision.
//
// The rule it exists for: a seed run that stopped partway leaves real invoices
// and real orders in Razorpay, and a second `make seedbook` used to overwrite
// the only file that named them. That happened twice on 2026-09-05 and orphaned
// five invoices, five orders, and six customers, which then fell inside the
// next risk run's sweep window with nothing in the manifest to explain them.
//
// So an unfinished manifest at -out is continued rather than replaced, under
// its own run tag, unless the operator named a different one. A finished one is
// refused, because seeding a second book over the file that names the first is
// the same loss with a full manifest instead of a partial one. Both are
// overridable with -force, which is the operator saying they know what is on
// disk.
func planFor(profile seed.Profile, tag string, tagSet bool, randSeed int64, out string, force bool) (seed.Plan, seed.Manifest, string, error) {
	fresh := seed.GeneratePlan(profile, tag, randSeed)
	if force {
		return fresh, seed.Manifest{}, "", nil
	}
	if _, err := os.Stat(out); errors.Is(err, fs.ErrNotExist) {
		return fresh, seed.Manifest{}, "", nil
	} else if err != nil {
		return seed.Plan{}, seed.Manifest{}, "", fmt.Errorf("check whether %s already holds a manifest: %w", out, err)
	}

	prior, err := seed.ReadManifest(out)
	if err != nil {
		return seed.Plan{}, seed.Manifest{}, "", fmt.Errorf("%w\n%s exists but could not be read as a manifest. Move it aside, choose another -out, or pass -force to overwrite it", err, out)
	}

	// The prior run's own tag, unless the operator named one. Adopting it is
	// what makes the plan the same plan, which is what makes continuing it safe.
	planTag := tag
	if !tagSet && prior.RunTag != "" {
		planTag = prior.RunTag
	}
	plan := seed.GeneratePlan(profile, planTag, randSeed)

	invoicesLeft, ordersLeft, err := plan.Remaining(prior)
	if err != nil {
		return seed.Plan{}, seed.Manifest{}, "", fmt.Errorf(
			"%s holds a manifest this run cannot continue (%w).\nIt records run tag %q with %d item(s). Move it aside, choose another -out, or pass -force to overwrite it",
			out, err, prior.RunTag, len(prior.Items))
	}
	if invoicesLeft == 0 && ordersLeft == 0 {
		return seed.Plan{}, seed.Manifest{}, "", fmt.Errorf(
			"%s already records a complete run: tag %q, %d item(s).\nSeeding over it would leave those in Razorpay with nothing naming them. Choose another -out, or pass -force to overwrite it",
			out, prior.RunTag, len(prior.Items))
	}

	note := fmt.Sprintf(
		"seedbook: %s holds an unfinished run, tag %s, with %d item(s) created.\nseedbook: continuing that run rather than seeding a second book: %d invoice(s) and %d order(s) left.\nseedbook: pass -force to overwrite it instead, or -out to write a new book elsewhere.\n",
		out, prior.RunTag, len(prior.Items), invoicesLeft, ordersLeft)
	return plan, prior, note, nil
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
	fmt.Printf("calls used    %d / %d\n", m.CallsUsed, m.CallBudget)
	if m.ResumedItems > 0 {
		fmt.Printf("resumed       %d item(s) carried over from an earlier attempt at this run tag\n", m.ResumedItems)
	}
	fmt.Println()

	fmt.Println("seeded:")
	for _, item := range m.Items {
		id := item.ID
		if item.Incomplete {
			// No id exists to print. Naming the customer is the only handle
			// there is on what this item did leave in Razorpay.
			id = "(not created, customer " + item.CustomerID + ")"
		}
		fmt.Printf("  %-8s %-16s age=%-6s %-10s %s\n",
			item.Kind, id, item.AgeBucket, formatPaise(item.AmountPaise), describeFlags(item.Flags))
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
