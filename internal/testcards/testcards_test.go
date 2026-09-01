package testcards_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/testcards"
)

var cardTablePath = filepath.Join("..", "..", testcards.DefaultPath)

// labelledEntry is the metadata phase 5 requires of every row in every table
// under testdata/. A row that does not say where it came from is a row a reader
// has to take on trust, and this repository does not ask for that anywhere else.
type labelledEntry struct {
	Label  string `json:"label"`
	Source string `json:"source"`
}

type cardTableFile struct {
	Meta struct {
		Labels map[string]string `json:"labels"`
	} `json:"_meta"`
	FailureCards []struct {
		labelledEntry
		Number         string `json:"card_number"`
		DocumentedDate string `json:"documented_date"`
	} `json:"failure_cards"`
	UPIVPAs []struct {
		labelledEntry
		VPA            string `json:"vpa"`
		DocumentedDate string `json:"documented_date"`
	} `json:"upi_vpas"`
}

// validLabels is the closed set. Phase 5's rule is that every fact in this
// repository is one of these three things, and there is no fourth bucket for
// "we are fairly sure".
var validLabels = []string{"documented-live", "documented-test-mode", "observed-test-mode", "cited-network"}

func TestCardTableEntriesCarryALabelAndASource(t *testing.T) {
	raw, err := os.ReadFile(cardTablePath)
	if err != nil {
		t.Fatalf("read %s: %v", cardTablePath, err)
	}
	var f cardTableFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse %s: %v", cardTablePath, err)
	}
	if len(f.FailureCards) == 0 {
		t.Fatalf("%s lists no failure cards", cardTablePath)
	}
	if len(f.Meta.Labels) == 0 {
		t.Errorf("%s has no _meta.labels explaining what each label means", cardTablePath)
	}

	check := func(name, label, source, date string) {
		t.Helper()
		if !slices.Contains(validLabels, label) {
			t.Errorf("%s has label %q, want one of %v", name, label, validLabels)
		}
		if source == "" {
			t.Errorf("%s has no source", name)
		}
		if date == "" {
			t.Errorf("%s has no documented_date", name)
		}
		if _, ok := f.Meta.Labels[label]; label != "" && !ok {
			t.Errorf("%s uses label %q and _meta.labels does not define it", name, label)
		}
	}

	for _, card := range f.FailureCards {
		check("card "+card.Number, card.Label, card.Source, card.DocumentedDate)
		// These are test-mode card numbers. Publishing one as live-mode
		// documentation would be the exact confusion phase 5 exists to remove.
		if card.Label == "documented-live" {
			t.Errorf("card %s is labelled documented-live, and this table is test mode only", card.Number)
		}
	}
	for _, vpa := range f.UPIVPAs {
		check("vpa "+vpa.VPA, vpa.Label, vpa.Source, vpa.DocumentedDate)
	}
}
