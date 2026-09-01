package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/lopster568/rzp-recovery-agent/internal/batch"
	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/runner"
)

// The batch composition moved to internal/batch as a named profile in phase 5.
//
// It used to be a table here whose comment called it "the shape of a real
// failure mix", which was a claim with nothing behind it. It is now
// batch.Profile "uniform-invented", with the same shares and a source string
// that says the author chose them. batch.Profiles has the other two.
//
// never_retry is still deliberately absent from every non-bait distribution.
// batch.MaxLegitAttemptsFor gives it 0 and batch.CorrectActionFor gives it
// do_nothing, which is the shape of a bait order, and
// TestManifestCarriesGroundTruthForEveryOrder requires a non-bait order to
// have at least one legitimate attempt. So never-retry orders enter a batch as
// bait, which is where an order nobody should act on belongs.

// defaultProfile keeps the old command line meaning the old thing.
const defaultProfile = "uniform-invented"

// runSeed writes a batch manifest under results/batches/.
func runSeed(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	seed := fs.Int64("seed", 1234, "the seed that makes the batch reproducible")
	n := fs.Int("n", 40, "how many orders in total, bait included")
	bait := fs.Int("bait", 3, "how many of them are bait orders")
	layer := fs.String("layer", "fake", "which measurement layer this batch is for: fake, replay, or live")
	profileName := fs.String("profile", defaultProfile,
		"the failure mix: "+strings.Join(batch.ProfileNames(), ", "))
	out := fs.String("out", "", "where the manifest goes (default: results/batches/<batch_id>.json)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	baitSetByHand := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "bait" {
			baitSetByHand = true
		}
	})

	if *bait >= *n {
		return fmt.Errorf("a batch of %d orders cannot be %d bait: there would be nothing to recover", *n, *bait)
	}

	profile, ok := batch.ProfileByName(*profileName)
	if !ok {
		return fmt.Errorf("no profile named %s. The three are: %s", *profileName, strings.Join(batch.ProfileNames(), ", "))
	}
	// A profile whose bait share is cited computes its own bait count. Taking
	// --bait as well and ignoring it would be a flag that silently does
	// nothing, so it is refused instead.
	if profile.DefinesBait() && baitSetByHand {
		return fmt.Errorf("profile %s sets its own bait share of %.2f, so --bait cannot be given as well",
			profile.Name, profile.BaitShare())
	}

	spec, err := profile.Spec(*seed, *n, *bait)
	if err != nil {
		return err
	}
	manifest, err := batch.Generate(spec)
	if err != nil {
		return err
	}

	// The default profile keeps the old batch id, so every path in the phase 2
	// and phase 3 documents still names the file it named. Any other profile
	// puts its name in the id, because two batches with the same seed and size
	// and different mixes are two different batches and must not share a path.
	batchID := fmt.Sprintf("b-%d-%d", *seed, len(manifest.Orders))
	if profile.Name != defaultProfile {
		batchID += "-" + profile.Name
	}

	file := runner.BatchFile{
		BatchID: batchID,
		Seed:    *seed,
		Layer:   *layer,
		Orders:  manifest.Orders,
	}

	path := *out
	if path == "" {
		path = filepath.Join("results", "batches", file.BatchID+".json")
	}
	if err := writeJSON(path, file); err != nil {
		return err
	}

	fmt.Printf("batch    %s\n", file.BatchID)
	fmt.Printf("seed     %d\n", file.Seed)
	fmt.Printf("layer    %s\n", file.Layer)
	fmt.Printf("profile  %s (%s)\n", profile.Name, citedWord(profile.Cited))
	fmt.Printf("source   %s\n", profile.Source)
	fmt.Printf("orders   %d, of which %d are bait\n", len(file.Orders), spec.BaitOrders)
	for _, class := range sortedClasses(manifest.CountsByClass()) {
		fmt.Printf("  %-26s %d\n", class, manifest.CountsByClass()[class])
	}
	counts := baitCounts(manifest)
	for _, kind := range slices.Sorted(maps.Keys(counts)) {
		fmt.Printf("  bait %-21s %d\n", kind, counts[kind])
	}
	fmt.Printf("manifest %s\n", path)
	fmt.Println()
	fmt.Println("The ground truth in this file is the answer key. Nothing in it reaches an")
	fmt.Println("arm: batch.AgentVisibleOrder is a separate type carrying four fields.")
	return nil
}

// citedWord is what the seed output prints next to a profile name, so an
// operator reading the terminal knows which kind of mix they just built.
func citedWord(cited bool) string {
	if cited {
		return "cited"
	}
	return "not cited"
}

func sortedClasses(counts map[classify.Class]int) []classify.Class {
	out := make([]classify.Class, 0, len(counts))
	for class := range counts {
		out = append(out, class)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

func baitCounts(m *batch.Manifest) map[batch.BaitKind]int {
	out := make(map[batch.BaitKind]int)
	for _, o := range m.Orders {
		if o.IsBait {
			out[o.BaitKind]++
		}
	}
	return out
}

// writeJSON writes v as indented JSON, creating the directory.
func writeJSON(path string, v any) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("make %s: %w", dir, err)
		}
	}
	encoded, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o644)
}
