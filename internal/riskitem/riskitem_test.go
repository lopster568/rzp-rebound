package riskitem_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
)

// The golden fixtures under testdata/ are transcribed from the Razorpay test
// mode probe of 2026-09-05, not composed by hand. Every value below can be
// found in the probe capture named beside it, and no field is invented.
//
//	failed_payment.json   probe 17_order_payments.json and 18_pay_TWu8GufuR8yXmA.json
//	unpaid_order.json     probe 20_create_order.json
//	overdue_invoice.json  probe 10_issue_invoice.json and 15_get_invoice_poll.json
//
// Two values in failed_payment.json are read off the payment rather than off
// the order, and that is deliberate. The order behind that payment,
// order_TWu8G6mQV0Drc9, is paid in probe 02_list_orders.json with attempts 2,
// because a second card captured it thirty-six seconds later. The fixture
// describes the debt as it stood at the failed payment: amount 100000 from the
// payment, nothing captured so paid 0 and due 100000, and attempts 1 because
// pay_TWu8GufuR8yXmA at created_at 1788294474 is the first of the two on that
// order. A detector reading a live order that is still unpaid copies the
// order's own amount_paid, amount_due and attempts.
const (
	failedPaymentFixture  = "failed_payment.json"
	unpaidOrderFixture    = "unpaid_order.json"
	overdueInvoiceFixture = "overdue_invoice.json"
)

func loadFixture(t *testing.T, name string) (riskitem.RiskItem, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var item riskitem.RiskItem
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&item); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	return item, raw
}

func TestNewIDIsDeterministic(t *testing.T) {
	first := riskitem.NewID(riskitem.SourceFailedPayment, "pay_TWu8GufuR8yXmA")
	second := riskitem.NewID(riskitem.SourceFailedPayment, "pay_TWu8GufuR8yXmA")

	if first != second {
		t.Errorf("NewID is not deterministic: %q then %q", first, second)
	}
	if !strings.HasPrefix(first, "ri_") {
		t.Errorf("NewID = %q, want the ri_ prefix", first)
	}
	if len(first) != len("ri_")+12 {
		t.Errorf("NewID = %q, length %d, want %d", first, len(first), len("ri_")+12)
	}
}

// TestNewIDSeparatesSources is the reason Source is inside the digest. The
// same Razorpay id can be reported by more than one detector only through a
// mistake, but an id that ignored the source would silently merge those two
// sightings into one audit row.
func TestNewIDSeparatesSources(t *testing.T) {
	const sourceID = "order_TYEwKA0KjwEW3t"
	sources := []riskitem.Source{
		riskitem.SourceFailedPayment,
		riskitem.SourceUnpaidOrder,
		riskitem.SourceOverdueInvoice,
	}

	seen := make(map[string]riskitem.Source, len(sources))
	for _, source := range sources {
		id := riskitem.NewID(source, sourceID)
		if other, ok := seen[id]; ok {
			t.Errorf("NewID(%q, %q) collides with source %q at %q", source, sourceID, other, id)
		}
		seen[id] = source
	}
}

// TestNewIDDoesNotShuffleAcrossTheSeparator pins the vertical bar. Without a
// separator inside the hash, a source ending in some prefix of an id would
// digest to the same bytes as a shorter source and a longer id.
func TestNewIDDoesNotShuffleAcrossTheSeparator(t *testing.T) {
	a := riskitem.NewID(riskitem.Source("failed_payment"), "pay_1")
	b := riskitem.NewID(riskitem.Source("failed_payment|pay"), "_1")

	if a == b {
		t.Errorf("NewID collapses %q + %q into the same id as %q + %q: %q",
			"failed_payment", "pay_1", "failed_payment|pay", "_1", a)
	}
}

// TestFixtureIDsMatchTheConstructor keeps the golden files honest. A fixture
// whose id was typed by hand would pass every other test in this file.
func TestFixtureIDsMatchTheConstructor(t *testing.T) {
	for _, name := range []string{failedPaymentFixture, unpaidOrderFixture, overdueInvoiceFixture} {
		item, _ := loadFixture(t, name)
		want := riskitem.NewID(item.Source, item.SourceID)
		if item.ID != want {
			t.Errorf("%s has id %q, and NewID(%q, %q) is %q", name, item.ID, item.Source, item.SourceID, want)
		}
	}
}

