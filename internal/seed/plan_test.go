package seed

import (
	"strings"
	"testing"
)

func TestGeneratePlanDemoProfileCounts(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "t1", 42)

	// 2 fresh + 3 aged + 1 disputed + 1 partial-plan + 1 no-contact.
	wantInvoices := 8
	if len(plan.Invoices) != wantInvoices {
		t.Fatalf("len(Invoices) = %d, want %d", len(plan.Invoices), wantInvoices)
	}
	if len(plan.Orders) != 3 {
		t.Fatalf("len(Orders) = %d, want 3", len(plan.Orders))
	}
}

func TestGeneratePlanIsDeterministic(t *testing.T) {
	first := GeneratePlan(DemoProfile(), "same-tag", 7)
	second := GeneratePlan(DemoProfile(), "same-tag", 7)

	if len(first.Invoices) != len(second.Invoices) {
		t.Fatalf("invoice counts differ between two runs with the same seed: %d vs %d",
			len(first.Invoices), len(second.Invoices))
	}
	for i := range first.Invoices {
		a, b := first.Invoices[i], second.Invoices[i]
		if a != b {
			t.Fatalf("invoice %d differs between two runs with the same seed:\n%+v\n%+v", i, a, b)
		}
	}
	for i := range first.Orders {
		if first.Orders[i] != second.Orders[i] {
			t.Fatalf("order %d differs between two runs with the same seed", i)
		}
	}
}

func TestGeneratePlanDifferentSeedsDiffer(t *testing.T) {
	a := GeneratePlan(DemoProfile(), "tag", 1)
	b := GeneratePlan(DemoProfile(), "tag", 2)

	same := true
	for i := range a.Invoices {
		if a.Invoices[i].CustomerName != b.Invoices[i].CustomerName ||
			a.Invoices[i].AmountPaise != b.Invoices[i].AmountPaise {
			same = false
			break
		}
	}
	if same {
		t.Fatal("two different rand seeds produced an identical invoice list")
	}
}

func TestGeneratePlanCoversTheRequiredMix(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "mix", 99)

	var fresh, age30, age60, age90, disputed, partialPlan, noContact int
	for _, inv := range plan.Invoices {
		switch inv.AgeBucket {
		case AgeFresh:
			fresh++
		case Age30:
			age30++
		case Age60:
			age60++
		case Age90:
			age90++
		default:
			t.Fatalf("invoice has an unrecognised age bucket %q", inv.AgeBucket)
		}
		if inv.Disputed {
			disputed++
		}
		if inv.PartialPlan {
			partialPlan++
		}
		if inv.CustomerContact == "" {
			noContact++
		}
	}

	if fresh != 2 {
		t.Errorf("fresh invoices = %d, want 2", fresh)
	}
	if age30 == 0 || age60 == 0 || age90 == 0 {
		t.Errorf("expected at least one invoice in each aged bucket, got 30=%d 60=%d 90=%d", age30, age60, age90)
	}
	if disputed != 1 {
		t.Errorf("disputed invoices = %d, want 1", disputed)
	}
	if partialPlan != 1 {
		t.Errorf("partial-plan invoices = %d, want 1", partialPlan)
	}
	if noContact < 1 {
		t.Errorf("no-contact invoices = %d, want at least 1", noContact)
	}
}

func TestGeneratePlanNoContactInvoiceHasNoContactAtAll(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "nc", 5)

	found := false
	for _, inv := range plan.Invoices {
		if inv.CustomerContact != "" {
			continue
		}
		found = true
		if inv.CustomerEmail != "" {
			// An invoice with no contact channel means no email either: the
			// no-contact item exists to demonstrate the R10 escalate rule,
			// and giving it an email would make it not that item.
			t.Errorf("the no-contact invoice for %s carries an email %q; it should have no contact channel at all",
				inv.CustomerName, inv.CustomerEmail)
		}
	}
	if !found {
		t.Fatal("no invoice in the demo profile had an empty contact")
	}
}

func TestGeneratePlanEmailsAreOnExampleDotCom(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "email-check", 3)

	for _, inv := range plan.Invoices {
		if inv.CustomerEmail == "" {
			continue // the no-contact invoice
		}
		if !strings.HasSuffix(inv.CustomerEmail, "@example.com") {
			t.Errorf("customer email %q is not on the RFC 2606 example.com domain", inv.CustomerEmail)
		}
	}
}

func TestGeneratePlanPartialPlanInvoiceSetsAPIPartialPaymentToo(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "pp", 11)

	found := false
	for _, inv := range plan.Invoices {
		if !inv.PartialPlan {
			continue
		}
		found = true
		if !inv.PartialPayment {
			t.Errorf("invoice %+v carries PartialPlan but not PartialPayment", inv)
		}
	}
	if !found {
		t.Fatal("no partial-plan invoice found")
	}
}

func TestGeneratePlanSomeInvoicesHavePartialPaymentBeyondThePlanCandidate(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "pp2", 11)

	partialPayment := 0
	for _, inv := range plan.Invoices {
		if inv.PartialPayment {
			partialPayment++
		}
	}
	if partialPayment < 2 {
		t.Errorf("partial_payment invoices = %d, want at least 2 (the plan candidate plus another)", partialPayment)
	}
}

