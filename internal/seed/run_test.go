package seed

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/detect"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
)

// stubClient satisfies Client without a single network call, so ExecutePlan's
// tests never spend a real Razorpay call. It fails the call named failOn on
// its failAfter'th invocation, which is how the budget and error-propagation
// tests force a stop partway through a plan.
type stubClient struct {
	customers, invoices, issues, orders int

	// orderNotes records the notes map passed to each CreateOrder call, in
	// order, so a test can check exactly what a seeded order's notes carry
	// without a real Razorpay account to read them back from.
	orderNotes []map[string]string

	failOn    string
	failAfter int
	calls     int
}

func (s *stubClient) shouldFail(method string) error {
	s.calls++
	if method == s.failOn && s.calls >= s.failAfter {
		return fmt.Errorf("stub: %s refused on call %d", method, s.calls)
	}
	return nil
}

func (s *stubClient) CreateCustomer(_ context.Context, req razorpay.CreateCustomerRequest) (razorpay.Customer, error) {
	if err := s.shouldFail("CreateCustomer"); err != nil {
		return razorpay.Customer{}, err
	}
	s.customers++
	return razorpay.Customer{
		ID:      fmt.Sprintf("cust_%04d", s.customers),
		Name:    req.Name,
		Email:   req.Email,
		Contact: req.Contact,
	}, nil
}

func (s *stubClient) CreateInvoice(_ context.Context, req razorpay.CreateInvoiceRequest) (razorpay.Invoice, error) {
	if err := s.shouldFail("CreateInvoice"); err != nil {
		return razorpay.Invoice{}, err
	}
	s.invoices++
	return razorpay.Invoice{
		ID:             fmt.Sprintf("inv_%04d", s.invoices),
		CustomerID:     req.CustomerID,
		Status:         razorpay.InvoiceStatusDraft,
		PartialPayment: req.PartialPayment,
	}, nil
}

func (s *stubClient) IssueInvoice(_ context.Context, invoiceID string) (razorpay.Invoice, error) {
	if err := s.shouldFail("IssueInvoice"); err != nil {
		return razorpay.Invoice{}, err
	}
	s.issues++
	return razorpay.Invoice{
		ID:       invoiceID,
		Status:   razorpay.InvoiceStatusIssued,
		OrderID:  "order_" + invoiceID,
		ShortURL: "https://rzp.io/i/" + invoiceID,
	}, nil
}

func (s *stubClient) CreateOrder(_ context.Context, req razorpay.CreateOrderRequest) (razorpay.Order, error) {
	if err := s.shouldFail("CreateOrder"); err != nil {
		return razorpay.Order{}, err
	}
	s.orders++
	s.orderNotes = append(s.orderNotes, req.Notes)
	return razorpay.Order{
		ID:          fmt.Sprintf("order_%04d", s.orders),
		AmountPaise: req.AmountPaise,
		Currency:    req.Currency,
		Status:      razorpay.OrderStatusCreated,
	}, nil
}

var testClock = clock.NewFake(time.Date(2026, 9, 5, 8, 0, 0, 0, time.UTC))

