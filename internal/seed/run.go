package seed

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/detect"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
)

// DefaultCallBudget caps how many Razorpay calls one seed run makes. It is a
// safety rail, not a measurement of what the demo profile needs: that plan
// costs one CreateCustomer, one CreateInvoice, and one IssueInvoice per
// invoice plus one CreateOrder per order, which is under 30 calls for
// DemoProfile. 60 leaves headroom for a larger profile without leaving the
// cap effectively unlimited.
const DefaultCallBudget = 60

// DefaultPace is how long ExecutePlan waits between two creation calls.
//
// Razorpay test mode answered 429 to POST /invoices on 2026-09-05, twice, at
// call 17 of a 27-call demo seed, when the run made its calls back to back at
// 1.9 to 2.1 writes per second. The account's own probe on 2026-08-31 made 40
// sequential calls at 1.3 to 1.4 per second and saw none, so the limit sits
// somewhere between those two rates. razorpay.Client retries a 429 four times
// with 250ms, 500ms and 1s of backoff, which is 1.75s of budget, and that was
// not enough to get through.
//
// The pace is therefore set under the rate that is known to be safe rather than
// just under the rate that is known to fail: 750ms between calls is 1.33 calls
// per second, and it costs about 20 seconds on the 27 calls DemoProfile spends.
// A seed run is a setup step nobody is filming, so 20 seconds is the cheapest
// thing in this fix.
const DefaultPace = 750 * time.Millisecond

// ErrCallBudgetExceeded is returned when a run stops because the next call
// would spend more than its budget allows. Whatever was created before the
// stop is still in Razorpay and still in the returned manifest: this package
// never deletes anything, including on its own budget refusal.
var ErrCallBudgetExceeded = errors.New("seed: call budget exceeded")

// ErrResumeMismatch is returned when RunOptions.Resume holds a manifest that
// is not an earlier attempt at the plan being executed. Resuming across two
// different plans would skip items by position that were never created, so it
// refuses instead of guessing.
var ErrResumeMismatch = errors.New("seed: the manifest to resume from does not match this plan")

// RunOptions configures ExecutePlan.
type RunOptions struct {
	// CallBudget caps API calls. Zero or negative means DefaultCallBudget.
	CallBudget int
	// Clock is read once, at the start of the run, for every item's
	// CreatedAt and for computing SimulatedAtRiskSince from its age bucket.
	// Nil means the wall clock.
	Clock clock.Clock
	// Pace is the minimum interval between two creation calls. Zero or negative
	// means DefaultPace, which follows CallBudget's convention in this struct.
	// A caller that wants the old back-to-back behaviour asks for an interval
	// small enough to be irrelevant, such as one nanosecond, and thereby says so
	// in its own flags.
	Pace time.Duration
	// Sleep is how the pace is waited out. Nil means a context-aware sleep on
	// the wall clock. A test passes one that records the interval and returns.
	Sleep func(ctx context.Context, d time.Duration) error
	// Resume is the manifest an earlier attempt at this same plan wrote. Its
	// completed items are carried into the returned manifest and the plan
	// entries behind them are not created a second time. The zero value means
	// a fresh run.
	Resume Manifest
}

// pacer waits out the configured interval before every call except the first.
type pacer struct {
	interval time.Duration
	sleep    func(context.Context, time.Duration) error
	started  bool
}

func (p *pacer) wait(ctx context.Context) error {
	if !p.started {
		p.started = true
		return nil
	}
	if p.interval <= 0 {
		return nil
	}
	return p.sleep(ctx, p.interval)
}

// sleepContext waits for d, or until ctx is done. A cancelled seed run stops
// waiting rather than finishing its pace and then noticing.
func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// budget refuses a call that would cross its cap before making it, rather
// than after.
type budget struct {
	max, used int
}

