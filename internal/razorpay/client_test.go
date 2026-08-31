package razorpay_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// recordingWait is a razorpay.WaitFunc that writes down what it was asked to
// wait for and moves a fake clock instead of sleeping. Every backoff assertion
// in this file reads its record, so the suite spends no wall-clock time on
// retries.
type recordingWait struct {
	mu    sync.Mutex
	waits []time.Duration
	clock *clock.FakeClock
}

func newRecordingWait() *recordingWait {
	return &recordingWait{clock: clock.NewFake(fakeStart)}
}

func (w *recordingWait) Wait(_ context.Context, d time.Duration) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.waits = append(w.waits, d)
	w.clock.Advance(d)
	return nil
}

func (w *recordingWait) recorded() []time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]time.Duration(nil), w.waits...)
}

func newTestClient(t *testing.T, baseURL string, mutate func(*razorpay.ClientOptions)) *razorpay.Client {
	t.Helper()

	opts := razorpay.ClientOptions{
		KeyID:     testKeyID,
		KeySecret: testKeySecret,
		BaseURL:   baseURL,
		Clock:     clock.NewFake(fakeStart),
	}
	if mutate != nil {
		mutate(&opts)
	}

	c, err := razorpay.NewClient(opts)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

func TestClientSetsBasicAuthFromKeyPair(t *testing.T) {
	var (
		gotUser, gotPass string
		gotOK            bool
		gotURL           string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, gotOK = r.BasicAuth()
		gotURL = r.URL.String()
		writeJSON(w, http.StatusOK, razorpay.Order{ID: "order_auth00000001", Status: razorpay.OrderStatusCreated})
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL+"/v1", nil)

	if _, err := c.FetchOrder(context.Background(), "order_auth00000001"); err != nil {
		t.Fatalf("FetchOrder: %v", err)
	}

	if !gotOK {
		t.Fatal("the request carried no HTTP Basic auth header")
	}
	if gotUser != testKeyID {
		t.Errorf("basic auth user = %q, want the configured key id", gotUser)
	}
	if gotPass != testKeySecret {
		t.Errorf("basic auth password does not match the configured key secret")
	}
	// A credential in a URL reaches access logs, referrers, and span
	// attributes. The header is the only place it belongs.
	if strings.Contains(gotURL, testKeySecret) || strings.Contains(gotURL, testKeyID) {
		t.Errorf("a credential reached the request URL: %q", gotURL)
	}
}

func TestClientCreateOrderPostsExpectedPayload(t *testing.T) {
	var (
		gotMethod      string
		gotPath        string
		gotContentType string
		gotBody        map[string]any
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, razorpay.Order{
			ID:          "order_created00001",
			AmountPaise: 349900,
			AmountDue:   349900,
			Currency:    "INR",
			Receipt:     "rcpt_payload",
			Status:      razorpay.OrderStatusCreated,
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL+"/v1", nil)

	order, err := c.CreateOrder(context.Background(), razorpay.CreateOrderRequest{
		AmountPaise: 349900,
		Currency:    "INR",
		Receipt:     "rcpt_payload",
		Notes:       map[string]string{"batch": "b1"},
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/orders" {
		t.Errorf("path = %q, want /v1/orders", gotPath)
	}
	if !strings.HasPrefix(gotContentType, "application/json") {
		t.Errorf("content type = %q, want application/json", gotContentType)
	}

	if got, want := gotBody["amount"], float64(349900); got != want {
		t.Errorf("body amount = %v, want %v (paise, not rupees)", got, want)
	}
	if got := gotBody["currency"]; got != "INR" {
		t.Errorf("body currency = %v, want INR", got)
	}
	if got := gotBody["receipt"]; got != "rcpt_payload" {
		t.Errorf("body receipt = %v, want rcpt_payload", got)
	}
	notes, ok := gotBody["notes"].(map[string]any)
	if !ok {
		t.Fatalf("body notes = %v, want an object", gotBody["notes"])
	}
	if notes["batch"] != "b1" {
		t.Errorf("body notes.batch = %v, want b1", notes["batch"])
	}

	if order.ID != "order_created00001" {
		t.Errorf("decoded id = %q, want order_created00001", order.ID)
	}
	if order.AmountPaise != 349900 {
		t.Errorf("decoded amount = %d paise, want 349900", order.AmountPaise)
	}
	if order.Status != razorpay.OrderStatusCreated {
		t.Errorf("decoded status = %q, want %q", order.Status, razorpay.OrderStatusCreated)
	}
}

func TestClientRetriesOn429WithBackoffUpToCap(t *testing.T) {
	t.Run("gives up after the attempt limit", func(t *testing.T) {
		var calls atomic.Int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"description": "rate limited"})
		}))
		defer srv.Close()

		wait := newRecordingWait()
		c := newTestClient(t, srv.URL+"/v1", func(o *razorpay.ClientOptions) {
			o.MaxAttempts = 4
			o.BaseBackoff = 100 * time.Millisecond
			o.MaxBackoff = 200 * time.Millisecond
			o.Wait = wait.Wait
		})

		_, err := c.FetchOrder(context.Background(), "order_ratelimited1")
		if err == nil {
			t.Fatal("a run of 429s returned no error")
		}
		if !errors.Is(err, razorpay.ErrRetryBudgetExhausted) {
			t.Errorf("error = %v, want it to wrap ErrRetryBudgetExhausted", err)
		}
		if got := calls.Load(); got != 4 {
			t.Errorf("server saw %d requests, want 4 (the first try plus three retries)", got)
		}

		// 100ms, then double to 200ms, then held at the 200ms cap rather than
		// growing to 400ms.
		want := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond, 200 * time.Millisecond}
		if got := wait.recorded(); !reflect.DeepEqual(got, want) {
			t.Errorf("backoff waits = %v, want %v", got, want)
		}
		for _, d := range wait.recorded() {
			if d > 200*time.Millisecond {
				t.Errorf("a backoff of %v exceeded the configured 200ms cap", d)
			}
		}
	})

	t.Run("succeeds once the 429s stop", func(t *testing.T) {
		var calls atomic.Int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if calls.Add(1) <= 2 {
				writeJSON(w, http.StatusTooManyRequests, map[string]string{"description": "rate limited"})
				return
			}
			writeJSON(w, http.StatusOK, razorpay.Order{ID: "order_recovered001", Status: razorpay.OrderStatusCreated})
		}))
		defer srv.Close()

		wait := newRecordingWait()
		c := newTestClient(t, srv.URL+"/v1", func(o *razorpay.ClientOptions) {
			o.MaxAttempts = 4
			o.BaseBackoff = 100 * time.Millisecond
			o.MaxBackoff = 200 * time.Millisecond
			o.Wait = wait.Wait
		})

		order, err := c.FetchOrder(context.Background(), "order_recovered001")
		if err != nil {
			t.Fatalf("FetchOrder: %v", err)
		}
		if order.ID != "order_recovered001" {
			t.Errorf("id = %q, want order_recovered001", order.ID)
		}
		if got := calls.Load(); got != 3 {
			t.Errorf("server saw %d requests, want 3", got)
		}
		if got := len(wait.recorded()); got != 2 {
			t.Errorf("waited %d times, want 2", got)
		}
	})
}

