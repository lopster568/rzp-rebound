package razorpay_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
)

// The bodies below are what Razorpay test mode returned on 2026-09-05, copied
// unchanged. They are held as raw strings rather than built from the structs
// under test on purpose: a fixture marshalled through the same tags the client
// decodes with proves nothing about the wire shape, which is the point
// apiserver_test.go already makes about the fake API server.
//
// Nothing here is a real credential. The ids are test-mode ids from a
// throwaway account and carry no key material.

// probeCustomer is POST /v1/customers. Note gstin: null, which has to decode
// to an empty string rather than failing the call.
const probeCustomer = `{
  "id": "cust_TYEw5izKFR0iJr",
  "entity": "customer",
  "name": "Probe Customer",
  "email": "probe-2026-09-05@example.com",
  "contact": "9000090000",
  "gstin": null,
  "notes": {"purpose": "feasibility probe 2026-09-05"},
  "shipping_address": [],
  "created_at": 1788586075
}`

// probeInvoiceDraft is POST /v1/invoices with draft "1". order_id, short_url,
// issued_at, amount_paid, and amount_due are all null.
const probeInvoiceDraft = `{
  "id": "inv_TYEwC7POHGFZNa",
  "entity": "invoice",
  "receipt": null,
  "invoice_number": null,
  "customer_id": "cust_TYEw5izKFR0iJr",
  "customer_details": {
    "id": "cust_TYEw5izKFR0iJr",
    "name": "Probe Customer",
    "email": "probe-2026-09-05@example.com",
    "contact": "9000090000",
    "gstin": null,
    "billing_address": null,
    "shipping_address": null,
    "customer_name": "Probe Customer",
    "customer_email": "probe-2026-09-05@example.com",
    "customer_contact": "9000090000"
  },
  "order_id": null,
  "line_items": [
    {
      "id": "li_TYEwCBYqSEp1Lt",
      "item_id": null,
      "ref_id": null,
      "ref_type": null,
      "name": "Recovery probe item",
      "description": null,
      "amount": 50000,
      "unit_amount": 50000,
      "gross_amount": 50000,
      "tax_amount": 0,
      "taxable_amount": 50000,
      "net_amount": 50000,
      "currency": "INR",
      "type": "invoice",
      "tax_inclusive": false,
      "hsn_code": null,
      "sac_code": null,
      "tax_rate": null,
      "unit": null,
      "quantity": 1,
      "taxes": []
    }
  ],
  "payment_id": null,
  "status": "draft",
  "expire_by": null,
  "issued_at": null,
  "paid_at": null,
  "cancelled_at": null,
  "expired_at": null,
  "sms_status": null,
  "email_status": null,
  "date": 1788586081,
  "terms": null,
  "partial_payment": false,
  "gross_amount": 50000,
  "tax_amount": 0,
  "taxable_amount": 50000,
  "amount": 50000,
  "amount_paid": null,
  "amount_due": null,
  "currency": "INR",
  "currency_symbol": "₹",
  "description": "feasibility probe draft 2026-09-05",
  "notes": [],
  "comment": null,
  "short_url": null,
  "view_less": true,
  "billing_start": null,
  "billing_end": null,
  "type": "invoice",
  "group_taxes_discounts": false,
  "created_at": 1788586081,
  "ref_num": null
}`

// probeInvoiceIssued is POST /v1/invoices/{id}/issue on that same draft. The
// order and the short URL exist now and email_status is still null: issuing is
// not sending.
const probeInvoiceIssued = `{
  "id": "inv_TYEwC7POHGFZNa",
  "entity": "invoice",
  "receipt": null,
  "invoice_number": null,
  "customer_id": "cust_TYEw5izKFR0iJr",
  "customer_details": {
    "id": "cust_TYEw5izKFR0iJr",
    "name": "Probe Customer",
    "email": "probe-2026-09-05@example.com",
    "contact": "9000090000",
    "gstin": null,
    "billing_address": null,
    "shipping_address": null,
    "customer_name": "Probe Customer",
    "customer_email": "probe-2026-09-05@example.com",
    "customer_contact": "9000090000"
  },
  "order_id": "order_TYEwKA0KjwEW3t",
  "line_items": [
    {
      "id": "li_TYEwCBYqSEp1Lt",
      "item_id": null,
      "name": "Recovery probe item",
      "description": null,
      "amount": 50000,
      "unit_amount": 50000,
      "gross_amount": 50000,
      "tax_amount": 0,
      "taxable_amount": 50000,
      "net_amount": 50000,
      "currency": "INR",
      "type": "invoice",
      "quantity": 1,
      "taxes": []
    }
  ],
  "payment_id": null,
  "status": "issued",
  "expire_by": null,
  "issued_at": 1788586088,
  "paid_at": null,
  "cancelled_at": null,
  "expired_at": null,
  "sms_status": null,
  "email_status": null,
  "date": 1788586081,
  "terms": null,
  "partial_payment": false,
  "gross_amount": 50000,
  "tax_amount": 0,
  "taxable_amount": 50000,
  "amount": 50000,
  "amount_paid": 0,
  "amount_due": 50000,
  "currency": "INR",
  "currency_symbol": "₹",
  "description": "feasibility probe draft 2026-09-05",
  "notes": [],
  "comment": null,
  "short_url": "https://rzp.io/rzp/4U2HXcQ",
  "view_less": true,
  "type": "invoice",
  "group_taxes_discounts": false,
  "created_at": 1788586081,
  "idempotency_key": null,
  "ref_num": null
}`

