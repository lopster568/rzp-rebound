package batch

// A batch profile is a named failure mix. Before phase 5 there was one mix,
// hard coded in cmd/rzp/seed.go, whose own comment called it "the shape of a
// real failure mix" with nothing behind that sentence. Three named profiles
// replace it, and the rule is the phase 5 rule: a profile's shares are either
// cited, with a source and a vintage, or they say they are invented.

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/lopster568/rzp-recovery-agent/internal/classify"
)

// ObservedMixFileEnv names the environment variable holding the path to a real
// merchant's failure-mix file.
//
// It is an environment variable pointing outside the repository, and not a file
// under testdata/, for one reason: a real merchant's failure mix is that
// merchant's data. Nothing this loader reads enters git, and nothing it reads
// is published.
const ObservedMixFileEnv = "RZP_OBSERVED_MIX_FILE"

// ClassShare is one class's share of a batch.
type ClassShare struct {
	Class classify.Class
	Share float64
	// Cited reports that this share comes from the profile's published source
	// rather than from a residual the source does not break out.
	Cited bool
	// Note says what an uncited share is.
	Note string
}

// Profile is a named failure mix.
type Profile struct {
	// Name is what a caller asks for and what a batch id carries.
	Name string
	// Vintage is the year the source describes. A cited profile has one,
	// because a 2019 decline mix is a 2019 decline mix and a reader has to be
	// able to see that without opening the source.
	Vintage string
	// Source is where the shares came from, or the word invented.
	Source string
	// Cited reports that the shares come from a published source.
	Cited bool

	shares    []ClassShare
	baitShare float64
	// baitKind is the single kind this profile's bait takes when it defines its
	// own bait share. Empty means the two-kind rotation.
	baitKind BaitKind
	// load fills shares and baitShare at Spec time. Only the observed-live-mix
	// profile has one, because only it reads its numbers off disk.
	load func() ([]ClassShare, float64, error)
}

// residual is the note on a share the source does not break out.
const residual = "residual: the source names three categories summing to 79 percent and does not break out the rest, so this share is ours and is not cited"

// profiles is the closed list, in the order Profiles returns them.
var profiles = []Profile{
	{
		Name:   "uniform-invented",
		Source: "invented. These are the shares cmd/rzp/seed.go carried from phase 2, chosen by the author with no source behind them. Kept unchanged under a name that says so, because the phase 3 tables were produced on them and a comparison needs one arm of it not to move.",
		Cited:  false,
		shares: []ClassShare{
			{Class: classify.TransientRetryEligible, Share: 0.28},
			{Class: classify.RetryEligible, Share: 0.24},
			{Class: classify.ReauthRequired, Share: 0.24},
			{Class: classify.NewInstrumentRequired, Share: 0.24},
		},
	},
	{
		Name:    "ethoca-card-mix-2017",
		Vintage: "2017",
		Source:  "Mastercard/Ethoca published card-decline shares: insufficient funds 44 percent, lost or stolen 26 percent, fraud 9 percent. https://www.ethoca.com/in-the-media/ article dated 2017-04-28, read 2026-09-01. The vintage was recorded as 2019 until a first-hand check of the article's date on 2026-09-01 corrected it. A widely quoted companion claim, that response codes 05 and 51 together are about 80 percent of declines, is not in this article and could not be verified anywhere, so it is not carried.",
		Cited:   true,
		shares: []ClassShare{
			{
				Class: classify.RetryEligible, Share: 0.44, Cited: true,
				Note: "insufficient funds, the source's largest category",
			},
			{Class: classify.TransientRetryEligible, Share: 0.07, Note: residual},
			{Class: classify.ReauthRequired, Share: 0.07, Note: residual},
			{Class: classify.NewInstrumentRequired, Share: 0.07, Note: residual},
		},
		// Lost or stolen 0.26 plus fraud 0.09. Both are declines no merchant
		// should reattempt and no merchant should message the cardholder about,
		// which is what a bait order is in this repository's vocabulary: an
		// order whose correct action is to do nothing. The share is the
		// source's.
		baitShare: 0.35,
		baitKind:  BaitNeverRetry,
	},
	{
		Name:   "observed-live-mix",
		Source: "a real merchant's own aggregates, not published and not in this repository. Set " + ObservedMixFileEnv + " to a path outside the working tree.",
		Cited:  false,
		load:   loadObservedMix,
	},
}

// Shares returns the non-bait class shares.
func (p Profile) Shares() []ClassShare { return p.shares }

// BaitShare returns the share of the batch this profile says no arm should act
// on, or zero when the profile leaves bait to an explicit count.
func (p Profile) BaitShare() float64 { return p.baitShare }

// DefinesBait reports whether the profile sets its own bait share.
func (p Profile) DefinesBait() bool { return p.baitShare > 0 }

