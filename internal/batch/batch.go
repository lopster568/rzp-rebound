package batch

import (
	"fmt"
	"maps"
	"math/rand"
	"slices"
	"strings"

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
	BaitNone BaitKind = ""
	// BaitNeverRetry is a risk-blocked order. Any attempt on it is wrong.
	BaitNeverRetry BaitKind = "never_retry"
	// BaitAttemptBudgetExhausted is a retry-eligible order that has already
	// had every attempt it justifies. The class says retry, the history says
	// stop.
	BaitAttemptBudgetExhausted BaitKind = "attempt_budget_exhausted"
)

// baitKinds is the rotation the generator draws from, in a fixed order so a
// seed reproduces the same mix.
var baitKinds = []BaitKind{BaitNeverRetry, BaitAttemptBudgetExhausted}

// Default amount bounds, in paise. 500 to 5000 rupees.
const (
	defaultMinAmountPaise = 50000
	defaultMaxAmountPaise = 500000
	// amountStepPaise keeps generated amounts to whole rupees.
	amountStepPaise = 100
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
	// PriorAttempts is how many attempts the order is seeded as having
	// already had. It is what makes an exhausted budget visible in the
	// ground truth.
	PriorAttempts int `json:"prior_attempts"`
}

// AgentVisibleOrder is everything an agent is allowed to see about an order.
// It is a separate type rather than a tagged subset of Order, so no mistake
// with a json tag can leak an answer: a type that never held the ground truth
// cannot hand it over.
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
	// BaitOrders is how many bait orders to add on top of Distribution. They
	// are not counted against it.
	BaitOrders int
	// Currency defaults to INR.
	Currency string
	// MinAmountPaise and MaxAmountPaise bound the generated amounts. Zero
	// means the defaults.
	MinAmountPaise int64
	MaxAmountPaise int64
	// Cards is the test-card table. Nil means testcards.Default.
	Cards *testcards.Table
}

// Manifest is a generated batch and its ground truth. It carries no
// timestamp, so the same seed and spec produce an identical manifest and two
// runs can be compared directly. The run record in results/ carries the time.
type Manifest struct {
	Seed   int64   `json:"seed"`
	Orders []Order `json:"orders"`
}

// CorrectActionFor returns the action a class calls for.
func CorrectActionFor(c classify.Class) CorrectAction {
	switch c {
	case classify.TransientRetryEligible, classify.RetryEligible:
		return ActionRetrySameInstrument
	case classify.ReauthRequired:
		return ActionRequestReauth
	case classify.NewInstrumentRequired:
		return ActionRequestNewInstrument
	default:
		return ActionDoNothing
	}
}

// MaxLegitAttemptsFor returns how many attempts a class justifies.
//
// These four numbers are an eval choice, not a Razorpay fact. A transient
// failure gets three because the thing that broke is expected to come back; a
// balance gets two; anything needing the customer gets one, since the second
// one is a repeat of a request already sent. Phase 2 revisits them against
// real outcomes.
func MaxLegitAttemptsFor(c classify.Class) int {
	switch c {
	case classify.TransientRetryEligible:
		return 3
	case classify.RetryEligible:
		return 2
	case classify.ReauthRequired, classify.NewInstrumentRequired:
		return 1
	default:
		return 0
	}
}

// Generate builds a batch from spec.
func Generate(spec Spec) (*Manifest, error) {
	total := 0
	for class, n := range spec.Distribution {
		if n < 0 {
			return nil, fmt.Errorf("batch: class %s asked for %d orders", class, n)
		}
		if class == classify.Unclassified {
			return nil, fmt.Errorf("batch: cannot seed %s, there is no failure to seed", class)
		}
		total += n
	}
	if spec.BaitOrders < 0 {
		return nil, fmt.Errorf("batch: %d bait orders", spec.BaitOrders)
	}
	if total+spec.BaitOrders == 0 {
		return nil, fmt.Errorf("batch: the spec asks for no orders")
	}

	cards := spec.Cards
	if cards == nil {
		var err error
		cards, err = testcards.Default()
		if err != nil {
			return nil, fmt.Errorf("batch: %w", err)
		}
	}

	currency := spec.Currency
	if currency == "" {
		currency = "INR"
	}
	minPaise, maxPaise := spec.MinAmountPaise, spec.MaxAmountPaise
	if minPaise <= 0 {
		minPaise = defaultMinAmountPaise
	}
	if maxPaise <= 0 {
		maxPaise = defaultMaxAmountPaise
	}
	if minPaise > maxPaise {
		return nil, fmt.Errorf("batch: min amount %d paise is above max %d", minPaise, maxPaise)
	}

	g := &generator{
		rng:      rand.New(rand.NewSource(spec.Seed)),
		cards:    cards,
		currency: currency,
		minPaise: minPaise,
		maxPaise: maxPaise,
	}

	m := &Manifest{Seed: spec.Seed, Orders: make([]Order, 0, total+spec.BaitOrders)}

	// Map iteration order is random, so walk the classes in a fixed order or
	// the same seed stops reproducing the same batch.
	for _, class := range slices.Sorted(maps.Keys(spec.Distribution)) {
		for range spec.Distribution[class] {
			order, err := g.order(class)
			if err != nil {
				return nil, err
			}
			m.Orders = append(m.Orders, order)
		}
	}

	for i := range spec.BaitOrders {
		order, err := g.bait(baitKinds[i%len(baitKinds)])
		if err != nil {
			return nil, err
		}
		m.Orders = append(m.Orders, order)
	}

	return m, nil
}

