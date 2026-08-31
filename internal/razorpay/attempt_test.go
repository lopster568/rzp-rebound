package razorpay_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// checkoutBackend replays the five-call sequence the 2026-08-31 spike observed
// against Razorpay test mode, in the shapes it observed, so Attempter can be
// driven with no credential and no network.
//
// It is not evidence about Razorpay. It is a recording of one, and the notes
// in docs/RAZORPAY-TEST-MODE-NOTES.md are what it was written from. What it
// does prove is that Attempter follows the sequence rather than inventing one:
// each handler refuses a call that arrives without the field the real endpoint
// required.
type checkoutBackend struct {
	mu       sync.Mutex
	seen     []string
	fields   map[string]map[string]string
	settleAs string
}

func newCheckoutBackend(t *testing.T) (*checkoutBackend, *httptest.Server) {
	t.Helper()

	b := &checkoutBackend{fields: make(map[string]map[string]string)}
	mux := http.NewServeMux()

	record := func(name string, r *http.Request) map[string]string {
		_ = r.ParseForm()
		got := make(map[string]string, len(r.Form))
		for k := range r.Form {
			got[k] = r.Form.Get(k)
		}
		b.mu.Lock()
		defer b.mu.Unlock()
		b.seen = append(b.seen, name)
		b.fields[name] = got
		return got
	}

	// Step 2. key_id arrives as a form field and there is no Basic auth on
	// this endpoint, which is the whole reason Attempter is not a method on
	// Client.
	mux.HandleFunc("/v1/payments/create/ajax", func(w http.ResponseWriter, r *http.Request) {
		got := record("create_ajax", r)
		if _, _, hasBasic := r.BasicAuth(); hasBasic {
			http.Error(w, `{"error":{"description":"Authentication failed"}}`, http.StatusUnauthorized)
			return
		}
		if got["key_id"] == "" {
			http.Error(w, `{"error":{"description":"Please provide your api key"}}`, http.StatusUnauthorized)
			return
		}
		if got["order_id"] == "" || got["card[number]"] == "" {
			http.Error(w, `{"error":{"description":"missing order_id or card"}}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"payment_id":"pay_TWUV7spZH4h8rl","redirect":true,"type":"redirect",`+
			`"request":{"url":"https://api.razorpay.com/v1/payments/TWUV7spZH4h8rl/authenticate","method":"POST"},"version":1}`)
	})

	// Step 3. The page carries form1, and in test mode it also carries the key
	// id inside the action URL, which is why nothing from here is written
	// anywhere without going through redaction.
	mux.HandleFunc("/v1/payments/TWUV7spZH4h8rl/authenticate", func(w http.ResponseWriter, r *http.Request) {
		got := record("authenticate", r)
		if got["key_id"] == "" {
			http.Error(w, "no key_id", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<!doctype html><html><body>
<form id="form1" name="form1" action="https://api.razorpay.com/v1/gateway/mocksharp/payment?key_id=`+testKeyID+`" method="post">
<input type="hidden" name="action" value="authorize">
<input type="hidden" name="amount" value="100000">
<input type="hidden" name="method" value="card">
<input type="hidden" name="payment_id" value="TWUV7spZH4h8rl">
<input type="hidden" name="callback_url" value="https://api.razorpay.com/v1/payments/pay_TWUV7spZH4h8rl/callback/abc/`+testKeyID+`">
<input type="hidden" name="recurring" value="0">
<input type="hidden" name="encrypt" value="1">
</form></body></html>`)
	})

	// Step 4. The mock bank page. Its form takes a single success field with
	// two values in it, which is the finding that sank the magic card table.
	mux.HandleFunc("/v1/gateway/mocksharp/payment", func(w http.ResponseWriter, r *http.Request) {
		got := record("mocksharp_payment", r)
		if got["payment_id"] == "" || got["action"] != "authorize" {
			http.Error(w, "bad form1 fields", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<!doctype html><html><body>
<form onsubmit="return false" method="post" action="https://api.razorpay.com/v1/gateway/mocksharp/payment/submit?key_id=`+testKeyID+`">
<input type="hidden" name="callback_url" value="https://api.razorpay.com/v1/payments/pay_TWUV7spZH4h8rl/callback/abc/`+testKeyID+`">
<input type="hidden" name="language_code" value="en">
<input type="hidden" name="success">
<button data-val="S" class="success">Success</button>
<button data-val="F" class="danger">Failure</button>
</form></body></html>`)
	})

	// Step 5. The one that settles the payment.
	mux.HandleFunc("/v1/gateway/mocksharp/payment/submit", func(w http.ResponseWriter, r *http.Request) {
		got := record("mocksharp_submit", r)
		b.mu.Lock()
		b.settleAs = got["success"]
		b.mu.Unlock()
		if got["success"] != razorpay.AttemptSucceed && got["success"] != razorpay.AttemptFail {
			http.Error(w, "the bank form takes S or F", http.StatusBadRequest)
			return
		}
		if got["callback_url"] == "" {
			http.Error(w, "the bank form needs the callback url it was given", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<!doctype html><html><body>done</body></html>`)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return b, srv
}

func (b *checkoutBackend) calls() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.seen...)
}

func (b *checkoutBackend) fieldsFor(step string) map[string]string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.fields[step]
}

func newAttempter(t *testing.T, srv *httptest.Server) *razorpay.Attempter {
	t.Helper()

	a, err := razorpay.NewAttempter(razorpay.AttempterOptions{
		KeyID:     testKeyID,
		KeySecret: testKeySecret,
		BaseURL:   srv.URL + "/v1",
	})
	if err != nil {
		t.Fatalf("NewAttempter: %v", err)
	}
	return a
}

// TestAttempterWalksTheFiveCallCheckoutSequence is the PRD Q1 answer written
// as a test. Before 2026-08-31 nothing in this repository could make a payment
// attempt happen: AttemptPayment existed on the fake and nowhere else, and
// both contract harnesses reached past the client into the fake for it.
func TestAttempterWalksTheFiveCallCheckoutSequence(t *testing.T) {
	backend, srv := newCheckoutBackend(t)

	outcome, err := newAttempter(t, srv).Attempt(context.Background(), razorpay.AttemptRequest{
		OrderID:     "order_TWUV6Jba72pLIG",
		AmountPaise: 100000,
		CardNumber:  "4100280000080001",
		Email:       "probe@example.com",
		Contact:     "9999999999",
		Outcome:     razorpay.AttemptFail,
	})
	if err != nil {
		t.Fatalf("Attempt: %v", err)
	}

	if outcome.PaymentID != "pay_TWUV7spZH4h8rl" {
		t.Errorf("payment id = %q, want the id the create call returned", outcome.PaymentID)
	}

	want := []string{"create_ajax", "authenticate", "mocksharp_payment", "mocksharp_submit"}
	got := backend.calls()
	if len(got) != len(want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, got[i], want[i])
		}
	}

	// The bank form's fields have to be carried forward, not reconstructed.
	// The callback url is signed per payment and there is no way to build one.
	submit := backend.fieldsFor("mocksharp_submit")
	if submit["success"] != razorpay.AttemptFail {
		t.Errorf("success = %q, want %q", submit["success"], razorpay.AttemptFail)
	}
	if !strings.Contains(submit["callback_url"], "pay_TWUV7spZH4h8rl") {
		t.Errorf("callback_url = %q, want the one the bank page carried", submit["callback_url"])
	}

	// The card details reach the create call and nothing after it.
	create := backend.fieldsFor("create_ajax")
	if create["card[number]"] != "4100280000080001" {
		t.Errorf("card[number] = %q, want the card the caller asked for", create["card[number]"])
	}
	for _, step := range []string{"authenticate", "mocksharp_payment", "mocksharp_submit"} {
		for key, value := range backend.fieldsFor(step) {
			if strings.Contains(value, "4100280000080001") {
				t.Errorf("%s carried the full card number in %s", step, key)
			}
		}
	}
}

// TestAttempterSendsTheOutcomeTheCallerAsksFor pins the finding that the card
// number does not choose the outcome. The mock bank does, through one field
// with two values in it, so Attempt has to be told which one to send.
func TestAttempterSendsTheOutcomeTheCallerAsksFor(t *testing.T) {
	for _, outcome := range []string{razorpay.AttemptSucceed, razorpay.AttemptFail} {
		t.Run(outcome, func(t *testing.T) {
			backend, srv := newCheckoutBackend(t)

			if _, err := newAttempter(t, srv).Attempt(context.Background(), razorpay.AttemptRequest{
				OrderID:     "order_TWUV6Jba72pLIG",
				AmountPaise: 100000,
				CardNumber:  "4100280000080001",
				Outcome:     outcome,
			}); err != nil {
				t.Fatalf("Attempt: %v", err)
			}

			if got := backend.fieldsFor("mocksharp_submit")["success"]; got != outcome {
				t.Errorf("the bank was sent success=%q, want %q", got, outcome)
			}
		})
	}
}

// TestAttempterRejectsAnOutcomeItCannotDrive keeps the two-valued field
// honest. An empty or invented outcome silently defaulting to one of the two
// would make a demo report a result nobody asked for.
func TestAttempterRejectsAnOutcomeItCannotDrive(t *testing.T) {
	_, srv := newCheckoutBackend(t)
	a := newAttempter(t, srv)

	for _, outcome := range []string{"", "success", "yes", "s"} {
		_, err := a.Attempt(context.Background(), razorpay.AttemptRequest{
			OrderID:     "order_TWUV6Jba72pLIG",
			AmountPaise: 100000,
			CardNumber:  "4100280000080001",
			Outcome:     outcome,
		})
		if !errors.Is(err, razorpay.ErrUnsupportedAttemptOutcome) {
			t.Errorf("Attempt with outcome %q returned %v, want ErrUnsupportedAttemptOutcome", outcome, err)
		}
	}
}

// TestAttempterRedactsCredentialsFromItsErrors is the concrete case the
// redaction work exists for. The authenticate page and the bank page both
// carry the key id in a form action, so an error quoting either of those
// pages would put half a credential pair into a log line.
func TestAttempterRedactsCredentialsFromItsErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"description":"upstream blew up for key `+testKeyID+
			` with secret `+testKeySecret+`"}}`)
	}))
	defer srv.Close()

	_, err := newAttempter(t, srv).Attempt(context.Background(), razorpay.AttemptRequest{
		OrderID:     "order_TWUV6Jba72pLIG",
		AmountPaise: 100000,
		CardNumber:  "4100280000080001",
		Outcome:     razorpay.AttemptFail,
	})
	if err == nil {
		t.Fatal("a 500 on the first call returned no error")
	}
	for _, credential := range []string{testKeyID, testKeySecret} {
		if strings.Contains(err.Error(), credential) {
			t.Errorf("the error carries a credential: %s", err)
		}
	}
	if !strings.Contains(err.Error(), razorpay.Redacted) {
		t.Errorf("err = %q, which does not say anything was redacted", err)
	}
}

// TestAttempterKeepsTheKeyIDOutOfEverySpanAttribute is the leak the live half
// found, written as the assertion that would have caught it.
//
// Two of the four checkout calls take key_id as a query parameter, because
// that is how the form actions on Razorpay's own pages are built, and the
// callback the last one redirects to carries the key id as a path segment.
// otelhttp records url.full, so an instrumented transport on this sequence put
// half a credential pair into six span attributes of a single demo run,
// observed in Jaeger on 2026-08-31.
//
// The fix is that Attempter does not use otelhttp at all. It opens its own
// span per step with attributes it chose, and a URL is not one of them. The
// spans are better for it: "razorpay.checkout.settle" says more than
// "HTTP POST" ever did.
func TestAttempterKeepsTheKeyIDOutOfEverySpanAttribute(t *testing.T) {
	_, srv := newCheckoutBackend(t)

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Errorf("tracer provider shutdown: %v", err)
		}
	})

	a, err := razorpay.NewAttempter(razorpay.AttempterOptions{
		KeyID:          testKeyID,
		KeySecret:      testKeySecret,
		BaseURL:        srv.URL + "/v1",
		TracerProvider: tp,
	})
	if err != nil {
		t.Fatalf("NewAttempter: %v", err)
	}

	if _, err := a.Attempt(context.Background(), razorpay.AttemptRequest{
		OrderID:     "order_TWUV6Jba72pLIG",
		AmountPaise: 100000,
		CardNumber:  "4100280000080001",
		Outcome:     razorpay.AttemptFail,
	}); err != nil {
		t.Fatalf("Attempt: %v", err)
	}

	spans := recorder.Ended()
	if len(spans) == 0 {
		t.Fatal("the attempt recorded no spans at all, so this test proves nothing")
	}

	// The checkout pages in the backend carry the key id in their form actions,
	// exactly as the real ones do, so a URL attribute would carry it here too.
	attributes := 0
	names := make(map[string]bool, len(spans))
	for _, span := range spans {
		names[span.Name()] = true
		for _, attr := range span.Attributes() {
			attributes++
			value := attr.Value.Emit()
			for label, credential := range map[string]string{
				"key id":     testKeyID,
				"key secret": testKeySecret,
			} {
				if strings.Contains(value, credential) {
					t.Errorf("span %q attribute %s carries the %s", span.Name(), attr.Key, label)
				}
			}
		}
	}
	if attributes == 0 {
		t.Error("the attempt spans carry no attributes at all, so the scan above proved nothing")
	}

	// The steps have to be named, or a trace of a failed attempt says only
	// that something went wrong somewhere in four calls.
	for _, want := range []string{
		"razorpay.checkout.attempt",
		"razorpay.checkout.create_payment",
		"razorpay.checkout.authenticate",
		"razorpay.checkout.gateway",
		"razorpay.checkout.settle",
	} {
		if !names[want] {
			t.Errorf("no span named %q. recorded: %v", want, sortedNames(names))
		}
	}
}

func sortedNames(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