// probeInvoiceNotified is GET /v1/invoices/{id} on the same invoice after the
// email notify. Only email_status moved.
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
  "paid_at": null,
  "cancelled_at": null,
  "expired_at": null,
  "sms_status": null,
  "email_status": "sent",
  "date": 1788586081,
  "partial_payment": false,
  "amount": 50000,
  "amount_paid": 0,
  "amount_due": 50000,
  "currency": "INR",
  "currency_symbol": "₹",
  "description": "feasibility probe draft 2026-09-05",
  "notes": [],
  "short_url": "https://rzp.io/rzp/4U2HXcQ",
  "type": "invoice",
  "created_at": 1788586081
}`

// probeInvoiceCancelled is POST /v1/invoices/{id}/cancel on a second,
// separately issued invoice. cancelled_at is set and the short URL survives.
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
  "payment_id": null,
  "status": "cancelled",
  "issued_at": 1788586109,
  "paid_at": null,
  "cancelled_at": 1788586116,
  "expired_at": null,
  "sms_status": null,
  "email_status": null,
  "date": 1788586109,
  "partial_payment": true,
  "amount": 25000,
  "amount_paid": 0,
  "amount_due": 25000,
  "currency": "INR",
  "description": "feasibility probe cancel 2026-09-05",
  "notes": [],
  "short_url": "https://rzp.io/rzp/WtHmZor",
  "type": "invoice",
  "created_at": 1788586109
}`

// probeNotifySuccess is what both notify endpoints answered with.
const probeNotifySuccess = `{"success": true}`

// probeNotifyRefused is what POST /v1/invoices/{id}/notify_by/whatsapp
// answered with, status 400.
const probeNotifyRefused = `{
  "error": {
    "code": "BAD_REQUEST_ERROR",
    "description": "whatsapp is not a valid communication medium.",
    "source": "business",
    "step": null,
    "reason": "input_validation_failed",
    "metadata": {}
  }
}`

// probeOrderList is GET /v1/orders.
const probeOrderList = `{
  "entity": "collection",
  "count": 2,
  "items": [
    {
      "id": "order_TWu8G6mQV0Drc9",
      "entity": "order",
      "amount": 100000,
      "amount_paid": 100000,
      "amount_due": 0,
      "currency": "INR",
      "receipt": "rcpt_demo_1788294472",
      "offer_id": null,
      "status": "paid",
      "attempts": 2,
      "notes": {"purpose": "phase-1 demo"},
      "created_at": 1788294472
    },
    {
      "id": "order_TWu1ye8JOkvDeD",
      "entity": "order",
      "amount": 100000,
      "amount_paid": 100000,
      "amount_due": 0,
      "currency": "INR",
      "receipt": "rcpt_demo_1788294115",
      "offer_id": null,
      "status": "paid",
      "attempts": 2,
      "notes": {"purpose": "phase-1 demo"},
      "created_at": 1788294116
    }
  ]
}`

// probeEmptyCollection is GET /v1/invoices on an account with none.
const probeEmptyCollection = `{"entity": "collection", "count": 0, "items": []}`

// probePaymentLinkList is GET /v1/payment_links. The envelope is not the one
// orders and invoices use, and customer, notes, and reminders all arrive as
// empty arrays rather than as objects.
const probePaymentLinkList = `{
  "payment_links": [
    {
      "accept_partial": false,
      "amount": 100000,
      "amount_paid": 0,
      "cancelled_at": 0,
      "created_at": 1788294509,
      "currency": "INR",
      "customer": [],
      "description": "recovery for order_TWu8G6mQV0Drc9",
      "expire_by": 0,
      "expired_at": 0,
      "id": "plink_TWu8teKMrJUQ38",
      "notes": [],
      "notify": {"email": false, "sms": false, "whatsapp": false},
      "payments": null,
      "reference_id": "ref_demo_1788294508",
      "reminder_enable": false,
      "reminders": [],
      "short_url": "https://rzp.io/rzp/kAFprTA",
      "status": "created",
      "updated_at": 1788294509,
      "upi_link": false,
      "user_id": "",
      "whatsapp_link": false
    },
    {
      "accept_partial": false,
      "amount": 100000,
      "amount_paid": 0,
      "cancelled_at": 0,
      "created_at": 1788294150,
      "currency": "INR",
      "customer": [],
      "description": "recovery for order_TWu1ye8JOkvDeD",
      "expire_by": 0,
      "expired_at": 0,
      "id": "plink_TWu2ai8MJdv8wa",
      "notes": [],
      "notify": {"email": false, "sms": false, "whatsapp": false},
      "payments": null,
      "reference_id": "ref_demo_1788294150",
      "reminder_enable": false,
      "reminders": [],
      "short_url": "https://rzp.io/rzp/Stbd1bmR",
      "status": "created",
      "updated_at": 1788294150,
      "upi_link": false,
      "user_id": "",
      "whatsapp_link": false
    }
  ]
}`

// probePaymentLink is GET /v1/payment_links/{id} on a link created with
// contact details. Here customer is an object and reminders is an object with
// a status in it.
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

// probeMissingResource is what a call for an id that does not exist answers
// with. It is a 400, not a 404, which is why mapNotFound reads the
// description.
const probeMissingResource = `{
  "error": {
    "code": "BAD_REQUEST_ERROR",
    "description": "The id provided does not exist",
    "source": "business",
    "step": null,
    "reason": "input_validation_failed",
    "metadata": {}
  }
}`