// TestOneDebtFromTwoDetectorsCollapsesToOneKey is the dedupe rule. Issuing
// invoice inv_TYEwC7POHGFZNa minted order_TYEwKA0KjwEW3t, so the invoice
// detector and the unpaid-order detector both see that debt, under two
// different SourceIDs and therefore two different ids. The queue collapses on
// DedupeKey, and if it collapsed on ID the customer would be contacted twice
// about one invoice.
func TestOneDebtFromTwoDetectorsCollapsesToOneKey(t *testing.T) {
	invoiceItem, _ := loadFixture(t, overdueInvoiceFixture)
	orderItem := riskitem.RiskItem{
		ID:          riskitem.NewID(riskitem.SourceUnpaidOrder, invoiceItem.RootOrderID),
		Source:      riskitem.SourceUnpaidOrder,
		SourceID:    invoiceItem.RootOrderID,
		RootOrderID: invoiceItem.RootOrderID,
	}

	if invoiceItem.ID == orderItem.ID {
		t.Fatalf("both sightings have id %q, and they are two sightings", invoiceItem.ID)
	}
	if invoiceItem.DedupeKey() != orderItem.DedupeKey() {
		t.Errorf("DedupeKey() = %q and %q, want one key for one debt",
			invoiceItem.DedupeKey(), orderItem.DedupeKey())
	}
	if invoiceItem.DedupeKey() != "order_TYEwKA0KjwEW3t" {
		t.Errorf("DedupeKey() = %q, want the minted order id", invoiceItem.DedupeKey())
	}
}

// TestDedupeKeyFallsBackToTheSighting covers the item with no order behind it.
// Falling back to a constant, or to the empty string, would merge every such
// item into one.
func TestDedupeKeyFallsBackToTheSighting(t *testing.T) {
	first := riskitem.RiskItem{Source: riskitem.SourceFailedPayment, SourceID: "pay_A"}
	second := riskitem.RiskItem{Source: riskitem.SourceFailedPayment, SourceID: "pay_B"}

	if first.DedupeKey() == second.DedupeKey() {
		t.Errorf("two rootless items share the key %q", first.DedupeKey())
	}
	if first.DedupeKey() != "failed_payment|pay_A" {
		t.Errorf("DedupeKey() = %q, want the source and the source id", first.DedupeKey())
	}
}