// Spec turns the profile into a batch specification.
//
// n is the whole batch, bait included. bait is the explicit bait count and is
// used only by a profile that does not define its own share; a profile that
// does define one computes bait from it, and cmd/rzp/seed.go refuses an
// explicit --bait against such a profile rather than ignoring the flag.
func (p Profile) Spec(seed int64, n, bait int) (Spec, error) {
	if n <= 0 {
		return Spec{}, fmt.Errorf("batch: a batch of %d orders", n)
	}
	if bait < 0 || bait >= n {
		return Spec{}, fmt.Errorf("batch: %d bait in a batch of %d", bait, n)
	}

	shares, baitShare := p.shares, p.baitShare
	if p.load != nil {
		var err error
		shares, baitShare, err = p.load()
		if err != nil {
			return Spec{}, err
		}
	}
	if len(shares) == 0 {
		return Spec{}, fmt.Errorf("batch: profile %s has no shares", p.Name)
	}

	var counts []int
	if baitShare > 0 {
		// Bait is one bucket in the apportionment, so the cited bait share is
		// held exactly rather than being whatever is left after rounding.
		weights := make([]float64, 0, len(shares)+1)
		for _, s := range shares {
			weights = append(weights, s.Share)
		}
		weights = append(weights, baitShare)
		counts = apportion(weights, n)
		bait = counts[len(counts)-1]
		counts = counts[:len(counts)-1]
	} else {
		weights := make([]float64, 0, len(shares))
		for _, s := range shares {
			weights = append(weights, s.Share)
		}
		counts = apportion(weights, n-bait)
	}

	dist := make(map[classify.Class]int, len(shares))
	for i, s := range shares {
		dist[s.Class] += counts[i]
	}

	spec := Spec{Seed: seed, Distribution: dist, BaitOrders: bait}
	if p.baitKind != "" {
		kinds := make([]BaitKind, bait)
		for i := range kinds {
			kinds[i] = p.baitKind
		}
		spec.BaitKinds = kinds
	}
	return spec, nil
}

// apportion splits total across weights by the largest-remainder method.
//
// Largest remainder rather than "give the leftover to the first class", which
// is what cmd/rzp/seed.go did. The old rule is fine when every share is the
// author's own and nothing rides on any single one being exact. It is wrong the
// moment one share is cited: the leftover would land on the cited share and
// move it, and a 44 percent figure read off somebody's published research is
// the last number in the batch that should absorb a rounding error.
//
// Ties go to the earlier weight, so the result is deterministic and a seed
// still reproduces a batch.
func apportion(weights []float64, total int) []int {
	counts := make([]int, len(weights))
	type remainder struct {
		index int
		frac  float64
	}
	rems := make([]remainder, 0, len(weights))
	assigned := 0
	for i, w := range weights {
		quota := w * float64(total)
		counts[i] = int(quota)
		assigned += counts[i]
		rems = append(rems, remainder{index: i, frac: quota - float64(counts[i])})
	}
	sort.SliceStable(rems, func(a, b int) bool { return rems[a].frac > rems[b].frac })
	for i := 0; assigned < total; i++ {
		counts[rems[i%len(rems)].index]++
		assigned++
	}
	return counts
}

// observedMixFile is the shape RZP_OBSERVED_MIX_FILE holds. It is not a type
// this repository ever writes: a merchant produces it, and this is the reader.
type observedMixFile struct {
	Source       string             `json:"source"`
	ObservedFrom string             `json:"observed_from"`
	ObservedTo   string             `json:"observed_to"`
	Shares       map[string]float64 `json:"shares"`
	BaitShare    float64            `json:"bait_share"`
}

// loadObservedMix reads a merchant's own failure mix.
//
// Nothing ships in this slot. A read-only probe of the author's live Razorpay
// merchant account on 2026-09-01 returned two payments over six weeks, one
// captured and one failed. Two payments are a specimen and not a distribution,
// and seeding a mix from them would be exactly the invention phase 5 exists to
// remove. The loader is here for the day there is real data, and the profile
// errors until then.
func loadObservedMix() ([]ClassShare, float64, error) {
	path := os.Getenv(ObservedMixFileEnv)
	if path == "" {
		return nil, 0, fmt.Errorf(
			"batch: the observed-live-mix profile has no data. Set %s to a JSON file outside this repository holding a real merchant's aggregates. Nothing ships in this slot, because the only live account this project has read holds two payments, and two payments are a specimen rather than a distribution",
			ObservedMixFileEnv)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, fmt.Errorf("batch: read %s from %s: %w", path, ObservedMixFileEnv, err)
	}
	var f observedMixFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, 0, fmt.Errorf("batch: parse %s: %w", path, err)
	}
	if len(f.Shares) == 0 {
		return nil, 0, fmt.Errorf("batch: %s lists no shares", path)
	}

	shares := make([]ClassShare, 0, len(f.Shares))
	for name := range f.Shares {
		class, ok := classify.ParseClass(name)
		if !ok {
			return nil, 0, fmt.Errorf("batch: %s names a class %q that does not exist", path, name)
		}
		shares = append(shares, ClassShare{
			Class: class,
			Share: f.Shares[name],
			Note:  "observed by a merchant over " + f.ObservedFrom + " to " + f.ObservedTo + ", from " + f.Source,
		})
	}
	// Map iteration is random and a seed has to reproduce a batch.
	sort.Slice(shares, func(a, b int) bool { return shares[a].Class < shares[b].Class })
	return shares, f.BaitShare, nil
}

// Profiles returns every profile.
func Profiles() []Profile { return append([]Profile(nil), profiles...) }

// ProfileByName returns the profile with this name.
func ProfileByName(name string) (Profile, bool) {
	for _, p := range profiles {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

// ProfileNames returns every profile name, for a flag's help text and for an
// error message that can tell a caller what it could have asked for.
func ProfileNames() []string {
	out := make([]string, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, p.Name)
	}
	return out
}