// writeRawJSON answers with a body byte for byte, which writeJSON cannot do:
// it marshals, so a null or an unmodelled field would not survive the trip.
func writeRawJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func TestClientCreateCustomerPostsExpectedPayload(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotBody   map[string]any
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeRawJSON(w, http.StatusOK, probeCustomer)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL+"/v1", nil)

	customer, err := c.CreateCustomer(context.Background(), razorpay.CreateCustomerRequest{
		Name:    "Probe Customer",
		Email:   "probe-2026-09-05@example.com",
		Contact: "9000090000",
		Notes:   map[string]string{"purpose": "feasibility probe 2026-09-05"},
	})
	if err != nil {
		t.Fatalf("CreateCustomer: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/customers" {
		t.Errorf("path = %q, want /v1/customers", gotPath)
	}
	if got := gotBody["name"]; got != "Probe Customer" {
		t.Errorf("body name = %v, want Probe Customer", got)
	}
	// Razorpay spells this flag as a string. A JSON boolean is a different
	// field value to it, and the default when it is absent is to fail on a
	// duplicate, which is the opposite of what a re-runnable batch wants.
	if got := gotBody["fail_existing"]; got != "0" {
		t.Errorf("body fail_existing = %v, want the string \"0\"", got)
	}

	if customer.ID != "cust_TYEw5izKFR0iJr" {
		t.Errorf("decoded id = %q, want cust_TYEw5izKFR0iJr", customer.ID)
	}
	if customer.Email != "probe-2026-09-05@example.com" {
		t.Errorf("decoded email = %q, want the probe address", customer.Email)
	}
	if customer.CreatedAt != 1788586075 {
		t.Errorf("decoded created_at = %d, want 1788586075", customer.CreatedAt)
	}
	if customer.Notes["purpose"] != "feasibility probe 2026-09-05" {
		t.Errorf("decoded notes = %v, want the probe purpose", customer.Notes)
	}
	// gstin arrives as null on every test-mode customer.
	if customer.GSTIN != "" {
		t.Errorf("decoded gstin = %q, want empty from a null", customer.GSTIN)
	}
}

func TestClientCreateCustomerNeedsAName(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeRawJSON(w, http.StatusOK, probeCustomer)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL+"/v1", nil)

	_, err := c.CreateCustomer(context.Background(), razorpay.CreateCustomerRequest{Email: "nobody@example.com"})
	if !errors.Is(err, razorpay.ErrCustomerNameMissing) {
		t.Errorf("error = %v, want it to wrap ErrCustomerNameMissing", err)
	}
	if got := calls.Load(); got != 0 {
		t.Errorf("server saw %d request(s) for a request the client can refuse itself, want 0", got)
	}
}

func TestClientCreateInvoiceSendsRazorpaysOwnFlagSpellings(t *testing.T) {
	cases := []struct {
		name      string
		draft     bool
		notifySMS bool
		wantDraft string
		wantSMS   float64
	}{
		{name: "as a draft", draft: true, wantDraft: "1", wantSMS: 0},
		{name: "issued on creation", draft: false, notifySMS: true, wantDraft: "0", wantSMS: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var (
				gotMethod string
				gotPath   string
				gotBody   map[string]any
			)

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod = r.Method
				gotPath = r.URL.Path
				if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				writeRawJSON(w, http.StatusOK, probeInvoiceDraft)
			}))
			defer srv.Close()

			c := newTestClient(t, srv.URL+"/v1", nil)

			_, err := c.CreateInvoice(context.Background(), razorpay.CreateInvoiceRequest{
				CustomerID:  "cust_TYEw5izKFR0iJr",
				Draft:       tc.draft,
				NotifySMS:   tc.notifySMS,
				Description: "feasibility probe draft 2026-09-05",
				LineItems: []razorpay.CreateInvoiceLineItem{
					{Name: "Recovery probe item", AmountPaise: 50000, Currency: "INR"},
				},
			})
			if err != nil {
				t.Fatalf("CreateInvoice: %v", err)
			}

			if gotMethod != http.MethodPost {
				t.Errorf("method = %q, want POST", gotMethod)
			}
			if gotPath != "/v1/invoices" {
				t.Errorf("path = %q, want /v1/invoices", gotPath)
			}
			// The endpoint also creates links and e-invoices. Omitting type is
			// a 400, not a default.
			if got := gotBody["type"]; got != "invoice" {
				t.Errorf("body type = %v, want invoice", got)
			}
			// draft is a string to Razorpay. A JSON true is rejected.
			if got := gotBody["draft"]; got != tc.wantDraft {
				t.Errorf("body draft = %#v, want the string %q", got, tc.wantDraft)
			}
			// The notify flags are integers, not booleans.
			if got := gotBody["sms_notify"]; got != tc.wantSMS {
				t.Errorf("body sms_notify = %#v, want the number %v", got, tc.wantSMS)
			}
			if got := gotBody["email_notify"]; got != float64(0) {
				t.Errorf("body email_notify = %#v, want the number 0", got)
			}

			items, ok := gotBody["line_items"].([]any)
			if !ok || len(items) != 1 {
				t.Fatalf("body line_items = %v, want one item", gotBody["line_items"])
			}
			item, ok := items[0].(map[string]any)
			if !ok {
				t.Fatalf("line item = %v, want an object", items[0])
			}
			if item["amount"] != float64(50000) {
				t.Errorf("line item amount = %v, want 50000 (paise, not rupees)", item["amount"])
			}
			// Quantity is filled in rather than sent as a zero, which
			// Razorpay would price at nothing.
			if item["quantity"] != float64(1) {
				t.Errorf("line item quantity = %v, want a defaulted 1", item["quantity"])
			}
		})
	}
}

