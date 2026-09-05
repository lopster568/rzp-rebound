package seed

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
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

// paceRecorder stands in for the wall-clock sleep the pace is waited out on.
// It records every interval and returns immediately, so a test spends no real
// time on a rate limit it is not talking to.
type paceRecorder struct {
	waits []time.Duration
}

func (p *paceRecorder) sleep(_ context.Context, d time.Duration) error {
	p.waits = append(p.waits, d)
	return nil
}

// testRun is the RunOptions every test in this file starts from: the fake
// clock, and a pace that is recorded rather than waited.
func testRun() (RunOptions, *paceRecorder) {
	rec := &paceRecorder{}
	return RunOptions{Clock: testClock, Sleep: rec.sleep}, rec
}

// runOpts is testRun for the tests that do not read the recorder.
func runOpts() RunOptions {
	opts, _ := testRun()
	return opts
}

func withBudget(opts RunOptions, n int) RunOptions {
	opts.CallBudget = n
	return opts
}

func TestExecutePlanHappyPath(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "run-happy", 1)
	client := &stubClient{}

	manifest, err := ExecutePlan(context.Background(), client, plan, runOpts())
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
	manifest, err := ExecutePlan(context.Background(), client, plan, withBudget(runOpts(), 2))
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

	manifest, err := ExecutePlan(context.Background(), client, plan, runOpts())
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

	manifest, err := ExecutePlan(context.Background(), client, plan, runOpts())
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

	manifest, err := ExecutePlan(context.Background(), client, plan, runOpts())
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

// TestExecutePlanPacesItsCreationCalls. Razorpay test mode answered 429 at
// call 17 of 27 on 2026-09-05, twice, when the seeder made its calls back to
// back. There is a wait before every call after the first, and there is no
// wait before the first, because a run that has made no call yet has nothing
// to pace itself against.
func TestExecutePlanPacesItsCreationCalls(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "run-pace", 1)
	client := &stubClient{}
	opts, rec := testRun()

	manifest, err := ExecutePlan(context.Background(), client, plan, opts)
	if err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}

	if want := plan.CallEstimate() - 1; len(rec.waits) != want {
		t.Fatalf("waited %d time(s) for %d call(s), want %d", len(rec.waits), manifest.CallsUsed, want)
	}
	for i, d := range rec.waits {
		if d != DefaultPace {
			t.Fatalf("wait %d was %s, want the default pace %s", i, d, DefaultPace)
		}
	}
	// The rate this puts the seeder at. It is politeness rather than the
	// rate-limit fix: the 429 turned out to be a burst quota that no interval
	// prevents, and TestExecutePlanWaitsOutTheInvoiceBurstQuota is what covers
	// that. What this bound still says is that the run stays under the 1.3 to
	// 1.4 calls per second the 2026-08-31 probe ran clean at.
	if perSecond := float64(time.Second) / float64(DefaultPace); perSecond > 1.4 {
		t.Errorf("the default pace is %g calls per second, which is over the rate the 2026-08-31 probe ran clean at", perSecond)
	}
}

// TestExecutePlanPaceIsConfigurable. A caller that wants the calls back to back
// asks for an interval small enough to be irrelevant and thereby says so.
func TestExecutePlanPaceIsConfigurable(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "run-pace-off", 1)
	opts, rec := testRun()
	opts.Pace = time.Nanosecond

	if _, err := ExecutePlan(context.Background(), &stubClient{}, plan, opts); err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}
	for i, d := range rec.waits {
		if d != time.Nanosecond {
			t.Fatalf("wait %d was %s, want the configured 1ns", i, d)
		}
	}
}

