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

// ErrCallBudgetExceeded is returned when a run stops because the next call
// would spend more than its budget allows. Whatever was created before the
// stop is still in Razorpay and still in the returned manifest: this package
// never deletes anything, including on its own budget refusal.
var ErrCallBudgetExceeded = errors.New("seed: call budget exceeded")

// RunOptions configures ExecutePlan.
type RunOptions struct {
	// CallBudget caps API calls. Zero or negative means DefaultCallBudget.
	CallBudget int
	// Clock is read once, at the start of the run, for every item's
	// CreatedAt and for computing SimulatedAtRiskSince from its age bucket.
	// Nil means the wall clock.
	Clock clock.Clock
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
func ExecutePlan(ctx context.Context, client Client, plan Plan, opts RunOptions) (Manifest, error) {
	clk := opts.Clock
	if clk == nil {
		clk = clock.Real()
	}
	max := opts.CallBudget
	if max <= 0 {
		max = DefaultCallBudget
	}
	b := &budget{max: max}

	now := clk.Now()
	m := Manifest{
		RunTag:     plan.RunTag,
		CreatedAt:  now,
		Gateway:    GatewayLiveTestMode,
		CallBudget: max,
	}

	for _, inv := range plan.Invoices {
		item, err := createInvoiceItem(ctx, client, b, inv, plan.RunTag, now)
		m.CallsUsed = b.used
		if item.CustomerID != "" || item.ID != "" {
			// Something was actually created in Razorpay, even if a later
			// call in this same item failed. It belongs in the ground truth
			// either way.
			m.Items = append(m.Items, item)
		}
		if err != nil {
			return m, err
		}
	}

	for _, ord := range plan.Orders {
		item, err := createOrderItem(ctx, client, b, ord, plan.RunTag, now)
		m.CallsUsed = b.used
		if item.ID != "" {
			m.Items = append(m.Items, item)
		}
		if err != nil {
			return m, err
		}
	}

	instructions, err := buildInstructions(m.Items)
	if err != nil {
		return m, fmt.Errorf("build the operator instructions: %w", err)
	}
	m.Instructions = instructions
	return m, nil
}

// createInvoiceItem creates one customer and one invoice, issues it, and
// returns the manifest item for it. On an error partway, the returned Item
// carries whatever fields the successful calls filled in, so a caller that
// appends it never loses track of a customer or a draft invoice that exists
// in Razorpay even though this function did not finish.
func createInvoiceItem(ctx context.Context, client Client, b *budget, spec InvoiceSpec, runTag string, now time.Time) (Item, error) {
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

	if err := b.spend(1); err != nil {
		return item, fmt.Errorf("create a customer for %s: %w", spec.CustomerName, err)
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

	if err := b.spend(1); err != nil {
		return item, err
	}
	invoice, err := client.CreateInvoice(ctx, razorpay.CreateInvoiceRequest{
		CustomerID:     customer.ID,
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
		return item, fmt.Errorf("create an invoice for customer %s: %w", customer.ID, err)
	}
	item.ID = invoice.ID
	item.Status = invoice.Status

	if err := b.spend(1); err != nil {
		return item, err
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
func createOrderItem(ctx context.Context, client Client, b *budget, spec OrderSpec, runTag string, now time.Time) (Item, error) {
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