func TestClientCreateInvoiceDecodesADraftWithNullsInIt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeRawJSON(w, http.StatusOK, probeInvoiceDraft)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL+"/v1", nil)

	invoice, err := c.CreateInvoice(context.Background(), razorpay.CreateInvoiceRequest{
		CustomerID: "cust_TYEw5izKFR0iJr",
		Draft:      true,
		LineItems: []razorpay.CreateInvoiceLineItem{
			{Name: "Recovery probe item", AmountPaise: 50000, Currency: "INR"},
		},
	})
	if err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}

	if invoice.Status != razorpay.InvoiceStatusDraft {
		t.Errorf("status = %q, want %q", invoice.Status, razorpay.InvoiceStatusDraft)
	}
	// A draft has nothing to pay against and nothing to send. Both fields are
	// null on the wire, and null into a string is the empty string rather than
	// a decode failure.
	if invoice.OrderID != "" {
		t.Errorf("draft order id = %q, want empty: a draft has no order behind it", invoice.OrderID)
	}
	if invoice.ShortURL != "" {
		t.Errorf("draft short url = %q, want empty: a draft has nothing to open", invoice.ShortURL)
	}
	if invoice.IssuedAt != 0 {
		t.Errorf("draft issued_at = %d, want 0", invoice.IssuedAt)
	}
	// amount_paid and amount_due are null on a draft and numbers once issued.
	// Null into an int64 has to leave a zero rather than fail the invoice.
	if invoice.AmountPaid != 0 || invoice.AmountDue != 0 {
		t.Errorf("draft amount_paid/amount_due = %d/%d, want 0/0 from nulls", invoice.AmountPaid, invoice.AmountDue)
	}
	if invoice.AmountPaise != 50000 {
		t.Errorf("amount = %d paise, want 50000", invoice.AmountPaise)
	}
	// notes arrives as an empty array, which is the shape Notes exists for.
	if len(invoice.Notes) != 0 {
		t.Errorf("notes = %v, want empty from an empty array", invoice.Notes)
	}
	if len(invoice.LineItems) != 1 || invoice.LineItems[0].ID != "li_TYEwCBYqSEp1Lt" {
		t.Errorf("line items = %v, want the one probe line item", invoice.LineItems)
	}
	if invoice.CustomerDetails.Email != "probe-2026-09-05@example.com" {
		t.Errorf("customer_details email = %q, want the probe address", invoice.CustomerDetails.Email)
	}
}

func TestClientCreateInvoiceRejectsAnIncompleteRequest(t *testing.T) {
	oneItem := []razorpay.CreateInvoiceLineItem{{Name: "item", AmountPaise: 50000}}

	cases := []struct {
		name string
		req  razorpay.CreateInvoiceRequest
		want error
	}{
		{
			name: "no customer at all",
			req:  razorpay.CreateInvoiceRequest{LineItems: oneItem},
			want: razorpay.ErrInvoiceCustomerMissing,
		},
		{
			name: "no line items",
			req:  razorpay.CreateInvoiceRequest{CustomerID: "cust_TYEw5izKFR0iJr"},
			want: razorpay.ErrInvoiceLineItemsMissing,
		},
		{
			name: "a line item worth nothing",
			req: razorpay.CreateInvoiceRequest{
				CustomerID: "cust_TYEw5izKFR0iJr",
				LineItems:  []razorpay.CreateInvoiceLineItem{{Name: "free", AmountPaise: 0}},
			},
			want: razorpay.ErrAmountNotPositive,
		},
	}

	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeRawJSON(w, http.StatusOK, probeInvoiceDraft)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL+"/v1", nil)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.CreateInvoice(context.Background(), tc.req)
			if !errors.Is(err, tc.want) {
				t.Errorf("error = %v, want it to wrap %v", err, tc.want)
			}
		})
	}

	// None of the three is worth a call: Razorpay would refuse each one, and a
	// refused call still costs a rate-limit slot.
	if got := calls.Load(); got != 0 {
		t.Errorf("server saw %d request(s), want 0", got)
	}
}

func TestClientCreateInvoiceSendsAnInlineCustomerOnlyWithoutAnID(t *testing.T) {
	t.Run("an inline customer with no id", func(t *testing.T) {
		var gotBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			writeRawJSON(w, http.StatusOK, probeInvoiceDraft)
		}))
		defer srv.Close()

		c := newTestClient(t, srv.URL+"/v1", nil)
		if _, err := c.CreateInvoice(context.Background(), razorpay.CreateInvoiceRequest{
			Customer:  &razorpay.InlineCustomer{Name: "Probe Customer", Email: "probe-2026-09-05@example.com"},
			LineItems: []razorpay.CreateInvoiceLineItem{{Name: "item", AmountPaise: 50000}},
		}); err != nil {
			t.Fatalf("CreateInvoice: %v", err)
		}

		customer, ok := gotBody["customer"].(map[string]any)
		if !ok {
			t.Fatalf("body customer = %v, want an object", gotBody["customer"])
		}
		if customer["email"] != "probe-2026-09-05@example.com" {
			t.Errorf("inline customer email = %v, want the probe address", customer["email"])
		}
		if _, present := gotBody["customer_id"]; present {
			t.Error("body carries a customer_id as well as an inline customer, which Razorpay refuses")
		}
	})

	t.Run("an id wins over an inline customer", func(t *testing.T) {
		var gotBody map[string]any
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&gotBody)
			writeRawJSON(w, http.StatusOK, probeInvoiceDraft)
		}))
		defer srv.Close()

		c := newTestClient(t, srv.URL+"/v1", nil)
		if _, err := c.CreateInvoice(context.Background(), razorpay.CreateInvoiceRequest{
			CustomerID: "cust_TYEw5izKFR0iJr",
			Customer:   &razorpay.InlineCustomer{Name: "Probe Customer"},
			LineItems:  []razorpay.CreateInvoiceLineItem{{Name: "item", AmountPaise: 50000}},
		}); err != nil {
			t.Fatalf("CreateInvoice: %v", err)
		}

		if got := gotBody["customer_id"]; got != "cust_TYEw5izKFR0iJr" {
			t.Errorf("body customer_id = %v, want cust_TYEw5izKFR0iJr", got)
		}
		if _, present := gotBody["customer"]; present {
			t.Error("body carries both a customer_id and an inline customer, which Razorpay refuses")
		}
	})
}

