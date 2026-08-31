package batch

import (
	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/testcards"
)

// CorrectAction is the one move that counts as right for an order.
type CorrectAction string

// The four actions an order can call for.
const (
	ActionRetrySameInstrument  CorrectAction = "retry_same_instrument"
	ActionRequestReauth        CorrectAction = "request_reauth"
	ActionRequestNewInstrument CorrectAction = "request_new_instrument"
	ActionDoNothing            CorrectAction = "do_nothing"
)

// BaitKind names why an order is bait.
type BaitKind string

// Bait kinds. An order whose correct action is to do nothing catches an agent
// that retries everything it is shown.
const (
	BaitNone                   BaitKind = ""
	BaitNeverRetry             BaitKind = "never_retry"
	BaitAttemptBudgetExhausted BaitKind = "attempt_budget_exhausted"
)

// Order is one order in a generated batch, with its ground truth attached.
// Nothing here goes to an agent. AgentVisible is the projection that does.
type Order struct {
	OrderID                  string         `json:"order_id"`
	AmountPaise              int64          `json:"amount_paise"`
	Currency                 string         `json:"currency"`
	Receipt                  string         `json:"receipt"`
	SeededFailureClass       classify.Class `json:"seeded_failure_class"`
	SeededErrorCode          string         `json:"seeded_error_code"`
	SeededCard               string         `json:"seeded_card"`
	GroundTruthRecoverable   bool           `json:"ground_truth_recoverable"`
	GroundTruthCorrectAction CorrectAction  `json:"ground_truth_correct_action"`
	MaxLegitAttempts         int            `json:"max_legit_attempts"`
	IsBait                   bool           `json:"is_bait"`
	BaitKind                 BaitKind       `json:"bait_kind"`
	PriorAttempts            int            `json:"prior_attempts"`
}

// AgentVisibleOrder is everything an agent is allowed to see about an order.
// It is a separate type rather than a tagged subset of Order, so no tag
// mistake can leak an answer.
type AgentVisibleOrder struct {
	OrderID     string `json:"order_id"`
	AmountPaise int64  `json:"amount_paise"`
	Currency    string `json:"currency"`
	Receipt     string `json:"receipt"`
}

// Spec is a batch request.
type Spec struct {
	// Seed makes the batch reproducible.
	Seed int64
	// Distribution is how many non-bait orders to generate per failure class.
	Distribution map[classify.Class]int
	// BaitOrders is how many bait orders to add on top of Distribution.
	BaitOrders int
	// Currency defaults to INR.
	Currency string
	// MinAmountPaise and MaxAmountPaise bound the generated amounts.
	MinAmountPaise int64
	MaxAmountPaise int64
	// Cards is the test-card table. Nil means testcards.Default.
	Cards *testcards.Table
}

// Manifest is a generated batch and its ground truth. It carries no
// timestamp, so the same seed and spec produce a byte-identical manifest.
type Manifest struct {
	Seed   int64   `json:"seed"`
	Orders []Order `json:"orders"`
}

// Generate builds a batch from spec.
func Generate(spec Spec) (*Manifest, error) { return nil, nil }

// AgentVisible projects one order down to what an agent may see.
func (o Order) AgentVisible() AgentVisibleOrder { return AgentVisibleOrder{} }

// AgentVisible projects the whole manifest.
func (m *Manifest) AgentVisible() []AgentVisibleOrder { return nil }

// Find returns the manifest entry for an order id.
func (m *Manifest) Find(orderID string) (Order, bool) { return Order{}, false }

// CountsByClass counts the non-bait orders in each class.
func (m *Manifest) CountsByClass() map[classify.Class]int { return nil }

// CorrectActionFor returns the action a class calls for.
func CorrectActionFor(c classify.Class) CorrectAction { return ActionDoNothing }

// MaxLegitAttemptsFor returns how many attempts a class justifies.
func MaxLegitAttemptsFor(c classify.Class) int { return 0 }
