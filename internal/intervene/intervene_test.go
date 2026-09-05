package intervene_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/audit"
	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/intervene"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/redact"
	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
)

// The bodies below are what Razorpay test mode returned on 2026-09-05, copied
// from internal/razorpay/invoices_test.go unchanged. They are raw strings
// rather than structs marshalled through the tags under test for the reason
// that file gives: a fixture built from the same tags the client decodes with
// proves nothing about the wire shape.
//
// Nothing here is a real credential. The ids are test-mode ids from a
// throwaway account.

// probeInvoiceIssued is an issued invoice. Both notification status fields are
// still null: issuing is not sending.
const probeInvoiceIssued = `{
  "id": "inv_TYEwC7POHGFZNa",
  "entity": "invoice",
  "customer_id": "cust_TYEw5izKFR0iJr",
  "customer_details": {
    "id": "cust_TYEw5izKFR0iJr",
    "name": "Probe Customer",
    "email": "probe-2026-09-05@example.com",
    "contact": "9000090000",
    "gstin": null
  },
  "order_id": "order_TYEwKA0KjwEW3t",
  "payment_id": null,
  "status": "issued",
  "issued_at": 1788586088,
  "paid_at": null,
  "cancelled_at": null,
  "expired_at": null,
  "sms_status": null,
  "email_status": null,
  "date": 1788586081,
  "partial_payment": false,
  "amount": 50000,
  "amount_paid": 0,
  "amount_due": 50000,
  "currency": "INR",
  "description": "feasibility probe draft 2026-09-05",
  "notes": [],
  "short_url": "https://rzp.io/rzp/4U2HXcQ",
  "type": "invoice",
  "created_at": 1788586081
}`

// probeInvoiceNotified is the same invoice after the email notify. Only
// email_status moved, from null to sent.
const probeInvoiceNotified = `{
  "id": "inv_TYEwC7POHGFZNa",
  "entity": "invoice",
  "customer_id": "cust_TYEw5izKFR0iJr",
  "customer_details": {
    "id": "cust_TYEw5izKFR0iJr",
    "name": "Probe Customer",
    "email": "probe-2026-09-05@example.com",
    "contact": "9000090000",
    "gstin": null
  },
  "order_id": "order_TYEwKA0KjwEW3t",
  "payment_id": null,
  "status": "issued",
  "issued_at": 1788586088,
  "sms_status": null,
  "email_status": "sent",
  "date": 1788586081,
  "partial_payment": false,
  "amount": 50000,
  "amount_paid": 0,
  "amount_due": 50000,
  "currency": "INR",
  "notes": [],
  "short_url": "https://rzp.io/rzp/4U2HXcQ",
  "type": "invoice",
  "created_at": 1788586081
}`

// probeInvoiceCancelled is a separately issued invoice after the cancel call.
const probeInvoiceCancelled = `{
  "id": "inv_TYEwgLnV0S7WQ0",
  "entity": "invoice",
  "customer_id": "cust_TYEw5izKFR0iJr",
  "customer_details": {
    "id": "cust_TYEw5izKFR0iJr",
    "name": "Probe Customer",
    "email": "probe-2026-09-05@example.com",
    "contact": "9000090000",
    "gstin": null
  },
  "order_id": "order_TYEwgWlGLJ5l4m",
  "status": "cancelled",
  "issued_at": 1788586109,
  "cancelled_at": 1788586116,
  "sms_status": null,
  "email_status": null,
  "date": 1788586109,
  "partial_payment": true,
  "amount": 25000,
  "amount_paid": 0,
  "amount_due": 25000,
  "currency": "INR",
  "notes": [],
  "short_url": "https://rzp.io/rzp/WtHmZor",
  "type": "invoice",
  "created_at": 1788586109
}`

// probeNotifySuccess is what both notify endpoints answered with. It means the
// API accepted the call and nothing else.
const probeNotifySuccess = `{"success": true}`

// probePaymentLink is a created payment link.
const probePaymentLink = `{
  "accept_partial": false,
  "amount": 30000,
  "amount_paid": 0,
  "cancelled_at": 0,
  "created_at": 1788586129,
  "currency": "INR",
  "customer": {
    "contact": "+919000090000",
    "email": "probe-2026-09-05@example.com",
    "name": "Probe Customer"
  },
  "description": "feasibility probe link 2026-09-05",
  "expire_by": 0,
  "expired_at": 0,
  "id": "plink_TYEx2CoiwQvYow",
  "notes": null,
  "notify": {"email": false, "sms": false, "whatsapp": false},
  "payments": [],
  "reference_id": "probe_20260905",
  "reminder_enable": true,
  "reminders": {"status": "failed"},
  "short_url": "https://rzp.io/rzp/W2ToDkL",
  "status": "created",
  "updated_at": 1788586129,
  "upi_link": false,
  "user_id": "",
  "whatsapp_link": false
}`

const (
	testKeyID     = "key_id_placeholder"
	testKeySecret = "key_secret_placeholder"
)

// testStart is a fixed instant, so nothing here depends on wall time.
var testStart = time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)

// writeRawJSON answers with a body byte for byte, which marshalling cannot do:
// a null or an unmodelled field would not survive the trip.
func writeRawJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// rig is one wired engine plus the four things a test reads back: the audit
// ledger, the promise ledger, the escalation sink, and a count of the requests
// the gateway saw.
type rig struct {
	engine      *intervene.Engine
	ledger      *bytes.Buffer
	promises    *intervene.MemoryPromiseLedger
	escalations *intervene.MemorySink
	calls       *atomic.Int64
}

// failingWriter is an io.Writer that always fails, which is how the audit
// write failure path is driven.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("the ledger is not writable") }

// failingPromiseLedger refuses every promise.
type failingPromiseLedger struct{}

func (failingPromiseLedger) Log(context.Context, intervene.PromiseRecord) error {
	return errors.New("the promise ledger is not writable")
}

// failingSink refuses every escalation.
type failingSink struct{}

func (failingSink) Escalate(context.Context, intervene.Escalation) error {
	return errors.New("the escalation queue is not writable")
}

// newRig builds an engine over an httptest server running handler. The client
// is the real razorpay.Client, so the wire shapes in this file go through the
// same decoder production does.
func newRig(t *testing.T, handler http.Handler) *rig {
	t.Helper()
	return newRigWith(t, handler, nil)
}

// newRigWith is newRig with a hook that swaps a dependency for a failing one.
func newRigWith(t *testing.T, handler http.Handler, mutate func(*intervene.Options)) *rig {
	t.Helper()
	return newRigWithClient(t, handler, nil, mutate)
}

// newRigWithClient is newRigWith plus a hook on the Razorpay client, which is
// how a test reaches the retry path without waiting on a real backoff.
func newRigWithClient(t *testing.T, handler http.Handler, clientMutate func(*razorpay.ClientOptions), mutate func(*intervene.Options)) *rig {
	t.Helper()

	calls := &atomic.Int64{}
	counted := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if handler == nil {
			t.Errorf("the gateway was called for %s %s by a test that expected no call", r.Method, r.URL.Path)
			writeRawJSON(w, http.StatusInternalServerError, `{}`)
			return
		}
		handler.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(counted)
	t.Cleanup(srv.Close)

	fake := clock.NewFake(testStart)
	clientOpts := razorpay.ClientOptions{
		KeyID:     testKeyID,
		KeySecret: testKeySecret,
		BaseURL:   srv.URL + "/v1",
		Clock:     fake,
	}
	if clientMutate != nil {
		clientMutate(&clientOpts)
	}
	client, err := razorpay.NewClient(clientOpts)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	ledger := &bytes.Buffer{}
	recorder, err := audit.NewRecorder(audit.Options{Writer: ledger, Clock: fake})
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	promises := intervene.NewMemoryPromiseLedger()
	escalations := intervene.NewMemorySink()
	opts := intervene.Options{
		Gateway:     client,
		Recorder:    recorder,
		Promises:    promises,
		Escalations: escalations,
		Clock:       fake,
	}
	if mutate != nil {
		mutate(&opts)
	}
	engine, err := intervene.New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return &rig{engine: engine, ledger: ledger, promises: promises, escalations: escalations, calls: calls}
}

// rows returns the audit ledger lines written so far.
func (r *rig) rows(t *testing.T) []audit.Record {
	t.Helper()

	var out []audit.Record
	for _, line := range strings.Split(strings.TrimSpace(r.ledger.String()), "\n") {
		if line == "" {
			continue
		}
		var rec audit.Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("ledger line is not valid JSON: %v (%q)", err, line)
		}
		out = append(out, rec)
	}
	return out
}

// onlyRow asserts there is exactly one ledger line and returns it.
func (r *rig) onlyRow(t *testing.T) audit.Record {
	t.Helper()

	rows := r.rows(t)
	if len(rows) != 1 {
		t.Fatalf("wrote %d ledger row(s), want exactly 1", len(rows))
	}
	return rows[0]
}

