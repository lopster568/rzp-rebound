package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
)

// labeledCapture is the RawCapture writer the capture run installs. It tags
// each response with the step that produced it, so a fixture file can be named
// after what the call was for rather than after a generated order id.
//
// The steps run one at a time and the label is set before each, which is what
// makes the tagging unambiguous. A concurrent capture run would need something
// better, and there is no reason to have one.
type labeledCapture struct {
	mu     sync.Mutex
	label  string
	order  []string
	byStep map[string][]razorpay.RawResponse
}

func newLabeledCapture() *labeledCapture {
	return &labeledCapture{byStep: make(map[string][]razorpay.RawResponse)}
}

func (c *labeledCapture) setLabel(label string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.label = label
	if _, seen := c.byStep[label]; !seen {
		c.order = append(c.order, label)
		c.byStep[label] = nil
	}
}

func (c *labeledCapture) Write(p []byte) (int, error) {
	var line razorpay.RawResponse
	if err := json.Unmarshal(p, &line); err != nil {
		return 0, fmt.Errorf("capture: the client wrote a line that is not a RawResponse: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.byStep[c.label] = append(c.byStep[c.label], line)
	return len(p), nil
}

func (c *labeledCapture) first(label string) (razorpay.RawResponse, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	lines := c.byStep[label]
	if len(lines) == 0 {
		return razorpay.RawResponse{}, false
	}
	return lines[0], true
}

// runCapture records real Razorpay test-mode responses into
// testdata/recorded/, in the Fixture shape the replay client reads.
//
// Every file it writes is a response Razorpay actually sent, scrubbed by the
// client's own capture hook on the way out. Nothing here is hand written, and
// nothing here carries the synthetic_ prefix, which LoadFixtures enforces
// against _meta.synthetic in both directions.
func runCapture(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("capture", flag.ContinueOnError)
	outDir := fs.String("out", "", "where to write fixtures (default: testdata/recorded)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	dir := *outDir
	if dir == "" {
		resolved, err := razorpay.RecordedDir()
		if err != nil {
			return err
		}
		dir = resolved
	}

	capture := newLabeledCapture()
	rig, err := newLiveRig(ctx, "rzp-capture", capture)
	if err != nil {
		return err
	}
	defer func() { _ = rig.Close() }()

	const amountPaise = 100000

	// Two orders, not one. The fixture map is keyed on method and path, and
	// LoadFixtures refuses a directory where two files answer the same key.
	// An order's payments collection is one path with two different bodies
	// over its life, empty before an attempt and populated after, so the
	// before-state captures come from an order nothing is ever attempted on
	// and the after-state captures come from a second one. PROBLEMS.md has
	// the entry: the first capture run wrote a directory that could not load.

	// 1. The untouched order. It exists to give the empty states a home.
	capture.setLabel("create_order")
	quiet, err := rig.client.CreateOrder(ctx, razorpay.CreateOrderRequest{
		AmountPaise: amountPaise,
		Currency:    "INR",
		Receipt:     receipt("rcpt_capture_quiet"),
		Notes:       map[string]string{"purpose": "phase-1 fixture capture, never attempted"},
	})
	if err != nil {
		return fmt.Errorf("create the untouched order: %w", err)
	}
	fmt.Printf("capture: created %s and will leave it alone\n", quiet.ID)

	capture.setLabel("fetch_order")
	if _, err := rig.client.FetchOrder(ctx, quiet.ID); err != nil {
		return fmt.Errorf("fetch the untouched order: %w", err)
	}

	// The envelope is the point here: it is what paymentCollection decodes.
	capture.setLabel("list_payments_empty")
	if _, err := rig.client.ListPaymentsForOrder(ctx, quiet.ID); err != nil {
		return fmt.Errorf("list payments on the untouched order: %w", err)
	}

	// 2. The order a payment is actually attempted on. No fixture is written
	// for its creation, because POST /v1/orders is one path and the untouched
	// order already recorded it. It still gets its own label: leaving the
	// previous one set filed this response under list_payments_empty, which
	// only wrote the right fixture because writeFixtures takes the first
	// response per label. Review finding, 2026-08-31.
	capture.setLabel("create_order_to_fail")
	failed, err := rig.client.CreateOrder(ctx, razorpay.CreateOrderRequest{
		AmountPaise: amountPaise,
		Currency:    "INR",
		Receipt:     receipt("rcpt_capture_failed"),
		Notes:       map[string]string{"purpose": "phase-1 fixture capture, driven to a failed payment"},
	})
	if err != nil {
		return fmt.Errorf("create the order to fail: %w", err)
	}

	// The checkout responses are captured too, under a label nothing writes a
	// file for. Two of those pages carry the key id in a form action, and
	// although the capture hook redacts it, a fixture built out of an HTML
	// page serves nothing the replay client can use.
	capture.setLabel("checkout_failed_attempt")
	attempt, err := rig.attempter.Attempt(ctx, razorpay.AttemptRequest{
		OrderID:     failed.ID,
		AmountPaise: amountPaise,
		CardNumber:  "4100280000080001",
		Outcome:     razorpay.AttemptFail,
	})
	if err != nil {
		return fmt.Errorf("drive a failed payment: %w", err)
	}
	fmt.Printf("capture: attempted %s on %s and asked the mock bank to decline it\n", attempt.PaymentID, failed.ID)

	// 3. The failed payment, read back through the documented API. This is the
	// capture PRD Q4 turns on.
	capture.setLabel("fetch_failed_payment")
	payment, err := rig.client.FetchPayment(ctx, attempt.PaymentID)
	if err != nil {
		return fmt.Errorf("fetch the failed payment: %w", err)
	}
	fmt.Printf("capture: %s is %s, reason %q, code %q, source %q, step %q\n",
		payment.ID, payment.Status, payment.ErrorReason, payment.ErrorCode,
		payment.ErrorSource, payment.ErrorStep)

	capture.setLabel("list_payments_after_failure")
	if _, err := rig.client.ListPaymentsForOrder(ctx, failed.ID); err != nil {
		return fmt.Errorf("list payments after the attempt: %w", err)
	}

	// The state the poller must not treat as terminal.
	capture.setLabel("fetch_order_after_failure")
	if _, err := rig.client.FetchOrder(ctx, failed.ID); err != nil {
		return fmt.Errorf("fetch the order after the failure: %w", err)
	}

	// 4. A payment link, with notification off. Nothing here asks Razorpay to
	// contact anybody, and the address is in a domain that cannot receive
	// mail.
	capture.setLabel("create_payment_link")
	link, err := rig.client.CreatePaymentLink(ctx, razorpay.CreatePaymentLinkRequest{
		AmountPaise: amountPaise,
		Currency:    "INR",
		Description: "phase-1 fixture capture",
		ReferenceID: receipt("ref_capture"),
	})
	if err != nil {
		return fmt.Errorf("create a payment link: %w", err)
	}
	fmt.Printf("capture: created %s\n", link.ID)

	// 5. The resend call. What comes back is an HTTP response about an API
	// call, and the fixture records exactly that.
	capture.setLabel("resend_payment_link_notification")
	if _, err := rig.client.ResendPaymentLinkNotification(ctx, link.ID, razorpay.MediumEmail); err != nil {
		return fmt.Errorf("resend the payment link notification: %w", err)
	}

	// 6. A missing resource, which is a 400 rather than a 404 and is the
	// capture mapNotFound's behaviour rests on.
	capture.setLabel("fetch_missing_order")
	if _, err := rig.client.FetchOrder(ctx, missingOrderID); err == nil {
		return fmt.Errorf("an order id nobody created came back as an order")
	}

	written, err := writeFixtures(dir, capture, map[string]string{
		"create_order":                     "The response to creating an order, at status created. Nothing is ever attempted on this one.",
		"fetch_order":                      "An order read back before any payment attempt.",
		"list_payments_empty":              "The payments collection envelope for an order with no attempts on it.",
		"fetch_failed_payment":             "A real failed payment. This is the capture PRD Q4 was settled from.",
		"list_payments_after_failure":      "The payments collection envelope with one failed attempt in it.",
		"fetch_order_after_failure":        "An order sitting at attempted with a failed payment under it, which the poller must not treat as terminal.",
		"create_payment_link":              "The response to creating a payment link, with the nested notify object accepted.",
		"resend_payment_link_notification": "The resend response. It reports that the notification API call succeeded and nothing about a person.",
		"fetch_missing_order":              "A missing resource. Razorpay answers 400 with a description rather than 404.",
	})
	if err != nil {
		return err
	}

	fmt.Printf("capture: wrote %d fixture(s) to %s\n", written, dir)
	return nil
}

// writeFixtures turns captured responses into fixture files. Only labels with
// a note are written, so the checkout pages stay out.
func writeFixtures(dir string, capture *labeledCapture, notes map[string]string) (int, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, fmt.Errorf("capture: make %s: %w", dir, err)
	}

	written := 0
	for _, label := range capture.order {
		note, wanted := notes[label]
		if !wanted {
			continue
		}
		line, ok := capture.first(label)
		if !ok {
			return written, fmt.Errorf("capture: step %q recorded no response", label)
		}

		fixture := razorpay.Fixture{
			Meta: razorpay.FixtureMeta{
				Synthetic:  false,
				CapturedAt: capturedAtOrNow(line.CapturedAt),
				Note:       note,
			},
			Request:  razorpay.FixtureRequest{Method: line.Method, Path: line.Path},
			Response: razorpay.FixtureResponse{Status: line.Status, Body: line.Body},
		}

		encoded, err := json.MarshalIndent(fixture, "", "  ")
		if err != nil {
			return written, fmt.Errorf("capture: encode the fixture for %s: %w", label, err)
		}
		if strings.HasPrefix(label, razorpay.SyntheticPrefix) {
			return written, fmt.Errorf("capture: %q would be written with the synthetic prefix, which is reserved for files nobody captured", label)
		}

		path := filepath.Join(dir, label+".json")
		if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
			return written, fmt.Errorf("capture: write %s: %w", path, err)
		}
		written++
	}
	return written, nil
}

// capturedAtOrNow is the timestamp a fixture carries when the capture line had
// none, which should not happen and is cheap to be sure about.
func capturedAtOrNow(s string) string {
	if s != "" {
		return s
	}
	return time.Now().UTC().Format(time.RFC3339Nano)
}
