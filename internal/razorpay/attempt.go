package razorpay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// AttempterTracerName names the tracer the checkout sequence opens spans on.
const AttempterTracerName = "github.com/lopster568/rzp-recovery-agent/internal/razorpay/attempt"

// Span attribute keys for a checkout attempt. There is deliberately no URL
// among them. See the doc comment on Attempter.
const (
	AttrCheckoutStep   = "rzp.checkout.step"
	AttrCheckoutStatus = "rzp.checkout.http_status"
	AttrAttemptOutcome = "rzp.attempt.outcome"
	AttrAttemptOrderID = "rzp.order_id"
	AttrAttemptPayment = "rzp.payment_id"
)

// Attempt outcomes. The mock bank page settles a payment through one form
// field with exactly two values in it, and these are the two. They are not a
// choice this project made: they are what the buttons on that page carry in
// their data-val attributes, observed on 2026-08-31.
const (
	// AttemptSucceed drives the payment to captured and the order to paid.
	AttemptSucceed = "S"
	// AttemptFail drives the payment to failed and leaves the order at
	// attempted.
	AttemptFail = "F"
)

// ErrUnsupportedAttemptOutcome is returned for an outcome the bank form has no
// value for. Defaulting to one of the two would make a run report a result
// nobody asked for.
var ErrUnsupportedAttemptOutcome = errors.New("razorpay: attempt outcome must be AttemptSucceed or AttemptFail")

// ErrAttemptSequenceBroke is returned when a page in the checkout sequence did
// not carry the form the next step needs. It is a distinct error because the
// sequence is undocumented: the day Razorpay changes one of those pages, this
// is what says so, rather than a decode failure somewhere further down.
var ErrAttemptSequenceBroke = errors.New("razorpay: the checkout sequence did not return the form the next step needs")

// Checkout endpoint paths. Unlike the paths in client.go these were confirmed
// against live test mode on 2026-08-31, and docs/RAZORPAY-TEST-MODE-NOTES.md
// records the sequence and what each step answers.
const (
	pathCreateAjax   = "/payments/create/ajax"
	pathAuthenticate = "/payments/%s/authenticate"
)

// AttempterOptions configures an Attempter.
type AttempterOptions struct {
	// KeyID is the test-mode key id. Required: the checkout endpoints take it
	// as a form field rather than through HTTP Basic auth.
	KeyID string
	// KeySecret is not sent anywhere by this type. It is held so Redact can
	// scrub it out of an error, because these endpoints answer with HTML
	// pages that carry credentials in form actions.
	KeySecret string
	// BaseURL defaults to DefaultBaseURL.
	BaseURL string
	// Transport is the round tripper the checkout calls go over. Nil means
	// http.DefaultTransport. Unlike Client's, it is not wrapped in otelhttp:
	// see the comment in NewAttempter.
	//
	// TracerProvider is where the per-step spans go. Nil means the global
	// provider.
	//
	// RawCapture and Clock are passed through to the internal client, so a
	// checkout response is scrubbed and recorded on the same path an API
	// response is.
	Transport      http.RoundTripper
	TracerProvider trace.TracerProvider
	RawCapture     io.Writer
	Clock          clock.Clock
}

// AttemptRequest is one payment attempt against an existing order.
type AttemptRequest struct {
	// OrderID is the order to attempt. Required.
	OrderID string
	// AmountPaise must match the order amount.
	AmountPaise int64
	// CardNumber is the card to attempt with. It reaches the first call and
	// no call after it.
	//
	// It does not choose the outcome. Every card in
	// testdata/magic_cards.json produced the identical failure through this
	// sequence on 2026-08-31, so Outcome is what decides.
	CardNumber string
	// Email and Contact are what checkout requires of a payer. They default
	// to values in reserved namespaces that reach nobody.
	Email   string
	Contact string
	// Outcome is AttemptSucceed or AttemptFail.
	Outcome string
}

