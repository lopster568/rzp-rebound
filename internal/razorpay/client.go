package razorpay

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/trace"
)

// DefaultBaseURL is the Razorpay API root, including the version segment.
const DefaultBaseURL = "https://api.razorpay.com/v1"

// Endpoint paths, appended to the base URL.
//
// These have not been checked against a live response. They are written from
// the API layout the Port in port.go was modelled on, and the phase 1 live half
// confirms each one against a captured request. Until then they are pending
// fixture capture, like the PaymentLink field set above them.
const (
	pathOrders            = "/orders"
	pathOrderByID         = "/orders/%s"
	pathOrderPayments     = "/orders/%s/payments"
	pathPaymentByID       = "/payments/%s"
	pathPaymentLinks      = "/payment_links"
	pathPaymentLinkNotify = "/payment_links/%s/notify_by/%s"
)

// Client defaults. The three retry numbers are a starting point, not a
// measurement: no Razorpay rate limit is documented in this repository (PRD
// Q5), so the live half records the first 429 it sees and these move to fit it.
const (
	DefaultMaxAttempts   = 4
	DefaultBaseBackoff   = 250 * time.Millisecond
	DefaultMaxBackoff    = 8 * time.Second
	DefaultMaxConcurrent = 4
)

// maxResponseBytes bounds how much of a response body is read, so a runaway
// response cannot fill memory and an error message cannot carry a whole page.
const maxResponseBytes = 1 << 20

// maxErrorBodyBytes bounds how much of a failing response body goes into an
// error message.
const maxErrorBodyBytes = 512

// Redacted is what a credential becomes in anything that leaves this package.
const Redacted = "[redacted]"

// ErrRetryBudgetExhausted wraps the last error when every retry was spent.
var ErrRetryBudgetExhausted = errors.New("razorpay: retry budget exhausted")

// ErrMissingCredentials is returned when a client is built without both halves
// of the key pair.
var ErrMissingCredentials = errors.New("razorpay: client needs both a key id and a key secret")

// WaitFunc blocks for d or until ctx ends. It is the backoff seam: a test
// supplies one that records the duration and advances a fake clock, so no test
// sleeps.
type WaitFunc func(ctx context.Context, d time.Duration) error

// APIError is a non-2xx response from the Razorpay API. Body is truncated and
// has had every credential scrubbed out of it before this value exists.
type APIError struct {
	StatusCode int
	Method     string
	Path       string
	Body       string
}

// Error renders the status, the call, and the truncated body.
func (e *APIError) Error() string {
	return fmt.Sprintf("razorpay: %s %s returned %d: %s", e.Method, e.Path, e.StatusCode, e.Body)
}

