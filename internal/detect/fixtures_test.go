package detect

// The response bodies below are wire shaped, not hand shaped.
//
// The envelopes are copied from internal/razorpay/invoices_test.go, whose
// bodies are transcriptions of the Razorpay test mode walk of 2026-09-05:
// orders and invoices come back as {"entity":"collection","count":n,"items":
// [...]}, notes arrive as an empty JSON array when there are none, and every
// field an issued invoice leaves unset arrives as null rather than as an
// omitted key.
//
// The record values are the ones the same walk recorded for the ids named in
// internal/riskitem/testdata, which is where each golden sighting in this
// package's tests is loaded from. Where a fixture describes a resource at a
// moment the walk did not capture whole, the comment above it says which
// moment and why. Nothing here is a field this project has never seen
// Razorpay send.

// probeOrderListMixed is GET /v1/orders over an account holding one order per
// case the two order detectors have to separate.
//
// order_TYEyaa7bjDHn7P and order_TYEwKA0KjwEW3t are the unpaid orders behind
// unpaid_order.json and overdue_invoice.json: the second is the order the
// invoice minted when it was issued, which is why its created_at is the
// invoice's issued_at. order_TWu8G6mQV0Drc9 is copied verbatim from
// probeOrderList in internal/razorpay/invoices_test.go, paid with two attempts.
// order_TYEzzz00attempted00 is the one record here with no probe behind it: no
// order at status attempted was observed at rest on 2026-09-05, and it is in
// the fixture precisely so that a detector reading the status literal rather
// than the attempt counter fails a test.
const probeOrderListMixed = `{
  "entity": "collection",
  "count": 4,
  "items": [
    {
      "id": "order_TYEyaa7bjDHn7P",
      "entity": "order",
      "amount": 50000,
      "amount_paid": 0,
      "amount_due": 50000,
      "currency": "INR",
      "receipt": null,
      "offer_id": null,
      "status": "created",
      "attempts": 0,
      "notes": [],
      "created_at": 1788586217
    },
    {
      "id": "order_TYEwKA0KjwEW3t",
      "entity": "order",
      "amount": 50000,
      "amount_paid": 0,
      "amount_due": 50000,
      "currency": "INR",
      "receipt": null,
      "offer_id": null,
      "status": "created",
      "attempts": 0,
      "notes": [],
      "created_at": 1788586088
    },
    {
      "id": "order_TYEzzz00attempted00",
      "entity": "order",
      "amount": 70000,
      "amount_paid": 0,
      "amount_due": 70000,
      "currency": "INR",
      "receipt": null,
      "offer_id": null,
      "status": "attempted",
      "attempts": 1,
      "notes": [],
      "created_at": 1788586300
    },
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
    }
  ]
}`

// probeOrderListFailed is GET /v1/orders holding order_TWu8G6mQV0Drc9 as it
// stood between its failed payment and the capture that paid it: created, one
// attempt, nothing paid, the full amount due.
//
// The walk captured that order after it was paid, and the golden sighting in
// internal/riskitem/testdata/failed_payment.json describes the debt as it
// stood at the failed payment for the reason its own header gives. This
// fixture is the order half of that same moment, so that the detector under
// test is reading the state the golden was written from.
const probeOrderListFailed = `{
  "entity": "collection",
  "count": 1,
  "items": [
    {
      "id": "order_TWu8G6mQV0Drc9",
      "entity": "order",
      "amount": 100000,
      "amount_paid": 0,
      "amount_due": 100000,
      "currency": "INR",
      "receipt": "rcpt_demo_1788294472",
      "offer_id": null,
      "status": "created",
      "attempts": 1,
      "notes": {"purpose": "phase-1 demo"},
      "created_at": 1788294472
    }
  ]
}`

// probeOrderPaymentsFailed is GET /v1/orders/order_TWu8G6mQV0Drc9/payments
// with the one failed attempt on it. Every error field is the value the golden
// sighting carries, and they are the five the 2026-08-31 card walk found on
// every declined test-mode payment.
//
// The email and the contact are the two the same walk captured, and they are
// verbatim from testdata/recorded/list_payments_after_failure.json. They were
// left out of this fixture while razorpay.Payment had no field to decode them
// into, which is why the golden sighting carried a contact this detector could
// not produce. See probeOrderPaymentsFailedNoContact for the other half of
// that question.
const probeOrderPaymentsFailed = `{
  "entity": "collection",
  "count": 1,
  "items": [
    {
      "id": "pay_TWu8GufuR8yXmA",
      "entity": "payment",
      "amount": 100000,
      "currency": "INR",
      "status": "failed",
      "order_id": "order_TWu8G6mQV0Drc9",
      "method": "card",
      "captured": false,
      "email": "probe@example.com",
      "contact": "+919999999999",
      "error_code": "BAD_REQUEST_ERROR",
      "error_description": "Payment failed",
      "error_source": "gateway",
      "error_step": "payment_authorization",
      "error_reason": "payment_failed",
      "created_at": 1788294474
    }
  ]
}`