// TestExecutePlanPrintsInstructionsOnAPartialSeed is the defect the 2026-09-05
// rehearsal hit twice: the operator to-do list was built after both creation
// loops, so a run that stopped at a 429 left the demoer with no URLs on screen
// and no way to reach the browser step.
func TestExecutePlanPrintsInstructionsOnAPartialSeed(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "run-partial", 1)

	// Two invoices are issued, and the third one's create call is refused.
	// Calls run customer, invoice, issue per invoice, so call 8 is the third
	// invoice's own create.
	client := &stubClient{failOn: "CreateInvoice", failAfter: 8}
	manifest, err := ExecutePlan(context.Background(), client, plan, runOpts())
	if err == nil {
		t.Fatal("ExecutePlan returned no error even though CreateInvoice was made to fail")
	}

	if len(manifest.Instructions.Targets) == 0 {
		t.Fatal("a partial seed that issued invoices printed no links to fail by hand")
	}
	if len(manifest.Instructions.TestCards) == 0 {
		t.Error("a partial seed printed no test cards")
	}
	for _, target := range manifest.Instructions.Targets {
		if target.URL == "" {
			t.Errorf("instruction target %s has no URL: %+v", target.ID, target)
		}
	}
}

// TestExecutePlanPrintsInstructionsOnABudgetStop is the same guarantee on the
// other early exit.
func TestExecutePlanPrintsInstructionsOnABudgetStop(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "run-partial-budget", 1)

	// Six calls is two whole invoices and nothing more.
	manifest, err := ExecutePlan(context.Background(), &stubClient{}, plan, withBudget(runOpts(), 6))
	if !errors.Is(err, ErrCallBudgetExceeded) {
		t.Fatalf("error = %v, want it to wrap ErrCallBudgetExceeded", err)
	}
	if len(manifest.Instructions.Targets) != 2 {
		t.Errorf("%d link(s) to fail by hand, want the 2 invoices the budget did issue: %+v",
			len(manifest.Instructions.Targets), manifest.Instructions.Targets)
	}
}

// TestExecutePlanMarksAnItemItDidNotFinish. A customer with no invoice behind
// it has no id anything can read back, and riskrun.Poll used to drop it inside
// a nil check, which made a short snapshot look like a complete one.
func TestExecutePlanMarksAnItemItDidNotFinish(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "run-incomplete", 1)

	// The first invoice's create call fails, after its customer was made.
	client := &stubClient{failOn: "CreateInvoice", failAfter: 2}
	manifest, err := ExecutePlan(context.Background(), client, plan, runOpts())
	if err == nil {
		t.Fatal("ExecutePlan returned no error")
	}
	if len(manifest.Items) != 1 {
		t.Fatalf("len(Items) = %d, want 1", len(manifest.Items))
	}
	item := manifest.Items[0]
	if item.ID != "" {
		t.Fatalf("the item claims an id from a call that never succeeded: %+v", item)
	}
	if !item.Incomplete {
		t.Error("the item has no id and is not marked incomplete, so nothing downstream can tell it apart from a readable entity")
	}
	if item.CustomerID == "" {
		t.Error("the item dropped the customer id it did create, which is the only handle on what exists in Razorpay")
	}
}