func (b *budget) spend(n int) error {
	if b.used+n > b.max {
		return fmt.Errorf("%w: %d call(s) already spent, %d more would pass the cap of %d",
			ErrCallBudgetExceeded, b.used, n, b.max)
	}
	b.used += n
	return nil
}

// ExecutePlan creates every item in plan against client, in the order the
// plan lists them, and returns the manifest ground truth.
//
// A budget refusal or an API error stops the run and returns the manifest
// with everything created before the failing call, alongside a non-nil
// error. The caller is expected to write that manifest out regardless: a
// half-finished run has still spent real API calls and created real
// resources, and a manifest that silently drops them is worse than one that
// reports the run stopped early. Nothing already created is ever rolled
// back; this package has no delete path, by design (see the seeding brief's
// own no-deletes rule), and adding one to unwind a partial run would still
// be adding a delete path.
// The operator instruction block is built on every exit path, not only the
// happy one. A seed that stopped at its ninth call has still issued invoices
// with payable links on them, and a demoer looking at a terminal with no URLs
// in it has no way to reach the browser step. That was the shape of the
// 2026-09-05 rehearsal: two 429s, two partial seeds, and nothing on screen to
// open.
func ExecutePlan(ctx context.Context, client Client, plan Plan, opts RunOptions) (Manifest, error) {
	m, err := executePlan(ctx, client, plan, opts)

	instructions, buildErr := buildInstructions(m.Items)
	if buildErr == nil {
		m.Instructions = instructions
	} else if err == nil {
		// The run's own failure is the one the operator has to act on, so it
		// wins. A failure to render the to-do list only surfaces when there is
		// nothing more important to report.
		err = fmt.Errorf("build the operator instructions: %w", buildErr)
	}
	return m, err
}

func executePlan(ctx context.Context, client Client, plan Plan, opts RunOptions) (Manifest, error) {
	clk := opts.Clock
	if clk == nil {
		clk = clock.Real()
	}
	max := opts.CallBudget
	if max <= 0 {
		max = DefaultCallBudget
	}
	b := &budget{max: max}

	pace := opts.Pace
	if pace <= 0 {
		pace = DefaultPace
	}
	sleep := opts.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	p := &pacer{interval: pace, sleep: sleep}

	now := clk.Now()
	m := Manifest{
		RunTag:     plan.RunTag,
		CreatedAt:  now,
		Gateway:    GatewayLiveTestMode,
		CallBudget: max,
	}

	done, err := resumeFrom(opts.Resume, plan)
	if err != nil {
		return m, err
	}
	m.Items = append(m.Items, done.items...)
	m.ResumedItems = len(done.items)
	if !done.createdAt.IsZero() {
		m.CreatedAt = done.createdAt
	}

	for i := done.invoices; i < len(plan.Invoices); i++ {
		item, err := createInvoiceItem(ctx, client, b, p, plan.Invoices[i], plan.RunTag, now, done.customerFor[i])
		m.CallsUsed = b.used
		if item.CustomerID != "" || item.ID != "" {
			// Something was actually created in Razorpay, even if a later
			// call in this same item failed. It belongs in the ground truth
			// either way, marked so nothing downstream mistakes it for an
			// entity it can read.
			item.Incomplete = item.ID == ""
			m.Items = append(m.Items, item)
		}
		if err != nil {
			return m, err
		}
	}

	for i := done.orders; i < len(plan.Orders); i++ {
		item, err := createOrderItem(ctx, client, b, p, plan.Orders[i], plan.RunTag, now)
		m.CallsUsed = b.used
		if item.ID != "" {
			m.Items = append(m.Items, item)
		}
		if err != nil {
			return m, err
		}
	}

	return m, nil
}

