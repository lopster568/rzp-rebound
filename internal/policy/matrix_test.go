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

// The signal dimension: what the detector saw, and what it classified to.
//
// "none" and "unclassified" are different facts that both leave Class at
// classify.Unclassified, which is why the request carries SignalPresent
// separately. An abandoned cart has no failure to report and is the first; a
// failed payment whose reason nothing could read is the second.
const (
	signalNone         = "none"
	signalClassified   = "classified"
	signalUnclassified = "unclassified"
	signalRiskRefused  = "risk-refused"
)

var allSignals = []string{signalNone, signalClassified, signalUnclassified, signalRiskRefused}

// The trigger dimension: one world state per rule this matrix reaches.
//
// Each one is a single mutation off the clean baseline, so a cell that changes
// names the rule whose input moved. The quiet-hours trigger is the one that
// moves the clock rather than the request, which is why matrixInput carries its
// own instant and its own policy.
const (
	triggerClean        = "clean"
	triggerKillSwitch   = "kill-switch"
	triggerReplay       = "replay"
	triggerNotYetDue    = "not-yet-due"
	triggerNoChannel    = "no-channel"
	triggerCancelled    = "cancelled-resource"
	triggerPromiseHold  = "promise-hold"
	triggerQuietHours   = "quiet-hours"
	triggerDisputed     = "disputed"
	triggerAboveCeiling = "above-ceiling"
	triggerTouchesSpent = "touches-spent"
	triggerCooldown     = "inside-cooldown"
	triggerNotifyBurst  = "inside-send-window"
	triggerBudgetSpent  = "budget-spent"
)

var allTriggers = []string{
	triggerClean,
	triggerKillSwitch,
	triggerReplay,
	triggerNotYetDue,
	triggerNoChannel,
	triggerCancelled,
	triggerPromiseHold,
	triggerQuietHours,
	triggerDisputed,
	triggerAboveCeiling,
	triggerTouchesSpent,
	triggerCooldown,
	triggerNotifyBurst,
	triggerBudgetSpent,
}

// matrixInput is one cell of the cross product.
type matrixInput struct {
	source  string
	action  string
	signal  string
	trigger string
}

// String is what a failure message and the golden file both key on, so a diff
// names the cell rather than a line number.
func (in matrixInput) String() string {
	return fmt.Sprintf("source=%s action=%s signal=%s trigger=%s",
		orNone(in.source), in.action, in.signal, in.trigger)
}

func orNone(source string) string {
	if source == "" {
		return "(unknown)"
	}
	return source
}

// now is the instant this cell is evaluated at. Every cell but the quiet-hours
// one reads noon.
func (in matrixInput) now() time.Time {
	if in.trigger == triggerQuietHours {
		return night
	}
	return start
}

// policy is the engine this cell is evaluated by. Two exist rather than one
// because the quiet-hours rule reads the clock and nothing else.
func (in matrixInput) policy() *policy.Policy {
	return policy.New(testConfig(), clock.NewFake(in.now()))
}

// build turns a cell into the pair Evaluate takes.
//
// Every offset is measured from now rather than from start, so the quiet-hours
// cells describe the same world as the rest and differ only in the hour.
func (in matrixInput) build(now time.Time) (policy.State, policy.Request) {
	state := policy.State{}
	req := policy.Request{
		RiskItemID:     "ri_matrix",
		Source:         in.source,
		Action:         in.action,
		AmountPaise:    testCeiling - 1,
		AmountDuePaise: testCeiling - 1,
		HasEmail:       true,
		HasContact:     true,
		AtRiskSince:    now.Add(-30 * 24 * time.Hour),
		TouchNo:        1,
	}

	switch in.signal {
	case signalNone:
	case signalClassified:
		req.SignalPresent, req.Class = true, classify.ReauthRequired
	case signalUnclassified:
		req.SignalPresent, req.Class = true, classify.Unclassified
	case signalRiskRefused:
		req.SignalPresent, req.Class = true, classify.NeverRetry
	}

	switch in.trigger {
	case triggerClean, triggerQuietHours:
	case triggerKillSwitch:
		state.KillSwitchEngaged = true
	case triggerReplay:
		state.IdempotencyKeySeen = true
	case triggerNotYetDue:
		req.AtRiskSince = now
	case triggerNoChannel:
		req.HasEmail, req.HasContact = false, false
	case triggerCancelled:
		req.SourceStatus = "cancelled"
	case triggerPromiseHold:
		req.PromiseHoldUntil = now.Add(24 * time.Hour)
	case triggerDisputed:
		req.Disputed = true
	case triggerAboveCeiling:
		req.AmountPaise, req.AmountDuePaise = testCeiling+1, testCeiling+1
	case triggerTouchesSpent:
		state.TouchesMade = 9
	case triggerCooldown:
		state.TouchesMade, state.LastTouchAt = 1, now.Add(-time.Second)
	case triggerNotifyBurst:
		state.LastNotifyAt = now.Add(-time.Second)
	case triggerBudgetSpent:
		state.ActionsThisRun = testBudget
	}

	return state, req
}

// matrixInputs is the full cross product, in a fixed order.
//
// Deliberately absent: the amount below the ceiling but above the write-off
// floor is fixed rather than swept, and the per-source cooldown and grace
// lengths are exercised at one point each rather than at their boundaries. The
// per-rule tables in policy_test.go walk those edges, where a failure names the
// boundary it crossed. Widening this file to reach them would multiply every
// cell by four for information a reader would then have to reconstruct from a
// golden diff.
func matrixInputs() []matrixInput {
	var out []matrixInput
	for _, source := range allSources {
		for _, action := range policy.LawfulActions() {
			for _, signal := range allSignals {
				for _, trigger := range allTriggers {
					out = append(out, matrixInput{
						source:  source,
						action:  action,
						signal:  signal,
						trigger: trigger,
					})
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
	wantCells := len(allSources) * len(policy.LawfulActions()) * len(allSignals) * len(allTriggers)

	inputs := matrixInputs()
	if len(inputs) != wantCells {
		t.Fatalf("the matrix has %d cells, want %d", len(inputs), wantCells)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# policy decision matrix, %d cells\n", len(inputs))
	fmt.Fprintf(&b, "# %d sources x %d actions x %d signals x %d triggers\n",
		len(allSources), len(policy.LawfulActions()), len(allSignals), len(allTriggers))
	fmt.Fprintf(&b, "# ceiling=%d paise write_off_floor=%d paise notify_window=%s budget=%d\n",
		testCeiling, testWriteOffFloor, testNotifyWindow, testBudget)
	fmt.Fprintf(&b, "# contact cadence comes from the per-source table, not from the config\n")
	for _, source := range policy.Sources() {
		p := policy.New(testConfig(), clock.NewFake(start)).ParamsFor(source)
		fmt.Fprintf(&b, "#   %-16s grace=%-6s max_touches=%d cooldown=%s\n",
			source, p.Grace, p.MaxTouches, p.Cooldown)
	}
	fmt.Fprintf(&b, "# the quiet-hours cells are evaluated at %s, every other cell at %s\n",
		night.Format("15:04 MST"), start.Format("15:04 MST"))
	fmt.Fprintf(&b, "# regenerate with: go test ./internal/policy -update\n")

	for _, in := range inputs {
		state, req := in.build(in.now())
		got := in.policy().Evaluate(state, req)
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

	// Report the first differing cell rather than the whole file. Two thousand
	// lines of diff in a test log is a diff nobody reads.
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
