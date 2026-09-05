package detect

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
)

// TestFailedPaymentDetectorMatchesTheGoldenSighting is the whole-item pin. It
// reads the order and the payment as Razorpay sends them and compares the
// result field by field with the frozen golden.
//
// Every field is compared, the customer included. It was not always: the
// golden carries a customer email and contact, transcribed from the payment
// body the 2026-09-05 probe captured, and razorpay.Payment had no email and
// no contact field to decode them into, so this test zeroed the Customer on
// the expected side and said in a comment that it was doing so. The two
// fields landed on 2026-09-05 and the fixture carries them, so the two sides
// agree with the frozen golden and there is nothing left to except out. The
// assertion that the golden still has a contact stays: it is what fails
// loudly if the fixture or the decode drops one again.
func TestFailedPaymentDetectorMatchesTheGoldenSighting(t *testing.T) {
	golden := loadGolden(t, "failed_payment.json")
	if !golden.Customer.HasContactChannel() {
		t.Fatal("the golden no longer carries a contact, so the sighting it pins has changed shape")
	}

	gateway := &stubGateway{
		orders: decodeOrders(t, probeOrderListFailed),
		payments: map[string][]razorpay.Payment{
			"order_TWu8G6mQV0Drc9": decodePayments(t, probeOrderPaymentsFailed),
		},
	}
	detector := NewFailedPaymentDetector(gateway, Config{})

	items, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Detect returned %d items, want 1", len(items))
	}

	if !reflect.DeepEqual(items[0], golden) {
		t.Errorf("Detect built\n %+v\nwant\n %+v", items[0], golden)
	}
}

// TestFailedPaymentDetectorIgnoresPaidOrders pins the amount_due predicate,
// and pins it at the request as well as at the result: an order Razorpay
// reports as settled is not asked about, so a long history of paid orders
// costs no requests.
func TestFailedPaymentDetectorIgnoresPaidOrders(t *testing.T) {
	gateway := &stubGateway{
		orders: decodeOrders(t, probeOrderListMixed),
		payments: map[string][]razorpay.Payment{
			"order_TWu8G6mQV0Drc9": decodePayments(t, probeOrderPaymentsFailed),
		},
	}
	detector := NewFailedPaymentDetector(gateway, Config{})

	items, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("Detect returned %d items, want 0: the only order with a failed payment is fully paid", len(items))
	}
	for _, id := range gateway.paymentOrders {
		if id == "order_TWu8G6mQV0Drc9" {
			t.Error("the detector spent a payments request on an order with nothing due")
		}
	}
}

// TestFailedPaymentDetectorIgnoresOrdersWithNoFailure pins the other half of
// the predicate. An unpaid order whose only payment is created is nobody's
// failure, and it belongs to the unpaid-order detector.
func TestFailedPaymentDetectorIgnoresOrdersWithNoFailure(t *testing.T) {
	const pending = `{
      "entity": "collection",
      "count": 1,
      "items": [
        {
          "id": "pay_TYEpending000001",
          "entity": "payment",
          "amount": 50000,
          "currency": "INR",
          "status": "created",
          "order_id": "order_TYEyaa7bjDHn7P",
          "method": "card",
          "created_at": 1788586220
        }
      ]
    }`

	gateway := &stubGateway{
		orders: decodeOrders(t, probeOrderListMixed),
		payments: map[string][]razorpay.Payment{
			"order_TYEyaa7bjDHn7P": decodePayments(t, pending),
		},
	}
	detector := NewFailedPaymentDetector(gateway, Config{})

	items, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("Detect returned %d items, want 0", len(items))
	}
}