// resumed is how much of a plan an earlier attempt at it already did.
type resumed struct {
	// invoices and orders are how many entries at the head of each plan list
	// are already created, so this run starts after them.
	invoices, orders int
	// items are the completed items to carry into the new manifest.
	items []Item
	// customerFor maps a plan invoice index to the customer an earlier attempt
	// created for it and never got an invoice out of. Reusing that id skips a
	// CreateCustomer call and, for the deliberately contactless invoice whose
	// synthetic email is empty, avoids minting a second customer that no email
	// can ever match back to the first.
	customerFor map[int]string
	// createdAt is the earlier attempt's own clock reading. See
	// Manifest.ResumedItems for why the new manifest keeps it.
	createdAt time.Time
}

// resumeFrom reads an earlier manifest as a position in plan.
//
// Resumption is positional because that is the only thing this package can
// honestly key on. Razorpay's list endpoints take count, skip, and a created_at
// range and nothing else (razorpay.ListOptions), so there is no server-side
// query for "the invoices tagged with this run", and the run tag lives only in
// notes. What is reliable instead is that GeneratePlan is deterministic in
// profile, run tag and seed, and ExecutePlan creates in plan order: invoices
// first, then orders. So a manifest for the same run tag names a prefix of the
// same plan.
//
// That assumption is checked rather than trusted. The run tags have to match,
// every carried item's amount has to equal the plan entry it is standing in
// for, and an item with no id has to be the last one, because the run stops at
// its first failure. Anything else is ErrResumeMismatch, which is the honest
// answer: creating from a wrong position would leave real invoices in Razorpay
// that no manifest names.
func resumeFrom(prior Manifest, plan Plan) (resumed, error) {
	out := resumed{customerFor: map[int]string{}}
	if prior.RunTag == "" && len(prior.Items) == 0 {
		return out, nil
	}
	if prior.RunTag != plan.RunTag {
		return out, fmt.Errorf("%w: it is run tag %q and this plan is %q",
			ErrResumeMismatch, prior.RunTag, plan.RunTag)
	}
	out.createdAt = prior.CreatedAt

	for i, item := range prior.Items {
		if item.ID == "" {
			if i != len(prior.Items)-1 {
				return resumed{}, fmt.Errorf("%w: item %d has no id but is not the last one, so the run it came from did not stop at its first failure",
					ErrResumeMismatch, i)
			}
			if item.Kind == EntityInvoice && item.CustomerID != "" {
				out.customerFor[out.invoices] = item.CustomerID
			}
			continue
		}

		switch item.Kind {
		case EntityInvoice:
			if out.invoices >= len(plan.Invoices) {
				return resumed{}, fmt.Errorf("%w: it holds %d invoice(s) and the plan has %d",
					ErrResumeMismatch, out.invoices+1, len(plan.Invoices))
			}
			if want := plan.Invoices[out.invoices].AmountPaise; item.AmountPaise != want {
				return resumed{}, fmt.Errorf("%w: its invoice %d is %d paise and the plan's is %d",
					ErrResumeMismatch, out.invoices, item.AmountPaise, want)
			}
			out.invoices++
		case EntityOrder:
			if out.orders >= len(plan.Orders) {
				return resumed{}, fmt.Errorf("%w: it holds %d order(s) and the plan has %d",
					ErrResumeMismatch, out.orders+1, len(plan.Orders))
			}
			if want := plan.Orders[out.orders].AmountPaise; item.AmountPaise != want {
				return resumed{}, fmt.Errorf("%w: its order %d is %d paise and the plan's is %d",
					ErrResumeMismatch, out.orders, item.AmountPaise, want)
			}
			out.orders++
		default:
			return resumed{}, fmt.Errorf("%w: item %d has kind %q", ErrResumeMismatch, i, item.Kind)
		}
		out.items = append(out.items, item)
	}
	return out, nil
}

// Remaining reports how many invoices and orders in plan a manifest has not
// created yet, and whether reading it as an attempt at plan is possible at all.
// It is what a command uses to tell an operator what is on disk before it
// decides to resume, refuse, or overwrite.
func (p Plan) Remaining(prior Manifest) (invoices, orders int, err error) {
	done, err := resumeFrom(prior, p)
	if err != nil {
		return 0, 0, err
	}
	return len(p.Invoices) - done.invoices, len(p.Orders) - done.orders, nil
}