// TestExecutePlanResumesAnEarlierAttempt is the answer to a seed that 429'd
// partway: a second invocation at the same run tag continues the book rather
// than seeding a parallel one, which is what orphaned five invoices and five
// orders on 2026-09-05.
func TestExecutePlanResumesAnEarlierAttempt(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "run-resume", 1)

	// The first attempt gets two whole invoices and the third one's customer.
	first, err := ExecutePlan(context.Background(), &stubClient{}, plan, withBudget(runOpts(), 7))
	if !errors.Is(err, ErrCallBudgetExceeded) {
		t.Fatalf("first attempt error = %v, want it to wrap ErrCallBudgetExceeded", err)
	}
	if len(first.Items) != 3 {
		t.Fatalf("the first attempt recorded %d item(s), want 3: two invoices and one bare customer", len(first.Items))
	}

	client := &stubClient{}
	opts := runOpts()
	opts.Resume = first
	second, err := ExecutePlan(context.Background(), client, plan, opts)
	if err != nil {
		t.Fatalf("the resumed run: %v", err)
	}

	if want := len(plan.Invoices) + len(plan.Orders); len(second.Items) != want {
		t.Fatalf("the resumed manifest holds %d item(s), want the whole book of %d", len(second.Items), want)
	}
	if second.ResumedItems != 2 {
		t.Errorf("ResumedItems = %d, want the 2 completed items carried over", second.ResumedItems)
	}
	for _, item := range second.Items {
		if item.Incomplete || item.ID == "" {
			t.Errorf("the resumed manifest still carries an unfinished item: %+v", item)
		}
	}

	// The two invoices the first attempt finished are not created again, and
	// the customer it did create for the third is reused rather than duplicated.
	if want := len(plan.Invoices) - 2; client.invoices != want {
		t.Errorf("the resumed run created %d invoice(s), want %d", client.invoices, want)
	}
	if want := len(plan.Invoices) - 3; client.customers != want {
		t.Errorf("the resumed run created %d customer(s), want %d: two invoices were done and one already had its customer",
			client.customers, want)
	}
	if client.orders != len(plan.Orders) {
		t.Errorf("the resumed run created %d order(s), want %d", client.orders, len(plan.Orders))
	}

	// The manifest keeps the first attempt's clock reading, because risk-run
	// derives its default --since floor from it and moving it forward would
	// scope the sweep past what the first attempt created.
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("CreatedAt = %s, want the first attempt's %s", second.CreatedAt, first.CreatedAt)
	}
}

// TestExecutePlanResumeRefusesAManifestFromAnotherPlan. Positional resumption
// is only safe while the plan is the same one, so the mismatch is an error
// rather than a guess that would leave uncatalogued invoices in Razorpay.
func TestExecutePlanResumeRefusesAManifestFromAnotherPlan(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "run-resume-tag", 1)
	other, err := ExecutePlan(context.Background(), &stubClient{}, GeneratePlan(DemoProfile(), "some-other-run", 1), runOpts())
	if err != nil {
		t.Fatalf("seeding the other book: %v", err)
	}

	t.Run("a different run tag", func(t *testing.T) {
		opts := runOpts()
		opts.Resume = other
		client := &stubClient{}
		if _, err := ExecutePlan(context.Background(), client, plan, opts); !errors.Is(err, ErrResumeMismatch) {
			t.Fatalf("error = %v, want it to wrap ErrResumeMismatch", err)
		}
		if client.calls != 0 {
			t.Errorf("the refused run still made %d call(s)", client.calls)
		}
	})

	t.Run("the same tag over a different seed", func(t *testing.T) {
		// The run tag matches and the amounts do not, which is what a
		// different -seed or -profile at the same tag produces.
		prior, err := ExecutePlan(context.Background(), &stubClient{},
			GeneratePlan(DemoProfile(), "run-resume-tag", 99), withBudget(runOpts(), 6))
		if !errors.Is(err, ErrCallBudgetExceeded) {
			t.Fatalf("seeding the differently seeded book: %v", err)
		}
		opts := runOpts()
		opts.Resume = prior
		client := &stubClient{}
		if _, err := ExecutePlan(context.Background(), client, plan, opts); !errors.Is(err, ErrResumeMismatch) {
			t.Fatalf("error = %v, want it to wrap ErrResumeMismatch", err)
		}
		if client.calls != 0 {
			t.Errorf("the refused run still made %d call(s)", client.calls)
		}
	})
}