// TestFailedPaymentDetectorReadsTheNewestFailure pins which attempt the
// evidence comes off. The customer last saw the newest decline, so that is the
// error the item carries and its created_at is when the debt is dated from.
func TestFailedPaymentDetectorReadsTheNewestFailure(t *testing.T) {
	const twoFailures = `{
      "entity": "collection",
      "count": 2,
      "items": [
        {
          "id": "pay_TWu8Gold000001",
          "entity": "payment",
          "amount": 100000,
          "currency": "INR",
          "status": "failed",
          "order_id": "order_TWu8G6mQV0Drc9",
          "method": "card",
          "error_code": "BAD_REQUEST_ERROR",
          "error_description": "Payment failed",
          "error_source": "gateway",
          "error_step": "payment_authorization",
          "error_reason": "payment_failed",
          "created_at": 1788294400
        },
        {
          "id": "pay_TWu8GufuR8yXmA",
          "entity": "payment",
          "amount": 100000,
          "currency": "INR",
          "status": "failed",
          "order_id": "order_TWu8G6mQV0Drc9",
          "method": "upi",
          "error_code": "BAD_REQUEST_ERROR",
          "error_description": "Payment failed",
          "error_source": "gateway",
          "error_step": "payment_authorization",
          "error_reason": "payment_failed",
          "created_at": 1788294474
        }
      ]
    }`

	gateway := &stubGateway{
		orders: decodeOrders(t, probeOrderListFailed),
		payments: map[string][]razorpay.Payment{
			"order_TWu8G6mQV0Drc9": decodePayments(t, twoFailures),
		},
	}
	detector := NewFailedPaymentDetector(gateway, Config{})

	items, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Detect returned %d items, want 1", len(items))
	}
	if items[0].SourceID != "pay_TWu8GufuR8yXmA" {
		t.Errorf("SourceID = %q, want the newer failure", items[0].SourceID)
	}
	if items[0].AtRiskSince != 1788294474 {
		t.Errorf("AtRiskSince = %d, want the newer failure's created_at 1788294474", items[0].AtRiskSince)
	}
	if items[0].Signal.Method != "upi" {
		t.Errorf("Signal.Method = %q, want upi off the newer failure", items[0].Signal.Method)
	}
}

// TestFailedPaymentDetectorCopiesTheOrdersAmounts pins the amounts rule. The
// debt is what Razorpay says is still owed on the order, not what the failed
// attempt was for: a partial capture makes those two different numbers, and
// chasing the payment's amount would ask the customer for money already paid.
func TestFailedPaymentDetectorCopiesTheOrdersAmounts(t *testing.T) {
	const partlyPaid = `{
      "entity": "collection",
      "count": 1,
      "items": [
        {
          "id": "order_TWu8G6mQV0Drc9",
          "entity": "order",
          "amount": 100000,
          "amount_paid": 40000,
          "amount_due": 60000,
          "currency": "INR",
          "receipt": "rcpt_demo_1788294472",
          "offer_id": null,
          "status": "created",
          "attempts": 3,
          "notes": [],
          "created_at": 1788294472
        }
      ]
    }`

	gateway := &stubGateway{
		orders: decodeOrders(t, partlyPaid),
		payments: map[string][]razorpay.Payment{
			"order_TWu8G6mQV0Drc9": decodePayments(t, probeOrderPaymentsFailed),
		},
	}
	detector := NewFailedPaymentDetector(gateway, Config{})

	items, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Detect returned %d items, want 1", len(items))
	}
	got := items[0]
	if got.AmountPaise != 100000 || got.AmountPaidPaise != 40000 || got.AmountDuePaise != 60000 {
		t.Errorf("amounts are %d/%d/%d, want the order's 100000/40000/60000", got.AmountPaise, got.AmountPaidPaise, got.AmountDuePaise)
	}
	if got.Signal.Attempts != 3 {
		t.Errorf("Signal.Attempts = %d, want the order's 3", got.Signal.Attempts)
	}
}

