package seed

import (
	"fmt"
	"math/rand"
	"strings"
)

// AgeBucket names the intended age of a seeded overdue invoice, for the
// scorer to read instead of doing arithmetic on Razorpay's own timestamps.
// See the package doc comment for why: nothing in CreateInvoiceRequest lets a
// caller backdate an invoice, so every bucket other than AgeFresh is a
// fiction this package writes into the manifest and never into Razorpay.
type AgeBucket string

// The four buckets the demo profile seeds.
const (
	AgeFresh AgeBucket = "fresh"
	Age30    AgeBucket = "30"
	Age60    AgeBucket = "60"
	Age90    AgeBucket = "90"
)

// AgeDays is how many days overdue the bucket claims to be. SimulatedAtRiskSince
// is computed as the run's clock reading minus this many days.
func (a AgeBucket) AgeDays() int {
	switch a {
	case Age30:
		return 30
	case Age60:
		return 60
	case Age90:
		return 90
	default:
		return 0
	}
}

// InvoiceSpec is one overdue invoice a plan intends to create.
type InvoiceSpec struct {
	CustomerName  string
	CustomerEmail string
	// CustomerContact is empty when this invoice is the deliberate no-contact
	// item: a customer with no email and no phone number, which is what the
	// R10 escalate-not-guess rule needs something to fire on.
	CustomerContact string
	AmountPaise     int64
	AgeBucket       AgeBucket
	// Disputed is a manifest-only flag. Razorpay has no field for it; the
	// scorer reads it here.
	Disputed bool
	// PartialPlan marks this invoice as the partial-payment-plan candidate:
	// a customer who is expected to pay in installments rather than all at
	// once. It is also a manifest-only flag.
	PartialPlan bool
	// PartialPayment is sent to Razorpay as the invoice's own partial_payment
	// field, which is what actually lets a customer pay less than the full
	// amount at checkout. It is set independently of PartialPlan: the plan
	// candidate always carries both, but the demo mix also puts it on a
	// second invoice so the API field is exercised more than once.
	PartialPayment bool
	Description    string
}

// OrderSpec is one abandoned order a plan intends to create and then leave
// untouched, so its attempts count stays 0.
//
// Razorpay's orders endpoint carries no customer email and no customer
// contact field at all (confirmed against live test mode on 2026-09-05, per
// internal/detect's own doc comments), so a contact channel on an order can
// only ever be a note the merchant chose to write. CustomerName,
// CustomerEmail, and CustomerContact are that note's contents, empty on the
// items this package deliberately seeds with no contact channel at all.
type OrderSpec struct {
	CustomerName    string
	CustomerEmail   string
	CustomerContact string
	AmountPaise     int64
	// AgeBucket is the age this order claims, on the same manifest-only terms
	// an invoice's is: nothing in the orders API backdates created_at either.
	// It exists because policy.GraceUnpaidOrder is one hour and every seeded
	// order is minutes old, so a book seeded shortly before a run denies every
	// order under R11-NOT-YET-DUE and the link-minting beat has nothing to mint
	// a link for. AgeFresh is the zero value and stays the honest default.
	AgeBucket   AgeBucket
	Description string
}

// Plan is everything a seed run intends to create, before any API call is
// made. GeneratePlan and everything it calls touch no network, which is what
// makes plan generation unit-testable without a live key.
type Plan struct {
	RunTag   string
	Invoices []InvoiceSpec
	Orders   []OrderSpec
}

// Profile sizes a plan: how many of each kind of item to build.
type Profile struct {
	Name string
	// FreshInvoices is how many invoices carry AgeFresh.
	FreshInvoices int
	// AgedInvoices lists one bucket per aged invoice. The first entry also
	// carries the API's partial_payment field, per PartialPayment's doc
	// comment.
	AgedInvoices []AgeBucket
	// DisputedInvoices, PartialPlanInvoices, and NoContactInvoices each add
	// one more invoice carrying that flag, over and above FreshInvoices and
	// AgedInvoices. They are separate invoices rather than flags layered onto
	// an existing one, so a scorer looking for "the disputed item" or "the
	// no-contact item" finds exactly one candidate instead of guessing which
	// aged invoice also happens to carry the flag.
	DisputedInvoices    int
	PartialPlanInvoices int
	NoContactInvoices   int
	// Orders is how many abandoned orders to create with a customer contact
	// written into their notes (see OrderSpec).
	Orders int
	// NoContactOrders is how many abandoned orders to create with no contact
	// channel at all, deliberately, over and above Orders. It exists for the
	// same reason NoContactInvoices does: UnpaidOrderDetector's items go
	// through the same policy gate overdue-invoice items do, and that gate
	// escalates rather than guesses when riskitem.Customer.HasContactChannel
	// is false, whichever of the three sources produced the item.
	NoContactOrders int
	// OrderAges is the age bucket for each order the profile seeds, in the
	// order they are created: the Orders that carry a contact first, then the
	// NoContactOrders. A position with no entry gets AgeFresh.
	//
	// It is a list rather than one bucket for the whole set because the demo
	// needs both answers out of one source: an order old enough for R11 to let
	// through, so the engine mints a link, and an order young enough for R11 to
	// deny, so the containment beat has something to fire on that is not a
	// contrived flag.
	OrderAges []AgeBucket
}

