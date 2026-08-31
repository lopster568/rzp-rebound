//go:build integration

// The tests in this file spend real Razorpay test-mode API calls. They are
// behind the integration build tag so `make ci` can never reach for a
// credential, which is NFR-2.
//
// Run them with:
//
//	RZP_CONTRACT_HARNESSES=live go test -tags=integration ./internal/razorpay/
//
// The live harness is registered here rather than in contract_test.go for the
// same reason: a name that only exists behind the tag cannot be selected by
// accident from an untagged run.

package razorpay_test

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/config"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/testcards"
)

func init() {
	contractHarnesses["live"] = newLiveHarness
}

// liveHarness runs the port contract against Razorpay test mode.
type liveHarness struct {
	client    *razorpay.Client
	attempter *razorpay.Attempter
}

func newLiveHarness(t *testing.T) contractHarness {
	t.Helper()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.RequireLiveAccess(); err != nil {
		t.Skipf("the live harness needs test-mode credentials: %v", err)
	}

	client, err := razorpay.NewClient(razorpay.ClientOptions{
		KeyID:     cfg.RazorpayKeyID,
		KeySecret: cfg.RazorpayKeySecret,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	attempter, err := razorpay.NewAttempter(razorpay.AttempterOptions{
		KeyID:     cfg.RazorpayKeyID,
		KeySecret: cfg.RazorpayKeySecret,
	})
	if err != nil {
		t.Fatalf("NewAttempter: %v", err)
	}
	return &liveHarness{client: client, attempter: attempter}
}

func (h *liveHarness) Port() razorpay.Port { return h.client }

// AttemptPayment drives the real checkout sequence and then reads the payment
// back through the documented API.
//
// This is the harness method that did not exist before 2026-08-31. Both other
// harnesses reach past the client into the fake for it, because until the
// spike nothing in this repository could make a payment attempt happen.
func (h *liveHarness) AttemptPayment(ctx context.Context, orderID, cardNumber string) (razorpay.Payment, error) {
	order, err := h.client.FetchOrder(ctx, orderID)
	if err != nil {
		return razorpay.Payment{}, err
	}

	attempt, err := h.attempter.Attempt(ctx, razorpay.AttemptRequest{
		OrderID:     orderID,
		AmountPaise: order.AmountPaise,
		CardNumber:  cardNumber,
		Outcome:     razorpay.AttemptFail,
	})
	if err != nil {
		return razorpay.Payment{}, err
	}
	return h.client.FetchPayment(ctx, attempt.PaymentID)
}

func (h *liveHarness) FailureCard(t *testing.T, reason string) string {
	t.Helper()

	// The card is asked for by its documented reason and the documented reason
	// is not what comes back. TestLiveFailedPaymentCarriesTheObservedReason is
	// where that is asserted; the contract only requires that the fields are
	// populated at all, which they are.
	table, err := testcards.Default()
	if err != nil {
		t.Fatalf("load the card table: %v", err)
	}
	card, ok := table.CardForErrorCode(reason)
	if !ok {
		t.Fatalf("no documented card forces %s", reason)
	}
	return card.Number
}

// requireLive gives every test in this file the same skip.
func requireLive(t *testing.T) config.Config {
	t.Helper()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfg.RequireLiveAccess(); err != nil {
		t.Skipf("this test needs test-mode credentials: %v", err)
	}
	return cfg
}

func liveClient(t *testing.T) *razorpay.Client {
	t.Helper()

	cfg := requireLive(t)
	c, err := razorpay.NewClient(razorpay.ClientOptions{
		KeyID:     cfg.RazorpayKeyID,
		KeySecret: cfg.RazorpayKeySecret,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// TestLiveMissingOrderIsReportedAsNotFound is the assertion the offline half
// could not make. Razorpay answers a missing resource with a 400 and a
// description, not a 404, so this is the test that would have caught
// mapNotFound reading only the status code.
func TestLiveMissingOrderIsReportedAsNotFound(t *testing.T) {
	ctx := context.Background()

	_, err := liveClient(t).FetchOrder(ctx, "order_AAAAAAAAAAAAAA")
	if err == nil {
		t.Fatal("an order id nobody created came back as an order")
	}
	if !errors.Is(err, razorpay.ErrOrderNotFound) {
		t.Errorf("err = %v, want it to wrap ErrOrderNotFound", err)
	}

	var apiErr *razorpay.APIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == 401 {
			t.Fatalf("the credentials were refused: %v", err)
		}
		t.Logf("observed status=%d description=%q", apiErr.StatusCode, apiErr.Description)
	}
}

// TestLiveFailedPaymentCarriesTheObservedReason is the live half's answer to
// PRD Q4, asserted rather than written down.
func TestLiveFailedPaymentCarriesTheObservedReason(t *testing.T) {
	ctx := context.Background()
	h := newLiveHarness(t).(*liveHarness)

	order, err := h.client.CreateOrder(ctx, razorpay.CreateOrderRequest{
		AmountPaise: 100000,
		Currency:    "INR",
		Receipt:     "rcpt_live_q4_" + time.Now().UTC().Format("20060102150405.000000000"),
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}

	payment, err := h.AttemptPayment(ctx, order.ID, "4100280000080001")
	if err != nil {
		t.Fatalf("AttemptPayment: %v", err)
	}

	if payment.Status != razorpay.PaymentStatusFailed {
		t.Fatalf("status = %q, want %q", payment.Status, razorpay.PaymentStatusFailed)
	}

	// Both fields are populated, with the coarse class in one and the specific
	// reason in the other. That is the shape internal/classify reads.
	if payment.ErrorCode != razorpay.ErrorClassBadRequest {
		t.Errorf("error_code = %q, want %q", payment.ErrorCode, razorpay.ErrorClassBadRequest)
	}
	if payment.ErrorReason != razorpay.ReasonPaymentFailed {
		t.Errorf("error_reason = %q, want %q. If this changed, the card table and "+
			"docs/RAZORPAY-TEST-MODE-NOTES.md both need re-walking",
			payment.ErrorReason, razorpay.ReasonPaymentFailed)
	}
	if payment.ErrorSource != razorpay.ErrorSourceGateway {
		t.Errorf("error_source = %q, want %q", payment.ErrorSource, razorpay.ErrorSourceGateway)
	}
	if payment.ErrorStep != razorpay.ErrorStepPaymentAuthorization {
		t.Errorf("error_step = %q, want %q", payment.ErrorStep, razorpay.ErrorStepPaymentAuthorization)
	}

	after, err := h.client.FetchOrder(ctx, order.ID)
	if err != nil {
		t.Fatalf("FetchOrder after the failure: %v", err)
	}
	if after.Status != razorpay.OrderStatusAttempted {
		t.Errorf("order status after a failed attempt = %q, want %q", after.Status, razorpay.OrderStatusAttempted)
	}
}

// TestLiveSecondAttemptCanPayAnAttemptedOrder is the recovery loop's premise,
// checked against the real API: an order sitting at attempted with a failed
// payment under it can still become paid.
//
// What it does not show is that anything about the agent caused that. The
// outcome is chosen by the caller and sent to the mock bank in one form field,
// which is written on the test as well as in the notes because a green test
// with this name would otherwise read as a recovery result.
func TestLiveSecondAttemptCanPayAnAttemptedOrder(t *testing.T) {
	ctx := context.Background()
	h := newLiveHarness(t).(*liveHarness)

	order, err := h.client.CreateOrder(ctx, razorpay.CreateOrderRequest{
		AmountPaise: 100000,
		Currency:    "INR",
		Receipt:     "rcpt_live_second_" + time.Now().UTC().Format("20060102150405.000000000"),
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}

	if _, err := h.AttemptPayment(ctx, order.ID, "4100280000080001"); err != nil {
		t.Fatalf("first attempt: %v", err)
	}

	if _, err := h.attempter.Attempt(ctx, razorpay.AttemptRequest{
		OrderID:     order.ID,
		AmountPaise: order.AmountPaise,
		CardNumber:  "4100280000080001",
		Outcome:     razorpay.AttemptSucceed,
	}); err != nil {
		t.Fatalf("second attempt: %v", err)
	}

	// The state comes from the gateway, never from what the attempt reported.
	final, err := h.client.FetchOrder(ctx, order.ID)
	if err != nil {
		t.Fatalf("FetchOrder: %v", err)
	}
	if final.Status != razorpay.OrderStatusPaid {
		t.Errorf("order status = %q, want %q", final.Status, razorpay.OrderStatusPaid)
	}
	if final.AmountPaid != order.AmountPaise {
		t.Errorf("amount_paid = %d paise, want %d", final.AmountPaid, order.AmountPaise)
	}
	if final.Attempts < 2 {
		t.Errorf("attempts = %d, want at least 2", final.Attempts)
	}
}

// TestLiveResendReportsAnAPICallAndNothingAboutAPerson is the honesty rule
// with a live call behind it.
//
// The link is created with no customer on it and notification off. Razorpay
// still answers the resend with success. That is exactly why this project
// reports an API call rather than a delivery.
func TestLiveResendReportsAnAPICallAndNothingAboutAPerson(t *testing.T) {
	ctx := context.Background()
	c := liveClient(t)

	link, err := c.CreatePaymentLink(ctx, razorpay.CreatePaymentLinkRequest{
		AmountPaise: 100000,
		Currency:    "INR",
		Description: "live contract check",
		ReferenceID: "ref_live_" + time.Now().UTC().Format("20060102150405"),
	})
	if err != nil {
		t.Fatalf("CreatePaymentLink: %v", err)
	}
	if link.ID == "" || link.ShortURL == "" {
		t.Fatalf("the payment link response decoded to %+v", link)
	}

	receipt, err := c.ResendPaymentLinkNotification(ctx, link.ID, razorpay.MediumSMS)
	if err != nil {
		t.Fatalf("ResendPaymentLinkNotification: %v", err)
	}
	if !receipt.Accepted {
		t.Errorf("the resend call was not accepted for %s", link.ID)
	}

	t.Logf("the resend call for a link with no contact on it was accepted, "+
		"which is why Receipt.DeliveryConfirmed is a false constant (link %s)", link.ID)
}

// statusCounter counts response codes underneath the client's retry loop.
//
// Counting at the call boundary instead was a review finding on 2026-08-31:
// the probe reported a rate limit only through ErrRetryBudgetExhausted, which
// do returns after four attempts, so a 429 that the backoff then retried
// successfully was counted as an ordinary answer. The instrument could report
// zero 429s while every call had been throttled three times, which is exactly
// the claim it exists to support.
type statusCounter struct {
	base http.RoundTripper
	mu   sync.Mutex
	seen map[int]int
}

func (t *statusCounter) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if resp != nil {
		t.mu.Lock()
		if t.seen == nil {
			t.seen = make(map[int]int)
		}
		t.seen[resp.StatusCode]++
		t.mu.Unlock()
	}
	return resp, err
}

func (t *statusCounter) count(status int) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.seen[status]
}

func (t *statusCounter) total() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, c := range t.seen {
		n += c
	}
	return n
}

// TestLiveRateLimitObservation is PRD Q5, and it is a measurement rather than
// an assertion. It sends a burst of the cheapest read the API has and reports
// how many 429s came back.
//
// It asserts nothing about the limit, because a number from one burst on one
// day is not a limit. What it does assert is that the client survives the
// burst without corrupting a response, and what it produces is a log line for
// docs/RAZORPAY-TEST-MODE-NOTES.md.
//
// The 429 count comes from a transport underneath the retry loop, so a
// throttle that the backoff absorbed is still counted.
func TestLiveRateLimitObservation(t *testing.T) {
	if os.Getenv("RZP_RATE_LIMIT_PROBE") == "" {
		t.Skip("set RZP_RATE_LIMIT_PROBE=1 to spend the calls this needs")
	}

	cfg := requireLive(t)
	counter := &statusCounter{}
	c, err := razorpay.NewClient(razorpay.ClientOptions{
		KeyID:     cfg.RazorpayKeyID,
		KeySecret: cfg.RazorpayKeySecret,
		Transport: counter,
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ctx := context.Background()
	const burst = 40
	var (
		notFound  int
		other     int
		exhausted int
	)

	start := time.Now()
	for i := 0; i < burst; i++ {
		_, err := c.FetchOrder(ctx, "order_AAAAAAAAAAAAAA")
		switch {
		case errors.Is(err, razorpay.ErrOrderNotFound):
			notFound++
		case errors.Is(err, razorpay.ErrRetryBudgetExhausted):
			exhausted++
		default:
			other++
			t.Logf("call %d returned %v", i, err)
		}
	}
	elapsed := time.Since(start)

	throttled := counter.count(http.StatusTooManyRequests)
	t.Logf("%d calls in %s (%.1f calls/s, %d HTTP requests): %d reported missing, %d exhausted the retry budget, %d other. "+
		"429 responses seen beneath the retry loop: %d",
		burst, elapsed.Round(time.Millisecond), float64(burst)/elapsed.Seconds(),
		counter.total(), notFound, exhausted, other, throttled)

	if notFound+exhausted+other != burst {
		t.Errorf("the calls do not add up: %d + %d + %d != %d", notFound, exhausted, other, burst)
	}
	// The counter has to have seen something, or the log line above is a
	// measurement of nothing.
	if counter.total() < burst {
		t.Errorf("the transport saw %d request(s) for %d call(s), so the 429 count is not trustworthy",
			counter.total(), burst)
	}
}