// TestFailedPaymentDetectorReturnsWhatItBuiltOnAPaymentsError pins the partial
// sweep across the second call, not only across the order pages.
func TestFailedPaymentDetectorReturnsWhatItBuiltOnAPaymentsError(t *testing.T) {
	const twoUnpaid = `{
      "entity": "collection",
      "count": 2,
      "items": [
        {
          "id": "order_TWu8G6mQV0Drc9",
          "entity": "order",
          "amount": 100000,
          "amount_paid": 0,
          "amount_due": 100000,
          "currency": "INR",
          "receipt": null,
          "offer_id": null,
          "status": "created",
          "attempts": 1,
          "notes": [],
          "created_at": 1788294472
        },
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
          "attempts": 1,
          "notes": [],
          "created_at": 1788586217
        }
      ]
    }`

	gateway := &stubGateway{
		orders: decodeOrders(t, twoUnpaid),
		payments: map[string][]razorpay.Payment{
			"order_TWu8G6mQV0Drc9": decodePayments(t, probeOrderPaymentsFailed),
		},
		paymentErrOnOrder: "order_TYEyaa7bjDHn7P",
	}
	detector := NewFailedPaymentDetector(gateway, Config{})

	items, err := detector.Detect(context.Background())

	if !errors.Is(err, errStub) {
		t.Fatalf("Detect error = %v, want the stub failure", err)
	}
	if len(items) != 1 {
		t.Fatalf("returned %d items with the error, want the 1 built before it", len(items))
	}
	if items[0].SourceID != "pay_TWu8GufuR8yXmA" {
		t.Errorf("SourceID = %q, want the item built before the failure", items[0].SourceID)
	}
}

// TestFailedPaymentDetectorReadsAContactFromDocumentedNotes pins the fallback
// path: the payment reported no contact at all, so the order's documented
// notes are the only place left, and only the documented keys are read.
//
// A note under any other key is ignored rather than guessed at, which is why
// the "purpose" note below produces nothing, and the missing contact key
// leaves the phone number empty instead of borrowing one from anywhere.
func TestFailedPaymentDetectorReadsAContactFromDocumentedNotes(t *testing.T) {
	const withNotes = `{
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
          "receipt": null,
          "offer_id": null,
          "status": "created",
          "attempts": 1,
          "notes": {"customer_email": "probe-2026-09-05@example.com", "purpose": "phase-1 demo"},
          "created_at": 1788294472
        }
      ]
    }`

	gateway := &stubGateway{
		orders: decodeOrders(t, withNotes),
		payments: map[string][]razorpay.Payment{
			"order_TWu8G6mQV0Drc9": decodePayments(t, probeOrderPaymentsFailedNoContact),
		},
	}
	detector := NewFailedPaymentDetector(gateway, Config{})

	items, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Detect returned %d items, want 1", len(items))
	}
	if items[0].Customer.Email != "probe-2026-09-05@example.com" {
		t.Errorf("Customer.Email = %q, want the documented note", items[0].Customer.Email)
	}
	if items[0].Customer.Contact != "" {
		t.Errorf("Customer.Contact = %q, want empty: no note carried one", items[0].Customer.Contact)
	}
}

// TestFailedPaymentDetectorPrefersThePaymentContactOverTheNote pins the
// precedence between the two sources, per field.
//
// The payment's address wins because the payer entered it at the checkout
// that failed, where a note is whatever the merchant wrote on the order. The
// phone number is the other half: the payment carries none here, so the note's
// survives rather than being dropped because the address next to it was
// overwritten.
func TestFailedPaymentDetectorPrefersThePaymentContactOverTheNote(t *testing.T) {
	const withNotes = `{
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
          "status": "created",
          "attempts": 1,
          "notes": {
            "customer_name": "Note Name",
            "customer_email": "note@example.com",
            "customer_contact": "+919000000000"
          },
          "created_at": 1788294472
        }
      ]
    }`
	const paymentEmailOnly = `{
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
          "email": "payer@example.com",
          "contact": null,
          "error_code": "BAD_REQUEST_ERROR",
          "error_reason": "payment_failed",
          "created_at": 1788294474
        }
      ]
    }`

	gateway := &stubGateway{
		orders: decodeOrders(t, withNotes),
		payments: map[string][]razorpay.Payment{
			"order_TWu8G6mQV0Drc9": decodePayments(t, paymentEmailOnly),
		},
	}
	detector := NewFailedPaymentDetector(gateway, Config{})

	items, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Detect returned %d items, want 1", len(items))
	}
	want := riskitem.Customer{
		Name:    "Note Name",
		Email:   "payer@example.com",
		Contact: "+919000000000",
	}
	if items[0].Customer != want {
		t.Errorf("Customer = %+v, want %+v", items[0].Customer, want)
	}
}

