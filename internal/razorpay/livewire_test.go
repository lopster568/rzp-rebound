package razorpay_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
)

// The tests in this file pin wire shapes that were observed coming back from
// Razorpay test mode on 2026-08-31, and that the offline half had guessed
// wrong. Each one runs against httptest, so it needs no credential and no
// network, but the shape it asserts is a captured fact rather than a
// restatement of the struct tags. docs/RAZORPAY-TEST-MODE-NOTES.md holds the
// observations with their date.

// newWireClient builds a client pointed at srv.
func newWireClient(t *testing.T, srv *httptest.Server) *razorpay.Client {
	t.Helper()

	c, err := razorpay.NewClient(razorpay.ClientOptions{
		KeyID:     testKeyID,
		KeySecret: testKeySecret,
		BaseURL:   srv.URL + "/v1",
		Clock:     clock.NewFake(fakeStart),
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c
}

// TestClientTreatsMissingResourceBadRequestAsNotFound covers the largest
// wire-shape miss the offline half made. Razorpay does not answer 404 for a
// resource that is not there: it answers 400 with the description "The id
// provided does not exist", observed on 2026-08-31 against both an order id
// and a payment link id. mapNotFound only looked at the status code, so every
// missing-resource call surfaced as a bare APIError and errors.Is against
// ErrOrderNotFound was false for the one case it exists to catch.
func TestClientTreatsMissingResourceBadRequestAsNotFound(t *testing.T) {
	const missingBody = `{"error":{"code":"BAD_REQUEST_ERROR","description":"The id provided does not exist","source":"internal","step":"payment_initiation","reason":"input_validation_failed","metadata":{}}}`

	tests := []struct {
		name    string
		path    string
		body    string
		status  int
		call    func(c *razorpay.Client) error
		wantErr error
	}{
		{
			name:   "missing order",
			status: http.StatusBadRequest,
			body:   missingBody,
			call: func(c *razorpay.Client) error {
				_, err := c.FetchOrder(context.Background(), "order_AAAAAAAAAAAAAA")
				return err
			},
			wantErr: razorpay.ErrOrderNotFound,
		},
		{
			name:   "missing payment",
			status: http.StatusBadRequest,
			body:   missingBody,
			call: func(c *razorpay.Client) error {
				_, err := c.FetchPayment(context.Background(), "pay_AAAAAAAAAAAAAA")
				return err
			},
			wantErr: razorpay.ErrPaymentNotFound,
		},
		{
			// A malformed id is a different 400 and must stay a plain API
			// error. Reading it as not-found would tell a caller the resource
			// is absent when what happened is that we sent nonsense.
			name:   "malformed id stays a bad request",
			status: http.StatusBadRequest,
			body:   `{"error":{"code":"BAD_REQUEST_ERROR","description":"doesnotexist000 is not a valid id","source":"internal","step":"payment_initiation","reason":"input_validation_failed","metadata":{}}}`,
			call: func(c *razorpay.Client) error {
				_, err := c.FetchOrder(context.Background(), "order_doesnotexist000")
				return err
			},
			wantErr: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			err := tc.call(newWireClient(t, srv))
			if err == nil {
				t.Fatal("the call returned no error")
			}
			if tc.wantErr == nil {
				if errors.Is(err, razorpay.ErrOrderNotFound) || errors.Is(err, razorpay.ErrPaymentNotFound) {
					t.Errorf("a malformed id was reported as a missing resource: %v", err)
				}
				var apiErr *razorpay.APIError
				if !errors.As(err, &apiErr) {
					t.Errorf("err = %v, want an *APIError", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("err = %v, want it to wrap %v", err, tc.wantErr)
			}
		})
	}
}

// TestClientCreatePaymentLinkSendsNestedNotifyObject pins the request body
// Razorpay accepts. The offline half sent flat notify_sms and notify_email
// fields, which test mode rejected on 2026-08-31 with a 400 and the
// description "extra fields sent". The real shape is a nested notify object.
func TestClientCreatePaymentLinkSendsNestedNotifyObject(t *testing.T) {
	var got map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"plink_TWUSsBJJxaHJ3W","short_url":"https://rzp.io/rzp/9mNywusp","status":"created","amount":100000,"currency":"INR","reference_id":"ref_nested_01","created_at":1788204082}`)
	}))
	defer srv.Close()

	link, err := newWireClient(t, srv).CreatePaymentLink(context.Background(), razorpay.CreatePaymentLinkRequest{
		AmountPaise: 100000,
		Currency:    "INR",
		Description: "probe nested",
		ReferenceID: "ref_nested_01",
		NotifySMS:   true,
		NotifyEmail: false,
	})
	if err != nil {
		t.Fatalf("CreatePaymentLink: %v", err)
	}

	if _, flat := got["notify_sms"]; flat {
		t.Error("the body carries a flat notify_sms field, which test mode rejects as an extra field")
	}
	if _, flat := got["notify_email"]; flat {
		t.Error("the body carries a flat notify_email field, which test mode rejects as an extra field")
	}
	notify, ok := got["notify"].(map[string]any)
	if !ok {
		t.Fatalf("the body has no nested notify object: %v", got)
	}
	if notify["sms"] != true {
		t.Errorf("notify.sms = %v, want true", notify["sms"])
	}
	if notify["email"] != false {
		t.Errorf("notify.email = %v, want false", notify["email"])
	}

	if link.ID != "plink_TWUSsBJJxaHJ3W" {
		t.Errorf("id = %q, want the plink id from the response", link.ID)
	}
	if link.ShortURL == "" {
		t.Error("the response short_url did not decode")
	}
}

// TestClientResendReadsSuccessFieldFromResponseBody pins the resend response.
// The offline half inferred Accepted from the 2xx because the body shape was
// unknown. It is known now: the call answers {"success":true}, observed on
// 2026-08-31.
//
// The subtest that matters is the second one. A 200 whose body says the send
// was not accepted must not report Accepted, because Accepted is what the
// audit trail turns into "notification API call succeeded".
func TestClientResendReadsSuccessFieldFromResponseBody(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantAccepted bool
	}{
		{name: "success true", body: `{"success":true}`, wantAccepted: true},
		{name: "success false", body: `{"success":false}`, wantAccepted: false},
		{
			// A body with no success field at all falls back to the status
			// code, which is what the offline half did for every case.
			name: "no success field falls back to the status", body: `{}`, wantAccepted: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/notify_by/email") {
					t.Errorf("resend went to %s, want a notify_by/email path", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()

			receipt, err := newWireClient(t, srv).ResendPaymentLinkNotification(
				context.Background(), "plink_TWUSsBJJxaHJ3W", razorpay.MediumEmail)
			if err != nil {
				t.Fatalf("ResendPaymentLinkNotification: %v", err)
			}
			if receipt.Accepted != tc.wantAccepted {
				t.Errorf("Accepted = %v, want %v for body %s", receipt.Accepted, tc.wantAccepted, tc.body)
			}
		})
	}
}

// TestFakeSplitsErrorCodeAndReasonTheWayRazorpayDoes closes PRD Q4 on the fake
// side. A real failed payment carries the coarse class in error_code and the
// specific reason in error_reason, observed on 2026-08-31. The fake used to
// put the reason string in both, because which field carried it was the open
// question, and a fake that answers a settled question wrongly is a fake that
// teaches the wrong shape to every offline test built on it.
func TestFakeSplitsErrorCodeAndReasonTheWayRazorpayDoes(t *testing.T) {
	f, err := razorpay.NewFake(razorpay.FakeOptions{Seed: 3, Clock: clock.NewFake(fakeStart)})
	if err != nil {
		t.Fatalf("NewFake: %v", err)
	}
	ctx := context.Background()

	order, err := f.CreateOrder(ctx, razorpay.CreateOrderRequest{AmountPaise: 100000, Currency: "INR"})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}

	card, ok := f.Cards().CardForErrorCode("insufficient_fund")
	if !ok {
		t.Fatal("no documented card forces insufficient_fund")
	}

	payment, err := f.AttemptPayment(ctx, order.ID, card.Number)
	if err != nil {
		t.Fatalf("AttemptPayment: %v", err)
	}

	if payment.ErrorReason != "insufficient_fund" {
		t.Errorf("error_reason = %q, want the specific reason", payment.ErrorReason)
	}
	if payment.ErrorCode != razorpay.ErrorClassBadRequest {
		t.Errorf("error_code = %q, want %q, the coarse class Razorpay returns",
			payment.ErrorCode, razorpay.ErrorClassBadRequest)
	}
	if payment.ErrorSource != razorpay.ErrorSourceGateway {
		t.Errorf("error_source = %q, want %q", payment.ErrorSource, razorpay.ErrorSourceGateway)
	}
	if payment.ErrorStep != razorpay.ErrorStepPaymentAuthorization {
		t.Errorf("error_step = %q, want %q", payment.ErrorStep, razorpay.ErrorStepPaymentAuthorization)
	}
}

// TestOrderDecodesNotesWhetherRazorpaySendsAnObjectOrAnEmptyArray is a bug the
// fixture captures did not find and the live contract harness did, within
// seconds of first being run on 2026-08-31.
//
// Every captured order was created with notes on it, and those come back as a
// JSON object. An order created with no notes comes back with notes as an
// empty JSON array, which is not a map, so the whole response failed to decode
// and CreateOrder returned an error for a call that had succeeded.
//
// That is the worst shape of failure available here: the order exists in
// Razorpay, the caller got an error, and nothing in the caller knows the id of
// the thing it just created.
func TestOrderDecodesNotesWhetherRazorpaySendsAnObjectOrAnEmptyArray(t *testing.T) {
	tests := []struct {
		name      string
		notesJSON string
		want      map[string]string
		wantErr   bool
	}{
		{name: "object", notesJSON: `{"purpose":"demo"}`, want: map[string]string{"purpose": "demo"}},
		{name: "empty object", notesJSON: `{}`, want: map[string]string{}},
		{name: "empty array", notesJSON: `[]`, want: map[string]string{}},
		{name: "null", notesJSON: `null`, want: nil},
		{
			// A non-empty array is not an empty map with a different spelling.
			// Quietly dropping its contents would lose data that was on the
			// order, so it stays an error.
			name: "non-empty array is still an error", notesJSON: `["unexpected"]`, wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"order_TWUltnSDVIxdYd","amount":100000,"amount_paid":0,`+
					`"amount_due":100000,"currency":"INR","receipt":"rcpt","status":"created",`+
					`"attempts":0,"created_at":1788204047,"notes":`+tc.notesJSON+`}`)
			}))
			defer srv.Close()

			order, err := newWireClient(t, srv).FetchOrder(context.Background(), "order_TWUltnSDVIxdYd")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("notes %s decoded without an error", tc.notesJSON)
				}
				return
			}
			if err != nil {
				t.Fatalf("FetchOrder with notes %s: %v", tc.notesJSON, err)
			}
			if order.ID != "order_TWUltnSDVIxdYd" {
				t.Errorf("id = %q, so the whole response failed to decode", order.ID)
			}
			if len(order.Notes) != len(tc.want) {
				t.Fatalf("notes = %v, want %v", order.Notes, tc.want)
			}
			for k, v := range tc.want {
				if order.Notes[k] != v {
					t.Errorf("notes[%q] = %q, want %q", k, order.Notes[k], v)
				}
			}
		})
	}
}
