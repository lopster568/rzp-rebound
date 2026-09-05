package seed

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
)

// EntityKind names what kind of Razorpay resource a manifest item points at.
type EntityKind string

// The two kinds this package can actually create. Failed payments are a
// third risk-item source but are never a manifest EntityKind here: see the
// package doc comment for why, and Instructions for how the gap is covered.
const (
	EntityInvoice EntityKind = "invoice"
	EntityOrder   EntityKind = "order"
)

// Flags are the markers Razorpay has no field for. The scorer reads these off
// the manifest and nowhere else: Razorpay does not know an invoice is
// disputed, and NoContact is derived from the customer record rather than
// read back from it, because an item with no contact channel is exactly the
// shape HasContactChannel in internal/riskitem is built to test for.
type Flags struct {
	Disputed    bool `json:"disputed,omitempty"`
	NoContact   bool `json:"no_contact,omitempty"`
	PartialPlan bool `json:"partial_plan,omitempty"`
}

// Item is one entity a seed run created, and everything the eval scorer needs
// to treat it as ground truth.
type Item struct {
	Kind EntityKind `json:"kind"`
	// ID is the invoice or order id Razorpay returned.
	ID string `json:"id"`
	// OrderID is the order an issued invoice minted. Empty for a plain order
	// item, whose own ID already is the order id.
	OrderID string `json:"order_id,omitempty"`
	// ShortURL is the payable link, when the item has one. An issued invoice
	// carries its own; a plain order does not, because nothing in this
	// package raises a payment link for an abandoned order (raising one would
	// give it a handle to pay against, and an abandoned order is supposed to
	// have none).
	ShortURL string `json:"short_url,omitempty"`

	CustomerID      string `json:"customer_id,omitempty"`
	CustomerName    string `json:"customer_name,omitempty"`
	CustomerEmail   string `json:"customer_email,omitempty"`
	CustomerContact string `json:"customer_contact,omitempty"`

	AmountPaise int64  `json:"amount_paise"`
	Currency    string `json:"currency"`

	// AgeBucket and SimulatedAtRiskSince are the manifest's own clock: what
	// this item is meant to look like to a detector, independent of what
	// Razorpay's own timestamps say. See the package doc comment.
	AgeBucket            AgeBucket `json:"age_bucket"`
	SimulatedAtRiskSince int64     `json:"simulated_at_risk_since"`

	Flags Flags `json:"flags"`

	// ExpectedRiskSource is which of riskitem's three detectors should
	// surface this item, so a scorer can check the detector that actually
	// fired against the one that should have.
	ExpectedRiskSource riskitem.Source `json:"expected_risk_source"`

	// CreatedAt is when this seed run created the item, by the run's own
	// clock. It is not read back from Razorpay's created_at, which a dry-run
	// or a stubbed test has no way to produce.
	CreatedAt time.Time `json:"created_at"`
	// Status is the last status this run observed: issued for an invoice,
	// created for an order.
	Status string `json:"status,omitempty"`
}

// Instructions is the operator's to-do list for the one class the API cannot
// seed: failed payments. Test-mode checkout is browser-only and the headless
// attempt path this repository otherwise uses answers 403, so this names
// links to open by hand and documented test-mode cards to fail them with. The
// seed command prints this block verbatim at the end of a run.
type Instructions struct {
	Headline  string       `json:"headline"`
	Targets   []FailTarget `json:"targets"`
	TestCards []TestCard   `json:"test_cards"`
}

// FailTarget is one payable link the operator should open and deliberately
// fail.
type FailTarget struct {
	Kind EntityKind `json:"kind"`
	ID   string     `json:"id"`
	URL  string     `json:"url"`
}

// TestCard is one documented test-mode card, and the failure it is
// documented to force. See internal/testcards and testdata/magic_cards.json,
// which is the single source of truth this package reads it from rather than
// carrying its own copy.
type TestCard struct {
	Number    string `json:"number"`
	ErrorCode string `json:"error_code"`
}

// GatewayLiveTestMode is what Manifest.Gateway reads for a real run. This
// package has no fake-gateway path: the class it exists to seed most (an
// account with real failed-payment history to detect) only exists once a
// human has actually failed a checkout in a browser, which nothing but the
// live test-mode API can produce.
const GatewayLiveTestMode = "live-test-mode"

// Manifest is a seed run's ground truth, written to disk as the committed
// format the eval scorer reads. Every field a detector or a scorer needs is
// here, because the live API cannot answer the one question that matters
// most, how old this debt is meant to look, at all.
type Manifest struct {
	RunTag     string    `json:"run_tag"`
	Profile    string    `json:"profile"`
	CreatedAt  time.Time `json:"created_at"`
	Gateway    string    `json:"gateway"`
	DryRun     bool      `json:"dry_run"`
	CallBudget int       `json:"call_budget"`
	CallsUsed  int       `json:"calls_used"`

	Items        []Item       `json:"items"`
	Instructions Instructions `json:"instructions"`
}

// Write encodes the manifest as indented JSON and writes it to path, creating
// its directory. It is not O_EXCL: a seed run is expected to be re-run at the
// same default path, and the run tag inside the file, not the path, is what
// makes two runs distinguishable.
func (m Manifest) Write(path string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("seed: make %s: %w", dir, err)
		}
	}
	encoded, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("seed: encode the manifest: %w", err)
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o644)
}

// ReadManifest reads a manifest written by Manifest.Write.
func ReadManifest(path string) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("seed: read %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, fmt.Errorf("seed: parse %s: %w", path, err)
	}
	return m, nil
}
