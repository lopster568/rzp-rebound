package batch_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lopster568/rzp-recovery-agent/internal/batch"
	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
)

func profile(t *testing.T, name string) batch.Profile {
	t.Helper()
	p, ok := batch.ProfileByName(name)
	if !ok {
		t.Fatalf("no profile named %s", name)
	}
	return p
}

func counts(t *testing.T, m *batch.Manifest) (byClass map[classify.Class]int, bait int) {
	t.Helper()
	byClass = make(map[classify.Class]int)
	for _, o := range m.Orders {
		if o.IsBait {
			bait++
			continue
		}
		byClass[o.SeededFailureClass]++
	}
	return byClass, bait
}

// TestUniformInventedProfileKeepsTheSharesItAlwaysHad pins the existing mix
// under its new name. The shares are unchanged: what changed is that the
// profile now says out loud that nobody published them.
func TestUniformInventedProfileKeepsTheSharesItAlwaysHad(t *testing.T) {
	p := profile(t, "uniform-invented")

	if p.Cited {
		t.Error("uniform-invented reports itself cited, and its shares came from nowhere")
	}
	if p.DefinesBait() {
		t.Error("uniform-invented defines a bait share, and its bait has always been an explicit count")
	}

	want := map[classify.Class]float64{
		classify.TransientRetryEligible: 0.28,
		classify.RetryEligible:          0.24,
		classify.ReauthRequired:         0.24,
		classify.NewInstrumentRequired:  0.24,
	}
	got := make(map[classify.Class]float64)
	for _, s := range p.Shares() {
		got[s.Class] = s.Share
		if s.Cited {
			t.Errorf("%s in uniform-invented reports itself cited", s.Class)
		}
	}
	for class, share := range want {
		if math.Abs(got[class]-share) > 1e-9 {
			t.Errorf("%s share = %v, want %v", class, got[class], share)
		}
	}
	if len(got) != len(want) {
		t.Errorf("uniform-invented has %d shares, want %d", len(got), len(want))
	}
}

// TestEthocaProfileSharesMatchThePublishedFigures is the citation, checked.
//
// Mastercard/Ethoca published card-decline shares: insufficient funds 44
// percent, lost or stolen 26 percent, fraud 9 percent. Those three sum to 79.
// The remaining 21 is not broken out by the source, so it is carried as a
// residual across the other classes and marked uncited, rather than being
// folded into one of the three to make the arithmetic tidy.
func TestEthocaProfileSharesMatchThePublishedFigures(t *testing.T) {
	p := profile(t, "ethoca-card-mix-2017")

	if !p.Cited {
		t.Error("ethoca-card-mix-2017 does not report itself cited")
	}
	if p.Vintage != "2017" {
		t.Errorf("Vintage = %q, want 2017: the source article is dated 2017-04-28, and this project recorded 2019 until a first-hand check corrected it", p.Vintage)
	}
	if !strings.Contains(p.Source, "ethoca.com") {
		t.Errorf("Source = %q, and it does not name the publisher", p.Source)
	}

	var citedTotal, residualTotal float64
	var citedClasses []classify.Class
	for _, s := range p.Shares() {
		if s.Cited {
			citedTotal += s.Share
			citedClasses = append(citedClasses, s.Class)
			continue
		}
		residualTotal += s.Share
		if s.Note == "" {
			t.Errorf("the uncited %s share carries no note saying it is a residual", s.Class)
		}
	}
	citedTotal += p.BaitShare()

	// insufficient funds 0.44 plus lost/stolen 0.26 plus fraud 0.09.
	if math.Abs(citedTotal-0.79) > 1e-9 {
		t.Errorf("cited shares sum to %v, want 0.79", citedTotal)
	}
	if math.Abs(residualTotal-0.21) > 1e-9 {
		t.Errorf("the residual sums to %v, want 0.21", residualTotal)
	}
	if math.Abs(citedTotal+residualTotal-1.0) > 1e-9 {
		t.Errorf("the profile sums to %v, want 1.0", citedTotal+residualTotal)
	}

	// The one cited non-bait share is insufficient funds, at 44 percent.
	if len(citedClasses) != 1 || citedClasses[0] != classify.RetryEligible {
		t.Errorf("cited non-bait classes = %v, want exactly [retry_eligible]", citedClasses)
	}
	for _, s := range p.Shares() {
		if s.Class == classify.RetryEligible && math.Abs(s.Share-0.44) > 1e-9 {
			t.Errorf("insufficient funds share = %v, want 0.44", s.Share)
		}
	}
}

