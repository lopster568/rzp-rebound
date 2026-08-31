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
	"github.com/lopster568/rzp-recovery-agent/internal/redact"
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
	// DefaultRequestTimeout bounds one request including reading its body.
	// Nothing observed on 2026-08-31 took longer than a couple of seconds.
	DefaultRequestTimeout = 30 * time.Second
)

// maxResponseBytes bounds how much of a response body is read, so a runaway
// response cannot fill memory and an error message cannot carry a whole page.
const maxResponseBytes = 1 << 20

// maxErrorBodyBytes bounds how much of a failing response body goes into an
// error message.
const maxErrorBodyBytes = 512

// maxCaptureBodyBytes bounds how much of a response body goes into a capture
// line.
//
// The capture path had no cap at all, so one checkout page produced a JSONL
// line of over a megabyte. Review finding, 2026-08-31. Every real API response
// this project has seen is under a kilobyte, and the pages that are not are
// HTML that no fixture is built from.
const maxCaptureBodyBytes = 64 << 10

// Redacted is what a credential becomes in anything that leaves this package.
// It is internal/redact's marker, because a capture line can be scrubbed by
// both and two spellings of the same thing would read as two different things
// having happened.
const Redacted = redact.Marker

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
	// Description is error.description out of the response envelope, when the
	// body carried one. It is parsed rather than left in the body string
	// because Razorpay answers a missing resource with a 400 and puts the
	// only distinguishing information in this field: the status code alone
	// cannot tell a missing order from a malformed request.
	Description string
	// Reason is error.reason out of the same envelope.
	Reason string
}

// apiErrorEnvelope is the shape a Razorpay error body arrives in.
type apiErrorEnvelope struct {
	Error struct {
		Code        string `json:"code"`
		Description string `json:"description"`
		Source      string `json:"source"`
		Step        string `json:"step"`
		Reason      string `json:"reason"`
	} `json:"error"`
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
	//
	// It must be safe for concurrent use if it is shared. Each Client holds
	// its own mutex around the write, and an Attempter builds a second Client
	// internally, so handing the same writer to both leaves the two
	// unsynchronised with each other. cmd/rzp does exactly that and is safe
	// because its writer has a mutex of its own.
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
		// The URL is not echoed. It is caller-supplied and can carry userinfo,
		// and this runs before the client exists to redact anything. A
		// *url.Error prints the URL it failed on, so only its inner cause is
		// reported.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return nil, fmt.Errorf("razorpay: the base url does not parse: %v", uerr.Err)
		}
		return nil, fmt.Errorf("razorpay: the base url does not parse")
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
		baseURL:   baseURL,
		basePath:  strings.TrimRight(parsed.Path, "/"),
		keyID:     opts.KeyID,
		keySecret: opts.KeySecret,
		http: &http.Client{
			Transport:     otelhttp.NewTransport(base, traceOpts...),
			CheckRedirect: pinnedRedirect(parsed),
			// A money call that hangs forever is worse than one that fails.
			Timeout: DefaultRequestTimeout,
		},
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

// maxRedirectHops bounds how many redirects any client here will follow. Go's
// default is 10. Nothing Razorpay does needs more than the one the checkout
// callback uses, and a lower ceiling bounds a redirect loop.
const maxRedirectHops = 3

// ErrRedirectOffOrigin is returned when a gateway tries to send a request
// somewhere other than the origin the client was pointed at.
var ErrRedirectOffOrigin = errors.New("razorpay: refusing to follow a redirect off the configured origin")