// invoiceItem is an overdue-invoice sighting with an invoice handle and a
// contact channel.
func invoiceItem() riskitem.RiskItem {
	return riskitem.RiskItem{
		ID:              riskitem.NewID(riskitem.SourceOverdueInvoice, "inv_TYEwC7POHGFZNa"),
		Source:          riskitem.SourceOverdueInvoice,
		SourceID:        "inv_TYEwC7POHGFZNa",
		RootOrderID:     "order_TYEwKA0KjwEW3t",
		Customer:        riskitem.Customer{Name: "Probe Customer", Email: "probe-2026-09-05@example.com", Contact: "9000090000"},
		AmountPaise:     50000,
		AmountDuePaise:  50000,
		Currency:        "INR",
		AtRiskSince:     1788586088,
		AmountPaidPaise: 0,
		PayHandle: riskitem.PayHandle{
			Kind: riskitem.HandleKindInvoice,
			URL:  "https://rzp.io/rzp/4U2HXcQ",
			ID:   "inv_TYEwC7POHGFZNa",
		},
	}
}

// linkItem is a failed-payment sighting that already has a payment link.
func linkItem() riskitem.RiskItem {
	return riskitem.RiskItem{
		ID:             riskitem.NewID(riskitem.SourceFailedPayment, "pay_TYEx2CoiwQvYow"),
		Source:         riskitem.SourceFailedPayment,
		SourceID:       "pay_TYEx2CoiwQvYow",
		RootOrderID:    "order_TYEwKA0KjwEW3t",
		Customer:       riskitem.Customer{Name: "Probe Customer", Email: "probe-2026-09-05@example.com"},
		AmountPaise:    30000,
		AmountDuePaise: 30000,
		Currency:       "INR",
		PayHandle: riskitem.PayHandle{
			Kind: riskitem.HandleKindPaymentLink,
			URL:  "https://rzp.io/rzp/W2ToDkL",
			ID:   "plink_TYEx2CoiwQvYow",
		},
	}
}

// bareItem is an unpaid order with nothing to pay against.
func bareItem() riskitem.RiskItem {
	return riskitem.RiskItem{
		ID:             riskitem.NewID(riskitem.SourceUnpaidOrder, "order_TYEwKA0KjwEW3t"),
		Source:         riskitem.SourceUnpaidOrder,
		SourceID:       "order_TYEwKA0KjwEW3t",
		RootOrderID:    "order_TYEwKA0KjwEW3t",
		Customer:       riskitem.Customer{Name: "Probe Customer", Email: "probe-2026-09-05@example.com"},
		AmountPaise:    30000,
		AmountDuePaise: 30000,
		Currency:       "INR",
		Signal:         riskitem.Signal{Attempts: 0},
	}
}

// notifyThenFetch is the invoice notify sequence: the notify endpoint, then a
// fetch that answers with whatever body the caller supplies.
func notifyThenFetch(t *testing.T, fetchBody string) http.Handler {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/invoices/{id}/notify_by/{medium}", func(w http.ResponseWriter, _ *http.Request) {
		writeRawJSON(w, http.StatusOK, probeNotifySuccess)
	})
	mux.HandleFunc("GET /v1/invoices/{id}", func(w http.ResponseWriter, _ *http.Request) {
		writeRawJSON(w, http.StatusOK, fetchBody)
	})
	return mux
}

func TestNewRejectsAnIncompleteWiring(t *testing.T) {
	client, err := razorpay.NewClient(razorpay.ClientOptions{
		KeyID:     testKeyID,
		KeySecret: testKeySecret,
		BaseURL:   "http://127.0.0.1:1/v1",
	})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	recorder, err := audit.NewRecorder(audit.Options{Writer: &bytes.Buffer{}})
	if err != nil {
		t.Fatalf("NewRecorder: %v", err)
	}

	full := intervene.Options{
		Gateway:     client,
		Recorder:    recorder,
		Promises:    intervene.NewMemoryPromiseLedger(),
		Escalations: intervene.NewMemorySink(),
	}

	tests := []struct {
		name   string
		mutate func(*intervene.Options)
		want   error
	}{
		{"no gateway", func(o *intervene.Options) { o.Gateway = nil }, intervene.ErrNoGateway},
		{"no recorder", func(o *intervene.Options) { o.Recorder = nil }, intervene.ErrNoRecorder},
		{"no promise ledger", func(o *intervene.Options) { o.Promises = nil }, intervene.ErrNoPromiseLedger},
		{"no escalation sink", func(o *intervene.Options) { o.Escalations = nil }, intervene.ErrNoEscalationSink},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := full
			tt.mutate(&opts)
			if _, err := intervene.New(opts); !errors.Is(err, tt.want) {
				t.Errorf("New = %v, want %v", err, tt.want)
			}
		})
	}

	if _, err := intervene.New(full); err != nil {
		t.Errorf("New with everything wired: %v", err)
	}
}

func TestApplyRefusesAnUnlawfulAction(t *testing.T) {
	// Every spelling of a retry, which riskitem has deleted rather than gated,
	// plus a string nobody would mean.
	for _, action := range []string{"retry", "retry_same_instrument", "charge_again", ""} {
		t.Run(action, func(t *testing.T) {
			r := newRig(t, nil)

			out, err := r.engine.Apply(context.Background(), invoiceItem(), action)
			if err != nil {
				t.Fatalf("Apply returned an error for a refusal: %v", err)
			}
			if out.Accepted {
				t.Error("an unlawful action was accepted")
			}
			if out.Err != intervene.RefusalUnlawfulAction {
				t.Errorf("Err = %q, want %q", out.Err, intervene.RefusalUnlawfulAction)
			}
			if out.Action != action {
				t.Errorf("Action = %q, want %q", out.Action, action)
			}
			if got := r.calls.Load(); got != 0 {
				t.Errorf("the gateway saw %d request(s) for an unlawful action, want 0", got)
			}

			row := r.onlyRow(t)
			if row.Kind != audit.KindActionSkipped {
				t.Errorf("audit kind = %q, want %q", row.Kind, audit.KindActionSkipped)
			}
			if row.Detail["error"] != intervene.RefusalUnlawfulAction {
				t.Errorf("audit detail error = %q, want the refusal reason", row.Detail["error"])
			}
		})
	}
}

func TestApplyRefusesANotifyForAnItemWithNoContactChannel(t *testing.T) {
	// The item still carries an invoice handle, so the only thing standing
	// between this and a notify call is the contact-channel gate.
	item := invoiceItem()
	item.Customer = riskitem.Customer{Name: "Probe Customer"}
	if item.Customer.HasContactChannel() {
		t.Fatal("the fixture has a contact channel, so this test would prove nothing")
	}

	for _, action := range []string{
		riskitem.ActionNotifyEmail,
		riskitem.ActionNotifySMS,
		riskitem.ActionResendLink,
	} {
		t.Run(action, func(t *testing.T) {
			r := newRig(t, nil)

			out, err := r.engine.Apply(context.Background(), item, action)
			if err != nil {
				t.Fatalf("Apply returned an error for a refusal: %v", err)
			}
			if out.Accepted {
				t.Error("a notify was accepted for an item with nowhere to send it")
			}
			if out.Err != intervene.RefusalNoContactChannel {
				t.Errorf("Err = %q, want %q", out.Err, intervene.RefusalNoContactChannel)
			}
			if got := r.calls.Load(); got != 0 {
				t.Errorf("the gateway saw %d request(s), want 0: an address must never be guessed", got)
			}
			if row := r.onlyRow(t); row.Kind != audit.KindActionSkipped {
				t.Errorf("audit kind = %q, want %q", row.Kind, audit.KindActionSkipped)
			}
		})
	}

	t.Run("escalate is still lawful for the same item", func(t *testing.T) {
		r := newRig(t, nil)

		out, err := r.engine.Apply(context.Background(), item, riskitem.ActionEscalate)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if !out.Accepted {
			t.Errorf("escalate was refused for an item with no contact channel: %q", out.Err)
		}
	})
}