// TestGoldenFixturesRoundTrip decodes each fixture into a RiskItem, encodes it
// again, and compares the two as generic JSON. A field the struct drops and a
// field the struct invents both fail here.
func TestGoldenFixturesRoundTrip(t *testing.T) {
	for _, name := range []string{failedPaymentFixture, unpaidOrderFixture, overdueInvoiceFixture} {
		t.Run(name, func(t *testing.T) {
			item, raw := loadFixture(t, name)

			encoded, err := json.Marshal(item)
			if err != nil {
				t.Fatalf("encode %s: %v", name, err)
			}

			var before, after map[string]any
			if err := json.Unmarshal(raw, &before); err != nil {
				t.Fatalf("parse fixture %s: %v", name, err)
			}
			if err := json.Unmarshal(encoded, &after); err != nil {
				t.Fatalf("parse re-encoded %s: %v", name, err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Errorf("%s does not round-trip\n before: %s\n  after: %s", name, raw, encoded)
			}
		})
	}
}

// TestFailedPaymentFixtureCarriesTheProbeValues transcribes probe capture
// 18_pay_TWu8GufuR8yXmA.json.
func TestFailedPaymentFixtureCarriesTheProbeValues(t *testing.T) {
	item, _ := loadFixture(t, failedPaymentFixture)

	if item.Source != riskitem.SourceFailedPayment {
		t.Errorf("Source = %q", item.Source)
	}
	if item.SourceID != "pay_TWu8GufuR8yXmA" {
		t.Errorf("SourceID = %q", item.SourceID)
	}
	if item.RootOrderID != "order_TWu8G6mQV0Drc9" {
		t.Errorf("RootOrderID = %q", item.RootOrderID)
	}
	if item.AmountPaise != 100000 || item.AmountDuePaise != 100000 || item.AmountPaidPaise != 0 {
		t.Errorf("amounts = %d/%d/%d, want 100000/0/100000",
			item.AmountPaise, item.AmountPaidPaise, item.AmountDuePaise)
	}
	if item.AtRiskSince != 1788294474 {
		t.Errorf("AtRiskSince = %d, want the payment created_at", item.AtRiskSince)
	}
	want := riskitem.Signal{
		FailureCode:   "BAD_REQUEST_ERROR",
		FailureReason: "payment_failed",
		FailureSource: "gateway",
		FailureStep:   "payment_authorization",
		Method:        "card",
		Attempts:      1,
	}
	if item.Signal != want {
		t.Errorf("Signal = %+v, want %+v", item.Signal, want)
	}
	if item.PayHandle.Kind != riskitem.HandleKindNone {
		t.Errorf("PayHandle.Kind = %q, and a failed payment carries no link", item.PayHandle.Kind)
	}
	if !item.Customer.HasContactChannel() {
		t.Error("the probe payment carried an email and a contact, and HasContactChannel is false")
	}
}

// TestUnpaidOrderFixtureHasNoContactChannel is the rule that must not be
// softened. Probe capture 20_create_order.json is an order created with no
// customer on it, so there is nowhere to send anything, and nothing in the
// engine may fill that in from a receipt, a note, or another order.
func TestUnpaidOrderFixtureHasNoContactChannel(t *testing.T) {
	item, _ := loadFixture(t, unpaidOrderFixture)

	if item.Customer.HasContactChannel() {
		t.Errorf("Customer = %+v, and the probe order carried none", item.Customer)
	}
	if item.Customer != (riskitem.Customer{}) {
		t.Errorf("Customer = %+v, want the zero value", item.Customer)
	}
	if item.Signal.Attempts != 0 {
		t.Errorf("Attempts = %d, want 0: nobody tried this order", item.Signal.Attempts)
	}
	if item.Signal.FailureCode != "" {
		t.Errorf("FailureCode = %q, and an abandonment is not a failure", item.Signal.FailureCode)
	}
	if item.DedupeKey() != item.SourceID {
		t.Errorf("DedupeKey() = %q, want the order id %q", item.DedupeKey(), item.SourceID)
	}
}

// TestOverdueInvoiceFixtureCarriesTheMintedOrderAndTheSendStatus transcribes
// probe captures 10_issue_invoice.json and 15_get_invoice_poll.json. The
// email_status went from null at issue to sent on the poll, which is what
// EmailStatus records: the send was accepted by Razorpay.
func TestOverdueInvoiceFixtureCarriesTheMintedOrderAndTheSendStatus(t *testing.T) {
	item, _ := loadFixture(t, overdueInvoiceFixture)

	if item.SourceID != "inv_TYEwC7POHGFZNa" {
		t.Errorf("SourceID = %q", item.SourceID)
	}
	if item.RootOrderID != "order_TYEwKA0KjwEW3t" {
		t.Errorf("RootOrderID = %q, want the order the invoice minted", item.RootOrderID)
	}
	if item.Signal.EmailStatus != "sent" {
		t.Errorf("EmailStatus = %q, want sent", item.Signal.EmailStatus)
	}
	if item.Signal.SmsStatus != "" {
		t.Errorf("SmsStatus = %q, and the probe invoice reported null", item.Signal.SmsStatus)
	}
	if item.AtRiskSince != 1788586088 {
		t.Errorf("AtRiskSince = %d, want the invoice issued_at", item.AtRiskSince)
	}
	if item.PayHandle.Kind != riskitem.HandleKindInvoice {
		t.Errorf("PayHandle.Kind = %q, want %q", item.PayHandle.Kind, riskitem.HandleKindInvoice)
	}
	if item.PayHandle.URL != "https://rzp.io/rzp/4U2HXcQ" {
		t.Errorf("PayHandle.URL = %q", item.PayHandle.URL)
	}
}

// TestTheActionSetIsClosedAndHasNoRetry is the load-bearing test in this file.
// Unattended retry of a one-off payment is not lawful in India, and this
// engine deleted the concept rather than gating it. A constant added later
// with retry in its name fails here, in the package every other work package
// imports.
func TestTheActionSetIsClosedAndHasNoRetry(t *testing.T) {
	want := []string{
		riskitem.ActionNotifyEmail,
		riskitem.ActionNotifySMS,
		riskitem.ActionCreatePaymentLink,
		riskitem.ActionResendLink,
		riskitem.ActionLogPromise,
		riskitem.ActionEscalate,
		riskitem.ActionCancelWriteOff,
		riskitem.ActionDoNothing,
	}

	got := riskitem.LawfulActions()
	if !slices.Equal(got, want) {
		t.Errorf("LawfulActions() = %v, want %v", got, want)
	}
	for _, action := range got {
		if strings.Contains(action, "retry") || strings.Contains(action, "reattempt") {
			t.Errorf("the lawful set contains %q", action)
		}
		if !riskitem.IsLawfulAction(action) {
			t.Errorf("IsLawfulAction(%q) is false and the set lists it", action)
		}
	}
	for _, action := range []string{"retry", "retry_same_instrument", "auto_retry", "", "RETRY"} {
		if riskitem.IsLawfulAction(action) {
			t.Errorf("IsLawfulAction(%q) is true", action)
		}
	}
}

// TestLawfulActionsCannotBeWidenedByACaller checks that the returned slice is
// a copy. A caller that appends to a shared backing array would add an action
// to every other caller's set.
func TestLawfulActionsCannotBeWidenedByACaller(t *testing.T) {
	first := riskitem.LawfulActions()
	for i := range first {
		first[i] = "retry"
	}

	second := riskitem.LawfulActions()
	if slices.Contains(second, "retry") {
		t.Errorf("LawfulActions() = %v after a caller wrote to an earlier result", second)
	}
}