func TestClientIssueInvoiceMintsAnOrderAndAShortURL(t *testing.T) {
	var gotMethod, gotPath string

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/invoices", func(w http.ResponseWriter, _ *http.Request) {
		writeRawJSON(w, http.StatusOK, probeInvoiceDraft)
	})
	mux.HandleFunc("POST /v1/invoices/{id}/issue", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		writeRawJSON(w, http.StatusOK, probeInvoiceIssued)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := newTestClient(t, srv.URL+"/v1", nil)
	ctx := context.Background()

	draft, err := c.CreateInvoice(ctx, razorpay.CreateInvoiceRequest{
		CustomerID: "cust_TYEw5izKFR0iJr",
		Draft:      true,
		LineItems:  []razorpay.CreateInvoiceLineItem{{Name: "Recovery probe item", AmountPaise: 50000}},
	})
	if err != nil {
		t.Fatalf("CreateInvoice: %v", err)
	}
	if draft.OrderID != "" || draft.ShortURL != "" {
		t.Fatalf("the draft already has order %q and url %q, so the assertion below would prove nothing",
			draft.OrderID, draft.ShortURL)
	}

	issued, err := c.IssueInvoice(ctx, draft.ID)
	if err != nil {
		t.Fatalf("IssueInvoice: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/invoices/inv_TYEwC7POHGFZNa/issue" {
		t.Errorf("path = %q, want /v1/invoices/inv_TYEwC7POHGFZNa/issue", gotPath)
	}

	if issued.Status != razorpay.InvoiceStatusIssued {
		t.Errorf("status = %q, want %q", issued.Status, razorpay.InvoiceStatusIssued)
	}
	// This is the whole reason the draft path exists: issuing is what creates
	// the order the invoice is paid against.
	if issued.OrderID != "order_TYEwKA0KjwEW3t" {
		t.Errorf("issued order id = %q, want order_TYEwKA0KjwEW3t minted by the issue call", issued.OrderID)
	}
	if issued.ShortURL != "https://rzp.io/rzp/4U2HXcQ" {
		t.Errorf("issued short url = %q, want the one the issue call minted", issued.ShortURL)
	}
	if issued.IssuedAt != 1788586088 {
		t.Errorf("issued_at = %d, want 1788586088", issued.IssuedAt)
	}
	if issued.AmountDue != 50000 {
		t.Errorf("amount_due = %d, want 50000 once the invoice carries an order", issued.AmountDue)
	}
	// Issuing is not sending. Both status fields are still null here.
	if issued.EmailStatus != "" {
		t.Errorf("email_status = %q on a freshly issued invoice, want empty", issued.EmailStatus)
	}
}

func TestClientNotifyInvoice(t *testing.T) {
	t.Run("reports the success flag Razorpay returns", func(t *testing.T) {
		var gotMethod, gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotMethod = r.Method
			gotPath = r.URL.Path
			writeRawJSON(w, http.StatusOK, probeNotifySuccess)
		}))
		defer srv.Close()

		c := newTestClient(t, srv.URL+"/v1", nil)

		receipt, err := c.NotifyInvoice(context.Background(), "inv_TYEwC7POHGFZNa", razorpay.MediumEmail)
		if err != nil {
			t.Fatalf("NotifyInvoice: %v", err)
		}
		if gotMethod != http.MethodPost {
			t.Errorf("method = %q, want POST", gotMethod)
		}
		if gotPath != "/v1/invoices/inv_TYEwC7POHGFZNa/notify_by/email" {
			t.Errorf("path = %q, want /v1/invoices/inv_TYEwC7POHGFZNa/notify_by/email", gotPath)
		}
		if !receipt.Accepted {
			t.Error("receipt reports the call was not accepted, but Razorpay answered success true")
		}
		// NotifyReceipt is shared with the payment link resend, so its LinkID
		// carries the invoice id here.
		if receipt.LinkID != "inv_TYEwC7POHGFZNa" {
			t.Errorf("receipt id = %q, want the invoice id", receipt.LinkID)
		}
		if receipt.Medium != razorpay.MediumEmail {
			t.Errorf("receipt medium = %q, want email", receipt.Medium)
		}
	})

	t.Run("a success false is not an acceptance", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeRawJSON(w, http.StatusOK, `{"success": false}`)
		}))
		defer srv.Close()

		c := newTestClient(t, srv.URL+"/v1", nil)

		receipt, err := c.NotifyInvoice(context.Background(), "inv_TYEwC7POHGFZNa", razorpay.MediumSMS)
		if err != nil {
			t.Fatalf("NotifyInvoice: %v", err)
		}
		if receipt.Accepted {
			t.Error("a 200 carrying success false was counted as an acceptance")
		}
	})

	t.Run("an empty 2xx body still counts as accepted", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		c := newTestClient(t, srv.URL+"/v1", nil)

		receipt, err := c.NotifyInvoice(context.Background(), "inv_TYEwC7POHGFZNa", razorpay.MediumEmail)
		if err != nil {
			t.Fatalf("NotifyInvoice: %v", err)
		}
		if !receipt.Accepted {
			t.Error("an empty 200 was not counted as an acceptance")
		}
	})

	t.Run("the client refuses a medium the API has no endpoint for", func(t *testing.T) {
		var calls atomic.Int64
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			calls.Add(1)
			writeRawJSON(w, http.StatusOK, probeNotifySuccess)
		}))
		defer srv.Close()

		c := newTestClient(t, srv.URL+"/v1", nil)

		_, err := c.NotifyInvoice(context.Background(), "inv_TYEwC7POHGFZNa", "whatsapp")
		if !errors.Is(err, razorpay.ErrUnsupportedMedium) {
			t.Errorf("error = %v, want it to wrap ErrUnsupportedMedium", err)
		}
		if got := calls.Load(); got != 0 {
			t.Errorf("server saw %d request(s) for a medium the client can refuse itself, want 0", got)
		}
	})

	t.Run("a refusal keeps its description and reason", func(t *testing.T) {
		// This body is what test mode answered notify_by/whatsapp with on
		// 2026-09-05. It is served for an email notify here because the
		// client's own guard means no caller can reach it with whatsapp; what
		// is being pinned is the shape of a refusal on this endpoint, and that
		// the two fields the classifier reads survive the redaction path.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeRawJSON(w, http.StatusBadRequest, probeNotifyRefused)
		}))
		defer srv.Close()

		c := newTestClient(t, srv.URL+"/v1", nil)

		_, err := c.NotifyInvoice(context.Background(), "inv_TYEwC7POHGFZNa", razorpay.MediumEmail)
		if err == nil {
			t.Fatal("a 400 returned no error")
		}
		// The description names a medium rather than a missing resource, so
		// this must not be mistaken for an invoice that is not there.
		if errors.Is(err, razorpay.ErrInvoiceNotFound) {
			t.Errorf("a rejected medium was reported as a missing invoice: %v", err)
		}

		var apiErr *razorpay.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("error = %v, want an *razorpay.APIError", err)
		}
		if apiErr.StatusCode != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", apiErr.StatusCode)
		}
		if apiErr.Description != "whatsapp is not a valid communication medium." {
			t.Errorf("description = %q, want the one Razorpay sent", apiErr.Description)
		}
		if apiErr.Reason != "input_validation_failed" {
			t.Errorf("reason = %q, want input_validation_failed", apiErr.Reason)
		}
	})
}