func TestNotifyEmailObservableIsVerifiedByTheFetchBack(t *testing.T) {
	t.Run("email_status moves from null to sent", func(t *testing.T) {
		var notifyPath, fetchPath string
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/invoices/{id}/notify_by/{medium}", func(w http.ResponseWriter, req *http.Request) {
			notifyPath = req.URL.Path
			writeRawJSON(w, http.StatusOK, probeNotifySuccess)
		})
		mux.HandleFunc("GET /v1/invoices/{id}", func(w http.ResponseWriter, req *http.Request) {
			fetchPath = req.URL.Path
			writeRawJSON(w, http.StatusOK, probeInvoiceNotified)
		})
		r := newRig(t, mux)

		out, err := r.engine.Apply(context.Background(), invoiceItem(), riskitem.ActionNotifyEmail)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if !out.Accepted {
			t.Fatalf("the notification API call was not accepted: %q", out.Err)
		}
		if notifyPath != "/v1/invoices/inv_TYEwC7POHGFZNa/notify_by/email" {
			t.Errorf("notify path = %q, want the invoice notify_by/email endpoint", notifyPath)
		}
		// The read-back is the whole point: the notify response only reports
		// that the call was accepted.
		if fetchPath != "/v1/invoices/inv_TYEwC7POHGFZNa" {
			t.Errorf("fetch path = %q, want the invoice read that carries email_status", fetchPath)
		}
		if out.Observable != "email_status:sent" {
			t.Errorf("Observable = %q, want email_status:sent read off the invoice", out.Observable)
		}
		if out.Handle != (riskitem.PayHandle{
			Kind: riskitem.HandleKindInvoice,
			URL:  "https://rzp.io/rzp/4U2HXcQ",
			ID:   "inv_TYEwC7POHGFZNa",
		}) {
			t.Errorf("Handle = %+v, want the invoice handle the item came with", out.Handle)
		}
		if !out.At.Equal(testStart) {
			t.Errorf("At = %s, want the fake clock reading %s", out.At, testStart)
		}

		row := r.onlyRow(t)
		if row.Kind != audit.KindInterventionApplied {
			t.Errorf("audit kind = %q, want %q", row.Kind, audit.KindInterventionApplied)
		}
		if row.OrderID != "order_TYEwKA0KjwEW3t" {
			t.Errorf("audit order_id = %q, want the order behind the invoice", row.OrderID)
		}
		if row.ProposedAction != riskitem.ActionNotifyEmail {
			t.Errorf("audit proposed_action = %q, want notify_email", row.ProposedAction)
		}
		if row.Detail["observable"] != "email_status:sent" {
			t.Errorf("audit detail observable = %q, want email_status:sent", row.Detail["observable"])
		}
	})

	t.Run("sms reads the sms field", func(t *testing.T) {
		notified := strings.Replace(probeInvoiceNotified,
			`"sms_status": null,`+"\n"+`  "email_status": "sent",`,
			`"sms_status": "sent",`+"\n"+`  "email_status": null,`, 1)
		if notified == probeInvoiceNotified {
			t.Fatal("the sms fixture was not derived, so this test would read the email field")
		}
		r := newRig(t, notifyThenFetch(t, notified))

		out, err := r.engine.Apply(context.Background(), invoiceItem(), riskitem.ActionNotifySMS)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if out.Observable != "sms_status:sent" {
			t.Errorf("Observable = %q, want sms_status:sent", out.Observable)
		}
	})

	t.Run("a fetch-back that still reports nothing does not claim a send", func(t *testing.T) {
		// probeInvoiceIssued has both status fields null. The call was
		// accepted, and that is all the Outcome gets to say.
		r := newRig(t, notifyThenFetch(t, probeInvoiceIssued))

		out, err := r.engine.Apply(context.Background(), invoiceItem(), riskitem.ActionNotifyEmail)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if !out.Accepted {
			t.Fatalf("the notification API call was not accepted: %q", out.Err)
		}
		if out.Observable != intervene.ObservableNotifyAccepted {
			t.Errorf("Observable = %q, want %q for an invoice that reports no send yet",
				out.Observable, intervene.ObservableNotifyAccepted)
		}
	})

	t.Run("a success false is not an acceptance", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/invoices/{id}/notify_by/{medium}", func(w http.ResponseWriter, _ *http.Request) {
			writeRawJSON(w, http.StatusOK, `{"success": false}`)
		})
		mux.HandleFunc("GET /v1/invoices/{id}", func(w http.ResponseWriter, _ *http.Request) {
			t.Error("the invoice was read back after a refused notify")
			writeRawJSON(w, http.StatusOK, probeInvoiceNotified)
		})
		r := newRig(t, mux)

		out, err := r.engine.Apply(context.Background(), invoiceItem(), riskitem.ActionNotifyEmail)
		if err != nil {
			t.Fatalf("Apply returned an error for a gateway refusal: %v", err)
		}
		if out.Accepted {
			t.Error("a 200 carrying success false was counted as an acceptance")
		}
		if out.Observable != intervene.ObservableNotifyRefused {
			t.Errorf("Observable = %q, want %q", out.Observable, intervene.ObservableNotifyRefused)
		}
		// The call was made and the gateway declined it. That is an attempt,
		// not a refusal, and phase 1's decision of 2026-08-31 is that the two
		// do not share a row kind.
		if row := r.onlyRow(t); row.Kind != audit.KindInterventionApplied {
			t.Errorf("audit kind = %q, want %q", row.Kind, audit.KindInterventionApplied)
		}
	})

	t.Run("a failed verifying read leaves the send accepted", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/invoices/{id}/notify_by/{medium}", func(w http.ResponseWriter, _ *http.Request) {
			writeRawJSON(w, http.StatusOK, probeNotifySuccess)
		})
		mux.HandleFunc("GET /v1/invoices/{id}", func(w http.ResponseWriter, _ *http.Request) {
			writeRawJSON(w, http.StatusInternalServerError, `{"error":{"code":"SERVER_ERROR"}}`)
		})
		r := newRig(t, mux)

		out, err := r.engine.Apply(context.Background(), invoiceItem(), riskitem.ActionNotifyEmail)
		if err != nil {
			t.Fatalf("a failed read-back after an accepted send was reported as an error: %v", err)
		}
		if !out.Accepted {
			t.Error("a failed read-back downgraded a send that the API accepted")
		}
		if out.Observable != intervene.ObservableNotifyAccepted {
			t.Errorf("Observable = %q, want %q", out.Observable, intervene.ObservableNotifyAccepted)
		}
		if out.Err == "" {
			t.Error("the failed read-back left no trace in Err")
		}
	})

	t.Run("an item with an invoice source and no handle is refused", func(t *testing.T) {
		item := invoiceItem()
		item.PayHandle = riskitem.PayHandle{}
		r := newRig(t, nil)

		out, err := r.engine.Apply(context.Background(), item, riskitem.ActionNotifyEmail)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if out.Accepted {
			t.Error("a notify was accepted for an item with nothing to notify about")
		}
		if out.Err != intervene.RefusalNoHandle {
			t.Errorf("Err = %q, want %q", out.Err, intervene.RefusalNoHandle)
		}
		if got := r.calls.Load(); got != 0 {
			t.Errorf("the gateway saw %d request(s), want 0", got)
		}
	})
}

func TestResendLinkReportsOnlyThatTheCallWasAccepted(t *testing.T) {
	var gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/payment_links/{id}/notify_by/{medium}", func(w http.ResponseWriter, req *http.Request) {
		gotPath = req.URL.Path
		writeRawJSON(w, http.StatusOK, probeNotifySuccess)
	})
	r := newRig(t, mux)

	out, err := r.engine.Apply(context.Background(), linkItem(), riskitem.ActionResendLink)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Accepted {
		t.Fatalf("the resend was not accepted: %q", out.Err)
	}
	// The item carries an email address, so resend_link picks email.
	if gotPath != "/v1/payment_links/plink_TYEx2CoiwQvYow/notify_by/email" {
		t.Errorf("path = %q, want the payment link resend endpoint over email", gotPath)
	}
	// A payment link carries no field equivalent to the invoice's
	// email_status, so this is the strongest true observable.
	if out.Observable != intervene.ObservableNotifyAccepted {
		t.Errorf("Observable = %q, want %q", out.Observable, intervene.ObservableNotifyAccepted)
	}
	if out.Handle.ID != "plink_TYEx2CoiwQvYow" {
		t.Errorf("Handle.ID = %q, want the link the item already had", out.Handle.ID)
	}
	if row := r.onlyRow(t); row.Kind != audit.KindInterventionApplied {
		t.Errorf("audit kind = %q, want %q", row.Kind, audit.KindInterventionApplied)
	}
}

