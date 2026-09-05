package riskrun

import (
	"sync"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/policy"
)

// ledger is what one run knows about the items it has touched.
//
// It is the policy.State supplier, keyed by risk item id rather than by order
// id. internal/store is the retry engine's version of this and is keyed on an
// order, counts a non-notification as a payment attempt on that order, and
// still speaks the deprecated AttemptsMade spelling. None of those three is
// true of a run over risk items, so this is a separate ledger rather than a
// widened one, and internal/store is left to the package that still uses it.
//
// It is in memory and it lives for one run, which is the same limitation
// internal/intervene's idempotency guard has and it is the same reason: nothing
// in this repository has a durable action store yet. A second run over the same
// manifest starts with an empty ledger and will contact an item it already
// contacted, so R1 and R2 bound one run and not a campaign. Saying so is better
// than a ledger that looks durable.
type ledger struct {
	mu         sync.Mutex
	touches    map[string]int
	lastTouch  map[string]time.Time
	keys       map[string]bool
	lastNotify time.Time
	actions    int
}

func newLedger() *ledger {
	return &ledger{
		touches:   make(map[string]int),
		lastTouch: make(map[string]time.Time),
		keys:      make(map[string]bool),
	}
}

// touchNo is which outbound contact the next action on this item would be,
// counting from 1.
func (l *ledger) touchNo(riskItemID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.touches[riskItemID] + 1
}

// state builds the policy input for one proposed action. It reads without
// inserting, so asking about an item does not make the ledger think it was
// touched.
func (l *ledger) state(riskItemID, idempotencyKey string, killSwitchEngaged bool) policy.State {
	l.mu.Lock()
	defer l.mu.Unlock()
	return policy.State{
		TouchesMade:        l.touches[riskItemID],
		LastTouchAt:        l.lastTouch[riskItemID],
		LastNotifyAt:       l.lastNotify,
		ActionsThisRun:     l.actions,
		KillSwitchEngaged:  killSwitchEngaged,
		IdempotencyKeySeen: l.keys[idempotencyKey],
	}
}

// commit records that an action was run. It returns true when the key had
// already been committed, in which case nothing else moved.
//
// The two halves move on different conditions, and the split is the point.
//
// The run-wide action count moves whatever the outcome was, because R5 bounds
// the blast radius of one run and an action that was attempted and refused
// still reached out. The per-item touch count and the two clocks move only on
// an accepted action, because R1, R2, and R6 are about messages that went out
// and a refusal sent nothing: charging an item a contact for a notification the
// engine declined to make would spend a customer's contact budget on a message
// they never got.
//
// The key is recorded only on an accepted action too, which is what
// internal/intervene's own guard does. A key recorded for something that never
// happened would make R9 report a replay of nothing.
//
// What counts as a touch is policy.IsContactAction and what counts as a
// notification is policy.IsNotifyAction, so each rule reads exactly the events
// it is written about. An escalation and a logged promise are neither: they
// reach no customer, and counting one would spend an item's contact budget on a
// decision to hand it to a person.
func (l *ledger) commit(riskItemID, idempotencyKey, action string, accepted bool, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	// A replay moves nothing at all, the run's action count included. The
	// second call did not act, so charging the run for it would report a
	// blast radius bigger than the one it had.
	if accepted && idempotencyKey != "" && l.keys[idempotencyKey] {
		return true
	}

	l.actions++
	if !accepted {
		return false
	}
	if idempotencyKey != "" {
		l.keys[idempotencyKey] = true
	}

	if policy.IsContactAction(action) {
		l.touches[riskItemID]++
		l.lastTouch[riskItemID] = now
	}
	if policy.IsNotifyAction(action) {
		l.lastNotify = now
	}
	return false
}