func TestClientFetchInvoiceSeesTheEmailStatusTransition(t *testing.T) {
	var reads atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if reads.Add(1) == 1 {
			writeRawJSON(w, http.StatusOK, probeInvoiceIssued)
			return
		}
		writeRawJSON(w, http.StatusOK, probeInvoiceNotified)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL+"/v1", nil)
	ctx := context.Background()

	before, err := c.FetchInvoice(ctx, "inv_TYEwC7POHGFZNa")
	if err != nil {
		t.Fatalf("FetchInvoice before: %v", err)
	}
	if before.EmailStatus != "" {
		t.Errorf("email_status before the notify = %q, want empty from a null", before.EmailStatus)
	}

	after, err := c.FetchInvoice(ctx, "inv_TYEwC7POHGFZNa")
	if err != nil {
		t.Fatalf("FetchInvoice after: %v", err)
	}
	// The invoice is the only place this is readable. The notify call reports
	// that it was accepted and nothing more.
	if after.EmailStatus != razorpay.InvoiceNotifyStatusSent {
		t.Errorf("email_status after the notify = %q, want %q", after.EmailStatus, razorpay.InvoiceNotifyStatusSent)
	}
	if after.SMSStatus != "" {
		t.Errorf("sms_status = %q after an email notify, want it untouched", after.SMSStatus)
	}
	if after.Status != razorpay.InvoiceStatusIssued {
		t.Errorf("status = %q, want it still issued: a sent notification is not a payment", after.Status)
	}
}

func TestClientCancelInvoiceFlipsTheStatus(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		writeRawJSON(w, http.StatusOK, probeInvoiceCancelled)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL+"/v1", nil)

	invoice, err := c.CancelInvoice(context.Background(), "inv_TYEwgLnV0S7WQ0")
	if err != nil {
		t.Fatalf("CancelInvoice: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/invoices/inv_TYEwgLnV0S7WQ0/cancel" {
		t.Errorf("path = %q, want /v1/invoices/inv_TYEwgLnV0S7WQ0/cancel", gotPath)
	}
	if invoice.Status != razorpay.InvoiceStatusCancelled {
		t.Errorf("status = %q, want %q", invoice.Status, razorpay.InvoiceStatusCancelled)
	}
	if invoice.CancelledAt != 1788586116 {
		t.Errorf("cancelled_at = %d, want 1788586116", invoice.CancelledAt)
	}
	// The order and the short URL stay on the cancelled invoice, so a run that
	// recorded either of them can still tie them back to what happened.
	if invoice.OrderID != "order_TYEwgWlGLJ5l4m" {
		t.Errorf("order id = %q, want it kept on the cancelled invoice", invoice.OrderID)
	}
	if invoice.ShortURL == "" {
		t.Error("short url is empty on a cancelled invoice, want it kept")
	}
	if invoice.IssuedAt != 1788586109 {
		t.Errorf("issued_at = %d, want it kept at 1788586109", invoice.IssuedAt)
	}
}

func TestClientListInvoices(t *testing.T) {
	t.Run("an empty collection is an empty slice", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeRawJSON(w, http.StatusOK, probeEmptyCollection)
		}))
		defer srv.Close()

		c := newTestClient(t, srv.URL+"/v1", nil)

		invoices, err := c.ListInvoices(context.Background(), razorpay.ListOptions{})
		if err != nil {
			t.Fatalf("ListInvoices: %v", err)
		}
		if invoices == nil {
			t.Fatal("an empty collection decoded to a nil slice, which reads as an error rather than as none")
		}
		if len(invoices) != 0 {
			t.Errorf("got %d invoice(s), want 0", len(invoices))
		}
	})

	t.Run("a populated collection keeps the item shape", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeRawJSON(w, http.StatusOK, `{"entity":"collection","count":1,"items":[`+probeInvoiceIssued+`]}`)
		}))
		defer srv.Close()

		c := newTestClient(t, srv.URL+"/v1", nil)

		invoices, err := c.ListInvoices(context.Background(), razorpay.ListOptions{})
		if err != nil {
			t.Fatalf("ListInvoices: %v", err)
		}
		if len(invoices) != 1 {
			t.Fatalf("got %d invoice(s), want 1", len(invoices))
		}
		if invoices[0].ID != "inv_TYEwC7POHGFZNa" || invoices[0].OrderID != "order_TYEwKA0KjwEW3t" {
			t.Errorf("listed invoice = %+v, want the issued probe invoice", invoices[0])
		}
	})

	t.Run("the paging options reach the query string", func(t *testing.T) {
		var gotQuery string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			writeRawJSON(w, http.StatusOK, probeEmptyCollection)
		}))
		defer srv.Close()

		c := newTestClient(t, srv.URL+"/v1", nil)

		if _, err := c.ListInvoices(context.Background(), razorpay.ListOptions{
			Count: 25,
			Skip:  50,
			From:  1788586000,
			To:    1788586999,
		}); err != nil {
			t.Fatalf("ListInvoices: %v", err)
		}
		want := "count=25&from=1788586000&skip=50&to=1788586999"
		if gotQuery != want {
			t.Errorf("query = %q, want %q", gotQuery, want)
		}
	})

	t.Run("a zero value asks for nothing in particular", func(t *testing.T) {
		var gotURL string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotURL = r.URL.String()
			writeRawJSON(w, http.StatusOK, probeEmptyCollection)
		}))
		defer srv.Close()

		c := newTestClient(t, srv.URL+"/v1", nil)

		if _, err := c.ListInvoices(context.Background(), razorpay.ListOptions{}); err != nil {
			t.Fatalf("ListInvoices: %v", err)
		}
		// A trailing question mark is not the same URL, and a gateway is
		// entitled to treat it differently.
		if gotURL != "/v1/invoices" {
			t.Errorf("url = %q, want /v1/invoices with no query at all", gotURL)
		}
	})
}