func TestCreatePaymentLinkFillsTheHandle(t *testing.T) {
	t.Run("mints a link for an item with nothing to pay against", func(t *testing.T) {
		var body createLinkBody
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/payment_links", func(w http.ResponseWriter, req *http.Request) {
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode the request body: %v", err)
			}
			writeRawJSON(w, http.StatusOK, probePaymentLink)
		})
		r := newRig(t, mux)

		item := bareItem()
		out, err := r.engine.Apply(context.Background(), item, riskitem.ActionCreatePaymentLink)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if !out.Accepted {
			t.Fatalf("the create was not accepted: %q", out.Err)
		}
		if out.Observable != "plink_status:created" {
			t.Errorf("Observable = %q, want plink_status:created", out.Observable)
		}
		want := riskitem.PayHandle{
			Kind: riskitem.HandleKindPaymentLink,
			URL:  "https://rzp.io/rzp/W2ToDkL",
			ID:   "plink_TYEx2CoiwQvYow",
		}
		if out.Handle != want {
			t.Errorf("Handle = %+v, want %+v", out.Handle, want)
		}

		if body.Amount != item.AmountDuePaise {
			t.Errorf("amount = %d, want the amount due %d", body.Amount, item.AmountDuePaise)
		}
		if body.ReferenceID != item.ID {
			t.Errorf("reference_id = %q, want the sighting id %q", body.ReferenceID, item.ID)
		}
		if body.Currency != item.Currency {
			t.Errorf("currency = %q, want the item's %q", body.Currency, item.Currency)
		}
		// The description is the one field on this request a customer reads.
		if body.Description != intervene.DefaultLinkDescription {
			t.Errorf("description = %q, want %q", body.Description, intervene.DefaultLinkDescription)
		}
		// The request carries no customer, so a notify flag would ask Razorpay
		// to send to a contact that is not on the request. Sending is
		// resend_link's job.
		if body.Notify.Email || body.Notify.SMS {
			t.Errorf("notify = %+v, want both false on a request that carries no customer", body.Notify)
		}

		if row := r.onlyRow(t); row.Detail["handle_id"] != "plink_TYEx2CoiwQvYow" {
			t.Errorf("audit detail handle_id = %q, want the minted link id", row.Detail["handle_id"])
		}
	})

	t.Run("refused when the item already has a handle", func(t *testing.T) {
		for _, item := range []riskitem.RiskItem{invoiceItem(), linkItem()} {
			r := newRig(t, nil)

			out, err := r.engine.Apply(context.Background(), item, riskitem.ActionCreatePaymentLink)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if out.Accepted {
				t.Errorf("a second thing to pay was minted for a %s item that already had one", item.Source)
			}
			if out.Err != intervene.RefusalHandleExists {
				t.Errorf("Err = %q, want %q", out.Err, intervene.RefusalHandleExists)
			}
			if got := r.calls.Load(); got != 0 {
				t.Errorf("the gateway saw %d request(s), want 0", got)
			}
		}
	})

	t.Run("refused when there is nothing to collect", func(t *testing.T) {
		item := bareItem()
		item.AmountPaise = 0
		item.AmountDuePaise = 0
		r := newRig(t, nil)

		out, err := r.engine.Apply(context.Background(), item, riskitem.ActionCreatePaymentLink)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if out.Accepted {
			t.Error("a payment link was minted for a debt of zero")
		}
		if out.Err != intervene.RefusalNoAmount {
			t.Errorf("Err = %q, want %q", out.Err, intervene.RefusalNoAmount)
		}
	})

	t.Run("Options.LinkDescription overrides the default", func(t *testing.T) {
		var body createLinkBody
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/payment_links", func(w http.ResponseWriter, req *http.Request) {
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode the request body: %v", err)
			}
			writeRawJSON(w, http.StatusOK, probePaymentLink)
		})
		r := newRigWith(t, mux, func(o *intervene.Options) {
			o.LinkDescription = "Balance on your subscription"
		})

		if _, err := r.engine.Apply(context.Background(), bareItem(), riskitem.ActionCreatePaymentLink); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if body.Description != "Balance on your subscription" {
			t.Errorf("description = %q, want the configured one", body.Description)
		}
	})

	t.Run("a response with no status does not claim one", func(t *testing.T) {
		// The create call succeeded and the body carried no status. Defaulting
		// it to created would put an observation in the trail that the
		// response did not carry.
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/payment_links", func(w http.ResponseWriter, _ *http.Request) {
			writeRawJSON(w, http.StatusOK,
				`{"id":"plink_TYEx2CoiwQvYow","short_url":"https://rzp.io/rzp/W2ToDkL","amount":30000,"currency":"INR"}`)
		})
		r := newRig(t, mux)

		out, err := r.engine.Apply(context.Background(), bareItem(), riskitem.ActionCreatePaymentLink)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if !out.Accepted {
			t.Fatalf("the create was not accepted: %q", out.Err)
		}
		if out.Observable != intervene.ObservableCreateAccepted {
			t.Errorf("Observable = %q, want %q", out.Observable, intervene.ObservableCreateAccepted)
		}
		if out.Handle.ID != "plink_TYEx2CoiwQvYow" {
			t.Errorf("Handle.ID = %q, want the minted link", out.Handle.ID)
		}
	})

	t.Run("a 5xx is an error and a 4xx is a refusal", func(t *testing.T) {
		for _, tc := range []struct {
			name    string
			status  int
			wantErr bool
		}{
			{"a 500 is a call with no answer about it", http.StatusInternalServerError, true},
			{"a 400 is the gateway declining", http.StatusBadRequest, false},
		} {
			t.Run(tc.name, func(t *testing.T) {
				mux := http.NewServeMux()
				mux.HandleFunc("POST /v1/payment_links", func(w http.ResponseWriter, _ *http.Request) {
					writeRawJSON(w, tc.status, `{"error":{"code":"BAD_REQUEST_ERROR","description":"amount exceeds maximum"}}`)
				})
				r := newRig(t, mux)

				out, err := r.engine.Apply(context.Background(), bareItem(), riskitem.ActionCreatePaymentLink)
				if tc.wantErr && err == nil {
					t.Error("a call that got no answer about it returned no error")
				}
				if !tc.wantErr && err != nil {
					t.Errorf("a declined call was reported as an error: %v", err)
				}
				if out.Accepted {
					t.Error("a declined create was reported as accepted")
				}
				if out.Err == "" {
					t.Error("Err is empty on a declined create")
				}
			})
		}
	})

	t.Run("a settled debt is not billed again", func(t *testing.T) {
		// Nothing due because it was all collected. Falling back to the full
		// amount here would mint a link for a debt that is already paid.
		item := bareItem()
		item.AmountDuePaise = 0
		item.AmountPaidPaise = item.AmountPaise
		r := newRig(t, nil)

		out, err := r.engine.Apply(context.Background(), item, riskitem.ActionCreatePaymentLink)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if out.Accepted {
			t.Errorf("a payment link was minted for a settled debt, handle %+v", out.Handle)
		}
		if out.Err != intervene.RefusalNoAmount {
			t.Errorf("Err = %q, want %q", out.Err, intervene.RefusalNoAmount)
		}
		if got := r.calls.Load(); got != 0 {
			t.Errorf("the gateway saw %d request(s), want 0", got)
		}
	})

	t.Run("a partly paid debt is billed only for the remainder", func(t *testing.T) {
		var body createLinkBody
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/payment_links", func(w http.ResponseWriter, req *http.Request) {
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Errorf("decode the request body: %v", err)
			}
			writeRawJSON(w, http.StatusOK, probePaymentLink)
		})
		r := newRig(t, mux)

		item := bareItem()
		item.AmountPaise = 30000
		item.AmountPaidPaise = 10000
		item.AmountDuePaise = 20000

		if _, err := r.engine.Apply(context.Background(), item, riskitem.ActionCreatePaymentLink); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if body.Amount != 20000 {
			t.Errorf("amount = %d, want the 20000 still due and not the 30000 total", body.Amount)
		}
	})
}

// createLinkBody is the payment-link request as it goes over the wire. It is
// decoded rather than compared against razorpay's own struct so the nested
// notify object, which test mode rejected a flat spelling of, stays asserted.
type createLinkBody struct {
	Amount      int64  `json:"amount"`
	Currency    string `json:"currency"`
	Description string `json:"description"`
	ReferenceID string `json:"reference_id"`
	Notify      struct {
		SMS   bool `json:"sms"`
		Email bool `json:"email"`
	} `json:"notify"`
}

func TestCancelWriteOffIsOnlyLawfulForAnInvoiceItem(t *testing.T) {
	t.Run("cancels an invoice-handled item", func(t *testing.T) {
		var gotPath string
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/invoices/{id}/cancel", func(w http.ResponseWriter, req *http.Request) {
			gotPath = req.URL.Path
			writeRawJSON(w, http.StatusOK, probeInvoiceCancelled)
		})
		r := newRig(t, mux)

		out, err := r.engine.Apply(context.Background(), invoiceItem(), riskitem.ActionCancelWriteOff)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if !out.Accepted {
			t.Fatalf("the cancel was not accepted: %q", out.Err)
		}
		if gotPath != "/v1/invoices/inv_TYEwC7POHGFZNa/cancel" {
			t.Errorf("path = %q, want the invoice cancel endpoint", gotPath)
		}
		if out.Observable != "invoice_status:cancelled" {
			t.Errorf("Observable = %q, want invoice_status:cancelled", out.Observable)
		}
		if row := r.onlyRow(t); row.Kind != audit.KindInterventionApplied {
			t.Errorf("audit kind = %q, want %q", row.Kind, audit.KindInterventionApplied)
		}
	})

	// There is no write-off call for an order or for a payment link, and
	// cancelling a link is not the same act, so both are refused.
	for _, tc := range []struct {
		name string
		item riskitem.RiskItem
	}{
		{"a payment link item", linkItem()},
		{"an item with no handle", bareItem()},
	} {
		t.Run("refused for "+tc.name, func(t *testing.T) {
			r := newRig(t, nil)

			out, err := r.engine.Apply(context.Background(), tc.item, riskitem.ActionCancelWriteOff)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if out.Accepted {
				t.Error("a write-off was accepted for an item with no invoice behind it")
			}
			if out.Err != intervene.RefusalNotAnInvoice {
				t.Errorf("Err = %q, want %q", out.Err, intervene.RefusalNotAnInvoice)
			}
			if got := r.calls.Load(); got != 0 {
				t.Errorf("the gateway saw %d request(s), want 0", got)
			}
			if row := r.onlyRow(t); row.Kind != audit.KindActionSkipped {
				t.Errorf("audit kind = %q, want %q", row.Kind, audit.KindActionSkipped)
			}
		})
	}
}