// TestPlanRemainingReportsWhatIsLeft is what the command reads to tell an
// operator what is already on disk before it decides to resume or refuse.
func TestPlanRemainingReportsWhatIsLeft(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "run-remaining", 1)

	invoices, orders, err := plan.Remaining(Manifest{})
	if err != nil {
		t.Fatalf("Remaining over an empty manifest: %v", err)
	}
	if invoices != len(plan.Invoices) || orders != len(plan.Orders) {
		t.Errorf("Remaining over nothing = %d, %d, want %d, %d", invoices, orders, len(plan.Invoices), len(plan.Orders))
	}

	partial, err := ExecutePlan(context.Background(), &stubClient{}, plan, withBudget(runOpts(), 6))
	if !errors.Is(err, ErrCallBudgetExceeded) {
		t.Fatalf("seeding a partial book: %v", err)
	}
	invoices, orders, err = plan.Remaining(partial)
	if err != nil {
		t.Fatalf("Remaining over a partial manifest: %v", err)
	}
	if want := len(plan.Invoices) - 2; invoices != want {
		t.Errorf("%d invoice(s) left, want %d", invoices, want)
	}
	if orders != len(plan.Orders) {
		t.Errorf("%d order(s) left, want %d", orders, len(plan.Orders))
	}

	whole, err := ExecutePlan(context.Background(), &stubClient{}, plan, runOpts())
	if err != nil {
		t.Fatalf("seeding the whole book: %v", err)
	}
	invoices, orders, err = plan.Remaining(whole)
	if err != nil {
		t.Fatalf("Remaining over a complete manifest: %v", err)
	}
	if invoices != 0 || orders != 0 {
		t.Errorf("Remaining over a complete book = %d, %d, want 0, 0", invoices, orders)
	}
}

// burstQuotaClient answers 429 to CreateInvoice once a fixed number of invoice
// creations have been made, and keeps answering 429 until release is called.
//
// That is the shape live test mode showed three times on 2026-09-05, and it is
// a quota rather than a rate: the sixth POST /v1/invoices failed at every pace
// tried, including 0.80 calls per second, an immediate retry failed too, and a
// retry about 45 seconds later succeeded on its first attempt. The error it
// returns is the one razorpay.Client produces when its own four attempts are
// spent, so the seeder is tested against the value it will actually be handed.
type burstQuotaClient struct {
	stubClient

	// quota is how many invoice creations succeed before the window closes.
	quota int
	// used is how many have been made in the current window. release resets it,
	// which is what makes an immediate retry fail and a retry after a wait
	// succeed.
	used int
	// refusals counts the 429s handed out, so a test can say the quota fired.
	refusals int
}

func (c *burstQuotaClient) release() { c.used = 0 }

func (c *burstQuotaClient) CreateInvoice(ctx context.Context, req razorpay.CreateInvoiceRequest) (razorpay.Invoice, error) {
	if c.used >= c.quota {
		c.refusals++
		return razorpay.Invoice{}, fmt.Errorf("%w after 4 attempt(s): %w",
			razorpay.ErrRetryBudgetExhausted,
			&razorpay.APIError{StatusCode: http.StatusTooManyRequests, Method: http.MethodPost, Path: "/invoices"})
	}
	c.used++
	return c.stubClient.CreateInvoice(ctx, req)
}

// burstRecorder is paceRecorder plus the one thing the quota needs: a wait long
// enough to be a burst wait clears the client's window, so the test models a
// recovery that takes time rather than one that takes a retry.
type burstRecorder struct {
	paceRecorder
	client *burstQuotaClient
	burst  time.Duration
}

func (b *burstRecorder) sleep(ctx context.Context, d time.Duration) error {
	if d == b.burst {
		b.client.release()
	}
	return b.paceRecorder.sleep(ctx, d)
}

func (b *burstRecorder) burstWaits() []time.Duration {
	var out []time.Duration
	for _, d := range b.waits {
		if d == b.burst {
			out = append(out, d)
		}
	}
	return out
}

