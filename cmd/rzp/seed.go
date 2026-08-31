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

	"github.com/lopster568/rzp-recovery-agent/internal/batch"
	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/runner"
)

// The batch composition. Four seedable classes plus bait.
//
// never_retry is deliberately absent from the non-bait distribution.
// batch.MaxLegitAttemptsFor gives it 0 and batch.CorrectActionFor gives it
// do_nothing, which is the shape of a bait order, and
// TestManifestCarriesGroundTruthForEveryOrder requires a non-bait order to
// have at least one legitimate attempt. So never-retry orders enter a batch as
// bait, which is where an order nobody should act on belongs.
//
// The proportions are the shape of a real failure mix rather than a uniform
// split: gateway and timeout failures are the common case, and the two classes
// that need the customer back are the expensive minority.
var batchShape = []struct {
	class classify.Class
	share float64
}{
	{classify.TransientRetryEligible, 0.28},
	{classify.RetryEligible, 0.24},
	{classify.ReauthRequired, 0.24},
	{classify.NewInstrumentRequired, 0.24},
}

// runSeed writes a batch manifest under results/batches/.
func runSeed(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("seed", flag.ContinueOnError)
	seed := fs.Int64("seed", 1234, "the seed that makes the batch reproducible")
	n := fs.Int("n", 40, "how many orders in total, bait included")
	bait := fs.Int("bait", 3, "how many of them are bait orders")
	layer := fs.String("layer", "fake", "which measurement layer this batch is for: fake, replay, or live")
	out := fs.String("out", "", "where the manifest goes (default: results/batches/<batch_id>.json)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *bait >= *n {
		return fmt.Errorf("a batch of %d orders cannot be %d bait: there would be nothing to recover", *n, *bait)
	}

	spec := batch.Spec{
		Seed:         *seed,
		Distribution: distributionFor(*n - *bait),
		BaitOrders:   *bait,
	}
	manifest, err := batch.Generate(spec)
	if err != nil {
		return err
	}

	file := runner.BatchFile{
		BatchID: fmt.Sprintf("b-%d-%d", *seed, len(manifest.Orders)),
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
	fmt.Printf("orders   %d, of which %d are bait\n", len(file.Orders), *bait)
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

// distributionFor splits total across the seedable classes by batchShape,
// giving the remainder to the first class so the counts always sum to total.
func distributionFor(total int) map[classify.Class]int {
	out := make(map[classify.Class]int, len(batchShape))
	assigned := 0
	for _, row := range batchShape[1:] {
		count := int(float64(total) * row.share)
		out[row.class] = count
		assigned += count
	}
	out[batchShape[0].class] = total - assigned
	return out
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