func TestLogPromiseWritesToTheLedgerAndCallsNoGateway(t *testing.T) {
	r := newRig(t, nil)
	item := invoiceItem()
	ctx := intervene.WithReason(context.Background(), "customer said they will pay on 2026-09-08")

	out, err := r.engine.Apply(ctx, item, riskitem.ActionLogPromise)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Accepted {
		t.Fatalf("the promise was not logged: %q", out.Err)
	}
	if got := r.calls.Load(); got != 0 {
		t.Errorf("the gateway saw %d request(s) for an action that touches no Razorpay resource, want 0", got)
	}

	records := r.promises.Records()
	if len(records) != 1 {
		t.Fatalf("the ledger holds %d record(s), want 1", len(records))
	}
	rec := records[0]
	if rec.RiskItemID != item.ID {
		t.Errorf("RiskItemID = %q, want %q", rec.RiskItemID, item.ID)
	}
	if rec.PromisedAtUnix != testStart.Unix() {
		t.Errorf("PromisedAtUnix = %d, want the fake clock reading %d", rec.PromisedAtUnix, testStart.Unix())
	}
	wantHold := testStart.Add(intervene.DefaultPromiseHold).Unix()
	if rec.HoldUntilUnix != wantHold {
		t.Errorf("HoldUntilUnix = %d, want %d", rec.HoldUntilUnix, wantHold)
	}
	if rec.Note != "customer said they will pay on 2026-09-08" {
		t.Errorf("Note = %q, want the reason from the context", rec.Note)
	}
	if want := "promise_hold_until:" + strconv.FormatInt(wantHold, 10); out.Observable != want {
		t.Errorf("Observable = %q, want %q", out.Observable, want)
	}

	row := r.onlyRow(t)
	if row.Kind != audit.KindPromiseLogged {
		t.Errorf("audit kind = %q, want %q", row.Kind, audit.KindPromiseLogged)
	}
}

func TestEscalateWritesARecordAndNotJustACounter(t *testing.T) {
	r := newRig(t, nil)
	item := invoiceItem()
	ctx := intervene.WithReason(context.Background(), "unclassified failure, fail closed")

	out, err := r.engine.Apply(ctx, item, riskitem.ActionEscalate)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Accepted {
		t.Fatalf("the escalation was not raised: %q", out.Err)
	}
	if out.Observable != intervene.ObservableEscalated {
		t.Errorf("Observable = %q, want %q", out.Observable, intervene.ObservableEscalated)
	}
	if got := r.calls.Load(); got != 0 {
		t.Errorf("the gateway saw %d request(s), want 0", got)
	}

	raised := r.escalations.Escalations()
	if len(raised) != 1 {
		t.Fatalf("the sink holds %d escalation(s), want 1", len(raised))
	}
	esc := raised[0]
	if esc.RiskItemID != item.ID {
		t.Errorf("RiskItemID = %q, want %q", esc.RiskItemID, item.ID)
	}
	if esc.DedupeKey != item.DedupeKey() {
		t.Errorf("DedupeKey = %q, want %q", esc.DedupeKey, item.DedupeKey())
	}
	if esc.AmountDuePaise != item.AmountDuePaise {
		t.Errorf("AmountDuePaise = %d, want %d", esc.AmountDuePaise, item.AmountDuePaise)
	}
	if esc.Reason != "unclassified failure, fail closed" {
		t.Errorf("Reason = %q, want the reason from the context", esc.Reason)
	}
	// The timestamp is what makes this a record rather than a counter.
	if !esc.RaisedAt.Equal(testStart) {
		t.Errorf("RaisedAt = %s, want %s", esc.RaisedAt, testStart)
	}

	if row := r.onlyRow(t); row.Kind != audit.KindEscalationRaised {
		t.Errorf("audit kind = %q, want %q", row.Kind, audit.KindEscalationRaised)
	}
}

func TestWriterSinkAppendsOneJSONLineWithATimestamp(t *testing.T) {
	buf := &bytes.Buffer{}
	sink, err := intervene.NewWriterSink(buf)
	if err != nil {
		t.Fatalf("NewWriterSink: %v", err)
	}

	for _, id := range []string{"ri_000000000001", "ri_000000000002"} {
		if err := sink.Escalate(context.Background(), intervene.Escalation{
			RiskItemID:     id,
			DedupeKey:      "order_TYEwKA0KjwEW3t",
			Source:         string(riskitem.SourceOverdueInvoice),
			AmountDuePaise: 50000,
			Currency:       "INR",
			Reason:         "no contact channel",
			RaisedAt:       testStart,
		}); err != nil {
			t.Fatalf("Escalate: %v", err)
		}
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("the sink wrote %d line(s), want 2", len(lines))
	}
	var first intervene.Escalation
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("the first line is not valid JSON: %v (%q)", err, lines[0])
	}
	if first.RiskItemID != "ri_000000000001" {
		t.Errorf("first risk_item_id = %q, want ri_000000000001", first.RiskItemID)
	}
	if !first.RaisedAt.Equal(testStart) {
		t.Errorf("first raised_at = %s, want %s", first.RaisedAt, testStart)
	}

	if _, err := intervene.NewWriterSink(nil); !errors.Is(err, intervene.ErrNoEscalationSink) {
		t.Errorf("NewWriterSink(nil) = %v, want ErrNoEscalationSink", err)
	}
}

func TestDoNothingIsAnAcceptedNoOp(t *testing.T) {
	r := newRig(t, nil)

	out, err := r.engine.Apply(context.Background(), invoiceItem(), riskitem.ActionDoNothing)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !out.Accepted {
		t.Error("the explicit no-op was not accepted")
	}
	// riskitem.Outcome names an empty Observable as the honest answer here.
	if out.Observable != "" {
		t.Errorf("Observable = %q, want empty: nothing was observable", out.Observable)
	}
	if got := r.calls.Load(); got != 0 {
		t.Errorf("the gateway saw %d request(s), want 0", got)
	}
	if row := r.onlyRow(t); row.Kind != audit.KindActionSkipped {
		t.Errorf("audit kind = %q, want %q", row.Kind, audit.KindActionSkipped)
	}
}

// digestWithA13DigitRun is a real sha256 digest, of the ASCII string "13",
// that holds a run of 13 consecutive decimal digits. internal/redact's
// cardLike matches that shape, so a ledger row carrying this string whole
// comes back with the marker in the middle of it. About one digest in twenty
// is like this, and on 2026-09-05 one of the two intervention_applied rows in
// a live test-mode run was mangled that way on camera-visible output.
const digestWithA13DigitRun = "3fdba35f04dc8c462986c992bcf875546257113072a909c162f7e470e581e278"

// TestTheLedgerRowCarriesTheShortIdempotencyKey pins the writing-side fix
// internal/redact's own doc comment names: policy.ShortKey puts 12 characters
// in the audit row, and 12 characters cannot hold a run of 13 digits.
func TestTheLedgerRowCarriesTheShortIdempotencyKey(t *testing.T) {
	// The premise. If this ever stops holding, the test below proves nothing.
	if redact.Value(digestWithA13DigitRun) == digestWithA13DigitRun {
		t.Fatalf("the fixture digest no longer trips the redactor, so this test is vacuous: %q", digestWithA13DigitRun)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/invoices/{id}/notify_by/{medium}", func(w http.ResponseWriter, _ *http.Request) {
		writeRawJSON(w, http.StatusOK, probeNotifySuccess)
	})
	mux.HandleFunc("GET /v1/invoices/{id}", func(w http.ResponseWriter, _ *http.Request) {
		writeRawJSON(w, http.StatusOK, probeInvoiceNotified)
	})
	r := newRig(t, mux)

	ctx := intervene.WithIdempotencyKey(context.Background(), digestWithA13DigitRun)
	if _, err := r.engine.Apply(ctx, invoiceItem(), riskitem.ActionNotifyEmail); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	row := r.onlyRow(t)
	got := row.Detail["idempotency_key"]
	if len(got) != policy.ShortKeyLen {
		t.Errorf("idempotency_key is %d character(s) (%q), want %d", len(got), got, policy.ShortKeyLen)
	}
	if want := digestWithA13DigitRun[:policy.ShortKeyLen]; got != want {
		t.Errorf("idempotency_key = %q, want the key's first %d characters %q", got, policy.ShortKeyLen, want)
	}
	// The point of the whole fix: what the ledger holds survives the redactor
	// unchanged, so a reviewer can still join this row to the policy_evaluated
	// row that carries the same prefix.
	if redacted := redact.Value(got); redacted != got {
		t.Errorf("the ledger's idempotency_key was mangled by the redactor: %q became %q", got, redacted)
	}
	if strings.Contains(got, redact.Marker) {
		t.Errorf("idempotency_key already carries the redaction marker: %q", got)
	}
}

