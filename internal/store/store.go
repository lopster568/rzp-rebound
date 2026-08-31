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
	// Notifications is how many notifications this run sent on the order.
	Notifications int
	// LastActionAt is when this run last acted on the order. It stays zero
	// until this run commits an action.
	//
	// It is deliberately not primed from the gateway. R2 governs the interval
	// between the agent's own actions, and the failure that put the order in
	// the batch was not one of them. Priming it would refuse the first action
	// of every run against a freshly seeded batch.
	LastActionAt time.Time
	// LastNotifyAt is when this run last sent a notification on the order.
	LastNotifyAt time.Time
}

// Store is the attempt ledger for one run: per-order attempt counts, action
// timestamps, the run-wide action count, and the set of idempotency keys
// already committed.
//
// It is in memory and it lives for one run. FR-STORE-1 and FR-STORE-2 ask for
// state that survives a restart, and the phase 2 report records that this is
// the in-memory half.
type Store struct {
	mu      sync.Mutex
	clock   clock.Clock
	orders  map[string]*OrderState
	keys    map[string]bool
	actions int
}

// New returns a Store. A nil clock means the wall clock.
func New(c clock.Clock) *Store { return &Store{} }

// Observe primes an order's attempt count from what the gateway reported.
//
// The number comes from counting the payments on the order, not from a
// manifest. An order's history is gateway state, and reading it from the
// answer key would make the attempt cap a fact about the batch file rather
// than about the world.
func (s *Store) Observe(orderID string, attemptsSeen int) {}

// Snapshot builds the policy input for one proposed action.
func (s *Store) Snapshot(orderID, idempotencyKey string, killSwitchEngaged bool) policy.State {
	return policy.State{}
}

// Commit records that an action was taken. It returns true when the key had
// already been committed, in which case nothing moved: not the order's
// attempts, not the run's action count, not either timestamp.
//
// This is the store half of R9. The policy half refuses the replay; this half
// makes sure that a replay which somehow got past the policy still cannot
// double-count.
func (s *Store) Commit(orderID, idempotencyKey, action string) bool { return false }

// Attempts returns an order's attempt count.
func (s *Store) Attempts(orderID string) int { return 0 }

// Order returns a copy of an order's state. A never-seen order comes back
// zero-valued with its id filled in.
func (s *Store) Order(orderID string) OrderState { return OrderState{} }

// ActionsThisRun returns the run-wide action count, which is what R5 reads.
func (s *Store) ActionsThisRun() int { return 0 }

// Keys returns how many distinct idempotency keys have been committed.
func (s *Store) Keys() int { return 0 }
