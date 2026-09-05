package store_test

import (
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/lopster568/rzp-recovery-agent/internal/store"
)

var start = time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

func key(orderID, action string, attemptNo int) string {
	return policy.IdempotencyKey(orderID, action, attemptNo)
}

func TestStoreCountsAttemptsPerOrder(t *testing.T) {
	s := store.New(clock.NewFake(start))

	s.Commit("order_a", key("order_a", policy.ActionRetrySameInstrument, 1), policy.ActionRetrySameInstrument)
	s.Commit("order_a", key("order_a", policy.ActionRetrySameInstrument, 2), policy.ActionRetrySameInstrument)
	s.Commit("order_b", key("order_b", policy.ActionRetrySameInstrument, 1), policy.ActionRetrySameInstrument)

	if got := s.Attempts("order_a"); got != 2 {
		t.Errorf("order_a attempts = %d, want 2", got)
	}
	if got := s.Attempts("order_b"); got != 1 {
		t.Errorf("order_b attempts = %d, want 1", got)
	}
	if got := s.Attempts("order_never_seen"); got != 0 {
		t.Errorf("an unseen order has %d attempts, want 0", got)
	}

	// Observe primes an order from the gateway's own count, and a commit
	// after it counts on top rather than resetting.
	s.Observe("order_c", 2)
	if got := s.Attempts("order_c"); got != 2 {
		t.Fatalf("after Observe(2), attempts = %d, want 2", got)
	}
	s.Commit("order_c", key("order_c", policy.ActionRetrySameInstrument, 3), policy.ActionRetrySameInstrument)
	if got := s.Attempts("order_c"); got != 3 {
		t.Errorf("after Observe(2) and one commit, attempts = %d, want 3", got)
	}
}

func TestStoreCommitIsANoOpOnAReplayedKey(t *testing.T) {
	s := store.New(clock.NewFake(start))
	k := key("order_a", policy.ActionRetrySameInstrument, 1)

	if replayed := s.Commit("order_a", k, policy.ActionRetrySameInstrument); replayed {
		t.Fatal("the first commit reported a replay")
	}
	before := s.Order("order_a")
	if s.ActionsThisRun() != 1 {
		t.Fatalf("actions this run = %d after one commit, want 1", s.ActionsThisRun())
	}

	if replayed := s.Commit("order_a", k, policy.ActionRetrySameInstrument); !replayed {
		t.Error("the second commit of the same key did not report a replay")
	}

	after := s.Order("order_a")
	if after.Attempts != before.Attempts {
		t.Errorf("a replay moved attempts from %d to %d", before.Attempts, after.Attempts)
	}
	if !after.LastActionAt.Equal(before.LastActionAt) {
		t.Errorf("a replay moved last_action_at from %s to %s", before.LastActionAt, after.LastActionAt)
	}
	if !after.LastNotifyAt.Equal(before.LastNotifyAt) {
		t.Errorf("a replay moved last_notify_at from %s to %s", before.LastNotifyAt, after.LastNotifyAt)
	}
	if s.ActionsThisRun() != 1 {
		t.Errorf("a replay moved the run action count to %d, want 1", s.ActionsThisRun())
	}
	if s.Keys() != 1 {
		t.Errorf("a replay recorded a second key: %d, want 1", s.Keys())
	}
}

func TestStoreSnapshotCarriesLastActionAndNotifyTimes(t *testing.T) {
	c := clock.NewFake(start)
	s := store.New(c)

	// Before anything is committed both timestamps are zero, which is what
	// tells the policy that this run has not acted yet.
	fresh := s.Snapshot("order_a", "k0", false)
	if !fresh.LastActionAt.IsZero() || !fresh.LastNotifyAt.IsZero() {
		t.Errorf("a fresh order carries timestamps: %+v", fresh)
	}

	s.Commit("order_a", "k1", policy.ActionRetrySameInstrument)
	afterRetry := s.Snapshot("order_a", "k0", false)
	if !afterRetry.LastActionAt.Equal(start) {
		t.Errorf("last_action_at = %s, want the clock reading %s", afterRetry.LastActionAt, start)
	}
	if !afterRetry.LastNotifyAt.IsZero() {
		t.Error("a retry moved last_notify_at, which is what R6 rate limits")
	}

	// A notification, under what policy.IsNotifyAction now means: a message
	// goes out. It used to mean "asks the customer for a reauthentication or a
	// new card", and this line committed request_reauth on that reading. That
	// action is not a message and is not in the lawful set any more, so it is
	// no longer a notification to the store either, and the assertion below
	// was failing against a last_notify_at that correctly did not move.
	c.Advance(time.Minute)
	s.Commit("order_a", "k2", policy.ActionNotifyEmail)
	afterNotify := s.Snapshot("order_a", "k0", false)
	if !afterNotify.LastNotifyAt.Equal(start.Add(time.Minute)) {
		t.Errorf("last_notify_at = %s, want %s", afterNotify.LastNotifyAt, start.Add(time.Minute))
	}
	if !afterNotify.LastActionAt.Equal(start.Add(time.Minute)) {
		t.Errorf("last_action_at = %s, want %s: a notification is also an action",
			afterNotify.LastActionAt, start.Add(time.Minute))
	}

	// The kill-switch flag the runner read off disk is passed through
	// untouched, because the store does no I/O either.
	if !s.Snapshot("order_a", "k0", true).KillSwitchEngaged {
		t.Error("Snapshot dropped the kill-switch state it was handed")
	}
}

func TestStoreActionsThisRunCountsEveryOrder(t *testing.T) {
	s := store.New(clock.NewFake(start))

	for _, id := range []string{"order_a", "order_b", "order_c"} {
		s.Commit(id, key(id, policy.ActionRetrySameInstrument, 1), policy.ActionRetrySameInstrument)
	}
	if got := s.ActionsThisRun(); got != 3 {
		t.Errorf("actions this run = %d, want 3", got)
	}
	if got := s.Snapshot("order_a", "k", false).ActionsThisRun; got != 3 {
		t.Errorf("the snapshot reports %d actions this run, want 3", got)
	}
}

func TestStoreSnapshotReportsASeenIdempotencyKey(t *testing.T) {
	s := store.New(clock.NewFake(start))
	k := key("order_a", policy.ActionRetrySameInstrument, 1)

	if s.Snapshot("order_a", k, false).IdempotencyKeySeen {
		t.Fatal("a key reported as seen before it was committed")
	}
	s.Commit("order_a", k, policy.ActionRetrySameInstrument)
	if !s.Snapshot("order_a", k, false).IdempotencyKeySeen {
		t.Error("a committed key did not report as seen")
	}

	// A different key on the same order is a different action.
	other := key("order_a", policy.ActionRetrySameInstrument, 2)
	if s.Snapshot("order_a", other, false).IdempotencyKeySeen {
		t.Error("committing one key marked another as seen")
	}
}