func TestTheSecondCallOnOneIdempotencyKeyDoesNotFireAgain(t *testing.T) {
	t.Run("a notify is sent once", func(t *testing.T) {
		var notifies atomic.Int64
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/invoices/{id}/notify_by/{medium}", func(w http.ResponseWriter, _ *http.Request) {
			notifies.Add(1)
			writeRawJSON(w, http.StatusOK, probeNotifySuccess)
		})
		mux.HandleFunc("GET /v1/invoices/{id}", func(w http.ResponseWriter, _ *http.Request) {
			writeRawJSON(w, http.StatusOK, probeInvoiceNotified)
		})
		r := newRig(t, mux)

		ctx := intervene.WithIdempotencyKey(context.Background(), "sweep-2026-09-05|inv_TYEwC7POHGFZNa|notify_email")
		first, err := r.engine.Apply(ctx, invoiceItem(), riskitem.ActionNotifyEmail)
		if err != nil {
			t.Fatalf("first Apply: %v", err)
		}
		second, err := r.engine.Apply(ctx, invoiceItem(), riskitem.ActionNotifyEmail)
		if err != nil {
			t.Fatalf("second Apply: %v", err)
		}

		if got := notifies.Load(); got != 1 {
			t.Errorf("the notify endpoint saw %d call(s) for one idempotency key, want 1", got)
		}
		// The effect is the first call's effect, so the second call reports
		// it unchanged rather than inventing a second one.
		if second != first {
			t.Errorf("the replay returned %+v, want the first outcome %+v", second, first)
		}

		rows := r.rows(t)
		if len(rows) != 2 {
			t.Fatalf("wrote %d ledger row(s), want 2: a replay is still a decision", len(rows))
		}
		if rows[0].Kind != audit.KindInterventionApplied {
			t.Errorf("first audit kind = %q, want %q", rows[0].Kind, audit.KindInterventionApplied)
		}
		// The ledger is the only place the replay is visible.
		if rows[1].Kind != audit.KindActionSkipped {
			t.Errorf("second audit kind = %q, want %q", rows[1].Kind, audit.KindActionSkipped)
		}
		if rows[1].Detail["idempotent_replay"] != "true" {
			t.Errorf("second audit detail idempotent_replay = %q, want true", rows[1].Detail["idempotent_replay"])
		}
	})

	t.Run("a different key fires again", func(t *testing.T) {
		var links atomic.Int64
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/payment_links", func(w http.ResponseWriter, _ *http.Request) {
			links.Add(1)
			writeRawJSON(w, http.StatusOK, probePaymentLink)
		})
		r := newRig(t, mux)

		base := context.Background()
		for _, key := range []string{"sweep-a", "sweep-b"} {
			if _, err := r.engine.Apply(intervene.WithIdempotencyKey(base, key), bareItem(), riskitem.ActionCreatePaymentLink); err != nil {
				t.Fatalf("Apply(%s): %v", key, err)
			}
		}
		if got := links.Load(); got != 2 {
			t.Errorf("the create endpoint saw %d call(s) for two keys, want 2", got)
		}
	})

	t.Run("with no key on the context the fallback still collapses a repeat", func(t *testing.T) {
		var links atomic.Int64
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/payment_links", func(w http.ResponseWriter, _ *http.Request) {
			links.Add(1)
			writeRawJSON(w, http.StatusOK, probePaymentLink)
		})
		r := newRig(t, mux)

		for range 2 {
			if _, err := r.engine.Apply(context.Background(), bareItem(), riskitem.ActionCreatePaymentLink); err != nil {
				t.Fatalf("Apply: %v", err)
			}
		}
		if got := links.Load(); got != 1 {
			t.Errorf("the create endpoint saw %d call(s), want 1: the fallback key is the sighting and the action", got)
		}
	})

	t.Run("one context reused for two actions does not replay the first", func(t *testing.T) {
		// A context is routinely reused across an item's whole decision. If
		// the key did not carry the action, the escalate below would come back
		// as the notify's Outcome: the escalation would never be written and
		// the ledger row would name notify_email.
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/invoices/{id}/notify_by/{medium}", func(w http.ResponseWriter, _ *http.Request) {
			writeRawJSON(w, http.StatusOK, probeNotifySuccess)
		})
		mux.HandleFunc("GET /v1/invoices/{id}", func(w http.ResponseWriter, _ *http.Request) {
			writeRawJSON(w, http.StatusOK, probeInvoiceNotified)
		})
		r := newRigWith(t, mux, nil)

		ctx := intervene.WithIdempotencyKey(context.Background(), "sweep-2026-09-05")
		item := invoiceItem()

		notified, err := r.engine.Apply(ctx, item, riskitem.ActionNotifyEmail)
		if err != nil {
			t.Fatalf("notify Apply: %v", err)
		}
		escalated, err := r.engine.Apply(ctx, item, riskitem.ActionEscalate)
		if err != nil {
			t.Fatalf("escalate Apply: %v", err)
		}

		if escalated.Action != riskitem.ActionEscalate {
			t.Errorf("the escalate call returned Action %q, want escalate", escalated.Action)
		}
		if escalated.Observable == notified.Observable {
			t.Errorf("the escalate call returned the notify's observable %q", escalated.Observable)
		}
		if got := len(r.escalations.Escalations()); got != 1 {
			t.Errorf("the sink holds %d escalation(s), want 1: the escalate was collapsed into the notify", got)
		}

		rows := r.rows(t)
		if len(rows) != 2 {
			t.Fatalf("wrote %d ledger row(s), want 2", len(rows))
		}
		if rows[1].ProposedAction != riskitem.ActionEscalate {
			t.Errorf("second row proposed_action = %q, want escalate", rows[1].ProposedAction)
		}
		if rows[1].Detail["idempotent_replay"] != "" {
			t.Error("a different action on the same context was recorded as an idempotent replay")
		}
	})

	t.Run("two concurrent calls on one key send once", func(t *testing.T) {
		// A lookup followed by a call is a check-then-act: both goroutines
		// miss and both send. The guard holds the key for the whole action so
		// the second one waits and finds the first one's result.
		var notifies atomic.Int64
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/invoices/{id}/notify_by/{medium}", func(w http.ResponseWriter, _ *http.Request) {
			notifies.Add(1)
			writeRawJSON(w, http.StatusOK, probeNotifySuccess)
		})
		mux.HandleFunc("GET /v1/invoices/{id}", func(w http.ResponseWriter, _ *http.Request) {
			writeRawJSON(w, http.StatusOK, probeInvoiceNotified)
		})
		r := newRig(t, mux)

		ctx := intervene.WithIdempotencyKey(context.Background(), "concurrent-key")
		const callers = 8
		var wg sync.WaitGroup
		start := make(chan struct{})
		outs := make([]riskitem.Outcome, callers)
		errs := make([]error, callers)
		for i := range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				outs[i], errs[i] = r.engine.Apply(ctx, invoiceItem(), riskitem.ActionNotifyEmail)
			}()
		}
		close(start)
		wg.Wait()

		for i, err := range errs {
			if err != nil {
				t.Fatalf("Apply %d: %v", i, err)
			}
		}
		if got := notifies.Load(); got != 1 {
			t.Errorf("the notify endpoint saw %d call(s) from %d concurrent callers on one key, want 1", got, callers)
		}
		// Every caller gets the same answer, because there was one effect.
		for i, out := range outs {
			if out != outs[0] {
				t.Errorf("caller %d got %+v, want the same outcome as caller 0 %+v", i, out, outs[0])
			}
			if !out.Accepted {
				t.Errorf("caller %d was refused: %q", i, out.Err)
			}
		}
		if rows := r.rows(t); len(rows) != callers {
			t.Errorf("wrote %d ledger row(s), want %d: every call is a decision", len(rows), callers)
		}
	})

	t.Run("a refusal is not remembered", func(t *testing.T) {
		// The first call is refused because the item has no contact channel.
		// Nothing happened, so nothing is held against the key, and an item
		// that later has a channel is not blocked by the earlier refusal.
		var notifies atomic.Int64
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/invoices/{id}/notify_by/{medium}", func(w http.ResponseWriter, _ *http.Request) {
			notifies.Add(1)
			writeRawJSON(w, http.StatusOK, probeNotifySuccess)
		})
		mux.HandleFunc("GET /v1/invoices/{id}", func(w http.ResponseWriter, _ *http.Request) {
			writeRawJSON(w, http.StatusOK, probeInvoiceNotified)
		})
		r := newRig(t, mux)

		ctx := intervene.WithIdempotencyKey(context.Background(), "one-key")
		noChannel := invoiceItem()
		noChannel.Customer = riskitem.Customer{Name: "Probe Customer"}

		if out, err := r.engine.Apply(ctx, noChannel, riskitem.ActionNotifyEmail); err != nil || out.Accepted {
			t.Fatalf("the first call was not a refusal: out=%+v err=%v", out, err)
		}
		out, err := r.engine.Apply(ctx, invoiceItem(), riskitem.ActionNotifyEmail)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if !out.Accepted {
			t.Errorf("a refusal blocked a later lawful call on the same key: %q", out.Err)
		}
		if got := notifies.Load(); got != 1 {
			t.Errorf("the notify endpoint saw %d call(s), want 1", got)
		}
	})
}

