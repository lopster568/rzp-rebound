package runner

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lopster568/rzp-recovery-agent/internal/batch"
)

// Materialised is one manifest order as it exists in a gateway.
//
// The manifest id and the gateway id are different on purpose. A manifest is a
// specification of a batch, and every arm materialises its own copy of it, so
// that one arm recovering an order cannot change what the next arm sees. On
// the live layer the gateway id is a real Razorpay order id.
type Materialised struct {
	ManifestID string
	Visible    batch.AgentVisibleOrder
	Attempts   int
}

// OutcomeRow is one order's result, one line of outcomes.jsonl. It is the file
// the Python scorer reads, and every field on it comes from the gateway or
// from the arm's own decision record, never from what an action claimed.
type OutcomeRow struct {
	RunID            string `json:"run_id"`
	Arm              string `json:"arm"`
	Layer            string `json:"layer"`
	BatchID          string `json:"batch_id"`
	ManifestOrderID  string `json:"manifest_order_id"`
	GatewayOrderID   string `json:"gateway_order_id"`
	Class            string `json:"class"`
	ActionKind       string `json:"action_kind"`
	FinalOrderStatus string `json:"final_order_status"`
	Recovered        bool   `json:"recovered"`
	ClaimedRecovered bool   `json:"claimed_recovered"`
	AmountPaidPaise  int64  `json:"amount_paid_paise"`
	AttemptsSeen     int    `json:"attempts_seen"`
	AttemptsAfter    int    `json:"attempts_after"`
	PolicyVerdict    string `json:"policy_verdict"`
	PolicyRule       string `json:"policy_rule"`
	Escalated        bool   `json:"escalated"`
	SideEffect       bool   `json:"side_effect"`
	TimedOut         bool   `json:"timed_out"`
	Error            string `json:"error"`
	// Observed reports that the final order state was read back from the
	// gateway. False makes the row unscorable, which the scorer counts and
	// keeps out of every denominator rather than dropping.
	Observed bool `json:"observed"`
	APICalls int  `json:"api_calls"`
}

// orderSequence returns the manifest orders in the order the harness asked
// for. An empty path means manifest order.
func OrderSequence(file *BatchFile, path string) ([]batch.Order, error) {
	if path == "" {
		return file.Orders, nil
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the order sequence at %s: %w", path, err)
	}
	byID := make(map[string]batch.Order, len(file.Orders))
	for _, o := range file.Orders {
		byID[o.OrderID] = o
	}

	var out []batch.Order
	seen := make(map[string]bool, len(file.Orders))
	for _, line := range strings.Split(string(raw), "\n") {
		id := strings.TrimSpace(line)
		if id == "" {
			continue
		}
		o, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("the order sequence names %s, which is not in batch %s", id, file.BatchID)
		}
		if seen[id] {
			return nil, fmt.Errorf("the order sequence names %s twice", id)
		}
		seen[id] = true
		out = append(out, o)
	}
	if len(out) != len(file.Orders) {
		return nil, fmt.Errorf("the order sequence has %d orders and batch %s has %d",
			len(out), file.BatchID, len(file.Orders))
	}
	return out, nil
}

func WriteJSONLine(w io.Writer, v any) error {
	encoded, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode an outcome row: %w", err)
	}
	if _, err := w.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("write an outcome row: %w", err)
	}
	return nil
}

// SeededAttempts is how many failed attempts an order arrives with.
//
// One for an ordinary order: the failure that put it in the batch. For the
// attempt-budget-exhausted bait it is the budget its class justifies, already
// spent, which is what makes another attempt wrong even though the class says
// retry.
func SeededAttempts(o batch.Order) int {
	if o.PriorAttempts > 0 {
		return o.PriorAttempts
	}
	return 1
}

// RecoversOnRetry is the gateway's answer to whether re-presenting the same
// instrument can work.
//
// It reads the manifest, and it is read by the gateway rather than by an arm.
// An order is recoverable by a retry when its ground truth says it is
// recoverable and the correct action is to retry. The two classes that need
// the customer back are not: the correct action there is to raise a payment
// link, and this project observes an API call and never a person, so nothing
// in it can model one coming back.
func RecoversOnRetry(o batch.Order) bool {
	return o.GroundTruthRecoverable && o.GroundTruthCorrectAction == batch.ActionRetrySameInstrument
}
