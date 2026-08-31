package policy_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
)

// update rewrites the golden file instead of comparing against it.
//
//	go test ./internal/policy -update
var update = flag.Bool("update", false, "rewrite testdata/policy_matrix.golden")

const goldenPath = "testdata/policy_matrix.golden"

// matrixInput is one cell of the cross product.
type matrixInput struct {
	class      classify.Class
	attempts   int
	amountName string
	amount     int64
	elapsedTag string
	elapsed    time.Duration
	killSwitch bool
}

// String is what a failure message and the golden file both key on, so a diff
// names the cell rather than a line number.
func (in matrixInput) String() string {
	return fmt.Sprintf("class=%s attempts=%d amount=%s elapsed=%s kill=%t",
		in.class, in.attempts, in.amountName, in.elapsedTag, in.killSwitch)
}

// build turns a cell into the pair Evaluate takes.
func (in matrixInput) build() (policy.State, policy.Request) {
	state := policy.State{
		AttemptsMade:      in.attempts,
		LastActionAt:      start.Add(-in.elapsed),
		KillSwitchEngaged: in.killSwitch,
	}
	req := policy.Request{
		OrderID:     "order_matrix",
		Action:      policy.ActionRetrySameInstrument,
		Class:       in.class,
		AmountPaise: in.amount,
		AttemptNo:   in.attempts + 1,
	}
	return state, req
}

// matrixInputs is the full cross product, in a fixed order.
//
// Deliberately absent: the action kind, the run-wide budget, and the
// idempotency key. R5, R6, and R9 are covered by the per-rule tables in
// policy_test.go instead. Widening this to reach them would multiply 576 rows
// by twelve for three rules' worth of new information, and PLAN.md says so.
func matrixInputs() []matrixInput {
	amounts := []struct {
		name string
		v    int64
	}{
		{"below", testCeiling - 1},
		{"at", testCeiling},
		{"above", testCeiling + 1},
	}
	elapsed := []struct {
		tag string
		d   time.Duration
	}{
		{"0", 0},
		{"cooldown-1s", testCooldown - time.Second},
		{"cooldown", testCooldown},
		{"cooldown+1s", testCooldown + time.Second},
	}

	var out []matrixInput
	for _, class := range allClasses {
		for attempts := range 4 {
			for _, amount := range amounts {
				for _, e := range elapsed {
					for _, kill := range []bool{false, true} {
						out = append(out, matrixInput{
							class:      class,
							attempts:   attempts,
							amountName: amount.name,
							amount:     amount.v,
							elapsedTag: e.tag,
							elapsed:    e.d,
							killSwitch: kill,
						})
					}
				}
			}
		}
	}
	return out
}

// TestPolicyGoldenMatrix serializes the whole cross product to one file, so a
// change to any rule or to the rule order arrives as a reviewable diff instead
// of as a behaviour change nobody looked at.
func TestPolicyGoldenMatrix(t *testing.T) {
	const wantCells = 6 * 4 * 3 * 4 * 2

	inputs := matrixInputs()
	if len(inputs) != wantCells {
		t.Fatalf("the matrix has %d cells, want %d", len(inputs), wantCells)
	}

	p := policy.New(testConfig(), clock.NewFake(start))

	var b strings.Builder
	fmt.Fprintf(&b, "# policy decision matrix, %d cells\n", len(inputs))
	fmt.Fprintf(&b, "# max_attempts=%d cooldown=%s ceiling=%d paise budget=%d\n",
		testConfig().MaxAttemptsPerOrder, testCooldown, testCeiling, testConfig().ActionBudget)
	fmt.Fprintf(&b, "# regenerate with: go test ./internal/policy -update\n")
	for _, in := range inputs {
		state, req := in.build()
		got := p.Evaluate(state, req)
		fmt.Fprintf(&b, "%s -> %s %s remaining=%d\n", in, got.Verdict, got.RuleID, got.Remaining)
	}
	got := b.String()

	if *update {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s with %d cells", goldenPath, len(inputs))
		return
	}

	wantBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read the golden file (run go test ./internal/policy -update to create it): %v", err)
	}
	want := string(wantBytes)
	if got == want {
		return
	}

	// Report the first differing cell rather than the whole file. 576 lines of
	// diff in a test log is a diff nobody reads.
	gotLines, wantLines := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := range max(len(gotLines), len(wantLines)) {
		g, w := "", ""
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if g != w {
			t.Fatalf("%s line %d differs:\n  got  %s\n  want %s\n(%d lines got, %d want; -update rewrites the file)",
				goldenPath, i+1, g, w, len(gotLines), len(wantLines))
		}
	}
	t.Fatalf("%s differs in a way the line walk did not find", goldenPath)
}