func TestEveryPathWritesExactlyOneAuditEvent(t *testing.T) {
	// One rig per case, so the row count is the count for that one Apply. The
	// handler answers every endpoint any lawful action reaches.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/invoices/{id}/notify_by/{medium}", func(w http.ResponseWriter, _ *http.Request) {
		writeRawJSON(w, http.StatusOK, probeNotifySuccess)
	})
	mux.HandleFunc("GET /v1/invoices/{id}", func(w http.ResponseWriter, _ *http.Request) {
		writeRawJSON(w, http.StatusOK, probeInvoiceNotified)
	})
	mux.HandleFunc("POST /v1/invoices/{id}/cancel", func(w http.ResponseWriter, _ *http.Request) {
		writeRawJSON(w, http.StatusOK, probeInvoiceCancelled)
	})
	mux.HandleFunc("POST /v1/payment_links", func(w http.ResponseWriter, _ *http.Request) {
		writeRawJSON(w, http.StatusOK, probePaymentLink)
	})
	mux.HandleFunc("POST /v1/payment_links/{id}/notify_by/{medium}", func(w http.ResponseWriter, _ *http.Request) {
		writeRawJSON(w, http.StatusOK, probeNotifySuccess)
	})

	tests := []struct {
		name   string
		item   riskitem.RiskItem
		action string
		kind   string
	}{
		{"notify_email", invoiceItem(), riskitem.ActionNotifyEmail, audit.KindInterventionApplied},
		{"notify_sms", invoiceItem(), riskitem.ActionNotifySMS, audit.KindInterventionApplied},
		{"create_payment_link", bareItem(), riskitem.ActionCreatePaymentLink, audit.KindInterventionApplied},
		{"resend_link", linkItem(), riskitem.ActionResendLink, audit.KindInterventionApplied},
		{"cancel_write_off", invoiceItem(), riskitem.ActionCancelWriteOff, audit.KindInterventionApplied},
		{"log_promise", invoiceItem(), riskitem.ActionLogPromise, audit.KindPromiseLogged},
		{"escalate", invoiceItem(), riskitem.ActionEscalate, audit.KindEscalationRaised},
		{"do_nothing", invoiceItem(), riskitem.ActionDoNothing, audit.KindActionSkipped},
		{"an unlawful action", invoiceItem(), "retry_same_instrument", audit.KindActionSkipped},
		{"a refused notify", noChannelItem(), riskitem.ActionNotifyEmail, audit.KindActionSkipped},
	}

	// The lawful set is closed, and this table covers all of it plus two
	// refusals. A constant added to riskitem without a case here fails this.
	covered := make(map[string]bool)
	for _, tt := range tests {
		covered[tt.action] = true
	}
	for _, action := range riskitem.LawfulActions() {
		if !covered[action] {
			t.Errorf("the lawful action %q has no case in this table", action)
		}
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t, mux)

			out, err := r.engine.Apply(context.Background(), tt.item, tt.action)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}

			row := r.onlyRow(t)
			if row.Kind != tt.kind {
				t.Errorf("audit kind = %q, want %q", row.Kind, tt.kind)
			}
			if row.ProposedAction != tt.action {
				t.Errorf("audit proposed_action = %q, want %q", row.ProposedAction, tt.action)
			}
			if row.Detail["risk_item_id"] != tt.item.ID {
				t.Errorf("audit detail risk_item_id = %q, want %q", row.Detail["risk_item_id"], tt.item.ID)
			}
			if row.Detail["accepted"] != boolText(out.Accepted) {
				t.Errorf("audit detail accepted = %q, want %q", row.Detail["accepted"], boolText(out.Accepted))
			}
			if row.OrderID == "" {
				t.Error("the row carries no order id, so it cannot be joined to a batch")
			}
		})
	}
}

func TestAGatewayFailureIsReportedAndStillRecorded(t *testing.T) {
	// The body echoes the key secret back, which is the case
	// razorpay.Client.Redact exists for: a gateway that reflects the request
	// into its error body would otherwise put a credential in the ledger by
	// way of the error text this package copies into Outcome.Err. A body that
	// never carried the secret would make the assertion below pass whether or
	// not any redaction ran.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/invoices/{id}/notify_by/{medium}", func(w http.ResponseWriter, _ *http.Request) {
		writeRawJSON(w, http.StatusInternalServerError,
			`{"error":{"code":"SERVER_ERROR","description":"we are having trouble with `+testKeySecret+`"}}`)
	})
	r := newRig(t, mux)

	out, err := r.engine.Apply(context.Background(), invoiceItem(), riskitem.ActionNotifyEmail)
	if err == nil {
		t.Fatal("a call that did not complete returned no error")
	}
	if out.Accepted {
		t.Error("a failed call was reported as accepted")
	}
	if out.Observable != intervene.ObservableNotifyFailed {
		t.Errorf("Observable = %q, want %q", out.Observable, intervene.ObservableNotifyFailed)
	}
	if out.Err == "" {
		t.Error("Err is empty on a failed call")
	}

	// An action that ran and errored is an attempt. Filing it as skipped would
	// put it in the same bucket as a refusal, which is what phase 1's decision
	// of 2026-08-31 fixed in internal/recovery.
	row := r.onlyRow(t)
	if row.Kind != audit.KindInterventionApplied {
		t.Errorf("audit kind = %q, want %q", row.Kind, audit.KindInterventionApplied)
	}
	if row.Detail["error"] == "" {
		t.Error("the row carries no error detail for a call that failed")
	}
	// A credential must not reach the ledger, or the Outcome, by way of an
	// error message the gateway echoed it into.
	if strings.Contains(r.ledger.String(), testKeySecret) {
		t.Error("a credential reached the audit ledger")
	}
	if strings.Contains(out.Err, testKeySecret) {
		t.Error("a credential reached Outcome.Err")
	}
	if !strings.Contains(r.ledger.String(), razorpay.Redacted) {
		t.Error("nothing in the row was redacted, so the echoed credential was not recognised")
	}
}

func TestResendLinkFailurePaths(t *testing.T) {
	t.Run("a 4xx on the resend is a refusal", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/payment_links/{id}/notify_by/{medium}", func(w http.ResponseWriter, _ *http.Request) {
			writeRawJSON(w, http.StatusBadRequest,
				`{"error":{"code":"BAD_REQUEST_ERROR","description":"payment link is not payable","reason":"input_validation_failed"}}`)
		})
		r := newRig(t, mux)

		out, err := r.engine.Apply(context.Background(), linkItem(), riskitem.ActionResendLink)
		if err != nil {
			t.Fatalf("a declined resend was reported as an error: %v", err)
		}
		if out.Accepted {
			t.Error("a declined resend was reported as accepted")
		}
		if out.Observable != intervene.ObservableNotifyRefused {
			t.Errorf("Observable = %q, want %q", out.Observable, intervene.ObservableNotifyRefused)
		}
	})

	t.Run("a 5xx on the resend is an error", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/payment_links/{id}/notify_by/{medium}", func(w http.ResponseWriter, _ *http.Request) {
			writeRawJSON(w, http.StatusInternalServerError, `{"error":{"code":"SERVER_ERROR"}}`)
		})
		r := newRig(t, mux)

		out, err := r.engine.Apply(context.Background(), linkItem(), riskitem.ActionResendLink)
		if err == nil {
			t.Fatal("a resend that got no answer about it returned no error")
		}
		if out.Observable != intervene.ObservableNotifyFailed {
			t.Errorf("Observable = %q, want %q", out.Observable, intervene.ObservableNotifyFailed)
		}
	})

	t.Run("a success false on the resend is not an acceptance", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/payment_links/{id}/notify_by/{medium}", func(w http.ResponseWriter, _ *http.Request) {
			writeRawJSON(w, http.StatusOK, `{"success": false}`)
		})
		r := newRig(t, mux)

		out, err := r.engine.Apply(context.Background(), linkItem(), riskitem.ActionResendLink)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if out.Accepted {
			t.Error("a 200 carrying success false was counted as an acceptance")
		}
	})

	t.Run("an item with only a phone number resends over sms", func(t *testing.T) {
		var gotPath string
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/payment_links/{id}/notify_by/{medium}", func(w http.ResponseWriter, req *http.Request) {
			gotPath = req.URL.Path
			writeRawJSON(w, http.StatusOK, probeNotifySuccess)
		})
		r := newRig(t, mux)

		item := linkItem()
		item.Customer = riskitem.Customer{Name: "Probe Customer", Contact: "9000090000"}

		if _, err := r.engine.Apply(context.Background(), item, riskitem.ActionResendLink); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if !strings.HasSuffix(gotPath, "/notify_by/sms") {
			t.Errorf("path = %q, want a resend over sms for an item with no email", gotPath)
		}
	})

	t.Run("a handle with an id and no kind is refused", func(t *testing.T) {
		// A detector that filled ID and left Kind empty. There is no endpoint
		// to send to, so this refuses rather than picking one.
		item := linkItem()
		item.PayHandle.Kind = ""
		r := newRig(t, nil)

		out, err := r.engine.Apply(context.Background(), item, riskitem.ActionResendLink)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if out.Accepted {
			t.Error("a resend was accepted for a handle of no known kind")
		}
		if out.Err != intervene.RefusalNoHandle {
			t.Errorf("Err = %q, want %q", out.Err, intervene.RefusalNoHandle)
		}
		if got := r.calls.Load(); got != 0 {
			t.Errorf("the gateway saw %d request(s), want 0", got)
		}
	})
}