func TestClientDoesNotRetryOn400(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeJSON(w, http.StatusBadRequest, map[string]string{"description": "amount must be at least 100"})
	}))
	defer srv.Close()

	wait := newRecordingWait()
	c := newTestClient(t, srv.URL+"/v1", func(o *razorpay.ClientOptions) {
		o.MaxAttempts = 4
		o.BaseBackoff = 100 * time.Millisecond
		o.Wait = wait.Wait
	})

	_, err := c.FetchOrder(context.Background(), "order_badrequest01")
	if err == nil {
		t.Fatal("a 400 returned no error")
	}
	// The same request gets the same refusal, so a retry spends a call and a
	// rate-limit slot on a certainty.
	if got := calls.Load(); got != 1 {
		t.Errorf("server saw %d requests for one 400, want 1", got)
	}
	if got := wait.recorded(); len(got) != 0 {
		t.Errorf("client backed off %v on a 400, want no wait at all", got)
	}
	if errors.Is(err, razorpay.ErrRetryBudgetExhausted) {
		t.Error("a 400 reported an exhausted retry budget, but it was never retried")
	}

	var apiErr *razorpay.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want an *razorpay.APIError", err)
	}
	if apiErr.StatusCode != http.StatusBadRequest {
		t.Errorf("APIError.StatusCode = %d, want 400", apiErr.StatusCode)
	}
}

func TestClientEmitsClientSpanPerRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, razorpay.Order{ID: "order_spanned00001", Status: razorpay.OrderStatusCreated})
	}))
	defer srv.Close()

	recorder := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() {
		if err := tp.Shutdown(context.Background()); err != nil {
			t.Errorf("tracer provider shutdown: %v", err)
		}
	})

	c := newTestClient(t, srv.URL+"/v1", func(o *razorpay.ClientOptions) {
		o.TracerProvider = tp
	})

	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := c.FetchOrder(ctx, "order_spanned00001"); err != nil {
			t.Fatalf("FetchOrder %d: %v", i, err)
		}
	}

	clientSpans := 0
	for _, span := range recorder.Ended() {
		if span.SpanKind() == trace.SpanKindClient {
			clientSpans++
		}
	}
	if clientSpans != 2 {
		t.Errorf("recorded %d client spans for 2 requests, want 2", clientSpans)
	}
}