// redactedError carries a scrubbed message and still unwraps to the original,
// so errors.Is and errors.As keep working while the printed text does not
// carry a credential.
type redactedError struct {
	msg string
	err error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

// RawResponse is one line of the raw capture stream. It holds no request
// header, so the Authorization header cannot reach a fixture file.
type RawResponse struct {
	CapturedAt string `json:"captured_at"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Status     int    `json:"status"`
	Body       any    `json:"body"`
}

// ClientOptions configures a Client.
type ClientOptions struct {
	// KeyID and KeySecret are the test-mode credentials. Both are required.
	KeyID     string
	KeySecret string
	// BaseURL defaults to DefaultBaseURL. A test points it at an
	// httptest.Server, which is how the client's own code path runs offline.
	BaseURL string
	// Transport is what the otelhttp transport wraps. Nil means
	// http.DefaultTransport.
	Transport http.RoundTripper
	// TracerProvider is where the per-request client spans go. Nil means the
	// global provider.
	TracerProvider trace.TracerProvider
	// MaxAttempts counts the first try. Zero means DefaultMaxAttempts.
	MaxAttempts int
	// BaseBackoff is the first wait after a 429. Zero means
	// DefaultBaseBackoff.
	BaseBackoff time.Duration
	// MaxBackoff caps the exponential growth. Zero means DefaultMaxBackoff.
	MaxBackoff time.Duration
	// MaxConcurrent bounds requests in flight. Zero means
	// DefaultMaxConcurrent.
	MaxConcurrent int
	// Wait is the backoff seam. Nil means a real timer.
	Wait WaitFunc
	// RawCapture, when set, gets one JSON line per response. It is what the
	// live half records testdata/recorded/ fixtures with.
	RawCapture io.Writer
	// Clock stamps the capture lines. Nil means the wall clock.
	Clock clock.Clock
}

// Client is the live Razorpay client: plain net/http per ADR-0002, with an
// otelhttp transport, 429 backoff, a concurrency cap, credential redaction on
// every error path, and an optional raw capture hook.
type Client struct {
	baseURL     string
	basePath    string
	keyID       string
	keySecret   string
	basicToken  string
	http        *http.Client
	maxAttempts int
	baseBackoff time.Duration
	maxBackoff  time.Duration
	sem         chan struct{}
	wait        WaitFunc
	clock       clock.Clock

	captureMu sync.Mutex
	capture   io.Writer
}

var _ Port = (*Client)(nil)

// NewClient returns a Client.
func NewClient(opts ClientOptions) (*Client, error) {
	if opts.KeyID == "" || opts.KeySecret == "" {
		return nil, ErrMissingCredentials
	}
	return newClient(opts)
}

// newClient builds a Client without demanding credentials, which is what the
// replay client needs: it answers from disk and has no gateway to authenticate
// to.
func newClient(opts ClientOptions) (*Client, error) {
	baseURL := strings.TrimRight(opts.BaseURL, "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("razorpay: base url %q: %w", baseURL, err)
	}

	base := opts.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	var traceOpts []otelhttp.Option
	if opts.TracerProvider != nil {
		traceOpts = append(traceOpts, otelhttp.WithTracerProvider(opts.TracerProvider))
	}

	c := &Client{
		baseURL:     baseURL,
		basePath:    strings.TrimRight(parsed.Path, "/"),
		keyID:       opts.KeyID,
		keySecret:   opts.KeySecret,
		http:        &http.Client{Transport: otelhttp.NewTransport(base, traceOpts...)},
		maxAttempts: opts.MaxAttempts,
		baseBackoff: opts.BaseBackoff,
		maxBackoff:  opts.MaxBackoff,
		wait:        opts.Wait,
		clock:       opts.Clock,
		capture:     opts.RawCapture,
	}

	// The base64 token is held so redaction can scrub it. A gateway that
	// echoes the Authorization header into an error body leaks the pair in a
	// form that scrubbing the two halves separately would not catch.
	if c.keyID != "" || c.keySecret != "" {
		c.basicToken = base64.StdEncoding.EncodeToString([]byte(c.keyID + ":" + c.keySecret))
	}

	if c.maxAttempts <= 0 {
		c.maxAttempts = DefaultMaxAttempts
	}
	if c.baseBackoff <= 0 {
		c.baseBackoff = DefaultBaseBackoff
	}
	if c.maxBackoff <= 0 {
		c.maxBackoff = DefaultMaxBackoff
	}
	if c.maxBackoff < c.baseBackoff {
		c.maxBackoff = c.baseBackoff
	}
	if c.wait == nil {
		c.wait = sleepWait
	}
	if c.clock == nil {
		c.clock = clock.Real()
	}

	concurrency := opts.MaxConcurrent
	if concurrency <= 0 {
		concurrency = DefaultMaxConcurrent
	}
	c.sem = make(chan struct{}, concurrency)

	return c, nil
}

// sleepWait is the default backoff: a timer that gives up when the context
// does.
func sleepWait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// BaseURL returns the API root the client sends to.
func (c *Client) BaseURL() string { return c.baseURL }

// Redact replaces every configured credential in s. It runs on every error
// message and every captured body before either leaves the package.
func (c *Client) Redact(s string) string {
	// Longest and most sensitive first. An empty credential is skipped,
	// because replacing the empty string would put the marker between every
	// character in the message.
	for _, secret := range []string{c.keySecret, c.basicToken, c.keyID} {
		if secret == "" {
			continue
		}
		s = strings.ReplaceAll(s, secret, Redacted)
	}
	return s
}

func (c *Client) redactErr(err error) error {
	if err == nil {
		return nil
	}
	msg := c.Redact(err.Error())
	if msg == err.Error() {
		return err
	}
	return &redactedError{msg: msg, err: err}
}

func (c *Client) apiError(method, path string, status int, body []byte) *APIError {
	trimmed := string(body)
	if len(trimmed) > maxErrorBodyBytes {
		trimmed = trimmed[:maxErrorBodyBytes] + "(truncated)"
	}
	return &APIError{
		StatusCode: status,
		Method:     method,
		Path:       path,
		Body:       c.Redact(trimmed),
	}
}

// mapNotFound turns a 404 into the port's own not-found error, so callers
// match with errors.Is instead of reading a status code.
func mapNotFound(err error, sentinel error, id string) error {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%w: %s: %w", sentinel, id, err)
	}
	return err
}

// do runs one API call, retrying only on 429.
//
// A 4xx other than 429 is not retried: the same request gets the same refusal,
// and a retry spends a call and a rate-limit slot on a certainty. A 5xx is not
// retried either, which is the conservative choice for a system that moves
// money: three of the six port calls have a side effect, and nothing here knows
// yet whether a Razorpay 5xx means the call did not happen or means the answer
// was lost on the way back. The live half observes one and this comment gets a
// decision instead of a caveat.
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return c.redactErr(fmt.Errorf("razorpay: encode %s %s: %w", method, path, err))
		}
	}

	backoff := c.baseBackoff
	var last error

	for attempt := 1; attempt <= c.maxAttempts; attempt++ {
		status, respBody, err := c.roundTrip(ctx, method, path, payload)
		if err != nil {
			return c.redactErr(err)
		}
		if err := c.captureResponse(method, path, status, respBody); err != nil {
			return c.redactErr(err)
		}

		switch {
		case status >= 200 && status < 300:
			if out == nil {
				return nil
			}
			if err := json.Unmarshal(respBody, out); err != nil {
				return c.redactErr(fmt.Errorf("razorpay: decode %s %s: %w", method, path, err))
			}
			return nil

		case status == http.StatusTooManyRequests:
			last = c.apiError(method, path, status, respBody)
			if attempt == c.maxAttempts {
				return c.redactErr(fmt.Errorf("%w after %d attempt(s): %w", ErrRetryBudgetExhausted, attempt, last))
			}
			if err := c.wait(ctx, backoff); err != nil {
				return c.redactErr(fmt.Errorf("razorpay: backoff on %s %s: %w", method, path, err))
			}
			backoff = min(backoff*2, c.maxBackoff)

		default:
			return c.redactErr(c.apiError(method, path, status, respBody))
		}
	}

	return c.redactErr(last)
}

// roundTrip holds a concurrency slot for the whole request, including reading
// the body. The slot is released before any backoff, so a retrying call does
// not sit on a socket budget it is not using.
func (c *Client) roundTrip(ctx context.Context, method, path string, payload []byte) (int, []byte, error) {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return 0, nil, fmt.Errorf("razorpay: waiting for a concurrency slot on %s %s: %w", method, path, ctx.Err())
	}
	defer func() { <-c.sem }()

	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return 0, nil, fmt.Errorf("razorpay: build %s %s: %w", method, path, err)
	}
	req.SetBasicAuth(c.keyID, c.keySecret)
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("razorpay: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("razorpay: read the response to %s %s: %w", method, path, err)
	}
	return resp.StatusCode, respBody, nil
}

// captureResponse appends one JSON line per response.
//
// A failed write fails the call. A fixture run that quietly wrote nothing looks
// exactly like a fixture run that worked, and the live half depends on this
// stream being the record of what Razorpay sent.
func (c *Client) captureResponse(method, path string, status int, body []byte) error {
	if c.capture == nil {
		return nil
	}

	line := RawResponse{
		CapturedAt: c.clock.Now().UTC().Format(time.RFC3339Nano),
		Method:     method,
		Path:       c.basePath + path,
		Status:     status,
	}
	if json.Valid(body) {
		line.Body = json.RawMessage(body)
	} else {
		line.Body = c.Redact(string(body))
	}

	encoded, err := json.Marshal(line)
	if err != nil {
		return fmt.Errorf("razorpay: encode the capture of %s %s: %w", method, path, err)
	}

	c.captureMu.Lock()
	defer c.captureMu.Unlock()
	if _, err := c.capture.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("razorpay: write the capture of %s %s: %w", method, path, err)
	}
	return nil
}

// createOrderBody is the POST body for the orders endpoint. The field names
// come from the json tags on Order in port.go and have not been checked against
// a live request. Pending fixture capture.
type createOrderBody struct {
	Amount   int64             `json:"amount"`
	Currency string            `json:"currency"`
	Receipt  string            `json:"receipt,omitempty"`
	Notes    map[string]string `json:"notes,omitempty"`
}

// createPaymentLinkBody is the POST body for the payment-links endpoint. Every
// field here is pending fixture capture, as the doc comment on
// CreatePaymentLinkRequest in port.go says.
type createPaymentLinkBody struct {
	Amount      int64  `json:"amount"`
	Currency    string `json:"currency"`
	Description string `json:"description,omitempty"`
	ReferenceID string `json:"reference_id,omitempty"`
	NotifySMS   bool   `json:"notify_sms"`
	NotifyEmail bool   `json:"notify_email"`
}

// paymentCollection is the list envelope the payments-for-order endpoint
// answers with. The envelope shape is pending fixture capture; the items decode
// through the same Payment tags every other call uses.
type paymentCollection struct {
	Count int       `json:"count"`
	Items []Payment `json:"items"`
}

// CreateOrder posts a new order.
func (c *Client) CreateOrder(ctx context.Context, req CreateOrderRequest) (Order, error) {
	if req.AmountPaise <= 0 {
		return Order{}, fmt.Errorf("%w: got %d paise", ErrAmountNotPositive, req.AmountPaise)
	}

	currency := req.Currency
	if currency == "" {
		currency = "INR"
	}

	var out Order
	err := c.do(ctx, http.MethodPost, pathOrders, createOrderBody{
		Amount:   req.AmountPaise,
		Currency: currency,
		Receipt:  req.Receipt,
		Notes:    req.Notes,
	}, &out)
	if err != nil {
		return Order{}, err
	}
	return out, nil
}

// FetchOrder reads one order.
func (c *Client) FetchOrder(ctx context.Context, orderID string) (Order, error) {
	if orderID == "" {
		return Order{}, fmt.Errorf("%w: no order id given", ErrOrderNotFound)
	}

	var out Order
	path := fmt.Sprintf(pathOrderByID, url.PathEscape(orderID))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return Order{}, mapNotFound(err, ErrOrderNotFound, orderID)
	}
	return out, nil
}

// ListPaymentsForOrder reads every payment attempt on an order.
func (c *Client) ListPaymentsForOrder(ctx context.Context, orderID string) ([]Payment, error) {
	if orderID == "" {
		return nil, fmt.Errorf("%w: no order id given", ErrOrderNotFound)
	}

	var out paymentCollection
	path := fmt.Sprintf(pathOrderPayments, url.PathEscape(orderID))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, mapNotFound(err, ErrOrderNotFound, orderID)
	}
	if out.Items == nil {
		return []Payment{}, nil
	}
	return out.Items, nil
}

// FetchPayment reads one payment.
func (c *Client) FetchPayment(ctx context.Context, paymentID string) (Payment, error) {
	if paymentID == "" {
		return Payment{}, fmt.Errorf("%w: no payment id given", ErrPaymentNotFound)
	}

	var out Payment
	path := fmt.Sprintf(pathPaymentByID, url.PathEscape(paymentID))
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return Payment{}, mapNotFound(err, ErrPaymentNotFound, paymentID)
	}
	return out, nil
}

// CreatePaymentLink posts a new payment link.
//
// The request body and the response field set are both pending fixture
// capture, as the doc comments on CreatePaymentLinkRequest and PaymentLink in
// port.go already say.
func (c *Client) CreatePaymentLink(ctx context.Context, req CreatePaymentLinkRequest) (PaymentLink, error) {
	if req.AmountPaise <= 0 {
		return PaymentLink{}, fmt.Errorf("%w: got %d paise", ErrAmountNotPositive, req.AmountPaise)
	}

	currency := req.Currency
	if currency == "" {
		currency = "INR"
	}

	var out PaymentLink
	err := c.do(ctx, http.MethodPost, pathPaymentLinks, createPaymentLinkBody{
		Amount:      req.AmountPaise,
		Currency:    currency,
		Description: req.Description,
		ReferenceID: req.ReferenceID,
		NotifySMS:   req.NotifySMS,
		NotifyEmail: req.NotifyEmail,
	}, &out)
	if err != nil {
		return PaymentLink{}, err
	}
	return out, nil
}

// ResendPaymentLinkNotification asks Razorpay to send a payment link again.
//
// The receipt reports that the API call succeeded. It says nothing about
// whether a person received or read anything, and neither does anything
// downstream of it.
func (c *Client) ResendPaymentLinkNotification(ctx context.Context, linkID, medium string) (NotifyReceipt, error) {
	if medium != MediumSMS && medium != MediumEmail {
		return NotifyReceipt{}, fmt.Errorf("%w: %q", ErrUnsupportedMedium, medium)
	}
	if linkID == "" {
		return NotifyReceipt{}, fmt.Errorf("%w: no link id given", ErrPaymentLinkNotFound)
	}

	path := fmt.Sprintf(pathPaymentLinkNotify, url.PathEscape(linkID), url.PathEscape(medium))
	if err := c.do(ctx, http.MethodPost, path, nil, nil); err != nil {
		return NotifyReceipt{}, mapNotFound(err, ErrPaymentLinkNotFound, linkID)
	}

	// Accepted comes from the 2xx, not from a field in the body. The response
	// shape is pending fixture capture, and a field name invented here would
	// decode to false on a call that actually worked. What is observed is an
	// HTTP status, which is what Accepted reports.
	return NotifyReceipt{
		LinkID:      linkID,
		Medium:      medium,
		Accepted:    true,
		RequestedAt: c.clock.Now(),
	}, nil
}
