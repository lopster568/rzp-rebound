package testcards

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// PendingRiskBlockCode stands in for the Razorpay error code that marks a
// payment blocked by risk. testdata/magic_cards.json documents no risk-block
// card and testdata/error_codes.json records the gap in _meta.gap, so no real
// code string is known. This constant is deliberately not shaped like a
// Razorpay code, so it cannot be mistaken for one. Phase 1 replaces it.
const PendingRiskBlockCode = "pending_risk_block_code"

// PendingSuccessCard stands in for the test card that forces a successful
// authorization. testdata/magic_cards.json records in _meta.open_question that
// the number is not documented there yet, so this is not a card number either.
// Phase 1 replaces it.
const PendingSuccessCard = "pending_success_card"

// DefaultPath is the card table this package reads, relative to the
// repository root.
const DefaultPath = "testdata/magic_cards.json"

// Card is one row of the Razorpay test-card table. Verified stays false until
// a live test-mode run observes the documented code coming back from the API.
type Card struct {
	Number         string `json:"card_number"`
	ErrorCode      string `json:"error_code"`
	Verified       bool   `json:"verified"`
	DocumentedDate string `json:"documented_date"`
}

// Table maps a card number to the failure it forces. It is the single source
// of truth for the fake gateway and for the batch seeder: two copies would
// drift, and a drift there means the gateway fails a payment one way while the
// ground-truth manifest records another.
type Table struct {
	byNumber    map[string]Card
	byErrorCode map[string]Card
	success     string
}

type cardFile struct {
	FailureCards []Card `json:"failure_cards"`
	SuccessCards []struct {
		Number string `json:"card_number"`
	} `json:"success_cards"`
}

// Load reads a magic-card table from path.
func Load(path string) (*Table, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("testcards: read %s: %w", path, err)
	}

	var f cardFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("testcards: parse %s: %w", path, err)
	}
	if len(f.FailureCards) == 0 {
		return nil, fmt.Errorf("testcards: %s lists no failure cards", path)
	}

	t := &Table{
		byNumber:    make(map[string]Card, len(f.FailureCards)),
		byErrorCode: make(map[string]Card, len(f.FailureCards)),
		success:     PendingSuccessCard,
	}
	for _, c := range f.FailureCards {
		if c.Number == "" || c.ErrorCode == "" {
			return nil, fmt.Errorf("testcards: %s has a card with an empty number or code", path)
		}
		if _, dup := t.byNumber[c.Number]; dup {
			return nil, fmt.Errorf("testcards: %s lists card %s twice", path, c.Number)
		}
		t.byNumber[c.Number] = c
		if _, seen := t.byErrorCode[c.ErrorCode]; !seen {
			t.byErrorCode[c.ErrorCode] = c
		}
	}
	if len(f.SuccessCards) > 0 && f.SuccessCards[0].Number != "" {
		t.success = f.SuccessCards[0].Number
	}
	return t, nil
}

var (
	defaultOnce  sync.Once
	defaultTable *Table
	defaultErr   error
)

// Default reads testdata/magic_cards.json from the repository this package was
// built from, and caches it. The path is resolved from the source file rather
// than the working directory, so it does not matter which directory a test or
// a command runs in.
func Default() (*Table, error) {
	defaultOnce.Do(func() {
		root, err := repoRoot()
		if err != nil {
			defaultErr = err
			return
		}
		defaultTable, defaultErr = Load(filepath.Join(root, DefaultPath))
	})
	return defaultTable, defaultErr
}

// repoRoot walks up from this source file to the directory holding go.mod.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("testcards: cannot locate this package on disk")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("testcards: no go.mod above %s", filepath.Dir(file))
		}
		dir = parent
	}
}

// FailureFor returns the row for a card number documented to force a failure.
func (t *Table) FailureFor(number string) (Card, bool) {
	c, ok := t.byNumber[number]
	return c, ok
}

// CardForErrorCode returns the first card documented to force code.
func (t *Table) CardForErrorCode(code string) (Card, bool) {
	c, ok := t.byErrorCode[code]
	return c, ok
}

// SuccessCard returns the card number that forces a successful authorization,
// or PendingSuccessCard while the table documents none.
func (t *Table) SuccessCard() string { return t.success }