func TestClientListOrdersDecodesTheCollectionEnvelope(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		writeRawJSON(w, http.StatusOK, probeOrderList)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL+"/v1", nil)

	orders, err := c.ListOrders(context.Background(), razorpay.ListOptions{Count: 2})
	if err != nil {
		t.Fatalf("ListOrders: %v", err)
	}
	if gotPath != "/v1/orders" {
		t.Errorf("path = %q, want /v1/orders", gotPath)
	}
	if len(orders) != 2 {
		t.Fatalf("got %d order(s), want 2", len(orders))
	}
	if orders[0].ID != "order_TWu8G6mQV0Drc9" {
		t.Errorf("first order id = %q, want order_TWu8G6mQV0Drc9", orders[0].ID)
	}
	if orders[0].Status != razorpay.OrderStatusPaid {
		t.Errorf("first order status = %q, want %q", orders[0].Status, razorpay.OrderStatusPaid)
	}
	if orders[0].AmountPaid != 100000 || orders[0].AmountDue != 0 {
		t.Errorf("first order paid/due = %d/%d, want 100000/0", orders[0].AmountPaid, orders[0].AmountDue)
	}
	if orders[0].Attempts != 2 {
		t.Errorf("first order attempts = %d, want 2", orders[0].Attempts)
	}
	if orders[0].Notes["purpose"] != "phase-1 demo" {
		t.Errorf("first order notes = %v, want the demo purpose", orders[0].Notes)
	}
}

func TestClientListPaymentLinksReadsItsOwnEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeRawJSON(w, http.StatusOK, probePaymentLinkList)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL+"/v1", nil)

	links, err := c.ListPaymentLinks(context.Background(), razorpay.ListOptions{})
	if err != nil {
		t.Fatalf("ListPaymentLinks: %v", err)
	}
	// The envelope key is payment_links, not items, and there is no count in
	// it. Reading it as the collection every other list uses returns nothing
	// and no error, which is the worst way to be wrong.
	if len(links) != 2 {
		t.Fatalf("got %d link(s), want 2", len(links))
	}
	if links[0].ID != "plink_TWu8teKMrJUQ38" {
		t.Errorf("first link id = %q, want plink_TWu8teKMrJUQ38", links[0].ID)
	}
	if links[0].Status != razorpay.PaymentLinkStatusCreated {
		t.Errorf("first link status = %q, want %q", links[0].Status, razorpay.PaymentLinkStatusCreated)
	}
	if links[0].AmountPaise != 100000 || links[0].AmountPaid != 0 {
		t.Errorf("first link amount/paid = %d/%d, want 100000/0", links[0].AmountPaise, links[0].AmountPaid)
	}
	if links[0].ShortURL != "https://rzp.io/rzp/kAFprTA" {
		t.Errorf("first link short url = %q, want the probe url", links[0].ShortURL)
	}
	// customer, notes, and reminders all arrive as empty arrays on a list.
	// Each one would fail a plain struct or map decode and take the whole page
	// with it.
	if links[0].Customer != (razorpay.PaymentLinkCustomer{}) {
		t.Errorf("first link customer = %+v, want empty from an empty array", links[0].Customer)
	}
	if links[0].Reminders != (razorpay.PaymentLinkReminders{}) {
		t.Errorf("first link reminders = %+v, want empty from an empty array", links[0].Reminders)
	}
	if len(links[0].Notes) != 0 {
		t.Errorf("first link notes = %v, want empty from an empty array", links[0].Notes)
	}
}

func TestClientFetchPaymentLinkDecodesTheObjectShapes(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		writeRawJSON(w, http.StatusOK, probePaymentLink)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL+"/v1", nil)

	link, err := c.FetchPaymentLink(context.Background(), "plink_TYEx2CoiwQvYow")
	if err != nil {
		t.Fatalf("FetchPaymentLink: %v", err)
	}
	if gotPath != "/v1/payment_links/plink_TYEx2CoiwQvYow" {
		t.Errorf("path = %q, want /v1/payment_links/plink_TYEx2CoiwQvYow", gotPath)
	}
	// AmountPaid is the field this type exists for: it is how a partly paid
	// link is told from an unpaid one, and PaymentLink does not carry it.
	if link.AmountPaise != 30000 || link.AmountPaid != 0 {
		t.Errorf("amount/paid = %d/%d, want 30000/0", link.AmountPaise, link.AmountPaid)
	}
	if link.Status != razorpay.PaymentLinkStatusCreated {
		t.Errorf("status = %q, want %q", link.Status, razorpay.PaymentLinkStatusCreated)
	}
	// Here customer is an object and reminders is an object with a status,
	// where the list sent an empty array for both.
	if link.Customer.Email != "probe-2026-09-05@example.com" {
		t.Errorf("customer email = %q, want the probe address", link.Customer.Email)
	}
	if link.Customer.Contact != "+919000090000" {
		t.Errorf("customer contact = %q, want the probe number", link.Customer.Contact)
	}
	// Test mode enabled reminders and then failed to send them. A run that
	// reads reminder_enable alone would report a reminder that never went out.
	if !link.ReminderEnable {
		t.Error("reminder_enable is false, want true")
	}
	if link.Reminders.Status != "failed" {
		t.Errorf("reminders status = %q, want failed", link.Reminders.Status)
	}
	if link.Notify.Email || link.Notify.SMS || link.Notify.WhatsApp {
		t.Errorf("notify = %+v, want all three false", link.Notify)
	}
	if len(link.Notes) != 0 {
		t.Errorf("notes = %v, want empty from a null", link.Notes)
	}
}

