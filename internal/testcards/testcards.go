package testcards

// PendingRiskBlockCode stands in for the Razorpay error code that marks a
// payment blocked by risk. testdata/magic_cards.json documents no risk-block
// card and testdata/error_codes.json records the gap in _meta.gap, so no real
// code string is known. This constant is deliberately not shaped like a
// Razorpay code, so it cannot be mistaken for one. Phase 1 replaces it.
const PendingRiskBlockCode = "pending_risk_block_code"

// PendingSuccessCard stands in for the test card that forces a successful
// authorization. testdata/magic_cards.json records in _meta.open_question that
// the number is not documented there yet. Phase 1 replaces it.
const PendingSuccessCard = "pending_success_card"

// Card is one row of the Razorpay test-card table.
type Card struct {
	Number         string `json:"card_number"`
	ErrorCode      string `json:"error_code"`
	Verified       bool   `json:"verified"`
	DocumentedDate string `json:"documented_date"`
}

// Table is the card-to-failure mapping. It is the single source of truth for
// both the fake gateway and the batch seeder.
type Table struct {
	failures []Card
	success  string
}

// Load reads a magic-card table from path.
func Load(path string) (*Table, error) { return nil, nil }

// Default reads testdata/magic_cards.json from the repository.
func Default() (*Table, error) { return nil, nil }

// FailureFor returns the row for a card number that forces a failure.
func (t *Table) FailureFor(number string) (Card, bool) { return Card{}, false }

// CardForErrorCode returns the first card documented to force code.
func (t *Table) CardForErrorCode(code string) (Card, bool) { return Card{}, false }

// SuccessCard returns the card number that forces a successful authorization.
func (t *Table) SuccessCard() string { return "" }

// FailureCards returns every documented failure card.
func (t *Table) FailureCards() []Card { return nil }