// TestExecutePlanWaitsOutTheInvoiceBurstQuota is the 2026-09-05 defect. The
// 750ms pace cannot prevent a quota, so a seed run that stopped on the sixth
// invoice needed a second process to finish the book. It now waits and resumes
// the same call inside one run, and the book it writes is whole and has no
// entity in it twice.
func TestExecutePlanWaitsOutTheInvoiceBurstQuota(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "run-burst", 1)
	client := &burstQuotaClient{quota: 5}
	rec := &burstRecorder{client: client, burst: DefaultBurstWait}
	var log strings.Builder

	manifest, err := ExecutePlan(context.Background(), client, plan, RunOptions{
		Clock: testClock,
		Sleep: rec.sleep,
		Log:   &log,
	})
	if err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}

	if client.refusals == 0 {
		t.Fatal("the quota never fired, so this test proves nothing")
	}
	waits := rec.burstWaits()
	if len(waits) == 0 {
		t.Fatalf("no burst wait was taken for %d refusal(s)", client.refusals)
	}
	for i, d := range waits {
		if d != DefaultBurstWait {
			t.Errorf("burst wait %d was %s, want %s", i, d, DefaultBurstWait)
		}
	}
	if !strings.Contains(log.String(), "invoice burst quota hit") {
		t.Errorf("the operator was told nothing about the wait, log:\n%s", log.String())
	}

	if want := len(plan.Invoices) + len(plan.Orders); len(manifest.Items) != want {
		t.Fatalf("len(Items) = %d, want %d", len(manifest.Items), want)
	}
	if client.invoices != len(plan.Invoices) {
		t.Errorf("created %d invoice(s), want %d", client.invoices, len(plan.Invoices))
	}
	if client.customers != len(plan.Invoices) {
		t.Errorf("created %d customer(s), want %d", client.customers, len(plan.Invoices))
	}
	if client.issues != len(plan.Invoices) {
		t.Errorf("issued %d invoice(s), want %d", client.issues, len(plan.Invoices))
	}
	if client.orders != len(plan.Orders) {
		t.Errorf("created %d order(s), want %d", client.orders, len(plan.Orders))
	}

	seen := map[string]bool{}
	for _, item := range manifest.Items {
		if item.ID == "" || item.Incomplete {
			t.Errorf("item is not finished: %+v", item)
			continue
		}
		if seen[item.ID] {
			t.Errorf("item %s is in the manifest twice", item.ID)
		}
		seen[item.ID] = true
	}
}

// TestExecutePlanStopsAfterTheBurstWaitBudget. The waiting is bounded: an
// account that answers 429 forever gets a run that stops and a manifest that
// records what it created, not a process that waits all night.
func TestExecutePlanStopsAfterTheBurstWaitBudget(t *testing.T) {
	plan := GeneratePlan(DemoProfile(), "run-burst-budget", 1)
	client := &burstQuotaClient{quota: 5}
	rec := &paceRecorder{}

	manifest, err := ExecutePlan(context.Background(), client, plan, RunOptions{
		Clock:         testClock,
		Sleep:         rec.sleep,
		MaxBurstWaits: 2,
	})
	if err == nil {
		t.Fatal("ExecutePlan returned no error over an account that never recovers")
	}
	var apiErr *razorpay.APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("ExecutePlan error = %v, want one carrying a 429", err)
	}

	var burstWaits int
	for _, d := range rec.waits {
		if d == DefaultBurstWait {
			burstWaits++
		}
	}
	if burstWaits != 2 {
		t.Errorf("took %d burst wait(s), want the configured 2", burstWaits)
	}
	// Five invoices were created and the sixth got as far as its customer, so
	// the manifest holds six items and the last one is marked unfinished. That
	// is the same shape a budget stop leaves, and it is what makes the run
	// resumable across processes when the waiting was not enough.
	if len(manifest.Items) != 6 {
		t.Fatalf("the manifest records %d item(s), want 5 created invoices plus the one that stopped", len(manifest.Items))
	}
	last := manifest.Items[len(manifest.Items)-1]
	if !last.Incomplete || last.ID != "" || last.CustomerID == "" {
		t.Errorf("the last item is %+v, want an unfinished invoice carrying only its customer", last)
	}
}