func TestClientPaymentLinkCustomerRejectsANonEmptyArray(t *testing.T) {
	// An empty array is Razorpay saying there is no customer. A populated one
	// would be a shape this package has never seen, and quietly dropping it
	// would lose the contact the link was sent to.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeRawJSON(w, http.StatusOK, `{"id":"plink_arrayshaped","customer":[{"email":"someone@example.com"}]}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL+"/v1", nil)

	_, err := c.FetchPaymentLink(context.Background(), "plink_arrayshaped")
	if err == nil {
		t.Fatal("a customer array with something in it decoded without complaint")
	}
	if !strings.Contains(err.Error(), "not an object") {
		t.Errorf("error = %v, want it to say the customer was not an object", err)
	}
}

func TestClientInvoiceCallsMapAMissingResourceToASentinel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Razorpay answers a missing resource with a 400 and puts the only
		// distinguishing information in the description.
		writeRawJSON(w, http.StatusBadRequest, probeMissingResource)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL+"/v1", nil)
	ctx := context.Background()

	cases := []struct {
		name string
		call func() error
		want error
	}{
		{
			name: "FetchInvoice",
			call: func() error { _, err := c.FetchInvoice(ctx, "inv_missing0000001"); return err },
			want: razorpay.ErrInvoiceNotFound,
		},
		{
			name: "IssueInvoice",
			call: func() error { _, err := c.IssueInvoice(ctx, "inv_missing0000001"); return err },
			want: razorpay.ErrInvoiceNotFound,
		},
		{
			name: "CancelInvoice",
			call: func() error { _, err := c.CancelInvoice(ctx, "inv_missing0000001"); return err },
			want: razorpay.ErrInvoiceNotFound,
		},
		{
			name: "NotifyInvoice",
			call: func() error {
				_, err := c.NotifyInvoice(ctx, "inv_missing0000001", razorpay.MediumEmail)
				return err
			},
			want: razorpay.ErrInvoiceNotFound,
		},
		{
			name: "FetchPaymentLink",
			call: func() error { _, err := c.FetchPaymentLink(ctx, "plink_missing00001"); return err },
			want: razorpay.ErrPaymentLinkNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if !errors.Is(err, tc.want) {
				t.Errorf("error = %v, want it to wrap %v", err, tc.want)
			}
			var apiErr *razorpay.APIError
			if !errors.As(err, &apiErr) {
				t.Errorf("error = %v, want the underlying *razorpay.APIError to survive the wrap", err)
			}
		})
	}
}

func TestClientInvoiceCallsRefuseAnEmptyID(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeRawJSON(w, http.StatusOK, probeInvoiceIssued)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL+"/v1", nil)
	ctx := context.Background()

	cases := []struct {
		name string
		call func() error
		want error
	}{
		{name: "FetchInvoice", call: func() error { _, err := c.FetchInvoice(ctx, ""); return err }, want: razorpay.ErrInvoiceNotFound},
		{name: "IssueInvoice", call: func() error { _, err := c.IssueInvoice(ctx, ""); return err }, want: razorpay.ErrInvoiceNotFound},
		{name: "CancelInvoice", call: func() error { _, err := c.CancelInvoice(ctx, ""); return err }, want: razorpay.ErrInvoiceNotFound},
		{
			name: "NotifyInvoice",
			call: func() error { _, err := c.NotifyInvoice(ctx, "", razorpay.MediumEmail); return err },
			want: razorpay.ErrInvoiceNotFound,
		},
		{
			name: "FetchPaymentLink",
			call: func() error { _, err := c.FetchPaymentLink(ctx, ""); return err },
			want: razorpay.ErrPaymentLinkNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, tc.want) {
				t.Errorf("error = %v, want it to wrap %v", err, tc.want)
			}
		})
	}

	// An empty id would build a path ending in a slash, which is a different
	// endpoint rather than a missing record.
	if got := calls.Load(); got != 0 {
		t.Errorf("server saw %d request(s) for an empty id, want 0", got)
	}
}

func TestClientInvoiceCallsRedactCredentialsAndCapture(t *testing.T) {
	// The invoice calls go through the same do path as everything else, so
	// this is a guard against one of them growing its own transport later.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeRawJSON(w, http.StatusInternalServerError,
			`{"error":{"description":"upstream failed for key `+testKeyID+` with secret `+testKeySecret+`"}}`)
	}))
	defer srv.Close()

	c := newTestClient(t, srv.URL+"/v1", nil)

	_, err := c.ListInvoices(context.Background(), razorpay.ListOptions{})
	if err == nil {
		t.Fatal("a 500 returned no error")
	}
	msg := err.Error()
	for name, secret := range map[string]string{
		"key id":     testKeyID,
		"key secret": testKeySecret,
	} {
		if strings.Contains(msg, secret) {
			t.Errorf("the %s reached the error message: %q", name, msg)
		}
	}
	if !strings.Contains(msg, razorpay.Redacted) {
		t.Errorf("error message %q carries no redaction marker, so nothing was scrubbed", msg)
	}
}