// createInvoiceItem creates one customer and one invoice, issues it, and
// returns the manifest item for it. On an error partway, the returned Item
// carries whatever fields the successful calls filled in, so a caller that
// appends it never loses track of a customer or a draft invoice that exists
// in Razorpay even though this function did not finish.
//
// A non-empty customerID is one an earlier attempt at this same spec already
// created. It is used as-is and the CreateCustomer call is skipped.
func createInvoiceItem(ctx context.Context, client Client, b *budget, p *pacer, spec InvoiceSpec, runTag string, now time.Time, customerID string) (Item, error) {
	item := Item{
		Kind:                 EntityInvoice,
		CustomerName:         spec.CustomerName,
		CustomerEmail:        spec.CustomerEmail,
		CustomerContact:      spec.CustomerContact,
		AmountPaise:          spec.AmountPaise,
		Currency:             "INR",
		AgeBucket:            spec.AgeBucket,
		SimulatedAtRiskSince: now.Add(-time.Duration(spec.AgeBucket.AgeDays()) * 24 * time.Hour).Unix(),
		Flags: Flags{
			Disputed: spec.Disputed,
			// NoContact is derived from riskitem.Customer.HasContactChannel
			// rather than a bare emptiness check on one field, so this
			// package's idea of "no contact channel" can never drift from the
			// frozen contract's.
			NoContact:   !riskitem.Customer{Email: spec.CustomerEmail, Contact: spec.CustomerContact}.HasContactChannel(),
			PartialPlan: spec.PartialPlan,
		},
		ExpectedRiskSource: riskitem.SourceOverdueInvoice,
		CreatedAt:          now,
	}

	item.CustomerID = customerID
	if item.CustomerID == "" {
		if err := b.spend(1); err != nil {
			return item, fmt.Errorf("create a customer for %s: %w", spec.CustomerName, err)
		}
		if err := p.wait(ctx); err != nil {
			return item, fmt.Errorf("pace the call before the customer for %s: %w", spec.CustomerName, err)
		}
		customer, err := client.CreateCustomer(ctx, razorpay.CreateCustomerRequest{
			Name:    spec.CustomerName,
			Email:   spec.CustomerEmail,
			Contact: spec.CustomerContact,
			Notes:   map[string]string{"seedbook_run": runTag},
		})
		if err != nil {
			return item, fmt.Errorf("create a customer for %s: %w", spec.CustomerName, err)
		}
		item.CustomerID = customer.ID
	}

	if err := b.spend(1); err != nil {
		return item, err
	}
	if err := p.wait(ctx); err != nil {
		return item, fmt.Errorf("pace the call before the invoice for customer %s: %w", item.CustomerID, err)
	}
	invoice, err := client.CreateInvoice(ctx, razorpay.CreateInvoiceRequest{
		CustomerID:     item.CustomerID,
		Draft:          true,
		Description:    spec.Description,
		Currency:       "INR",
		PartialPayment: spec.PartialPayment,
		LineItems: []razorpay.CreateInvoiceLineItem{
			{Name: "seedbook demo line item", AmountPaise: spec.AmountPaise, Currency: "INR", Quantity: 1},
		},
		Notes: map[string]string{
			"seedbook_run": runTag,
			"age_bucket":   string(spec.AgeBucket),
			"disputed":     boolNote(spec.Disputed),
			"partial_plan": boolNote(spec.PartialPlan),
			"no_contact":   boolNote(spec.CustomerContact == ""),
		},
	})
	if err != nil {
		return item, fmt.Errorf("create an invoice for customer %s: %w", item.CustomerID, err)
	}
	item.ID = invoice.ID
	item.Status = invoice.Status

	if err := b.spend(1); err != nil {
		return item, err
	}
	if err := p.wait(ctx); err != nil {
		return item, fmt.Errorf("pace the call before issuing invoice %s: %w", invoice.ID, err)
	}
	issued, err := client.IssueInvoice(ctx, invoice.ID)
	if err != nil {
		return item, fmt.Errorf("issue invoice %s: %w", invoice.ID, err)
	}
	item.OrderID = issued.OrderID
	item.ShortURL = issued.ShortURL
	item.Status = issued.Status

	return item, nil
}

