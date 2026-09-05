package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/seed"
)

// stubSeeder is seed.Client without a network call. It exists here only to
// write a realistic partial manifest for planFor to read; what ExecutePlan does
// with it is internal/seed's own tests' business.
type stubSeeder struct{ n int }

func (s *stubSeeder) CreateCustomer(context.Context, razorpay.CreateCustomerRequest) (razorpay.Customer, error) {
	s.n++
	return razorpay.Customer{ID: id("cust", s.n)}, nil
}

func (s *stubSeeder) CreateInvoice(context.Context, razorpay.CreateInvoiceRequest) (razorpay.Invoice, error) {
	s.n++
	return razorpay.Invoice{ID: id("inv", s.n), Status: razorpay.InvoiceStatusDraft}, nil
}

func (s *stubSeeder) IssueInvoice(_ context.Context, invoiceID string) (razorpay.Invoice, error) {
	s.n++
	return razorpay.Invoice{
		ID: invoiceID, Status: razorpay.InvoiceStatusIssued,
		OrderID: "order_" + invoiceID, ShortURL: "https://rzp.io/i/" + invoiceID,
	}, nil
}

func (s *stubSeeder) CreateOrder(_ context.Context, req razorpay.CreateOrderRequest) (razorpay.Order, error) {
	s.n++
	return razorpay.Order{ID: id("order", s.n), AmountPaise: req.AmountPaise, Status: razorpay.OrderStatusCreated}, nil
}

func id(prefix string, n int) string { return prefix + "_" + time.Duration(n).String() }

// seedTo writes a manifest at path for the plan, stopping after budget calls,
// and returns it.
func seedTo(t *testing.T, path string, plan seed.Plan, budget int) seed.Manifest {
	t.Helper()
	manifest, err := seed.ExecutePlan(context.Background(), &stubSeeder{}, plan, seed.RunOptions{
		CallBudget: budget,
		Pace:       time.Nanosecond,
	})
	if err != nil && !errors.Is(err, seed.ErrCallBudgetExceeded) {
		t.Fatalf("ExecutePlan: %v", err)
	}
	if err := manifest.Write(path); err != nil {
		t.Fatalf("write the manifest: %v", err)
	}
	return manifest
}

// TestPlanForContinuesAnUnfinishedManifest is the fix for what happened twice
// on 2026-09-05: a seed run stopped at a 429, the operator re-ran it, and the
// second run wrote a fresh manifest over the only file that named the first
// run's five invoices, five orders, and six customers.
func TestPlanForContinuesAnUnfinishedManifest(t *testing.T) {
	out := filepath.Join(t.TempDir(), "seedbook.json")
	profile := seed.DemoProfile()
	first := seedTo(t, out, seed.GeneratePlan(profile, "seedbook-1", 1234), 7)

	// The second invocation names no run tag, exactly as `make seedbook` does.
	plan, resume, note, err := planFor(profile, "seedbook-2", false, 1234, out, false)
	if err != nil {
		t.Fatalf("planFor: %v", err)
	}
	if plan.RunTag != "seedbook-1" {
		t.Errorf("the run tag is %q, want the unfinished run's %q", plan.RunTag, "seedbook-1")
	}
	if len(resume.Items) != len(first.Items) {
		t.Errorf("resuming from %d item(s), want the %d the first attempt recorded", len(resume.Items), len(first.Items))
	}
	if note == "" {
		t.Error("the operator was told nothing about the manifest already on disk")
	}

	invoices, orders, err := plan.Remaining(resume)
	if err != nil {
		t.Fatalf("Remaining: %v", err)
	}
	if want := len(plan.Invoices) - 2; invoices != want {
		t.Errorf("%d invoice(s) left, want %d", invoices, want)
	}
	if orders != len(plan.Orders) {
		t.Errorf("%d order(s) left, want %d", orders, len(plan.Orders))
	}
}

// TestPlanForRefusesToSeedOverAFinishedBook. Overwriting a complete manifest is
// the same loss as overwriting a partial one, with more items in it.
func TestPlanForRefusesToSeedOverAFinishedBook(t *testing.T) {
	out := filepath.Join(t.TempDir(), "seedbook.json")
	profile := seed.DemoProfile()
	seedTo(t, out, seed.GeneratePlan(profile, "seedbook-1", 1234), seed.DefaultCallBudget)

	if _, _, _, err := planFor(profile, "seedbook-2", false, 1234, out, false); err == nil {
		t.Fatal("planFor seeded a second book over a complete manifest")
	}

	// -force is the operator saying they know what is on disk.
	plan, resume, _, err := planFor(profile, "seedbook-2", false, 1234, out, true)
	if err != nil {
		t.Fatalf("planFor with -force: %v", err)
	}
	if plan.RunTag != "seedbook-2" || len(resume.Items) != 0 {
		t.Errorf("-force did not seed a fresh book: tag %q, %d resumed item(s)", plan.RunTag, len(resume.Items))
	}
}

// TestPlanForRefusesAManifestItCannotContinue. A named run tag that disagrees
// with what is on disk is a book this run has no position in, so it says so
// rather than creating from a guessed offset.
func TestPlanForRefusesAManifestItCannotContinue(t *testing.T) {
	out := filepath.Join(t.TempDir(), "seedbook.json")
	profile := seed.DemoProfile()
	seedTo(t, out, seed.GeneratePlan(profile, "seedbook-1", 1234), 7)

	if _, _, _, err := planFor(profile, "a-different-tag", true, 1234, out, false); err == nil {
		t.Fatal("planFor continued a manifest written under another run tag")
	}
}

// TestPlanForWithNothingOnDisk is the ordinary first run.
func TestPlanForWithNothingOnDisk(t *testing.T) {
	out := filepath.Join(t.TempDir(), "seedbook.json")
	plan, resume, note, err := planFor(seed.DemoProfile(), "seedbook-1", false, 1234, out, false)
	if err != nil {
		t.Fatalf("planFor: %v", err)
	}
	if plan.RunTag != "seedbook-1" || len(resume.Items) != 0 || note != "" {
		t.Errorf("a first run got tag %q, %d resumed item(s), and the note %q", plan.RunTag, len(resume.Items), note)
	}
}