// AttemptOutcome is what an attempt produced. It carries no payment state,
// because state is read back from the API rather than from the page that
// settled it, which is the rule the recovery orchestrator already follows.
type AttemptOutcome struct {
	// PaymentID is the payment the first call created.
	PaymentID string
	// Outcome is the value that was sent to the mock bank.
	Outcome string
	// Steps names each call that was attempted, in order, for the audit
	// trail.
	//
	// A step is recorded when it is sent, not when it comes back. A call that
	// reached Razorpay and had its effect but whose response failed is a call
	// that was made, and recording it as never made is the wrong direction to
	// be wrong in for a system whose argument is that the trail says what
	// happened. The last step is the case that matters: the mock bank can be
	// told to authorize and the response can still fail.
	//
	// So the presence of a step means it was sent, not that it succeeded. The
	// error returned alongside says which one stopped the run.
	Steps []string
}

// Attempter drives a Razorpay test-mode payment attempt to a settled state.
//
// It is deliberately not a method on Client and deliberately not on Port.
// Client is the documented server API, authenticated with the key pair. The
// four calls here are the checkout front end's own: they authenticate with the
// key id alone as a form field or a query parameter, two of them answer with
// HTML, and none of them is documented. Putting them behind the same type
// would tell a caller the whole surface carries the same support promise.
//
// The sequence and the evidence for it are in
// docs/RAZORPAY-TEST-MODE-NOTES.md. It is test mode only, and the mock bank
// page it ends at does not exist in live mode.
//
// What it does not inherit from Client: the 429 backoff and the concurrency
// semaphore. Those live on Client.do and these four calls do not go through
// it, so an attempt is one request per step with no retry and no cap. That is
// deliberate for now rather than hidden, because a retry of an undocumented
// checkout call is a second payment attempt, not a repeated read, and nothing
// has measured what these endpoints do under load. A batch runner that drives
// many attempts at once is phase 2, and it is the thing that will need this
// answered.
type Attempter struct {
	client  *Client
	http    *http.Client
	tracer  trace.Tracer
	keyID   string
	apiRoot string
}

// NewAttempter returns an Attempter.
func NewAttempter(opts AttempterOptions) (*Attempter, error) {
	if opts.KeyID == "" {
		return nil, fmt.Errorf("%w: an attempt needs a key id", ErrMissingCredentials)
	}

	c, err := newClient(ClientOptions{
		KeyID:          opts.KeyID,
		KeySecret:      opts.KeySecret,
		BaseURL:        opts.BaseURL,
		Transport:      opts.Transport,
		TracerProvider: opts.TracerProvider,
		RawCapture:     opts.RawCapture,
		Clock:          opts.Clock,
	})
	if err != nil {
		return nil, err
	}

	// Form actions on the checkout pages are absolute URLs pointing at
	// api.razorpay.com. Only their path and query are followed, resolved
	// against the configured root, so a test server sees its own host and a
	// page cannot send a run somewhere it was not pointed.
	parsed, err := url.Parse(c.BaseURL())
	if err != nil {
		// Not echoed, for the reason newClient gives.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return nil, fmt.Errorf("razorpay: the attempter base url does not parse: %v", uerr.Err)
		}
		return nil, fmt.Errorf("razorpay: the attempter base url does not parse")
	}

	// The HTTP client here is deliberately not otelhttp instrumented, unlike
	// Client's. otelhttp records url.full, and two of these four calls carry
	// key_id as a query parameter while the callback the last one redirects to
	// carries it as a path segment, so an instrumented transport put the key
	// id into six span attributes of one demo run. Observed in Jaeger on
	// 2026-08-31 and fixed here rather than by scrubbing after the fact: the
	// span never gets the URL in the first place.
	//
	// Nothing is lost. The per-step spans opened below say which of the four
	// calls a run was on, which "HTTP POST" never did.
	base := opts.Transport
	if base == nil {
		base = http.DefaultTransport
	}

	tp := opts.TracerProvider
	if tp == nil {
		tp = otel.GetTracerProvider()
	}

	return &Attempter{
		client: c,
		http: &http.Client{
			Transport: base,
			// The redirect pin matters more here than on Client. Two of these
			// four calls carry key_id in the query and the callback carries it
			// as a path segment, so an unpinned 302 hands a foreign host half
			// a credential pair and an unpinned 307 replays the form body,
			// which is the key id, the card number, and the CVV. Found in
			// review on 2026-08-31 and reproduced.
			//
			// The same-origin callback the settle call redirects to is still
			// followed, which the attempt sequence depends on.
			CheckRedirect: pinnedRedirect(parsed),
			Timeout:       DefaultRequestTimeout,
		},
		tracer:  tp.Tracer(AttempterTracerName),
		keyID:   opts.KeyID,
		apiRoot: parsed.Scheme + "://" + parsed.Host,
	}, nil
}