func TestClientRedactsSecretFromErrorMessages(t *testing.T) {
	token := base64.StdEncoding.EncodeToString([]byte(testKeyID + ":" + testKeySecret))

	// A gateway that echoes the request back into its error body is the case
	// that turns a 500 into a credential leak in a log line.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"description":   "upstream failed for key " + testKeyID + " with secret " + testKeySecret,
			"authorization": "Basic " + token,
		})
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL+"/v1", nil)

	_, err := c.FetchOrder(context.Background(), "order_leaky0000001")
	if err == nil {
		t.Fatal("a 500 returned no error")
	}

	msg := err.Error()
	for name, secret := range map[string]string{
		"key id":           testKeyID,
		"key secret":       testKeySecret,
		"basic auth token": token,
	} {
		if strings.Contains(msg, secret) {
			t.Errorf("the %s reached the error message: %q", name, msg)
		}
	}
	if !strings.Contains(msg, razorpay.Redacted) {
		t.Errorf("error message %q carries no redaction marker, so nothing was scrubbed", msg)
	}

	var apiErr *razorpay.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want an *razorpay.APIError", err)
	}
	for _, secret := range []string{testKeyID, testKeySecret, token} {
		if strings.Contains(apiErr.Body, secret) {
			t.Errorf("a credential survived into APIError.Body: %q", apiErr.Body)
		}
	}

	if got := c.Redact("plain " + testKeySecret + " text"); strings.Contains(got, testKeySecret) {
		t.Errorf("Redact left the secret in %q", got)
	}
}

func TestClientCapsConcurrencyAtConfiguredLimit(t *testing.T) {
	const (
		limit    = 2
		requests = 6
	)

	var inflight, maxSeen atomic.Int64
	release := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := inflight.Add(1)
		for {
			seen := maxSeen.Load()
			if n <= seen || maxSeen.CompareAndSwap(seen, n) {
				break
			}
		}
		<-release
		inflight.Add(-1)
		writeJSON(w, http.StatusOK, razorpay.Order{ID: "order_concurrent01", Status: razorpay.OrderStatusCreated})
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL+"/v1", func(o *razorpay.ClientOptions) {
		o.MaxConcurrent = limit
	})

	var wg sync.WaitGroup
	errs := make(chan error, requests)
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.FetchOrder(context.Background(), "order_concurrent01")
			errs <- err
		}()
	}

	// Wait for the semaphore to fill rather than sleeping for a guessed
	// interval. If the cap is broken upwards the handler count goes past the
	// limit and maxSeen catches it; if it is broken downwards this loop hits
	// its deadline.
	deadline := time.Now().Add(2 * time.Second)
	for inflight.Load() < limit {
		if time.Now().After(deadline) {
			close(release)
			t.Fatalf("only %d request(s) reached the server, want %d in flight at once", inflight.Load(), limit)
		}
		runtime.Gosched()
	}
	close(release)
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("FetchOrder: %v", err)
		}
	}
	if got := maxSeen.Load(); got != limit {
		t.Errorf("the server saw %d requests in flight at once, want exactly %d", got, limit)
	}
}

func TestClientCapturesRawResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, razorpay.Order{
			ID:          "order_captured0001",
			AmountPaise: 120000,
			Currency:    "INR",
			Status:      razorpay.OrderStatusCreated,
		})
	}))
	defer srv.Close()

	var buf bytes.Buffer
	c := newTestClient(t, srv.URL+"/v1", func(o *razorpay.ClientOptions) {
		o.RawCapture = &buf
	})

	if _, err := c.FetchOrder(context.Background(), "order_captured0001"); err != nil {
		t.Fatalf("FetchOrder: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("capture wrote %d line(s), want exactly 1: %q", len(lines), buf.String())
	}

	var got razorpay.RawResponse
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("capture line is not valid JSON: %v (%q)", err, lines[0])
	}
	if got.Method != http.MethodGet {
		t.Errorf("captured method = %q, want GET", got.Method)
	}
	if got.Path != "/v1/orders/order_captured0001" {
		t.Errorf("captured path = %q, want /v1/orders/order_captured0001", got.Path)
	}
	if got.Status != http.StatusOK {
		t.Errorf("captured status = %d, want 200", got.Status)
	}
	if got.CapturedAt == "" {
		t.Error("captured line carries no captured_at, so a fixture would not record when it was taken")
	}

	body, ok := got.Body.(map[string]any)
	if !ok {
		t.Fatalf("captured body = %v, want an object", got.Body)
	}
	if body["id"] != "order_captured0001" {
		t.Errorf("captured body id = %v, want order_captured0001", body["id"])
	}

	// A fixture file is committed. A request header in it would commit a
	// credential with it.
	if strings.Contains(lines[0], testKeySecret) || strings.Contains(lines[0], "Authorization") {
		t.Errorf("the capture line carries request auth: %q", lines[0])
	}
}