func TestExecutePlanHappyPath(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "run-happy", 1)
	client := &stubClient{}

	manifest, err := ExecutePlan(context.Background(), client, plan, RunOptions{Clock: testClock})
	if err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}

	if want := len(plan.Invoices) + len(plan.Orders); len(manifest.Items) != want {
		t.Fatalf("len(Items) = %d, want %d", len(manifest.Items), want)
	}
	if want := plan.CallEstimate(); manifest.CallsUsed != want {
		t.Errorf("CallsUsed = %d, want %d", manifest.CallsUsed, want)
	}
	if manifest.Gateway != GatewayLiveTestMode {
		t.Errorf("Gateway = %q, want %q", manifest.Gateway, GatewayLiveTestMode)
	}
	if manifest.RunTag != plan.RunTag {
		t.Errorf("RunTag = %q, want %q", manifest.RunTag, plan.RunTag)
	}

	var disputed, noContact, partialPlan, invoices, orders int
	for _, item := range manifest.Items {
		switch item.Kind {
		case EntityInvoice:
			invoices++
			if item.ID == "" || item.CustomerID == "" || item.OrderID == "" || item.ShortURL == "" {
				t.Errorf("invoice item incomplete: %+v", item)
			}
			if item.ExpectedRiskSource != riskitem.SourceOverdueInvoice {
				t.Errorf("invoice item ExpectedRiskSource = %q, want %q", item.ExpectedRiskSource, riskitem.SourceOverdueInvoice)
			}
		case EntityOrder:
			orders++
			if item.ID == "" {
				t.Errorf("order item has no id: %+v", item)
			}
			if item.ExpectedRiskSource != riskitem.SourceUnpaidOrder {
				t.Errorf("order item ExpectedRiskSource = %q, want %q", item.ExpectedRiskSource, riskitem.SourceUnpaidOrder)
			}
		default:
			t.Errorf("item has unknown kind %q", item.Kind)
		}
		if item.Flags.Disputed {
			disputed++
		}
		if item.Flags.NoContact {
			noContact++
		}
		if item.Flags.PartialPlan {
			partialPlan++
		}
	}
	if invoices != len(plan.Invoices) {
		t.Errorf("invoice items = %d, want %d", invoices, len(plan.Invoices))
	}
	if orders != len(plan.Orders) {
		t.Errorf("order items = %d, want %d", orders, len(plan.Orders))
	}
	if disputed != 1 {
		t.Errorf("disputed items = %d, want 1", disputed)
	}
	if noContact < 1 {
		t.Errorf("no-contact items = %d, want at least 1", noContact)
	}
	if partialPlan != 1 {
		t.Errorf("partial-plan items = %d, want 1", partialPlan)
	}

	if len(manifest.Instructions.Targets) == 0 {
		t.Error("Instructions.Targets is empty even though invoices were issued")
	}
	if len(manifest.Instructions.TestCards) == 0 {
		t.Error("Instructions.TestCards is empty")
	}
}

func TestExecutePlanRespectsCallBudget(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "run-budget", 1)
	client := &stubClient{}

	// Two calls is not enough to finish even the first invoice, which needs
	// three (customer, invoice, issue).
	manifest, err := ExecutePlan(context.Background(), client, plan, RunOptions{Clock: testClock, CallBudget: 2})
	if err == nil {
		t.Fatal("ExecutePlan with a call budget of 2 returned no error")
	}
	if !errors.Is(err, ErrCallBudgetExceeded) {
		t.Fatalf("error = %v, want it to wrap ErrCallBudgetExceeded", err)
	}
	if manifest.CallsUsed > 2 {
		t.Fatalf("CallsUsed = %d, must never exceed the budget of 2", manifest.CallsUsed)
	}
	// The customer call is the first spend and it fits inside a budget of 2,
	// so the manifest should still carry that partial item rather than
	// silently dropping a customer that now exists in Razorpay.
	if len(manifest.Items) == 0 {
		t.Fatal("a partial run recorded no items at all, even though a customer call was made")
	}
}

func TestExecutePlanStopsOnAPIError(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "run-error", 1)
	client := &stubClient{failOn: "IssueInvoice", failAfter: 1}

	manifest, err := ExecutePlan(context.Background(), client, plan, RunOptions{Clock: testClock})
	if err == nil {
		t.Fatal("ExecutePlan returned no error even though IssueInvoice was made to fail")
	}

	// The first invoice's customer and draft invoice were created before the
	// issue call failed, so the manifest must carry that partial item.
	if len(manifest.Items) == 0 {
		t.Fatal("no items recorded even though a customer and a draft invoice were created")
	}
	first := manifest.Items[0]
	if first.CustomerID == "" || first.ID == "" {
		t.Errorf("partial item missing the customer or invoice id it did manage to create: %+v", first)
	}
	if first.OrderID != "" || first.ShortURL != "" {
		t.Errorf("partial item claims an order id or short url from a call that never succeeded: %+v", first)
	}
	// The stub never got past the first invoice's issue call, so nothing
	// after it in the plan should have been attempted.
	if len(manifest.Items) != 1 {
		t.Errorf("len(Items) = %d, want exactly 1 (the run must stop at the first failure)", len(manifest.Items))
	}
}