// twoUnpaidOrders is two orders with money still due on both, so a walk that
// stops at the first failure and a walk that carries on give different answers.
const twoUnpaidOrders = `{
  "entity": "collection",
  "count": 2,
  "items": [
    {
      "id": "order_TWu8G6mQV0Drc9",
      "entity": "order",
      "amount": 100000,
      "amount_paid": 0,
      "amount_due": 100000,
      "currency": "INR",
      "receipt": null,
      "offer_id": null,
      "status": "created",
      "attempts": 1,
      "notes": [],
      "created_at": 1788294472
    },
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
      "attempts": 1,
      "notes": [],
      "created_at": 1788586217
    }
  ]
}`

// TestFailedPaymentDetectorCarriesOnPastOnePaymentsFailure is the other half of
// the partial-sweep contract, applied to the per-order call this detector makes
// on top of the sweep.
//
// The first order's payments call fails and the second order's succeeds. A walk
// that returned at the first failure would report an empty queue and one error,
// which reads as an account with no failed payments on it rather than as an
// account this run could not finish reading. The debt on every order after the
// failing one is real and it is what the run exists to find.
func TestFailedPaymentDetectorCarriesOnPastOnePaymentsFailure(t *testing.T) {
	gateway := &stubGateway{
		orders: decodeOrders(t, twoUnpaidOrders),
		payments: map[string][]razorpay.Payment{
			"order_TYEyaa7bjDHn7P": decodePayments(t, probeOrderPaymentsFailed),
		},
		paymentErrOnOrder: "order_TWu8G6mQV0Drc9",
	}
	detector := NewFailedPaymentDetector(gateway, Config{})

	items, err := detector.Detect(context.Background())

	if !errors.Is(err, errStub) {
		t.Fatalf("Detect error = %v, want the stub failure to survive", err)
	}
	if len(gateway.paymentOrders) != 2 {
		t.Fatalf("asked for the payments on %v, want both orders", gateway.paymentOrders)
	}
	if len(items) != 1 {
		t.Fatalf("returned %d item(s), want the 1 from the order after the failure", len(items))
	}
	if items[0].RootOrderID != "order_TYEyaa7bjDHn7P" {
		t.Errorf("RootOrderID = %q, want the order read after the failing one", items[0].RootOrderID)
	}
	if !strings.Contains(err.Error(), "order_TWu8G6mQV0Drc9") {
		t.Errorf("the error %q does not name the order whose payments could not be read", err)
	}
}

// TestFailedPaymentDetectorKeepsTheSweepErrorAlongsideAPaymentsError pins that
// one failure does not stand in for another.
//
// The sweep is cut short on its second page and the one order it did read then
// fails its payments call. Both are real and they mean different things: a
// truncated sweep says the queue is incomplete, and a failed payments call says
// one known order could not be judged. Returning either on its own loses a fact
// the caller needs to decide whether the run is worth acting on.
func TestFailedPaymentDetectorKeepsTheSweepErrorAlongsideAPaymentsError(t *testing.T) {
	gateway := &stubGateway{
		orders:            decodeOrders(t, twoUnpaidOrders),
		orderErrOnCall:    2,
		paymentErrOnOrder: "order_TWu8G6mQV0Drc9",
	}
	detector := NewFailedPaymentDetector(gateway, Config{PageSize: 1})

	_, err := detector.Detect(context.Background())

	joined, ok := err.(interface{ Unwrap() []error })
	if !ok {
		t.Fatalf("Detect error = %v (%T), want a joined error carrying both failures", err, err)
	}
	if got := len(joined.Unwrap()); got != 2 {
		t.Errorf("the joined error carries %d failure(s), want 2: the sweep and the payments call", got)
	}
}
