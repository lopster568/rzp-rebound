package store

import (
	"sync"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
)

// OrderState is what one run knows about one order.
type OrderState struct {
	OrderID string
	// Attempts is every payment attempt on the order, including the ones the
	// gateway reported from before this run started. Observe primes it.
	Attempts int
	// LastActionAt is when this run last acted on the order. It stays zero
	// until this run commits an action.
	//
	// It is deliberately not primed from the gateway. R2 governs the interval
	// between the agent's own actions, and the failure that put the order in
	// the batch was not one of them. Priming it would refuse the first action
	// of every run against a freshly seeded batch, which would make the
	// cooldown rule look like it was working when what it was doing was
	// refusing the whole experiment.
	LastActionAt time.Time
	// LastNotifyAt is when this run last sent a notification on the order.
	LastNotifyAt time.Time
}

// Store is the attempt ledger for one run: per-order attempt counts, action
// timestamps, the run-wide action count, and the set of idempotency keys
// already committed.
//
// It is in memory and it lives for one run. FR-STORE-1 and FR-STORE-2 ask for
// state that survives a restart; this is the in-memory half, and the phase 2
// report says so rather than letting a green suite imply the durable one.
//
// Every method holds the mutex, which makes each one safe on its own and does
// not make the sequence safe. An arm does Snapshot, then Evaluate, then
// Commit, across three separate acquisitions, so two goroutines could both
// read AttemptsMade at 2 against a cap of 3 and both commit, putting 4
// attempts on an order under R1. R9 survives that, because Commit rechecks the
// key set under the lock, but R1 and R5 do not.
//
// It is unreachable today because cmd/rzp/run.go processes orders one at a
// time. The comment used to say the concurrency cap made it safe, which was
// the wrong reason: liveMaxConcurrent caps HTTP requests inside the Razorpay
// client, not order processing. The day an arm runs orders in parallel this
// needs a Decide method that holds the lock across the evaluation. Review
// finding, 2026-08-31.
type Store struct {
	mu      sync.Mutex
	clock   clock.Clock
	orders  map[string]*OrderState
	keys    map[string]bool
	actions int
}

// New returns a Store. A nil clock means the wall clock.
func New(c clock.Clock) *Store {
	if c == nil {
		c = clock.Real()
	}
	return &Store{
		clock:  c,
		orders: make(map[string]*OrderState),
		keys:   make(map[string]bool),
	}
}

// order returns the state for an order, creating it. The caller holds the
// lock.
func (s *Store) order(orderID string) *OrderState {
	if st, ok := s.orders[orderID]; ok {
		return st
	}
	st := &OrderState{OrderID: orderID}
	s.orders[orderID] = st
	return st
}

// Observe primes an order's attempt count from what the gateway reported.
//
// The number comes from counting the payments on the order, not from a
// manifest. An order's history is gateway state, and reading it from the
// answer key would make the attempt cap a fact about the batch file rather
// than about the world.
//
// It raises the count and never lowers it. A later poll that saw fewer
// payments than an earlier one is a gateway that lost something, and the
// conservative reading of a disagreement about how many attempts an order has
// had is the larger number.
func (s *Store) Observe(orderID string, attemptsSeen int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := s.order(orderID)
	if attemptsSeen > st.Attempts {
		st.Attempts = attemptsSeen
	}
}

// Snapshot builds the policy input for one proposed action.
//
// It reads without inserting. A snapshot used to go through the same
// creating accessor Commit does, so asking about an order allocated a row for
// it and len(s.orders) stopped meaning "orders this run touched".
func (s *Store) Snapshot(orderID, idempotencyKey string, killSwitchEngaged bool) policy.State {
	s.mu.Lock()
	defer s.mu.Unlock()

	var st OrderState
	if existing, ok := s.orders[orderID]; ok {
		st = *existing
	}
	return policy.State{
		AttemptsMade:       st.Attempts,
		LastActionAt:       st.LastActionAt,
		LastNotifyAt:       st.LastNotifyAt,
		ActionsThisRun:     s.actions,
		KillSwitchEngaged:  killSwitchEngaged,
		IdempotencyKeySeen: s.keys[idempotencyKey],
	}
}

// Commit records that an action was taken. It returns true when the key had
// already been committed, in which case nothing moved: not the order's
// attempts, not the run's action count, not either timestamp.
//
// This is the store half of R9. The policy half refuses the replay; this half
// makes sure a replay that somehow got past the policy still cannot
// double-count. Two halves because they fail differently: the policy can be
// handed a stale snapshot, and this one cannot be.
//
// An empty key commits without deduplication. A caller that did not compute
// one gets the counter moved, because the alternative is that every action
// with no key looks like a replay of every other one.
func (s *Store) Commit(orderID, idempotencyKey, action string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if idempotencyKey != "" {
		if s.keys[idempotencyKey] {
			return true
		}
		s.keys[idempotencyKey] = true
	}

	now := s.clock.Now()
	st := s.order(orderID)
	st.LastActionAt = now
	s.actions++

	if policy.IsNotifyAction(action) {
		st.LastNotifyAt = now
		return false
	}

	// Anything that is not a notification put a payment on the order.
	st.Attempts++
	return false
}

// Attempts returns an order's attempt count.
func (s *Store) Attempts(orderID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.orders[orderID]; ok {
		return st.Attempts
	}
	return 0
}

// Order returns a copy of an order's state. A never-seen order comes back
// zero-valued with its id filled in.
func (s *Store) Order(orderID string) OrderState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if st, ok := s.orders[orderID]; ok {
		return *st
	}
	return OrderState{OrderID: orderID}
}

// ActionsThisRun returns the run-wide action count, which is what R5 reads.
func (s *Store) ActionsThisRun() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.actions
}

// Keys returns how many distinct idempotency keys have been committed.
func (s *Store) Keys() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.keys)
}