// pinnedRedirect keeps every hop on the origin the client was built for.
//
// Without it both clients here followed the Location header anywhere, which
// was found in review on 2026-08-31 and reproduced. The attempter was the
// serious one: two of its calls carry key_id in the query and the callback
// carries it as a path segment, so a 302 handed a foreign host half a
// credential pair, and a 307 replayed the whole form body, which is the key
// id, the card number, and the CVV. The client was milder, because Go strips
// the Authorization header on a cross-domain redirect, but it keeps it for a
// different port on the same host or for a subdomain.
//
// Same-origin redirects are still followed. The real checkout callback is on
// the same origin as the API, and the attempt sequence depends on it.
//
// The error names the host and never the URL. A refused redirect target is
// exactly the kind of URL that carries a credential, and an error message is
// the last place it should end up.
func pinnedRedirect(root *url.URL) func(*http.Request, []*http.Request) error {
	origin := root.Scheme + "://" + root.Host
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirectHops {
			return fmt.Errorf("razorpay: stopped after %d redirect(s)", len(via))
		}
		if req.URL.Scheme+"://"+req.URL.Host != origin {
			return fmt.Errorf("%w: %s wanted %s", ErrRedirectOffOrigin, root.Host, req.URL.Host)
		}
		return nil
	}
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
	// Scrub first, then truncate. Cutting first splits a credential that
	// straddles the boundary, and the surviving prefix is no longer the string
	// the replacer is looking for, so it goes into the error message and from
	// there into every log line that formats one. Measured on 2026-08-31 with
	// the cut inside the secret: 11 of 22 characters survived.
	// Parse the raw body, before anything rewrites it, and redact what comes
	// out. Parsing the scrubbed text instead was a real defect, found in
	// review on 2026-08-31 and reproduced: redact.Value replaces a run of 13
	// or more digits with a bare marker, and an unquoted JSON number is
	// exactly that shape, so a millisecond epoch anywhere in the envelope left
	// a document that no longer parsed. The unmarshal error was discarded,
	// Description stayed empty, and mapNotFound stopped recognising the one
	// case ErrOrderNotFound exists for. Razorpay's error envelope carries an
	// open-shaped metadata object, so it was reachable rather than
	// theoretical.
	//
	// The two extracted fields are scrubbed individually on the way out, so
	// nothing skips redaction by being parsed first.
	var envelope apiErrorEnvelope
	_ = json.Unmarshal(body, &envelope)

	scrubbed := redact.Value(c.Redact(string(body)))
	if len(scrubbed) > maxErrorBodyBytes {
		// Truncation is on a byte boundary and can split a rune, so drop what
		// it broke rather than putting invalid UTF-8 in an error.
		scrubbed = strings.ToValidUTF8(scrubbed[:maxErrorBodyBytes], "") + "(truncated)"
	}
	return &APIError{
		StatusCode:  status,
		Method:      method,
		Path:        path,
		Body:        scrubbed,
		Description: redact.Value(c.Redact(envelope.Error.Description)),
		Reason:      redact.Value(c.Redact(envelope.Error.Reason)),
	}
}

// mapNotFound turns a missing resource into the port's own not-found error, so
// callers match with errors.Is instead of reading a status code.
//
// Razorpay does not answer 404 for a resource that is not there. It answers
// 400 with error.description set to DescriptionMissingResource, observed on
// 2026-08-31 against both a missing order id and a missing payment link id.
// The offline half only looked at the status code, so errors.Is against
// ErrOrderNotFound was false for the exact case the sentinel exists to catch.
//
// The 404 branch is kept because it costs nothing and a gateway that starts
// answering the documented way should not break this.
//
// Matching on a description string is fragile and is the honest option
// available: a 400 is also what a malformed id and a rejected body produce, so
// the status code cannot separate them and the description is the only field
// that does. If Razorpay reword that string, this stops recognising a missing
// resource and callers see a plain APIError, which fails toward reporting less
// rather than toward reporting a resource absent that is not.
func mapNotFound(err error, sentinel error, id string) error {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return err
	}
	missing := apiErr.StatusCode == http.StatusNotFound ||
		(apiErr.StatusCode == http.StatusBadRequest && apiErr.Description == DescriptionMissingResource)
	if missing {
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
	return c.doWith(ctx, method, path, body, out, false)
}