// probeOrderPaymentsFailedNoContact is the same payment with neither contact
// field on it, which is what a payment made through a flow that collected
// neither looks like.
//
// It exists so the notes path can still be tested for what it is: the only
// contact an order-sourced sighting has when the payment itself reported
// none. Razorpay sends the two fields as null rather than omitting them on
// such a payment, and null decodes to the empty string.
const probeOrderPaymentsFailedNoContact = `{
  "entity": "collection",
  "count": 1,
  "items": [
    {
      "id": "pay_TWu8GufuR8yXmA",
      "entity": "payment",
      "amount": 100000,
      "currency": "INR",
      "status": "failed",
      "order_id": "order_TWu8G6mQV0Drc9",
      "method": "card",
      "captured": false,
      "email": null,
      "contact": null,
      "error_code": "BAD_REQUEST_ERROR",
      "error_description": "Payment failed",
      "error_source": "gateway",
      "error_step": "payment_authorization",
      "error_reason": "payment_failed",
      "created_at": 1788294474
    }
  ]
}`

// probeInvoiceListMixed is GET /v1/invoices holding one invoice per status the
// detector has to separate.
//
// inv_TYEwC7POHGFZNa is probeInvoiceNotified from
// internal/razorpay/invoices_test.go, verbatim: issued, unpaid, email_status
// sent, carrying the order it minted. inv_TYEwgLnV0S7WQ0 is
// probeInvoiceCancelled from the same file, verbatim. The draft, the paid, and
// the partially paid records carry the fields the walk saw on a draft, on
// issue, and on a partial-payment invoice, under the ids the account held.
const probeInvoiceListMixed = `{
  "entity": "collection",
  "count": 5,
  "items": [
    {
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
    },
    {
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
    },
    {
      "id": "inv_TYEwB1draft0000",
      "entity": "invoice",
      "customer_id": "cust_TYEw5izKFR0iJr",
      "customer_details": {
        "id": "cust_TYEw5izKFR0iJr",
        "name": "Probe Customer",
        "email": "probe-2026-09-05@example.com",
        "contact": "9000090000",
        "gstin": null
      },
      "order_id": null,
      "payment_id": null,
      "status": "draft",
      "issued_at": null,
      "paid_at": null,
      "cancelled_at": null,
      "expired_at": null,
      "sms_status": null,
      "email_status": null,
      "date": 1788586081,
      "partial_payment": false,
      "amount": 50000,
      "amount_paid": null,
      "amount_due": null,
      "currency": "INR",
      "description": "feasibility probe draft 2026-09-05",
      "notes": [],
      "short_url": null,
      "type": "invoice",
      "created_at": 1788586070
    },
    {
      "id": "inv_TYEwD2paid0000",
      "entity": "invoice",
      "customer_id": "cust_TYEw5izKFR0iJr",
      "customer_details": {
        "id": "cust_TYEw5izKFR0iJr",
        "name": "Probe Customer",
        "email": "probe-2026-09-05@example.com",
        "contact": "9000090000",
        "gstin": null
      },
      "order_id": "order_TYEwD2paidorder0",
      "payment_id": "pay_TYEwD2paidpayment",
      "status": "paid",
      "issued_at": 1788586090,
      "paid_at": 1788586120,
      "cancelled_at": null,
      "expired_at": null,
      "sms_status": null,
      "email_status": "sent",
      "date": 1788586090,
      "partial_payment": false,
      "amount": 50000,
      "amount_paid": 50000,
      "amount_due": 0,
      "currency": "INR",
      "description": "feasibility probe paid 2026-09-05",
      "notes": [],
      "short_url": "https://rzp.io/rzp/PaidLnk",
      "type": "invoice",
      "created_at": 1788586090
    },
    {
      "id": "inv_TYEwE3partial00",
      "entity": "invoice",
      "customer_id": "cust_TYEw5izKFR0iJr",
      "customer_details": {
        "id": "cust_TYEw5izKFR0iJr",
        "name": "Probe Customer",
        "email": "probe-2026-09-05@example.com",
        "contact": "9000090000",
        "gstin": null
      },
      "order_id": "order_TYEwE3partialord",
      "payment_id": null,
      "status": "partially_paid",
      "issued_at": 1788586100,
      "paid_at": null,
      "cancelled_at": null,
      "expired_at": null,
      "sms_status": "sent",
      "email_status": "sent",
      "date": 1788586100,
      "partial_payment": true,
      "amount": 80000,
      "amount_paid": 30000,
      "amount_due": 50000,
      "currency": "INR",
      "description": "feasibility probe partial 2026-09-05",
      "notes": [],
      "short_url": "https://rzp.io/rzp/PartLnk",
      "type": "invoice",
      "created_at": 1788586100
    }
  ]
}`