// DemoProfile is the mix the risk-engine demo asks for: a couple of fresh
// invoices, one aged invoice per bucket (30/60/90), one disputed, one
// partial-payment-plan candidate, one invoice with no contact channel at
// all, a couple of abandoned orders with a contact in their notes, and one
// abandoned order with none.
//
// Two of the three orders are aged and the middle one is deliberately not.
// policy.GraceUnpaidOrder is one hour and no API call can backdate an order, so
// a book seeded less than an hour before the run is taken has every order denied
// under R11-NOT-YET-DUE and the demo never mints a payment link. The two aged
// orders make that beat work whatever time the seeder ran; the fresh one keeps
// R11 firing on something real, which is a containment beat rather than a gap.
// It is one of the contact-bearing orders that stays fresh, so that the
// no-contact order is old enough to reach R10-NO-CONTACT-CHANNEL: R11 is
// evaluated before R10, and a fresh no-contact order would be denied for being
// young before anything asked whether it could be contacted at all.
func DemoProfile() Profile {
	return Profile{
		Name:                "demo",
		FreshInvoices:       2,
		AgedInvoices:        []AgeBucket{Age30, Age60, Age90},
		DisputedInvoices:    1,
		PartialPlanInvoices: 1,
		NoContactInvoices:   1,
		Orders:              2,
		NoContactOrders:     1,
		OrderAges:           []AgeBucket{Age30, AgeFresh, Age30},
	}
}

// ProfileByName returns the named profile. "demo" is the only one that exists
// today; the lookup is a function rather than a map literal so a later
// profile can be added the way batch.Profile was, without a caller-visible
// shape change.
func ProfileByName(name string) (Profile, bool) {
	if name == DemoProfile().Name {
		return DemoProfile(), true
	}
	return Profile{}, false
}