// Redact scrubs the configured credentials out of s, the same way Client does.
func (a *Attempter) Redact(s string) string { return a.client.Redact(s) }

// Attempt drives one payment attempt through the four checkout calls.
//
// It returns when the mock bank has been told which way to settle. The caller
// reads the resulting payment and order back through Client, because what a
// page said happened is not what this project counts.
func (a *Attempter) Attempt(ctx context.Context, req AttemptRequest) (AttemptOutcome, error) {
	if req.Outcome != AttemptSucceed && req.Outcome != AttemptFail {
		return AttemptOutcome{}, fmt.Errorf("%w: got %q", ErrUnsupportedAttemptOutcome, req.Outcome)
	}
	if req.OrderID == "" {
		return AttemptOutcome{}, fmt.Errorf("%w: no order id given", ErrOrderNotFound)
	}
	if req.AmountPaise <= 0 {
		return AttemptOutcome{}, fmt.Errorf("%w: got %d paise", ErrAmountNotPositive, req.AmountPaise)
	}

	email := req.Email
	if email == "" {
		// example.com is reserved by IANA and cannot receive mail, so a run
		// that leaks an address leaks one that reaches nobody.
		email = "probe@example.com"
	}
	contact := req.Contact
	if contact == "" {
		contact = "9999999999"
	}

	ctx, span := a.tracer.Start(ctx, "razorpay.checkout.attempt",
		trace.WithAttributes(
			attribute.String(AttrAttemptOrderID, req.OrderID),
			attribute.String(AttrAttemptOutcome, req.Outcome),
		))
	defer span.End()

	outcome := AttemptOutcome{Outcome: req.Outcome}

	// Step 1. Create the payment. key_id goes in the form body and there is no
	// Basic auth on this endpoint: sending one is answered with a 401.
	created, err := a.postForm(ctx, "create_payment", a.apiRoot+a.path(pathCreateAjax), url.Values{
		"key_id":             {a.keyID},
		"amount":             {strconv.FormatInt(req.AmountPaise, 10)},
		"currency":           {"INR"},
		"order_id":           {req.OrderID},
		"email":              {email},
		"contact":            {contact},
		"method":             {"card"},
		"card[number]":       {req.CardNumber},
		"card[name]":         {"Recovery Agent Test"},
		"card[expiry_month]": {"12"},
		"card[expiry_year]":  {"30"},
		"card[cvv]":          {"123"},
	})
	outcome.Steps = append(outcome.Steps, "create_payment")
	if err != nil {
		return outcome, a.fail(span, err)
	}

	var ajax struct {
		PaymentID string `json:"payment_id"`
		Request   struct {
			URL string `json:"url"`
		} `json:"request"`
	}
	if err := json.Unmarshal(created, &ajax); err != nil {
		return outcome, a.fail(span, a.client.redactErr(fmt.Errorf("razorpay: decode the create-payment response: %w", err)))
	}
	if ajax.PaymentID == "" {
		return outcome, a.fail(span, a.client.redactErr(fmt.Errorf("%w: the create-payment response carried no payment_id", ErrAttemptSequenceBroke)))
	}
	outcome.PaymentID = ajax.PaymentID
	span.SetAttributes(attribute.String(AttrAttemptPayment, ajax.PaymentID))

	// Step 2. The authenticate page. Its URL comes out of the response rather
	// than being rebuilt here, so the sequence follows what the API said to do
	// next. The id in that URL carries no pay_ prefix.
	authURL := a.resolve(ajax.Request.URL)
	if authURL == "" {
		authURL = a.apiRoot + a.path(fmt.Sprintf(pathAuthenticate,
			url.PathEscape(strings.TrimPrefix(ajax.PaymentID, "pay_"))))
	}
	outcome.Steps = append(outcome.Steps, "authenticate")
	authPage, err := a.postForm(ctx, "authenticate", authURL, url.Values{"key_id": {a.keyID}})
	if err != nil {
		return outcome, a.fail(span, err)
	}

	gatewayURL, gatewayFields, err := a.form(string(authPage), `id="form1"`)
	if err != nil {
		return outcome, a.fail(span, a.client.redactErr(fmt.Errorf("%w: the authenticate page has no form1", ErrAttemptSequenceBroke)))
	}

	// Step 3. The mock bank gateway. The fields are carried forward rather
	// than rebuilt: the callback url among them is signed per payment and
	// there is no way to construct one.
	outcome.Steps = append(outcome.Steps, "gateway")
	bankPage, err := a.postForm(ctx, "gateway", gatewayURL, gatewayFields)
	if err != nil {
		return outcome, a.fail(span, err)
	}

	submitURL, submitFields, err := a.form(string(bankPage), "")
	if err != nil {
		return outcome, a.fail(span, a.client.redactErr(fmt.Errorf("%w: the mock bank page has no form", ErrAttemptSequenceBroke)))
	}

	// Step 4. Settle it. This one field is the entire outcome, which is why
	// the card table could not be verified: the card never reaches this call.
	submitFields.Set("success", req.Outcome)
	outcome.Steps = append(outcome.Steps, "settle")
	if _, err := a.postForm(ctx, "settle", submitURL, submitFields); err != nil {
		return outcome, a.fail(span, err)
	}

	return outcome, nil
}