// TestExecutePlanSimulatedAtRiskSinceMatchesAgeBucket covers both kinds. Orders
// carry a bucket for the same reason invoices do: policy.GraceUnpaidOrder is an
// hour and nothing in the API can backdate an order either, so an order that is
// meant to look aged says so in the manifest and nowhere else.
func TestExecutePlanSimulatedAtRiskSinceMatchesAgeBucket(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "run-age", 1)
	client := &stubClient{}

	manifest, err := ExecutePlan(context.Background(), client, plan, RunOptions{Clock: testClock})
	if err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}

	now := testClock.Now()
	var agedOrders int
	for _, item := range manifest.Items {
		if item.AgeBucket == "" {
			t.Errorf("%s %s carries no age bucket at all", item.Kind, item.ID)
			continue
		}
		wantDays := item.AgeBucket.AgeDays()
		wantSince := now.Add(-time.Duration(wantDays) * 24 * time.Hour).Unix()
		if item.SimulatedAtRiskSince != wantSince {
			t.Errorf("%s %s age_bucket=%s: SimulatedAtRiskSince = %d, want %d",
				item.Kind, item.ID, item.AgeBucket, item.SimulatedAtRiskSince, wantSince)
		}
		if item.Kind == EntityOrder && wantDays > 0 {
			agedOrders++
		}
	}
	if agedOrders != 2 {
		t.Errorf("the demo manifest carries %d aged order(s), want 2", agedOrders)
	}
}

// TestExecutePlanOrderNotesCarryTheDetectorsContactKeys is the coordination
// check with internal/detect: UnpaidOrderDetector's customerFromNotes reads
// a contact off an order under detect.NoteKeyCustomerName/Email/Contact, and
// a seeded order that is supposed to be notifiable has to actually carry
// those exact keys, not merely claim a contact in the manifest.
func TestExecutePlanOrderNotesCarryTheDetectorsContactKeys(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "run-notes", 1)
	client := &stubClient{}

	manifest, err := ExecutePlan(context.Background(), client, plan, RunOptions{Clock: testClock})
	if err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}

	orderIndex := 0
	for _, item := range manifest.Items {
		if item.Kind != EntityOrder {
			continue
		}
		notes := client.orderNotes[orderIndex]
		orderIndex++

		if item.Flags.NoContact {
			for _, key := range []string{detect.NoteKeyCustomerName, detect.NoteKeyCustomerEmail, detect.NoteKeyCustomerContact} {
				if _, present := notes[key]; present {
					t.Errorf("order %s is flagged no_contact but its notes still carry %s=%q", item.ID, key, notes[key])
				}
			}
			continue
		}

		if notes[detect.NoteKeyCustomerName] != item.CustomerName {
			t.Errorf("order %s notes[%s] = %q, want the item's CustomerName %q",
				item.ID, detect.NoteKeyCustomerName, notes[detect.NoteKeyCustomerName], item.CustomerName)
		}
		if notes[detect.NoteKeyCustomerEmail] != item.CustomerEmail {
			t.Errorf("order %s notes[%s] = %q, want the item's CustomerEmail %q",
				item.ID, detect.NoteKeyCustomerEmail, notes[detect.NoteKeyCustomerEmail], item.CustomerEmail)
		}
		if notes[detect.NoteKeyCustomerContact] != item.CustomerContact {
			t.Errorf("order %s notes[%s] = %q, want the item's CustomerContact %q",
				item.ID, detect.NoteKeyCustomerContact, notes[detect.NoteKeyCustomerContact], item.CustomerContact)
		}
	}
	if orderIndex == 0 {
		t.Fatal("no order items were produced by the demo profile")
	}
}
