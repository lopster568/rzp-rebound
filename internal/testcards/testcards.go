package testcards

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

// The risk-block stand-in that used to live here is gone.
//
// PendingRiskBlockCode held the slot for the Razorpay error code that marks a
// payment blocked by risk, because no risk block was produced by any call in
// the 2026-08-31 live test-mode run and no real code string was known. Phase 5
// read the live-mode card error documentation and found it:
// payment_risk_check_failed. It is a documented live-mode reason, not a
// test-card artifact, so it lives in internal/classify with the rest of that
// vocabulary and this package no longer exports a stand-in for it. PRD Q2 is
// closed.

// PendingSuccessCard stands in for the test card that forces a successful
// authorization.
//
// The 2026-08-31 live run answered the question behind it, and the answer is
// that there is no such card. The outcome of a test-mode attempt is chosen at
// the last step of the checkout sequence through one form field carrying S or
// F, and the same card number produced both a captured payment and a failed
// one. See docs/RAZORPAY-TEST-MODE-NOTES.md.
//
// The constant stays because Table.SuccessCard has to return something for a
// table with no success_cards entry, and something that cannot be mistaken for
// a card number is the right something. It is not a fact waiting to be filled
// in any more; it is a concept that does not apply.
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