func TestAnExhaustedRetryBudgetIsAnErrorAndNotARefusal(t *testing.T) {
	// Every 429 carries a 4xx status, so a status check alone would file an
	// exhausted retry budget as the gateway declining. It is not: the request
	// never got through, so nothing knows whether it would have been accepted.
	var attempts atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/invoices/{id}/notify_by/{medium}", func(w http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		writeRawJSON(w, http.StatusTooManyRequests, `{"error":{"code":"BAD_REQUEST_ERROR","description":"Too many requests"}}`)
	})
	r := newRigWithClient(t, mux, func(o *razorpay.ClientOptions) {
		o.MaxAttempts = 2
		// No test sleeps: the backoff seam returns at once.
		o.Wait = func(context.Context, time.Duration) error { return nil }
	}, nil)

	out, err := r.engine.Apply(context.Background(), invoiceItem(), riskitem.ActionNotifyEmail)
	if err == nil {
		t.Fatal("an exhausted retry budget returned no error")
	}
	if !errors.Is(err, razorpay.ErrRetryBudgetExhausted) {
		t.Errorf("error = %v, want it to wrap ErrRetryBudgetExhausted", err)
	}
	if out.Accepted {
		t.Error("a request that never got through was reported as accepted")
	}
	if out.Observable != intervene.ObservableNotifyFailed {
		t.Errorf("Observable = %q, want %q", out.Observable, intervene.ObservableNotifyFailed)
	}
	if got := attempts.Load(); got != 2 {
		t.Errorf("the endpoint saw %d attempt(s), want 2", got)
	}
}

func TestNotifyInvoiceGatewayDeclineIsARefusal(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/invoices/{id}/notify_by/{medium}", func(w http.ResponseWriter, _ *http.Request) {
		writeRawJSON(w, http.StatusBadRequest,
			`{"error":{"code":"BAD_REQUEST_ERROR","description":"Invoice is not issued","reason":"input_validation_failed"}}`)
	})
	mux.HandleFunc("GET /v1/invoices/{id}", func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the invoice was read back after a declined notify")
		writeRawJSON(w, http.StatusOK, probeInvoiceNotified)
	})
	r := newRig(t, mux)

	out, err := r.engine.Apply(context.Background(), invoiceItem(), riskitem.ActionNotifyEmail)
	if err != nil {
		t.Fatalf("a declined notify was reported as an error: %v", err)
	}
	if out.Accepted {
		t.Error("a declined notify was reported as accepted")
	}
	if out.Observable != intervene.ObservableNotifyRefused {
		t.Errorf("Observable = %q, want %q", out.Observable, intervene.ObservableNotifyRefused)
	}
	if r.onlyRow(t).Kind != audit.KindInterventionApplied {
		t.Error("a notify that was called and declined was not recorded as an attempt")
	}
}

func TestCancelWriteOffGatewayFailure(t *testing.T) {
	// A paid invoice cannot be cancelled and Razorpay answers 400. The call
	// was made and declined, so it is a refusal rather than an error.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/invoices/{id}/cancel", func(w http.ResponseWriter, _ *http.Request) {
		writeRawJSON(w, http.StatusBadRequest,
			`{"error":{"code":"BAD_REQUEST_ERROR","description":"Invoice is already paid","reason":"input_validation_failed"}}`)
	})
	r := newRig(t, mux)

	out, err := r.engine.Apply(context.Background(), invoiceItem(), riskitem.ActionCancelWriteOff)
	if err != nil {
		t.Fatalf("a declined cancel was reported as an error: %v", err)
	}
	if out.Accepted {
		t.Error("a declined cancel was reported as accepted")
	}
	if out.Err == "" {
		t.Error("Err is empty on a declined cancel")
	}
	if r.onlyRow(t).Kind != audit.KindInterventionApplied {
		t.Error("a cancel that was called and declined was not recorded as an attempt")
	}

	t.Run("a 5xx on the cancel is an error", func(t *testing.T) {
		srvMux := http.NewServeMux()
		srvMux.HandleFunc("POST /v1/invoices/{id}/cancel", func(w http.ResponseWriter, _ *http.Request) {
			writeRawJSON(w, http.StatusInternalServerError, `{"error":{"code":"SERVER_ERROR"}}`)
		})
		r := newRig(t, srvMux)

		out, err := r.engine.Apply(context.Background(), invoiceItem(), riskitem.ActionCancelWriteOff)
		if err == nil {
			t.Fatal("a cancel that got no answer about it returned no error")
		}
		if out.Accepted {
			t.Error("a failed cancel was reported as accepted")
		}
	})
}

func TestAnUnwritableLedgerOrSinkIsNotReportedAsSuccess(t *testing.T) {
	// The claim escalation.go says this package must never make is reporting
	// an escalation as raised when nothing was written.
	t.Run("a failing escalation sink", func(t *testing.T) {
		r := newRigWith(t, nil, func(o *intervene.Options) { o.Escalations = failingSink{} })

		out, err := r.engine.Apply(context.Background(), invoiceItem(), riskitem.ActionEscalate)
		if err == nil {
			t.Fatal("an escalation that was not written returned no error")
		}
		if out.Accepted {
			t.Error("an escalation that was not written was reported as raised")
		}
		if out.Observable == intervene.ObservableEscalated {
			t.Error("an escalation that was not written carries the queued observable")
		}
		if row := r.onlyRow(t); row.Kind == audit.KindEscalationRaised {
			t.Error("an escalation that was not written was recorded as escalation_raised")
		}
	})

	t.Run("a failing promise ledger", func(t *testing.T) {
		r := newRigWith(t, nil, func(o *intervene.Options) { o.Promises = failingPromiseLedger{} })

		out, err := r.engine.Apply(context.Background(), invoiceItem(), riskitem.ActionLogPromise)
		if err == nil {
			t.Fatal("a promise that was not written returned no error")
		}
		if out.Accepted {
			t.Error("a promise that was not written was reported as logged")
		}
		if row := r.onlyRow(t); row.Kind == audit.KindPromiseLogged {
			t.Error("a promise that was not written was recorded as promise_logged")
		}
	})

	t.Run("a failing WriterSink reports the write error", func(t *testing.T) {
		sink, err := intervene.NewWriterSink(failingWriter{})
		if err != nil {
			t.Fatalf("NewWriterSink: %v", err)
		}
		if err := sink.Escalate(context.Background(), intervene.Escalation{RiskItemID: "ri_000000000001"}); err == nil {
			t.Error("a sink whose writer failed reported the escalation as written")
		}
	})

	t.Run("a failing audit ledger is reported even though the action happened", func(t *testing.T) {
		// The one documented case where Apply returns an error for an action
		// that succeeded: a side effect with no row is what the ledger exists
		// to prevent.
		r := newRigWith(t, nil, func(o *intervene.Options) {
			recorder, err := audit.NewRecorder(audit.Options{Writer: failingWriter{}, Clock: clock.NewFake(testStart)})
			if err != nil {
				t.Fatalf("NewRecorder: %v", err)
			}
			o.Recorder = recorder
		})

		out, err := r.engine.Apply(context.Background(), invoiceItem(), riskitem.ActionDoNothing)
		if err == nil {
			t.Fatal("an unwritable audit row returned no error")
		}
		if !out.Accepted {
			t.Error("a failed audit write downgraded an action that succeeded")
		}
	})
}

func TestGeneratedReasonNamesTheItemAndAction(t *testing.T) {
	// With no reason on the context both writers fall back to a generated
	// line. It has to name the item, or an escalation queue is unworkable.
	r := newRig(t, nil)
	item := invoiceItem()

	if _, err := r.engine.Apply(context.Background(), item, riskitem.ActionEscalate); err != nil {
		t.Fatalf("escalate: %v", err)
	}
	if _, err := r.engine.Apply(context.Background(), item, riskitem.ActionLogPromise); err != nil {
		t.Fatalf("log_promise: %v", err)
	}

	raised := r.escalations.Escalations()
	if len(raised) != 1 {
		t.Fatalf("the sink holds %d escalation(s), want 1", len(raised))
	}
	if !strings.Contains(raised[0].Reason, item.ID) || !strings.Contains(raised[0].Reason, riskitem.ActionEscalate) {
		t.Errorf("generated reason = %q, want it to name the item and the action", raised[0].Reason)
	}

	records := r.promises.Records()
	if len(records) != 1 {
		t.Fatalf("the ledger holds %d record(s), want 1", len(records))
	}
	if !strings.Contains(records[0].Note, item.ID) {
		t.Errorf("generated note = %q, want it to name the item", records[0].Note)
	}
}

func TestReadBackAccessorsReturnCopies(t *testing.T) {
	ledger := intervene.NewMemoryPromiseLedger()
	if err := ledger.Log(context.Background(), intervene.PromiseRecord{RiskItemID: "ri_000000000001"}); err != nil {
		t.Fatalf("Log: %v", err)
	}
	got := ledger.Records()
	got[0].RiskItemID = "mutated"
	if ledger.Records()[0].RiskItemID != "ri_000000000001" {
		t.Error("writing to the slice Records returned reached the ledger")
	}

	sink := intervene.NewMemorySink()
	if err := sink.Escalate(context.Background(), intervene.Escalation{RiskItemID: "ri_000000000002"}); err != nil {
		t.Fatalf("Escalate: %v", err)
	}
	raised := sink.Escalations()
	raised[0].RiskItemID = "mutated"
	if sink.Escalations()[0].RiskItemID != "ri_000000000002" {
		t.Error("writing to the slice Escalations returned reached the sink")
	}
}

// noChannelItem is an invoice item with nowhere to send a notification.
func noChannelItem() riskitem.RiskItem {
	item := invoiceItem()
	item.Customer = riskitem.Customer{Name: "Probe Customer"}
	return item
}

func boolText(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