// fail marks the attempt span and returns the error unchanged, so a trace
// shows which of the four calls a run stopped on.
func (a *Attempter) fail(span trace.Span, err error) error {
	span.SetStatus(codes.Error, "checkout attempt did not settle")
	span.RecordError(err)
	return err
}

// path joins a checkout path onto the configured base path, so a base URL that
// already carries /v1 does not end up with a second one.
func (a *Attempter) path(p string) string {
	parsed, err := url.Parse(a.client.BaseURL())
	if err != nil {
		return p
	}
	return strings.TrimRight(parsed.Path, "/") + p
}

// resolve takes the path and query of an absolute URL a checkout page handed
// back and rebuilds it against the configured root, so a page cannot send a
// run to a host it was not pointed at and a test server sees its own address.
func (a *Attempter) resolve(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Path == "" {
		return ""
	}
	// A path without a leading slash would concatenate straight onto the host
	// and produce something like https://host:9000relative, which url.Parse
	// then rejects with an invalid-port error that says nothing useful. The
	// observed pages all use absolute URLs, so this is a guard rather than a
	// fix for something seen. Review finding, 2026-08-31.
	path := parsed.Path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	out := a.apiRoot + path
	if parsed.RawQuery != "" {
		out += "?" + parsed.RawQuery
	}
	return out
}

// postForm sends one form-encoded request and returns the body.
//
// Everything it returns as an error has been through Client.Redact first. The
// authenticate page and the mock bank page both carry the key id inside a form
// action, so an error quoting either of them would otherwise put half a
// credential pair into a log line.
func (a *Attempter) postForm(ctx context.Context, step, rawURL string, form url.Values) ([]byte, error) {
	ctx, span := a.tracer.Start(ctx, "razorpay.checkout."+step,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String(AttrCheckoutStep, step)))
	defer span.End()

	// stepErr marks this step's own span before returning. Without it a
	// transport failure left the step span indistinguishable from a
	// successful one, with neither a status attribute nor an error on it,
	// which undercuts the reason these spans exist. Review finding,
	// 2026-08-31.
	stepErr := func(err error) ([]byte, error) {
		span.SetStatus(codes.Error, "checkout step failed")
		span.RecordError(err)
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return stepErr(a.client.redactErr(fmt.Errorf("razorpay: build the %s call: %w", step, err)))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.http.Do(req)
	if err != nil {
		return stepErr(a.client.redactErr(fmt.Errorf("razorpay: %s: %w", step, err)))
	}
	defer func() { _ = resp.Body.Close() }()
	span.SetAttributes(attribute.Int(AttrCheckoutStatus, resp.StatusCode))

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return stepErr(a.client.redactErr(fmt.Errorf("razorpay: read the %s response: %w", step, err)))
	}
	if len(body) > maxResponseBytes {
		return stepErr(fmt.Errorf("razorpay: the %s response is larger than the %d byte cap", step, maxResponseBytes))
	}

	// The capture line records the path that actually answered, not a
	// synthetic one. It used to say "/checkout/<step>", which is not a path
	// Razorpay serves, in a stream the client documents as the record of what
	// Razorpay sent. Review finding, 2026-08-31.
	path := resp.Request.URL.Path
	if path == "" {
		path = "/checkout/" + step
	}
	if err := a.client.captureResponse(http.MethodPost, path, resp.StatusCode, body); err != nil {
		return stepErr(a.client.redactErr(err))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return stepErr(a.client.redactErr(a.client.apiError(http.MethodPost, path, resp.StatusCode, body)))
	}
	return body, nil
}