// GeneratePlan turns a profile into a concrete Plan: names, synthetic
// example.com emails, amounts, and per-item flags, all deterministic in
// randSeed so the same profile, run tag, and seed always produce the same
// plan. It makes no API call and needs no credentials.
func GeneratePlan(profile Profile, runTag string, randSeed int64) Plan {
	rng := rand.New(rand.NewSource(randSeed))
	plan := Plan{RunTag: runTag}

	seq := 0
	newInvoice := func(bucket AgeBucket, disputed, partialPlan, noContact, partialPayment bool) InvoiceSpec {
		seq++
		name := syntheticName(rng)
		email := ""
		contact := ""
		if !noContact {
			email = syntheticEmail(name, runTag, seq)
			contact = syntheticContact(rng)
		}
		return InvoiceSpec{
			CustomerName:    name,
			CustomerEmail:   email,
			CustomerContact: contact,
			AmountPaise:     syntheticAmount(rng),
			AgeBucket:       bucket,
			Disputed:        disputed,
			PartialPlan:     partialPlan,
			PartialPayment:  partialPayment || partialPlan,
			Description:     fmt.Sprintf("seedbook %s invoice %d, age=%s", runTag, seq, bucket),
		}
	}

	for i := 0; i < profile.FreshInvoices; i++ {
		plan.Invoices = append(plan.Invoices, newInvoice(AgeFresh, false, false, false, false))
	}
	for i, bucket := range profile.AgedInvoices {
		// Only the first aged invoice also gets the API's partial_payment
		// field, so the demo mix exercises it on more than the one item
		// PartialPlanInvoices below flags, without every aged invoice
		// carrying it.
		plan.Invoices = append(plan.Invoices, newInvoice(bucket, false, false, false, i == 0))
	}
	for i := 0; i < profile.DisputedInvoices; i++ {
		plan.Invoices = append(plan.Invoices, newInvoice(Age60, true, false, false, false))
	}
	for i := 0; i < profile.PartialPlanInvoices; i++ {
		plan.Invoices = append(plan.Invoices, newInvoice(Age30, false, true, false, false))
	}
	for i := 0; i < profile.NoContactInvoices; i++ {
		plan.Invoices = append(plan.Invoices, newInvoice(Age90, false, false, true, false))
	}

	orderSeq := 0
	// orderAge reads the bucket for the order about to be appended. Positions
	// past the end of OrderAges get AgeFresh, so a profile that names no ages
	// seeds the book it seeded before this field existed.
	orderAge := func() AgeBucket {
		if len(plan.Orders) < len(profile.OrderAges) {
			return profile.OrderAges[len(plan.Orders)]
		}
		return AgeFresh
	}
	for i := 0; i < profile.Orders; i++ {
		orderSeq++
		name := syntheticName(rng)
		bucket := orderAge()
		plan.Orders = append(plan.Orders, OrderSpec{
			CustomerName:    name,
			CustomerEmail:   syntheticEmail(name, runTag, seq+orderSeq),
			CustomerContact: syntheticContact(rng),
			AmountPaise:     syntheticAmount(rng),
			AgeBucket:       bucket,
			Description:     fmt.Sprintf("seedbook %s order %d, abandoned, age=%s", runTag, i+1, bucket),
		})
	}
	for i := 0; i < profile.NoContactOrders; i++ {
		bucket := orderAge()
		plan.Orders = append(plan.Orders, OrderSpec{
			AmountPaise: syntheticAmount(rng),
			AgeBucket:   bucket,
			Description: fmt.Sprintf("seedbook %s order %d, abandoned, no contact channel, age=%s", runTag, profile.Orders+i+1, bucket),
		})
	}

	return plan
}

// CallEstimate returns how many Razorpay calls executing the plan would
// spend: one CreateCustomer, one CreateInvoice, and one IssueInvoice per
// invoice, and one CreateOrder per order.
func (p Plan) CallEstimate() int {
	return len(p.Invoices)*3 + len(p.Orders)
}

// syntheticFirstNames and syntheticLastNames are a fixed, clearly invented
// pool. None of these pairings are drawn from or checked against any real
// person; they exist only to make the demo's customer list readable instead
// of "Customer 1", "Customer 2".
var syntheticFirstNames = []string{
	"Aanya", "Vikram", "Priya", "Rohan", "Neha",
	"Karan", "Ishita", "Aditya", "Meera", "Suresh",
}

var syntheticLastNames = []string{
	"Shah", "Verma", "Iyer", "Kapoor", "Nair",
	"Malhotra", "Bose", "Reddy", "Chopra", "Menon",
}

func syntheticName(rng *rand.Rand) string {
	first := syntheticFirstNames[rng.Intn(len(syntheticFirstNames))]
	last := syntheticLastNames[rng.Intn(len(syntheticLastNames))]
	return first + " " + last
}

// syntheticEmail builds an address on example.com, the RFC 2606 reserved
// domain that will never resolve to a real mailbox. The run tag and sequence
// number are folded into the local part so two invoices in the same run never
// collide, and the same run tag re-run with the same seed produces the same
// address, which is what lets CreateCustomer's FailExisting=false match the
// customer it already made instead of minting a duplicate.
func syntheticEmail(name, runTag string, seq int) string {
	slug := strings.ToLower(strings.ReplaceAll(name, " ", "."))
	return fmt.Sprintf("%s+%s.%d@example.com", slug, runTag, seq)
}

// syntheticContact returns a ten-digit, India-shaped mobile number that is
// entirely invented. Whether the customer-creation endpoint validates this
// field beyond its shape has not been checked; a synthetic number in the
// documented format is the safest guess pending that check.
func syntheticContact(rng *rand.Rand) string {
	digits := make([]byte, 10)
	digits[0] = '9'
	for i := 1; i < len(digits); i++ {
		digits[i] = byte('0' + rng.Intn(10))
	}
	return string(digits)
}

// syntheticAmount returns a whole-rupee amount, in paise, between 500 and
// 7500 rupees. Whole rupees keep the numbers readable on a dashboard without
// paise arithmetic; the range is wide enough that a table of them does not
// read as one repeated value.
func syntheticAmount(rng *rand.Rand) int64 {
	rupees := 500 + rng.Intn(7000)
	return int64(rupees) * 100
}
