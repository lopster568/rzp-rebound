package detect

import (
	"context"
	"reflect"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
)

// TestUnpaidOrderDetectorMatchesTheGoldenSighting is the whole-item pin,
// including the empty Customer. Nothing about an unpaid order tells this
// engine who to contact, and the golden records that as an empty object rather
// than as a missing field.
func TestUnpaidOrderDetectorMatchesTheGoldenSighting(t *testing.T) {
	golden := loadGolden(t, "unpaid_order.json")

	gateway := &stubGateway{orders: decodeOrders(t, probeOrderListMixed)}
	detector := NewUnpaidOrderDetector(gateway, Config{})

	items, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	var got riskitem.RiskItem
	found := false
	for _, item := range items {
		if item.SourceID == golden.SourceID {
			got, found = item, true
		}
	}
	if !found {
		t.Fatalf("Detect did not return %s; it returned %d items", golden.SourceID, len(items))
	}
	if !reflect.DeepEqual(got, golden) {
		t.Errorf("Detect built\n %+v\nwant\n %+v", got, golden)
	}
}

// TestUnpaidOrderDetectorDiscriminatesOnAttemptsNotStatus is the trap this
// detector was written around.
//
// No order at status attempted was observed at rest on 2026-09-05, so a
// detector that took the status literal would find nothing on a live account.
// It takes attempts of exactly zero instead. The fixture holds one order at
// attempted with one attempt, and it must be excluded twice over: the status
// is not created, and the attempt count is not zero.
func TestUnpaidOrderDetectorDiscriminatesOnAttemptsNotStatus(t *testing.T) {
	gateway := &stubGateway{orders: decodeOrders(t, probeOrderListMixed)}
	detector := NewUnpaidOrderDetector(gateway, Config{})

	items, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}

	want := []string{"order_TYEyaa7bjDHn7P", "order_TYEwKA0KjwEW3t"}
	got := make([]string, 0, len(items))
	for _, item := range items {
		got = append(got, item.SourceID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Detect returned %v, want %v", got, want)
	}
	for _, item := range items {
		if item.Signal.Attempts != 0 {
			t.Errorf("%s came back with %d attempts, want 0", item.SourceID, item.Signal.Attempts)
		}
	}
}

// TestUnpaidOrderDetectorSkipsCreatedOrdersThatHaveBeenTried pins the other
// side of the same discriminator: an order at created with an attempt on it is
// a failure, and it belongs to FailedPaymentDetector, which can say what the
// failure was.
func TestUnpaidOrderDetectorSkipsCreatedOrdersThatHaveBeenTried(t *testing.T) {
	const tried = `{
      "entity": "collection",
      "count": 1,
      "items": [
        {
          "id": "order_TYEtried00000001",
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

	gateway := &stubGateway{orders: decodeOrders(t, tried)}
	detector := NewUnpaidOrderDetector(gateway, Config{})

	items, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("Detect returned %d items, want 0 for an order that has been tried", len(items))
	}
}

// TestUnpaidOrderDetectorSkipsOrdersWithNothingDue pins the amount predicate
// on its own, with an order that would otherwise pass every other test.
func TestUnpaidOrderDetectorSkipsOrdersWithNothingDue(t *testing.T) {
	const nothingDue = `{
      "entity": "collection",
      "count": 1,
      "items": [
        {
          "id": "order_TYEnothingdue001",
          "entity": "order",
          "amount": 50000,
          "amount_paid": 50000,
          "amount_due": 0,
          "currency": "INR",
          "receipt": null,
          "offer_id": null,
          "status": "created",
          "attempts": 0,
          "notes": [],
          "created_at": 1788586217
        }
      ]
    }`

	gateway := &stubGateway{orders: decodeOrders(t, nothingDue)}
	detector := NewUnpaidOrderDetector(gateway, Config{})

	items, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("Detect returned %d items, want 0 for an order with nothing due", len(items))
	}
}

// TestUnpaidOrderDetectorLeavesTheContactEmptyRatherThanGuessing is the
// caveat, made into an assertion. A /v1/orders record carries no email and no
// contact, so an item built from one has no channel, and the lawful move on it
// is escalation rather than a notification sent to an address nobody supplied.
func TestUnpaidOrderDetectorLeavesTheContactEmptyRatherThanGuessing(t *testing.T) {
	gateway := &stubGateway{orders: decodeOrders(t, probeOrderListMixed)}
	detector := NewUnpaidOrderDetector(gateway, Config{})

	items, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("Detect returned nothing, so this test proves nothing")
	}
	for _, item := range items {
		if item.Customer.HasContactChannel() {
			t.Errorf("%s carries a contact channel %+v that no order field supplied", item.SourceID, item.Customer)
		}
	}
}

// TestUnpaidOrderDetectorReadsAContactFromDocumentedNotes pins the one
// exception: a contact the merchant wrote onto the order deliberately, under
// the documented key.
func TestUnpaidOrderDetectorReadsAContactFromDocumentedNotes(t *testing.T) {
	const withNotes = `{
      "entity": "collection",
      "count": 1,
      "items": [
        {
          "id": "order_TYEnoted00000001",
          "entity": "order",
          "amount": 50000,
          "amount_paid": 0,
          "amount_due": 50000,
          "currency": "INR",
          "receipt": null,
          "offer_id": null,
          "status": "created",
          "attempts": 0,
          "notes": {"customer_name": "Probe Customer", "customer_contact": "9000090000"},
          "created_at": 1788586217
        }
      ]
    }`

	gateway := &stubGateway{orders: decodeOrders(t, withNotes)}
	detector := NewUnpaidOrderDetector(gateway, Config{})

	items, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Detect returned %d items, want 1", len(items))
	}
	want := riskitem.Customer{Name: "Probe Customer", Contact: "9000090000"}
	if items[0].Customer != want {
		t.Errorf("Customer = %+v, want %+v", items[0].Customer, want)
	}
}

// TestUnpaidOrderDetectorRootsEveryItemOnItsOwnOrder pins the field the queue
// collapses on. An order-sourced item is its own root, which is what lets the
// invoice that minted the order collapse onto it.
func TestUnpaidOrderDetectorRootsEveryItemOnItsOwnOrder(t *testing.T) {
	gateway := &stubGateway{orders: decodeOrders(t, probeOrderListMixed)}
	detector := NewUnpaidOrderDetector(gateway, Config{})

	items, err := detector.Detect(context.Background())
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	for _, item := range items {
		if item.RootOrderID != item.SourceID {
			t.Errorf("%s has RootOrderID %q, want its own id", item.SourceID, item.RootOrderID)
		}
		if item.DedupeKey() != item.SourceID {
			t.Errorf("%s has DedupeKey %q, want its own id", item.SourceID, item.DedupeKey())
		}
	}
}