// decodeHTMLAttr undoes the entity escaping an HTML attribute value carries.
//
// Only the actions were decoded until a review on 2026-08-31 pointed out that
// input values were not, which is asymmetric for no reason: a hidden field
// holding a URL with two query parameters must be written with an escaped
// ampersand in valid HTML, and the field this would land on is the callback
// url, the one the code carries forward precisely because it cannot be
// rebuilt. The observed pages have no such value, so this was latent.
//
// The five predefined entities are the whole set that matters here. A numeric
// character reference in a Razorpay hidden field would be a different problem
// and is not being guessed at.
func decodeHTMLAttr(s string) string {
	return htmlAttrReplacer.Replace(s)
}

// htmlAttrReplacer expands the ampersand entity last so that an already
// escaped sequence is not double decoded.
var htmlAttrReplacer = strings.NewReplacer(
	"&lt;", "<",
	"&gt;", ">",
	"&quot;", `"`,
	"&#39;", "'",
	"&apos;", "'",
	"&amp;", "&",
)

var (
	formTagPattern   = regexp.MustCompile(`(?is)<form[^>]*>`)
	actionPattern    = regexp.MustCompile(`(?is)action="([^"]*)"`)
	inputTagPattern  = regexp.MustCompile(`(?is)<input[^>]*>`)
	attrNamePattern  = regexp.MustCompile(`(?is)\bname="([^"]*)"`)
	attrValuePattern = regexp.MustCompile(`(?is)\bvalue="([^"]*)"`)
)

// form finds the first form tag containing marker and returns its resolved
// action plus every input on the page.
//
// The parsing is regexp rather than a real HTML parser on purpose: these two
// pages are a handful of hidden inputs each, the project has no other reason
// to carry an HTML parser, and a page whose shape changed enough to break a
// regexp has changed enough that the sequence needs re-checking anyway, which
// is what ErrAttemptSequenceBroke says when it does.
//
// Inputs are collected from the whole document rather than from inside the
// matched form, because both observed pages carry exactly one form of interest
// and its hidden inputs sit inside it. An input with no value attribute is
// collected with an empty value, which is how the success field on the bank
// page arrives before it is set.
func (a *Attempter) form(page, marker string) (string, url.Values, error) {
	var action string
	for _, tag := range formTagPattern.FindAllString(page, -1) {
		if marker != "" && !strings.Contains(tag, marker) {
			continue
		}
		m := actionPattern.FindStringSubmatch(tag)
		if m == nil {
			continue
		}
		action = decodeHTMLAttr(m[1])
		break
	}
	if action == "" {
		return "", nil, ErrAttemptSequenceBroke
	}

	resolved := a.resolve(action)
	if resolved == "" {
		return "", nil, ErrAttemptSequenceBroke
	}

	fields := url.Values{}
	for _, tag := range inputTagPattern.FindAllString(page, -1) {
		name := attrNamePattern.FindStringSubmatch(tag)
		if name == nil || name[1] == "" {
			continue
		}
		value := ""
		if v := attrValuePattern.FindStringSubmatch(tag); v != nil {
			value = decodeHTMLAttr(v[1])
		}
		fields.Set(decodeHTMLAttr(name[1]), value)
	}
	return resolved, fields, nil
}