// TestEthocaProfileBaitIsTheCitedNeverRetryShare is the finding this profile
// exists to produce. Lost, stolen, and fraud declines are orders no arm should
// act on, which is what this repository calls bait, and the source puts them at
// 35 percent of card declines. That share is not the author's.
func TestEthocaProfileBaitIsTheCitedNeverRetryShare(t *testing.T) {
	p := profile(t, "ethoca-card-mix-2017")

	if !p.DefinesBait() {
		t.Fatal("ethoca-card-mix-2017 defines no bait share, and 35 percent of its cited mix is unactionable")
	}
	if math.Abs(p.BaitShare()-0.35) > 1e-9 {
		t.Errorf("BaitShare() = %v, want 0.35 (lost or stolen 0.26 plus fraud 0.09)", p.BaitShare())
	}

	spec, err := p.Spec(1234, 40, 0)
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	m, err := batch.Generate(spec)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(m.Orders) != 40 {
		t.Fatalf("generated %d orders, want 40", len(m.Orders))
	}

	byClass, bait := counts(t, m)
	if bait != 14 {
		t.Errorf("bait = %d, want 14, which is 35 percent of 40", bait)
	}
	for _, o := range m.Orders {
		if o.IsBait && o.BaitKind != batch.BaitNeverRetry {
			t.Errorf("%s is bait of kind %q, and this profile's bait is the cited never-retry share",
				o.OrderID, o.BaitKind)
		}
	}
	if byClass[classify.RetryEligible] != 17 {
		t.Errorf("retry_eligible = %d, want 17, which is 44 percent of 40 under largest-remainder",
			byClass[classify.RetryEligible])
	}
}

func TestObservedLiveMixWithoutItsFileIsAnErrorAndSaysWhy(t *testing.T) {
	t.Setenv(batch.ObservedMixFileEnv, "")

	p := profile(t, "observed-live-mix")

	if p.Cited {
		t.Error("observed-live-mix reports itself cited, and nothing has been loaded")
	}
	_, err := p.Spec(1234, 40, 0)
	if err == nil {
		t.Fatal("a spec came back with no mix file set")
	}
	if !strings.Contains(err.Error(), batch.ObservedMixFileEnv) {
		t.Errorf("error = %q, and it does not name %s", err, batch.ObservedMixFileEnv)
	}
}

// TestObservedLiveMixReadsAFileOutsideTheRepository is why the loader takes a
// path from the environment rather than a file under testdata/. A real
// merchant's failure mix is that merchant's data and it does not enter git.
func TestObservedLiveMixReadsAFileOutsideTheRepository(t *testing.T) {
	path := filepath.Join(t.TempDir(), "observed-mix.json")
	body := map[string]any{
		"source":        "a merchant's own aggregates",
		"observed_from": "2026-07-15",
		"observed_to":   "2026-08-31",
		"shares": map[string]float64{
			"transient_retry_eligible": 0.25,
			"retry_eligible":           0.25,
			"reauth_required":          0.25,
			"new_instrument_required":  0.15,
		},
		"bait_share": 0.10,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(batch.ObservedMixFileEnv, path)

	spec, err := profile(t, "observed-live-mix").Spec(1234, 40, 0)
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	m, err := batch.Generate(spec)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	byClass, bait := counts(t, m)
	if bait != 4 {
		t.Errorf("bait = %d, want 4", bait)
	}
	if byClass[classify.NewInstrumentRequired] != 6 {
		t.Errorf("new_instrument_required = %d, want 6", byClass[classify.NewInstrumentRequired])
	}
	if len(m.Orders) != 40 {
		t.Errorf("generated %d orders, want 40", len(m.Orders))
	}
}

func TestEveryProfileNamesItsSourceOrSaysInvented(t *testing.T) {
	all := batch.Profiles()
	if len(all) != 3 {
		t.Errorf("%d profiles, want 3", len(all))
	}
	for _, p := range all {
		if p.Name == "" {
			t.Error("a profile has no name")
		}
		if p.Source == "" {
			t.Errorf("%s has no source", p.Name)
		}
		if !p.Cited && !strings.Contains(p.Source, "invented") && !strings.Contains(p.Source, "not published") {
			t.Errorf("%s is uncited and its source does not say so: %q", p.Name, p.Source)
		}
		if p.Cited && p.Vintage == "" {
			t.Errorf("%s is cited and names no vintage, so a reader cannot tell how old the figures are", p.Name)
		}
	}
}

// TestGeneratedAmountsStraddleTheAmountCeiling keeps R3 able to fire. The
// ceiling moved to the RBI e-mandate threshold of 1500000 paise in phase 5, and
// a batch whose largest order is 500000 would never reach it, which would
// retire a rule by accident rather than by decision.
func TestGeneratedAmountsStraddleTheAmountCeiling(t *testing.T) {
	spec, err := profile(t, "uniform-invented").Spec(1234, 200, 10)
	if err != nil {
		t.Fatalf("spec: %v", err)
	}
	m, err := batch.Generate(spec)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	var below, above int
	for _, o := range m.Orders {
		if o.AmountPaise > policy.DefaultAmountCeilingPaise {
			above++
			continue
		}
		below++
	}
	if above == 0 {
		t.Errorf("no order in a 200 order batch is above the %d paise ceiling, so R3 can never fire",
			policy.DefaultAmountCeilingPaise)
	}
	if below == 0 {
		t.Errorf("every order is above the ceiling, so every order escalates on amount alone")
	}
	// A ceiling in the middle of the distribution is not a ceiling: that is the
	// phase 2 finding, and it is the reason the range is what it is.
	if share := float64(above) / float64(len(m.Orders)); share > 0.2 {
		t.Errorf("%.2f of the batch is above the ceiling, which swamps every escalation number", share)
	}
}
