package riskrun

import (
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/riskitem"
)

var ledgerStart = time.Date(2026, 9, 5, 7, 0, 0, 0, time.UTC)

// TestLedgerChargesATouchOnlyForAMessageThatWentOut is the split inside commit.
//
// R5 bounds one run's blast radius, so an attempt that was refused still spent
// one. R1, R2, and R6 bound messages to a customer, and a refused notification
// is not one, so charging the item a contact for it would spend a customer's
// contact budget on a message they never got.
func TestLedgerChargesATouchOnlyForAMessageThatWentOut(t *testing.T) {
	l := newLedger()

	l.commit("ri_a", "key-refused", riskitem.ActionNotifyEmail, false, ledgerStart)
	state := l.state("ri_a", "key-refused", false)
	if state.ActionsThisRun != 1 {
		t.Errorf("actions this run = %d after a refused action, want 1", state.ActionsThisRun)
	}
	if state.TouchesMade != 0 {
		t.Errorf("touches made = %d after a refused notification, want 0", state.TouchesMade)
	}
	if !state.LastTouchAt.IsZero() || !state.LastNotifyAt.IsZero() {
		t.Errorf("a refused notification moved a clock: %+v", state)
	}
	if state.IdempotencyKeySeen {
		t.Error("a refused action recorded its key, so a retry of it would read as a replay of nothing")
	}
	if l.touchNo("ri_a") != 1 {
		t.Errorf("touch no = %d after a refused notification, want the first contact still ahead", l.touchNo("ri_a"))
	}

	l.commit("ri_a", "key-sent", riskitem.ActionNotifyEmail, true, ledgerStart.Add(time.Minute))
	state = l.state("ri_a", "key-sent", false)
	if state.ActionsThisRun != 2 {
		t.Errorf("actions this run = %d, want 2", state.ActionsThisRun)
	}
	if state.TouchesMade != 1 {
		t.Errorf("touches made = %d after one accepted notification, want 1", state.TouchesMade)
	}
	if !state.LastTouchAt.Equal(ledgerStart.Add(time.Minute)) || !state.LastNotifyAt.Equal(ledgerStart.Add(time.Minute)) {
		t.Errorf("the clocks did not move on an accepted notification: %+v", state)
	}
	if !state.IdempotencyKeySeen {
		t.Error("an accepted action did not record its key")
	}
}

// TestLedgerDoesNotChargeAContactForAnEscalationOrAPromise. Neither reaches a
// customer, so neither is a touch, and neither moves the notify clock R6 reads.
func TestLedgerDoesNotChargeAContactForAnEscalationOrAPromise(t *testing.T) {
	for _, action := range []string{riskitem.ActionEscalate, riskitem.ActionLogPromise, riskitem.ActionDoNothing} {
		l := newLedger()
		l.commit("ri_b", "key", action, true, ledgerStart)
		state := l.state("ri_b", "other", false)

		if state.TouchesMade != 0 {
			t.Errorf("%s charged the item %d touch(es)", action, state.TouchesMade)
		}
		if !state.LastNotifyAt.IsZero() {
			t.Errorf("%s moved the run-wide notify clock", action)
		}
		if state.ActionsThisRun != 1 {
			t.Errorf("%s did not count against the run's action budget", action)
		}
	}
}

// TestLedgerCountsALinkAsAContactButNotAsANotification. Creating a payment link
// sends nothing, so R6 and R12 do not read it, but a link is created to be sent
// and the per-item contact rules would be trivially escapable if minting one
// sat outside them. policy.IsContactAction and policy.IsNotifyAction are where
// that distinction lives, and this is the ledger reading both.
func TestLedgerCountsALinkAsAContactButNotAsANotification(t *testing.T) {
	l := newLedger()
	l.commit("ri_c", "key", riskitem.ActionCreatePaymentLink, true, ledgerStart)

	state := l.state("ri_c", "other", false)
	if state.TouchesMade != 1 {
		t.Errorf("touches made = %d after creating a link, want 1", state.TouchesMade)
	}
	if !state.LastTouchAt.Equal(ledgerStart) {
		t.Errorf("last touch at = %s, want %s", state.LastTouchAt, ledgerStart)
	}
	if !state.LastNotifyAt.IsZero() {
		t.Error("creating a link moved the notify clock, which is a send rate")
	}
}

// TestLedgerReadsWithoutInserting. Asking about an item does not make the
// ledger think it was touched, which is the defect internal/store's own
// Snapshot comment records having had.
func TestLedgerReadsWithoutInserting(t *testing.T) {
	l := newLedger()

	if got := l.state("ri_never_seen", "key", false); got.TouchesMade != 0 || !got.LastTouchAt.IsZero() {
		t.Errorf("a never-seen item carries state: %+v", got)
	}
	if l.touchNo("ri_never_seen") != 1 {
		t.Errorf("touch no = %d for a never-seen item, want 1", l.touchNo("ri_never_seen"))
	}
	if len(l.touches) != 0 || len(l.lastTouch) != 0 {
		t.Errorf("reading inserted rows: %d touch(es), %d clock(s)", len(l.touches), len(l.lastTouch))
	}

	// The kill-switch flag the caller read off disk passes through untouched:
	// the ledger does no I/O either.
	if !l.state("ri_never_seen", "key", true).KillSwitchEngaged {
		t.Error("the ledger dropped the kill-switch state it was handed")
	}
}

// TestLedgerReplayReportsTheSecondCommitOfOneKey.
func TestLedgerReplayReportsTheSecondCommitOfOneKey(t *testing.T) {
	l := newLedger()

	if replayed := l.commit("ri_d", "key", riskitem.ActionNotifyEmail, true, ledgerStart); replayed {
		t.Fatal("the first commit reported a replay")
	}
	before := l.state("ri_d", "key", false)

	if replayed := l.commit("ri_d", "key", riskitem.ActionNotifyEmail, true, ledgerStart.Add(time.Hour)); !replayed {
		t.Error("the second commit of one key did not report a replay")
	}
	after := l.state("ri_d", "key", false)

	if after.TouchesMade != before.TouchesMade {
		t.Errorf("a replay moved touches from %d to %d", before.TouchesMade, after.TouchesMade)
	}
	if !after.LastTouchAt.Equal(before.LastTouchAt) {
		t.Errorf("a replay moved last touch from %s to %s", before.LastTouchAt, after.LastTouchAt)
	}
	if after.ActionsThisRun != before.ActionsThisRun {
		t.Errorf("a replay moved the run action count from %d to %d, and a replay acted on nothing",
			before.ActionsThisRun, after.ActionsThisRun)
	}
}
