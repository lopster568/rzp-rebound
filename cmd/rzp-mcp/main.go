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
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/lopster568/rzp-recovery-agent/internal/runner"
)

// ArmAgent is the arm id for the LLM arm. a2 was reserved for it in phase 2 so
// a table from either phase can be read next to the other.
const ArmAgent = "a2-agent"

// defaultCard is the instrument every retry re-presents. It is the same value
// cmd/rzp run defaults to, because an arm that re-presented a different card
// would be a different experiment.
const defaultCard = "4100280000080001"

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

func run(ctx context.Context, args []string) error {
	if _, err := parseFlags(args); err != nil {
		return err
	}
	_ = ctx
	return errors.New("serving is not implemented yet")
}