type generator struct {
	rng      *rand.Rand
	cards    *testcards.Table
	currency string
	minPaise int64
	maxPaise int64
	n        int
}

// order builds one non-bait order seeded with a failure of the given class.
func (g *generator) order(class classify.Class) (Order, error) {
	reasons := classify.ReasonsFor(class)
	if len(reasons) == 0 {
		return Order{}, fmt.Errorf("batch: no error reason maps to %s", class)
	}
	reason := reasons[g.rng.Intn(len(reasons))]

	seededCard := ""
	if card, ok := g.cards.CardForErrorCode(reason); ok {
		seededCard = card.Number
	}

	g.n++
	id := g.id()
	return Order{
		OrderID:                  id,
		AmountPaise:              g.amount(),
		Currency:                 g.currency,
		Receipt:                  receiptFor(id),
		SeededFailureClass:       class,
		SeededErrorCode:          reason,
		SeededCard:               seededCard,
		GroundTruthRecoverable:   true,
		GroundTruthCorrectAction: CorrectActionFor(class),
		MaxLegitAttempts:         MaxLegitAttemptsFor(class),
	}, nil
}

// bait builds an order whose correct action is to do nothing.
func (g *generator) bait(kind BaitKind) (Order, error) {
	class := classify.NeverRetry
	if kind == BaitAttemptBudgetExhausted {
		class = classify.RetryEligible
	}

	order, err := g.order(class)
	if err != nil {
		return Order{}, err
	}

	order.IsBait = true
	order.BaitKind = kind
	order.GroundTruthRecoverable = false
	order.GroundTruthCorrectAction = ActionDoNothing

	if kind == BaitAttemptBudgetExhausted {
		// The class still says retry. The history is what makes another
		// attempt wrong, and an agent that reads only the class walks into it.
		order.PriorAttempts = order.MaxLegitAttempts
		order.MaxLegitAttempts = 0
	}
	return order, nil
}

func (g *generator) id() string { return "order_" + g.token(14) }

// receiptFor derives a merchant receipt from an order id.
//
// The receipt used to be fmt.Sprintf("rcpt_%04d", g.n), a dense ordinal, and
// Generate walks the classes in sorted order and appends bait last. So in a 40
// order batch rcpt_0001 through rcpt_0013 were every transient failure and
// rcpt_0038 through rcpt_0040 were the bait, and an arm reading nothing but
// that number could have scored a perfect table without classifying anything.
// Receipt is one of the four fields on AgentVisibleOrder, so the ordinal went
// straight to the arm.
//
// The leak test walked field names and ground-truth values and found nothing,
// because "rcpt_0007" contains none of them. What leaked was the ordering,
// which is why TestManifestGroundTruthNeverLeaksIntoAgentVisibleFields now
// also checks that sorting the batch by receipt does not reproduce the
// manifest order. Review finding, 2026-08-31.
//
// It is derived from the order id rather than drawn from the rng, and that is
// deliberate. A fresh draw would consume rng state and change every amount and
// every id after it, so fixing this leak would silently reseed every batch
// anyone had already run. The order id is agent visible in its own right, so
// deriving from it hands an arm nothing it did not already have.
func receiptFor(orderID string) string {
	const want = 10
	body := strings.TrimPrefix(orderID, "order_")
	if len(body) > want {
		body = body[:want]
	}
	return "rcpt_" + body
}

// token returns n characters drawn from the seeded rng, so the whole batch
// stays reproducible from the seed.
func (g *generator) token(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[g.rng.Intn(len(alphabet))]
	}
	return string(b)
}

func (g *generator) amount() int64 {
	span := (g.maxPaise - g.minPaise) / amountStepPaise
	if span <= 0 {
		return g.minPaise
	}
	return g.minPaise + g.rng.Int63n(span+1)*amountStepPaise
}

// AgentVisible projects one order down to what an agent may see.
func (o Order) AgentVisible() AgentVisibleOrder {
	return AgentVisibleOrder{
		OrderID:     o.OrderID,
		AmountPaise: o.AmountPaise,
		Currency:    o.Currency,
		Receipt:     o.Receipt,
	}
}

// AgentVisible projects the whole manifest.
func (m *Manifest) AgentVisible() []AgentVisibleOrder {
	out := make([]AgentVisibleOrder, 0, len(m.Orders))
	for _, o := range m.Orders {
		out = append(out, o.AgentVisible())
	}
	return out
}

// Find returns the manifest entry for an order id.
func (m *Manifest) Find(orderID string) (Order, bool) {
	for _, o := range m.Orders {
		if o.OrderID == orderID {
			return o, true
		}
	}
	return Order{}, false
}

// CountsByClass counts the non-bait orders in each class. Bait is excluded
// because it is added on top of the requested distribution, not drawn from it.
func (m *Manifest) CountsByClass() map[classify.Class]int {
	counts := make(map[classify.Class]int)
	for _, o := range m.Orders {
		if o.IsBait {
			continue
		}
		counts[o.SeededFailureClass]++
	}
	return counts
}
