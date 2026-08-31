package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/lopster568/rzp-recovery-agent/internal/config"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
)

// missingOrderID is a syntactically valid order id that does not exist.
// Razorpay ids are a prefix and fourteen alphanumeric characters, and this
// shape matters: a malformed id produces a different 400 with a different
// description, which would tell us nothing about whether the key works.
const missingOrderID = "order_AAAAAAAAAAAAAA"

// runAuthProbe is step 1 of the live half: prove the credentials reach
// Razorpay before spending anything else on them.
//
// One FetchOrder for an id that does not exist is the cheapest call in the
// API. A 401 means the key is wrong. Anything that comes back as a missing
// resource means the key is right and the request was understood, which is
// everything this needs to establish.
func runAuthProbe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("auth-probe", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
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

	fmt.Printf("auth-probe: GET an order that does not exist (%s)\n", missingOrderID)
	_, err = client.FetchOrder(ctx, missingOrderID)

	switch {
	case err == nil:
		return fmt.Errorf("an order id nobody created came back as an order, which means %s is not the probe it was written to be", missingOrderID)

	case errors.Is(err, razorpay.ErrOrderNotFound):
		// The good case. The credentials authenticated and the API understood
		// the request well enough to say the resource is not there.
		fmt.Println("auth-probe: the credentials reached Razorpay and the order was reported missing")
		fmt.Println("auth-probe: ok")
		return nil
	}

	var apiErr *razorpay.APIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == 401 {
			return fmt.Errorf("the credentials were refused with a 401. Check RAZORPAY_KEY_ID and RAZORPAY_KEY_SECRET in .env: %w", err)
		}
		// Neither a 401 nor a missing resource is a fact about the API worth
		// writing down rather than swallowing, which is what the phase 1
		// checklist asks for.
		fmt.Fprintf(os.Stderr,
			"auth-probe: an unexpected answer. status=%d description=%q reason=%q\n"+
				"auth-probe: this is a new fact about the API. Put it in docs/phases/phase-1-live-loop/PROBLEMS.md\n",
			apiErr.StatusCode, apiErr.Description, apiErr.Reason)
	}
	return err
}
