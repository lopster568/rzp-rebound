package razorpay_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
)

// The client under test is configured with these everywhere in this package.
// They are deliberately not shaped like Razorpay keys: a key-shaped literal in
// a tracked file trips the pre-commit secret scan, and no real credential
// belongs in this repository.
const (
	testKeyID     = "key_id_placeholder"
	testKeySecret = "key_secret_placeholder"
)

// fakeAPIServer is an httptest.Server that speaks the wire shape
// internal/razorpay's client encodes and decodes, with razorpay.Fake holding
// the state behind it.
//
// What it proves: the client's basic-auth header, its retry loop, its
// concurrency cap, its status-to-error mapping, and its JSON decode all work
// over a real socket, so the two TestPortContract_ functions can run against
// the client's own code path with no credential and no network.
//
// What it cannot prove: anything about Razorpay. Both ends of this exchange
// marshal and unmarshal through the same struct tags in port.go, so a field
// name that is wrong for Razorpay is wrong on both sides and the test still
// passes. Only a captured fixture settles a wire shape. DECISIONS.md has the
// longer version.
type fakeAPIServer struct {
	srv  *httptest.Server
	fake *razorpay.Fake
}

func newFakeAPIServer(t *testing.T, seed int64) *fakeAPIServer {
	t.Helper()

	f, err := razorpay.NewFake(razorpay.FakeOptions{
		Seed:  seed,
		Clock: clock.NewFake(fakeStart),
	})
	if err != nil {
		t.Fatalf("NewFake: %v", err)
	}

	s := &fakeAPIServer{fake: f}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/orders", s.createOrder)
	mux.HandleFunc("GET /v1/orders/{id}", s.fetchOrder)
	mux.HandleFunc("GET /v1/orders/{id}/payments", s.listPayments)
	mux.HandleFunc("GET /v1/payments/{id}", s.fetchPayment)
	mux.HandleFunc("POST /v1/payment_links", s.createPaymentLink)
	mux.HandleFunc("POST /v1/payment_links/{id}/notify_by/{medium}", s.notify)

	s.srv = httptest.NewServer(requireAuth(mux))
	t.Cleanup(s.srv.Close)
	return s
}

// baseURL is what a client under test points at.
func (s *fakeAPIServer) baseURL() string { return s.srv.URL + "/v1" }

// requireAuth refuses anything without the configured key pair, so a test that
// drops the auth header fails here rather than passing quietly.
func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != testKeyID || pass != testKeySecret {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"description": "auth missing or wrong"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *fakeAPIServer) createOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Amount   int64             `json:"amount"`
		Currency string            `json:"currency"`
		Receipt  string            `json:"receipt"`
		Notes    map[string]string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"description": err.Error()})
		return
	}

	order, err := s.fake.CreateOrder(r.Context(), razorpay.CreateOrderRequest{
		AmountPaise: body.Amount,
		Currency:    body.Currency,
		Receipt:     body.Receipt,
		Notes:       body.Notes,
	})
	if err != nil {
		writeJSON(w, statusFor(err), map[string]string{"description": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func (s *fakeAPIServer) fetchOrder(w http.ResponseWriter, r *http.Request) {
	order, err := s.fake.FetchOrder(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, statusFor(err), map[string]string{"description": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func (s *fakeAPIServer) listPayments(w http.ResponseWriter, r *http.Request) {
	payments, err := s.fake.ListPaymentsForOrder(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, statusFor(err), map[string]string{"description": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": len(payments), "items": payments})
}

func (s *fakeAPIServer) fetchPayment(w http.ResponseWriter, r *http.Request) {
	payment, err := s.fake.FetchPayment(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, statusFor(err), map[string]string{"description": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, payment)
}

func (s *fakeAPIServer) createPaymentLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Amount      int64  `json:"amount"`
		Currency    string `json:"currency"`
		Description string `json:"description"`
		ReferenceID string `json:"reference_id"`
		NotifySMS   bool   `json:"notify_sms"`
		NotifyEmail bool   `json:"notify_email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"description": err.Error()})
		return
	}

	link, err := s.fake.CreatePaymentLink(r.Context(), razorpay.CreatePaymentLinkRequest{
		AmountPaise: body.Amount,
		Currency:    body.Currency,
		Description: body.Description,
		ReferenceID: body.ReferenceID,
		NotifySMS:   body.NotifySMS,
		NotifyEmail: body.NotifyEmail,
	})
	if err != nil {
		writeJSON(w, statusFor(err), map[string]string{"description": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, link)
}

func (s *fakeAPIServer) notify(w http.ResponseWriter, r *http.Request) {
	_, err := s.fake.ResendPaymentLinkNotification(r.Context(), r.PathValue("id"), r.PathValue("medium"))
	if err != nil {
		writeJSON(w, statusFor(err), map[string]string{"description": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// statusFor maps a port error to the status a gateway would answer with.
func statusFor(err error) int {
	switch {
	case errors.Is(err, razorpay.ErrOrderNotFound),
		errors.Is(err, razorpay.ErrPaymentNotFound),
		errors.Is(err, razorpay.ErrPaymentLinkNotFound):
		return http.StatusNotFound
	case errors.Is(err, razorpay.ErrAmountNotPositive),
		errors.Is(err, razorpay.ErrUnsupportedMedium),
		errors.Is(err, razorpay.ErrUnknownCard),
		errors.Is(err, razorpay.ErrOrderAlreadyPaid):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
