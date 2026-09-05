package seed

import (
	"fmt"

	"github.com/lopster568/rzp-recovery-agent/internal/testcards"
)

// instructionsFailureCodes names the documented test-mode failure codes this
// package prints for the operator, looked up through testcards.Table rather
// than carrying their card numbers here a second time. Three is enough
// variety for an operator failing checkouts by hand without turning the
// printed block into the whole card table.
var instructionsFailureCodes = []string{"insufficient_fund", "card_declined", "payment_cancelled"}

// maxFailTargets bounds how many links the instructions block names. Two or
// three issued invoices are enough for an operator to enrich the
// failed-payment class by hand; naming every invoice in the run would make
// the printed block as long as the manifest.
const maxFailTargets = 3

// buildInstructions assembles the operator to-do list for the one class this
// package cannot seed through the API: failed payments. It reads the
// documented test-card table (the single source of truth for those numbers)
// rather than inventing one, and points at the issued invoices this run
// actually created.
func buildInstructions(items []Item) (Instructions, error) {
	var targets []FailTarget
	for _, it := range items {
		if it.Kind != EntityInvoice || it.ShortURL == "" {
			continue
		}
		targets = append(targets, FailTarget{Kind: it.Kind, ID: it.ID, URL: it.ShortURL})
		if len(targets) == maxFailTargets {
			break
		}
	}

	cards, err := documentedFailureCards()
	if err != nil {
		return Instructions{}, err
	}

	headline := "Failed payments cannot be created by this seeder: test-mode checkout is " +
		"browser-only and the headless attempt path answers 403. Open each link below " +
		"in a browser, pay with one of the cards, use any future expiry and any CVV, " +
		"and let the bank step decline it. The failed-payment detector reads the " +
		"account's history on its next sweep; nothing here writes to it directly."
	if len(targets) == 0 {
		headline = "This run issued no invoices to attach a failable link to. " +
			"Re-run with a profile that seeds at least one, or fail a payment " +
			"against an invoice from an earlier run using the cards below."
	}

	return Instructions{
		Headline:  headline,
		Targets:   targets,
		TestCards: cards,
	}, nil
}

// documentedFailureCards looks up a handful of documented test-mode cards
// from the repository's single source of truth for them,
// testdata/magic_cards.json, through internal/testcards.Default. It is a
// local, deterministic file read: nothing here reaches the network.
func documentedFailureCards() ([]TestCard, error) {
	table, err := testcards.Default()
	if err != nil {
		return nil, fmt.Errorf("seed: load the test-card table: %w", err)
	}

	var cards []TestCard
	for _, code := range instructionsFailureCodes {
		card, ok := table.CardForErrorCode(code)
		if !ok {
			continue
		}
		cards = append(cards, TestCard{Number: card.Number, ErrorCode: card.ErrorCode})
	}
	if len(cards) == 0 {
		return nil, fmt.Errorf("seed: the test-card table documents none of %v", instructionsFailureCodes)
	}
	return cards, nil
}
