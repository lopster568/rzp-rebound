// Command rzp is the CLI entrypoint for the recovery agent.
//
// Every subcommand here talks to Razorpay test mode. None of them is safe to
// point at a live key, and nothing in this repository will help you do that.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

// subcommands maps a name to what it runs.
var subcommands = map[string]func(ctx context.Context, args []string) error{
	"auth-probe": runAuthProbe,
	"capture":    runCapture,
	"demo":       runDemo,
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	run, ok := subcommands[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "rzp: %q is not a subcommand\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}

	if err := run(ctx, os.Args[2:]); err != nil {
		// Errors from internal/razorpay have been through Client.Redact by the
		// time they reach here, which is the control that keeps a credential
		// out of this line.
		fmt.Fprintf(os.Stderr, "rzp %s: %v\n", os.Args[1], err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: rzp <subcommand>

  auth-probe   Prove the configured test-mode credentials reach Razorpay
  capture      Capture real API responses into testdata/recorded/
  demo         Run the recovery loop end to end against test mode

Every subcommand reads RAZORPAY_KEY_ID and RAZORPAY_KEY_SECRET from the
environment. Test-mode keys only.
`)
}