// doWith is do, with the one behaviour that is not shared by every call.
//
// tolerateEmptyBody makes a 2xx with no body a success that leaves out
// untouched. It is false everywhere except the resend call, and it was
// unconditional until review on 2026-08-31 showed what that costs: every read
// call returned a zero value and a nil error, so FetchOrder reported an empty
// status and ListPaymentsForOrder reported that an order had no attempts on
// it. An empty slice is a positive claim, and the poller and the recovery
// orchestrator both act on it.
func (c *Client) doWith(ctx context.Context, method, path string, body any, out any, tolerateEmptyBody bool) error {
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
			// A 2xx with no body is only acceptable where the caller said so.
			// For the resend it means the call was accepted and carried
			// nothing else, which is all Accepted ever claims. For a read it
			// would mean inventing state.
			if tolerateEmptyBody && len(bytes.TrimSpace(respBody)) == 0 {
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

	// Only reachable if maxAttempts is not positive, which newClient
	// normalises away. Returning the nil last error here would be a silent
	// success with out left unfilled, so say what happened instead.
	if last == nil {
		return fmt.Errorf("razorpay: %s %s made no attempt", method, path)
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

	// Read one byte past the cap so a response that hits it is reported as
	// truncated rather than surfacing later as a JSON syntax error at some
	// character offset, which says nothing about what actually went wrong.
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("razorpay: read the response to %s %s: %w", method, path, err)
	}
	if len(respBody) > maxResponseBytes {
		return resp.StatusCode, nil, fmt.Errorf("razorpay: the response to %s %s is larger than the %d byte cap", method, path, maxResponseBytes)
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
	// Scrub before writing, not only on the error path. A capture line becomes
	// a committed file under testdata/recorded/, and a gateway that echoes the
	// request back inside a JSON error body would otherwise put a credential
	// in git. redact.Value runs too, because the card and key shapes it knows
	// are worth catching in something that gets committed even though Razorpay
	// returns a masked card rather than a full one. The marker holds no JSON
	// metacharacter, so replacing inside a string value leaves the document
	// parseable, and the check below covers the case where it did not.
	scrubbed := redact.Value(c.Redact(string(body)))
	switch {
	case len(scrubbed) > maxCaptureBodyBytes:
		// Store it as a truncated string rather than as raw JSON. A cut
		// document would not parse, and a capture line that cannot be read is
		// worse than one that says it is short.
		line.Body = strings.ToValidUTF8(scrubbed[:maxCaptureBodyBytes], "") +
			fmt.Sprintf("(truncated from %d bytes)", len(scrubbed))
	case json.Valid(body) && json.Valid([]byte(scrubbed)):
		line.Body = json.RawMessage(scrubbed)
	default:
		line.Body = scrubbed
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

// createPaymentLinkBody is the POST body for the payment-links endpoint,
// confirmed against test mode on 2026-08-31.
//
// The offline half sent flat notify_sms and notify_email fields. Test mode
// rejected that body with a 400 and the description "extra fields sent": the
// notification flags are a nested object. Razorpay validates this endpoint
// strictly, so an invented field name is a failed call rather than an ignored
// one, which is the good kind of strict.
type createPaymentLinkBody struct {
	Amount      int64                   `json:"amount"`
	Currency    string                  `json:"currency"`
	Description string                  `json:"description,omitempty"`
	ReferenceID string                  `json:"reference_id,omitempty"`
	Notify      createPaymentLinkNotify `json:"notify"`
}

// createPaymentLinkNotify is the nested notify object.
type createPaymentLinkNotify struct {
	SMS   bool `json:"sms"`
	Email bool `json:"email"`
}

// notifyResponse is the body the resend endpoint answers with, confirmed on
// 2026-08-31. Success is a pointer so a body carrying no such field stays
// distinguishable from one carrying false.
type notifyResponse struct {
	Success *bool `json:"success"`
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
		Notify: createPaymentLinkNotify{
			SMS:   req.NotifySMS,
			Email: req.NotifyEmail,
		},
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

	var out notifyResponse
	path := fmt.Sprintf(pathPaymentLinkNotify, url.PathEscape(linkID), url.PathEscape(medium))
	// The only call that tolerates an empty 2xx body. What is being observed
	// is that the notification API call was accepted, and a response with
	// nothing in it still says that.
	if err := c.doWith(ctx, http.MethodPost, path, nil, &out, true); err != nil {
		return NotifyReceipt{}, mapNotFound(err, ErrPaymentLinkNotFound, linkID)
	}

	// Accepted comes from the success field when the body carries one, and
	// from the 2xx when it does not. The field turned out to exist: the
	// endpoint answered {"success":true} on 2026-08-31. Reading it makes a
	// 200 that reports a refusal visible instead of being counted as an
	// acceptance.
	//
	// What Accepted means has not widened. On 2026-08-31 a payment link with
	// no contact on it at all still answered notify_by/sms with 200 and
	// {"success":true}, so this reports that the notification API call
	// succeeded and nothing more. Receipt.DeliveryConfirmed stays false.
	accepted := true
	if out.Success != nil {
		accepted = *out.Success
	}

	return NotifyReceipt{
		LinkID:      linkID,
		Medium:      medium,
		Accepted:    accepted,
		RequestedAt: c.clock.Now(),
	}, nil
}
