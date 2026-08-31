package runner

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/lopster568/rzp-recovery-agent/internal/batch"
)

// BatchFile is what a seeded batch looks like on disk. It is the manifest plus
// the two things a later run has to be able to name it by.
//
// It carries no timestamp, so the same seed and size produce a byte-identical
// file and two runs can be compared without a diff full of clocks. The run
// record under results/runs/ carries the time.
type BatchFile struct {
	BatchID string        `json:"batch_id"`
	Seed    int64         `json:"seed"`
	Layer   string        `json:"layer"`
	Orders  []batch.Order `json:"orders"`
}

// ReadBatchFile reads a manifest written by runSeed.
func ReadBatchFile(path string) (*BatchFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the batch manifest at %s: %w", path, err)
	}
	var file BatchFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("parse the batch manifest at %s: %w", path, err)
	}
	if len(file.Orders) == 0 {
		return nil, fmt.Errorf("the batch manifest at %s has no orders in it", path)
	}
	return &file, nil
}