func TestGeneratePlanAmountsArePositive(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "amt", 21)

	for _, inv := range plan.Invoices {
		if inv.AmountPaise <= 0 {
			t.Errorf("invoice for %s has non-positive amount %d", inv.CustomerName, inv.AmountPaise)
		}
	}
	for i, ord := range plan.Orders {
		if ord.AmountPaise <= 0 {
			t.Errorf("order %d has non-positive amount %d", i, ord.AmountPaise)
		}
	}
}

func TestPlanCallEstimate(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "est", 1)
	want := len(plan.Invoices)*3 + len(plan.Orders)
	if got := plan.CallEstimate(); got != want {
		t.Errorf("CallEstimate() = %d, want %d", got, want)
	}
}

func TestGeneratePlanOrdersHaveTheRightContactMix(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "orders", 4)

	if len(plan.Orders) != 3 {
		t.Fatalf("len(Orders) = %d, want 3", len(plan.Orders))
	}

	var withContact, withoutContact int
	for _, ord := range plan.Orders {
		if ord.CustomerContact == "" && ord.CustomerEmail == "" {
			withoutContact++
			if ord.CustomerName != "" {
				t.Errorf("a no-contact order still carries a customer name %q", ord.CustomerName)
			}
			continue
		}
		withContact++
		if ord.CustomerName == "" || ord.CustomerEmail == "" || ord.CustomerContact == "" {
			t.Errorf("an order meant to carry a contact is missing part of it: %+v", ord)
		}
	}
	if withContact != 2 {
		t.Errorf("orders with a contact = %d, want 2", withContact)
	}
	if withoutContact != 1 {
		t.Errorf("orders with no contact = %d, want 1", withoutContact)
	}
}

func TestProfileByNameUnknown(t *testing.T) {
	if _, ok := ProfileByName("does-not-exist"); ok {
		t.Fatal("ProfileByName found a profile that was never defined")
	}
	if _, ok := ProfileByName("demo"); !ok {
		t.Fatal("ProfileByName could not find the demo profile")
	}
}

// TestGeneratePlanAgesTwoOfTheThreeDemoOrders is the fix for a demo that
// depended on how long ago the book was seeded.
//
// policy.GraceUnpaidOrder is one hour and nothing in the orders API backdates
// created_at, so when every order carried AgeFresh a book seeded less than an
// hour before the take had all three denied under R11-NOT-YET-DUE, and the beat
// where the engine mints a payment link had nothing to mint one for. Two aged
// orders make that beat reproducible; the third stays fresh so R11 still fires
// on something real.
func TestGeneratePlanAgesTwoOfTheThreeDemoOrders(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "ages", 13)

	if len(plan.Orders) != 3 {
		t.Fatalf("len(Orders) = %d, want 3", len(plan.Orders))
	}

	var aged, fresh int
	for _, ord := range plan.Orders {
		if ord.AgeBucket.AgeDays() > 0 {
			aged++
			continue
		}
		fresh++
		if ord.AgeBucket != AgeFresh {
			t.Errorf("an order with no age carries bucket %q, want %q", ord.AgeBucket, AgeFresh)
		}
	}
	if aged != 2 {
		t.Errorf("aged orders = %d, want 2", aged)
	}
	if fresh != 1 {
		t.Errorf("fresh orders = %d, want 1", fresh)
	}
}

// TestGeneratePlanKeepsTheNoContactOrderOldEnoughToReachR10 pins the rule order
// the demo depends on.
//
// R11-NOT-YET-DUE is evaluated before R10-NO-CONTACT-CHANNEL, so a fresh order
// with no contact channel is denied for being young and never reaches the rule
// it exists to demonstrate. The order the profile keeps fresh has to be one of
// the contact-bearing ones.
func TestGeneratePlanKeepsTheNoContactOrderOldEnoughToReachR10(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "r10", 17)

	found := false
	for _, ord := range plan.Orders {
		if ord.CustomerEmail != "" || ord.CustomerContact != "" {
			continue
		}
		found = true
		if ord.AgeBucket.AgeDays() == 0 {
			t.Errorf("the no-contact order is %s, so R11 denies it before R10 can escalate it", ord.AgeBucket)
		}
	}
	if !found {
		t.Fatal("the demo profile seeded no order without a contact channel")
	}
}

// TestGeneratePlanOrderAgesDefaultToFresh pins the zero value: a profile that
// names no ages seeds the book it seeded before OrderAges existed.
func TestGeneratePlanOrderAgesDefaultToFresh(t *testing.T) {
	profile := DemoProfile()
	profile.OrderAges = nil
	plan := GeneratePlan(profile, "default", 19)

	for i, ord := range plan.Orders {
		if ord.AgeBucket != AgeFresh {
			t.Errorf("order %d has bucket %q with no OrderAges set, want %q", i, ord.AgeBucket, AgeFresh)
		}
	}
}