// createOrderItem creates one abandoned order. Nothing after this call
// attempts a payment on it or notifies anyone about it: an order the agent
// visibly sees with zero attempts is the point.
//
// Razorpay's orders endpoint has no customer email or contact field at all,
// per internal/detect's own doc comments, so a contact channel on an order
// can only exist as a note. When spec carries one, it goes into the order's
// notes under detect.NoteKeyCustomerName/Email/Contact, the exact keys
// UnpaidOrderDetector's customerFromNotes reads, so a seeded order with a
// contact is actually notifiable rather than merely labelled as if it were.
// An order deliberately seeded with no contact simply gets none of the three
// keys, which is what makes riskitem.Customer.HasContactChannel false for it
// and routes it to escalation instead of a guessed notification.
//
// The age bucket is the spec's, on the same manifest-only terms an invoice's
// is: no order field can be backdated either, so the bucket is a claim this
// package writes into the manifest and never into Razorpay. An unset bucket
// reads as AgeFresh rather than as the empty string, so every manifest item
// names a bucket a scorer recognises.
func createOrderItem(ctx context.Context, client Client, b *budget, p *pacer, spec OrderSpec, runTag string, now time.Time) (Item, error) {
	hasContact := riskitem.Customer{Email: spec.CustomerEmail, Contact: spec.CustomerContact}.HasContactChannel()
	bucket := spec.AgeBucket
	if bucket == "" {
		bucket = AgeFresh
	}

	item := Item{
		Kind:                 EntityOrder,
		CustomerName:         spec.CustomerName,
		CustomerEmail:        spec.CustomerEmail,
		CustomerContact:      spec.CustomerContact,
		AmountPaise:          spec.AmountPaise,
		Currency:             "INR",
		AgeBucket:            bucket,
		SimulatedAtRiskSince: now.Add(-time.Duration(bucket.AgeDays()) * 24 * time.Hour).Unix(),
		Flags:                Flags{NoContact: !hasContact},
		ExpectedRiskSource:   riskitem.SourceUnpaidOrder,
		CreatedAt:            now,
	}

	notes := map[string]string{
		"seedbook_run": runTag,
		"purpose":      "abandoned order for the unpaid-order detector, left untouched so attempts stays 0",
		"age_bucket":   string(bucket),
	}
	if hasContact {
		if spec.CustomerName != "" {
			notes[detect.NoteKeyCustomerName] = spec.CustomerName
		}
		if spec.CustomerEmail != "" {
			notes[detect.NoteKeyCustomerEmail] = spec.CustomerEmail
		}
		if spec.CustomerContact != "" {
			notes[detect.NoteKeyCustomerContact] = spec.CustomerContact
		}
	}

	if err := b.spend(1); err != nil {
		return item, err
	}
	if err := p.wait(ctx); err != nil {
		return item, fmt.Errorf("pace the call before the abandoned order: %w", err)
	}
	order, err := client.CreateOrder(ctx, razorpay.CreateOrderRequest{
		AmountPaise: spec.AmountPaise,
		Currency:    "INR",
		Receipt:     "seedbook_" + runTag,
		Notes:       notes,
	})
	if err != nil {
		return item, fmt.Errorf("create an abandoned order: %w", err)
	}
	item.ID = order.ID
	item.Status = order.Status
	return item, nil
}

func boolNote(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
