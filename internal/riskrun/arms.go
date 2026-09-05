package riskrun

import (
	"math/rand"
	"slices"

	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
)

// The two arms.
//
// There is no naive arm here. The retry engine had one because there was a
// wrong thing to do that a merchant might plausibly do anyway; this engine's
// action set has no such member, so the comparison worth drawing is between
// deciding and deciding-then-acting.
const (
	// ArmControl detects, classifies, and evaluates, and executes nothing. It
	// is the measurement of what the gate would have done, taken on the same
	// items and at the same instant as the arm that acts.
	ArmControl = "a0-control"
	// ArmEngine executes every action the gate allowed, through
	// internal/intervene.
	ArmEngine = "a1-engine"
)

// Arms returns the arm ids, in report order.
func Arms() []string { return []string{ArmControl, ArmEngine} }

// IsArm reports whether id is one of the two.
func IsArm(id string) bool { return id == ArmControl || id == ArmEngine }

// AssignArms assigns one arm per item, keyed by riskitem.RiskItem.ID.
//
// The assignment is randomised and it is stratified by source. Within each
// source the items are split as evenly as the count allows and then shuffled,
// so a run of seven overdue invoices and three unpaid orders puts four and two
// of them in one arm rather than putting every invoice in one arm and every
// order in the other. An unstratified shuffle would do exactly that often
// enough to matter at these sizes, and the arms would then differ by which
// debts they saw rather than by what they did with them.
//
// It is deterministic in seed and in the items it is handed: the same seed over
// the same sightings assigns the same arms, which is what lets a run be
// repeated. The sources are walked in sorted order so that the map iteration
// underneath cannot change the answer.
//
// An odd count gives the extra item to the engine arm. That is a choice and it
// is the useful direction: a source with one item is worth executing on rather
// than worth observing, and nothing here reads the imbalance as a measurement.
func AssignArms(items []riskitem.RiskItem, seed int64) map[string]string {
	bySource := make(map[string][]string)
	for _, item := range items {
		source := string(item.Source)
		bySource[source] = append(bySource[source], item.ID)
	}

	sources := make([]string, 0, len(bySource))
	for source := range bySource {
		sources = append(sources, source)
	}
	slices.Sort(sources)

	rng := rand.New(rand.NewSource(seed))
	out := make(map[string]string, len(items))
	for _, source := range sources {
		ids := bySource[source]
		arms := make([]string, len(ids))
		engine := (len(ids) + 1) / 2
		for i := range arms {
			if i < engine {
				arms[i] = ArmEngine
				continue
			}
			arms[i] = ArmControl
		}
		rng.Shuffle(len(arms), func(i, j int) { arms[i], arms[j] = arms[j], arms[i] })
		for i, id := range ids {
			out[id] = arms[i]
		}
	}
	return out
}
