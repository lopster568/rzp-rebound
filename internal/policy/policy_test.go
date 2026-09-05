package policy_test

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/lopster568/rzp-recovery-agent/internal/quiet"
	"github.com/lopster568/rzp-recovery-agent/internal/redact"
)

// start is the instant every fake clock in this file reads unless a test says
// otherwise. It is noon in IST, which is inside the default contact band, so a
// test that is not about quiet hours does not accidentally trip R12.
//
// night is the same day at 02:00 IST, which is outside it. Any instant would
// do for either; fixed ones keep a failure message readable.
var (
	start = time.Date(2026, 9, 5, 12, 0, 0, 0, quiet.IST())
	night = time.Date(2026, 9, 5, 2, 0, 0, 0, quiet.IST())
)

const (
	testCeiling       = int64(400000)
	testWriteOffFloor = int64(10000)
	testNotifyWindow  = 10 * time.Second
	testBudget        = 500
)

// testConfig is the policy every test in this file evaluates against, unless
// it says otherwise.
//
// MaxTouchesPerItem and Cooldown are deliberately left zero, which means the
// per-source table decides. A config that overrode them would test the
// override and never the table, and the table is where the source-specific
// behaviour lives.
func testConfig() policy.Config {
	return policy.Config{
		NotifyWindow:       testNotifyWindow,
		AmountCeilingPaise: testCeiling,
		WriteOffFloorPaise: testWriteOffFloor,
		ActionBudget:       testBudget,
	}
}

func newPolicy(t *testing.T, cfg policy.Config) *policy.Policy {
	t.Helper()
	return policy.New(cfg, clock.NewFake(start))
}

// baseReq is a plain, allowable email notification on an overdue invoice that
// passes every rule, so a test can change one field and see one rule fire.
func baseReq() policy.Request {
	return policy.Request{
		RiskItemID:     "ri_baseline",
		Source:         policy.SourceOverdueInvoice,
		Action:         policy.ActionNotifyEmail,
		AmountPaise:    testCeiling - 100,
		AmountDuePaise: testCeiling - 100,
		HasEmail:       true,
		HasContact:     true,
		AtRiskSince:    start.Add(-30 * 24 * time.Hour),
		TouchNo:        1,
	}
}

// allSources is every source the engine has rules for, plus the one it does
// not, because "a source nobody added a row for" is a case R7 exists to catch.
var allSources = append(policy.Sources(), "")

// contactActions and safeActions partition the lawful set the way the rules
// do.
var (
	contactActions = []string{
		policy.ActionNotifyEmail,
		policy.ActionNotifySMS,
		policy.ActionCreatePaymentLink,
		policy.ActionResendLink,
	}
	safeActions = []string{
		policy.ActionEscalate,
		policy.ActionDoNothing,
		policy.ActionLogPromise,
	}
)

func TestPolicyKillSwitchFlagDeniesEveryAction(t *testing.T) {
	cfg := testConfig()
	cfg.KillSwitch = true
	p := newPolicy(t, cfg)

	for _, source := range allSources {
		for _, action := range policy.LawfulActions() {
			req := baseReq()
			req.Source, req.Action = source, action
			got := p.Evaluate(policy.State{}, req)
			if got.Verdict != policy.VerdictDeny || got.RuleID != policy.RuleKillSwitch {
				t.Errorf("source %q action %s: got %s/%s, want %s/%s",
					source, action, got.Verdict, got.RuleID, policy.VerdictDeny, policy.RuleKillSwitch)
			}
		}
	}
}

func TestPolicyKillSwitchStateDeniesEveryAction(t *testing.T) {
	p := newPolicy(t, testConfig())

	for _, action := range policy.LawfulActions() {
		req := baseReq()
		req.Action = action
		got := p.Evaluate(policy.State{KillSwitchEngaged: true}, req)
		if got.Verdict != policy.VerdictDeny || got.RuleID != policy.RuleKillSwitch {
			t.Errorf("action %s: got %s/%s, want %s/%s", action, got.Verdict, got.RuleID,
				policy.VerdictDeny, policy.RuleKillSwitch)
		}
	}
}

func TestKillSwitchFileReportsEngagedWhenThePathExists(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "halt")
	if err := os.WriteFile(present, []byte("stop"), 0o600); err != nil {
		t.Fatal(err)
	}

	engaged, err := policy.KillSwitchFile(present)
	if err != nil {
		t.Fatalf("an existing file: %v", err)
	}
	if !engaged {
		t.Error("an existing kill-switch file did not report engaged")
	}

	engaged, err = policy.KillSwitchFile(filepath.Join(dir, "absent"))
	if err != nil {
		t.Fatalf("a missing file is not an error: %v", err)
	}
	if engaged {
		t.Error("a missing kill-switch file reported engaged")
	}

	// An empty path is how a run says it configured no kill-switch file. It
	// is not engaged and it is not an error.
	engaged, err = policy.KillSwitchFile("")
	if err != nil || engaged {
		t.Errorf(`KillSwitchFile("") = %v, %v, want false, nil`, engaged, err)
	}

	// A path that cannot be stat'ed for any reason other than absence has to
	// be an error. A kill switch that fails open is not a kill switch.
	//
	// A file used as a directory component produces ENOTDIR, which is the
	// portable way to reach that branch without changing permissions.
	if _, err := policy.KillSwitchFile(filepath.Join(present, "under-a-file")); err == nil {
		t.Error("an unreadable kill-switch path returned no error, so it failed open")
	}
}

func TestPolicyIdempotentReplayIsANoOp(t *testing.T) {
	p := newPolicy(t, testConfig())

	got := p.Evaluate(policy.State{IdempotencyKeySeen: true}, baseReq())
	if got.Verdict != policy.VerdictDeny {
		t.Errorf("verdict = %s, want %s", got.Verdict, policy.VerdictDeny)
	}
	if got.RuleID != policy.RuleIdempotency {
		t.Errorf("rule = %s, want %s", got.RuleID, policy.RuleIdempotency)
	}
	if !got.IdempotentReplay {
		t.Error("IdempotentReplay is false on a seen key")
	}
	if got.IdempotencyKey == "" {
		t.Error("the decision carries no idempotency key")
	}

	fresh := p.Evaluate(policy.State{}, baseReq())
	if fresh.IdempotentReplay {
		t.Error("IdempotentReplay is true on an unseen key")
	}
	if fresh.IdempotencyKey != got.IdempotencyKey {
		t.Errorf("the same request hashed two ways: %q and %q", fresh.IdempotencyKey, got.IdempotencyKey)
	}
}

func TestPolicyIdempotencyKeyIsSha256OfItemActionTouch(t *testing.T) {
	sum := sha256.Sum256([]byte("ri_x|notify_email|2"))
	want := hex.EncodeToString(sum[:])

	if got := policy.IdempotencyKey("ri_x", policy.ActionNotifyEmail, 2); got != want {
		t.Errorf("IdempotencyKey = %q, want %q", got, want)
	}

	// One field different is one key different, in all three fields.
	base := policy.IdempotencyKey("ri_x", policy.ActionNotifyEmail, 2)
	for name, other := range map[string]string{
		"item":   policy.IdempotencyKey("ri_y", policy.ActionNotifyEmail, 2),
		"action": policy.IdempotencyKey("ri_x", policy.ActionNotifySMS, 2),
		"touch":  policy.IdempotencyKey("ri_x", policy.ActionNotifyEmail, 3),
	} {
		if other == base {
			t.Errorf("a different %s produced the same key", name)
		}
	}

	// The short form is what goes in an audit row, and its whole job is to be
	// too short to look like a card number to internal/redact, which replaces
	// any run of 13 or more digits. Twelve characters cannot hold one.
	if policy.ShortKeyLen >= 13 {
		t.Fatalf("ShortKeyLen is %d, which can hold a 13 digit run and will be redacted out of the ledger",
			policy.ShortKeyLen)
	}
	if got := policy.ShortKey(want); got != want[:policy.ShortKeyLen] {
		t.Errorf("ShortKey = %q, want the first %d characters %q", got, policy.ShortKeyLen, want[:policy.ShortKeyLen])
	}
	if got := policy.ShortKey("abc"); got != "abc" {
		t.Errorf("ShortKey truncated a string shorter than the prefix: %q", got)
	}

	// The case that actually happened: a digest whose first characters are all
	// digits still has to survive redaction. Search for one rather than
	// assuming, so this fails loudly if the key format ever changes.
	for i := range 5000 {
		key := policy.IdempotencyKey("ri_x", policy.ActionNotifyEmail, i)
		short := policy.ShortKey(key)
		if redact.Value(short) != short {
			t.Fatalf("touch %d: the short key %q does not survive redaction", i, short)
		}
	}
}

// TestPolicyUnknownSourceFailsClosed is the first half of R7. A source nobody
// wrote a row for gets a person, whatever is proposed for it.
func TestPolicyUnknownSourceFailsClosed(t *testing.T) {
	p := newPolicy(t, testConfig())

	for _, source := range []string{"", "subscription", "chargeback", "FAILED_PAYMENT"} {
		for _, action := range policy.LawfulActions() {
			req := baseReq()
			req.Source, req.Action = source, action
			got := p.Evaluate(policy.State{}, req)
			if got.Verdict != policy.VerdictEscalate || got.RuleID != policy.RuleUnknownFailClosed {
				t.Errorf("source %q action %s: got %s/%s, want %s/%s", source, action,
					got.Verdict, got.RuleID, policy.VerdictEscalate, policy.RuleUnknownFailClosed)
			}
		}
	}
}

// TestPolicyUnlawfulActionFailsClosed is what keeps the retry vocabulary out.
//
// The four retry-engine action strings still exist as deprecated constants so
// that packages another work package owns keep compiling. None of them is in
// the lawful set and none of them has a rule branch, so every one reaches R7
// and escalates. A half-ported caller is visible in the trail rather than
// falling through to an allow.
func TestPolicyUnlawfulActionFailsClosed(t *testing.T) {
	p := newPolicy(t, testConfig())

	for _, action := range []string{
		policy.ActionRetrySameInstrument,
		policy.ActionRequestReauth,
		policy.ActionRequestNewInstrument,
		policy.ActionNone,
		"",
		"retry",
		"charge_saved_card",
	} {
		req := baseReq()
		req.Action = action
		got := p.Evaluate(policy.State{}, req)
		if got.Verdict != policy.VerdictEscalate || got.RuleID != policy.RuleUnknownFailClosed {
			t.Errorf("action %q: got %s/%s, want %s/%s", action, got.Verdict, got.RuleID,
				policy.VerdictEscalate, policy.RuleUnknownFailClosed)
		}
		if policy.IsLawfulAction(action) {
			t.Errorf("%q is in the lawful action set and must not be", action)
		}
	}
}

// TestPolicySignalRequirementIsPerSource is the arm of R7 that the pivot
// added. A failed payment with no failure evidence is unreadable and escalates.
// An abandoned cart with no failure evidence is ordinary and does not.
func TestPolicySignalRequirementIsPerSource(t *testing.T) {
	p := newPolicy(t, testConfig())

	for _, tc := range []struct {
		source string
		want   policy.Verdict
	}{
		{policy.SourceFailedPayment, policy.VerdictEscalate},
		{policy.SourceUnpaidOrder, policy.VerdictAllow},
		{policy.SourceOverdueInvoice, policy.VerdictAllow},
	} {
		t.Run(tc.source, func(t *testing.T) {
			req := baseReq()
			req.Source = tc.source
			req.SignalPresent = false
			got := p.Evaluate(policy.State{}, req)
			if got.Verdict != tc.want {
				t.Errorf("no signal on a %s: verdict = %s (%s), want %s",
					tc.source, got.Verdict, got.RuleID, tc.want)
			}
		})
	}

	// A signal that is present and did not classify escalates from any source.
	// That is a different fact from carrying no signal at all, which is why
	// the request carries both.
	for _, source := range policy.Sources() {
		req := baseReq()
		req.Source = source
		req.SignalPresent = true
		req.Class = classify.Unclassified
		got := p.Evaluate(policy.State{}, req)
		if got.Verdict != policy.VerdictEscalate || got.RuleID != policy.RuleUnknownFailClosed {
			t.Errorf("an unreadable signal on a %s: got %s/%s, want %s/%s", source,
				got.Verdict, got.RuleID, policy.VerdictEscalate, policy.RuleUnknownFailClosed)
		}
	}
}

func TestPolicyNotYetDueUsesThePerSourceGrace(t *testing.T) {
	p := newPolicy(t, testConfig())

	for _, tc := range []struct {
		source string
		grace  time.Duration
	}{
		{policy.SourceFailedPayment, policy.GraceFailedPayment},
		{policy.SourceUnpaidOrder, policy.GraceUnpaidOrder},
		{policy.SourceOverdueInvoice, policy.GraceOverdueInvoice},
	} {
		t.Run(tc.source, func(t *testing.T) {
			req := baseReq()
			req.Source = tc.source
			req.SignalPresent = tc.source == policy.SourceFailedPayment
			if req.SignalPresent {
				req.Class = classify.ReauthRequired
			}

			if tc.grace == 0 {
				req.AtRiskSince = start
				if got := p.Evaluate(policy.State{}, req); got.Verdict != policy.VerdictAllow {
					t.Errorf("a %s with a zero grace was refused the instant it was seen: %s (%s)",
						tc.source, got.Verdict, got.RuleID)
				}
				return
			}

			// One second inside the grace is not yet due.
			req.AtRiskSince = start.Add(-tc.grace + time.Second)
			got := p.Evaluate(policy.State{}, req)
			if got.Verdict != policy.VerdictDeny || got.RuleID != policy.RuleNotYetDue {
				t.Errorf("inside the %s grace: got %s/%s, want %s/%s", tc.grace,
					got.Verdict, got.RuleID, policy.VerdictDeny, policy.RuleNotYetDue)
			}

			// Exactly at the grace it is due, because the window is the wait
			// and not a second more.
			req.AtRiskSince = start.Add(-tc.grace)
			if got := p.Evaluate(policy.State{}, req); got.Verdict != policy.VerdictAllow {
				t.Errorf("exactly at the %s grace: verdict = %s (%s), want allow",
					tc.grace, got.Verdict, got.RuleID)
			}
		})
	}
}

// TestPolicyNotYetDueDeniesWhenTheItemCarriesNoAtRiskInstant is the fail-closed
// half of R11. Without the instant there is no way to show the grace has gone
// by, and the safe reading is that it has not.
func TestPolicyNotYetDueDeniesWhenTheItemCarriesNoAtRiskInstant(t *testing.T) {
	p := newPolicy(t, testConfig())

	req := baseReq()
	req.AtRiskSince = time.Time{}
	got := p.Evaluate(policy.State{}, req)
	if got.Verdict != policy.VerdictDeny || got.RuleID != policy.RuleNotYetDue {
		t.Errorf("an invoice with no at-risk instant: got %s/%s, want %s/%s",
			got.Verdict, got.RuleID, policy.VerdictDeny, policy.RuleNotYetDue)
	}

	// A source whose grace is zero is due the moment it is seen, so a missing
	// instant cannot change the answer there.
	req.Source = policy.SourceFailedPayment
	req.SignalPresent, req.Class = true, classify.ReauthRequired
	if got := p.Evaluate(policy.State{}, req); got.Verdict != policy.VerdictAllow {
		t.Errorf("a failed payment with no at-risk instant and a zero grace: %s (%s)", got.Verdict, got.RuleID)
	}

	// And a safe action is never not-yet-due. Escalating an item early, or
	// recording a promise on it, is always available.
	req = baseReq()
	req.AtRiskSince = start
	for _, action := range safeActions {
		req.Action = action
		if got := p.Evaluate(policy.State{}, req); got.RuleID == policy.RuleNotYetDue {
			t.Errorf("%s was refused as not yet due", action)
		}
	}
}

func TestPolicyNoContactChannelEscalatesAndNeverGuesses(t *testing.T) {
	p := newPolicy(t, testConfig())

	// Neither channel: every contact action escalates.
	for _, action := range contactActions {
		req := baseReq()
		req.Action = action
		req.HasEmail, req.HasContact = false, false
		got := p.Evaluate(policy.State{}, req)
		if got.Verdict != policy.VerdictEscalate || got.RuleID != policy.RuleNoContactChannel {
			t.Errorf("%s with no channel: got %s/%s, want %s/%s", action, got.Verdict, got.RuleID,
				policy.VerdictEscalate, policy.RuleNoContactChannel)
		}
	}

	// One channel, and the action asks for the other one. This is the guess
	// the rule exists to refuse.
	for _, tc := range []struct {
		name       string
		action     string
		hasEmail   bool
		hasContact bool
		want       policy.Verdict
	}{
		{"email notification, email only", policy.ActionNotifyEmail, true, false, policy.VerdictAllow},
		{"email notification, phone only", policy.ActionNotifyEmail, false, true, policy.VerdictEscalate},
		{"SMS notification, phone only", policy.ActionNotifySMS, false, true, policy.VerdictAllow},
		{"SMS notification, email only", policy.ActionNotifySMS, true, false, policy.VerdictEscalate},
		{"a link needs only somewhere to send it", policy.ActionCreatePaymentLink, true, false, policy.VerdictAllow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := baseReq()
			req.Action, req.HasEmail, req.HasContact = tc.action, tc.hasEmail, tc.hasContact
			if got := p.Evaluate(policy.State{}, req); got.Verdict != tc.want {
				t.Errorf("verdict = %s (%s), want %s", got.Verdict, got.RuleID, tc.want)
			}
		})
	}

	// A safe action on an item with no channel is still available. An item
	// nobody can be reached about is exactly one somebody has to look at.
	for _, action := range safeActions {
		req := baseReq()
		req.Action = action
		req.HasEmail, req.HasContact = false, false
		if got := p.Evaluate(policy.State{}, req); got.Verdict != policy.VerdictAllow {
			t.Errorf("%s on an unreachable item: %s (%s)", action, got.Verdict, got.RuleID)
		}
	}
}

func TestPolicyNeverContactEscalatesOnRiskAndOnTerminalStatus(t *testing.T) {
	p := newPolicy(t, testConfig())

	for _, action := range policy.LawfulActions() {
		req := baseReq()
		req.Action = action
		req.SignalPresent, req.Class = true, classify.NeverRetry

		got := p.Evaluate(policy.State{}, req)
		switch {
		case policy.IsContactAction(action):
			if got.Verdict != policy.VerdictEscalate || got.RuleID != policy.RuleNeverContact {
				t.Errorf("%s on a risk-refused item: got %s/%s, want %s/%s", action,
					got.Verdict, got.RuleID, policy.VerdictEscalate, policy.RuleNeverContact)
			}
		case policy.IsSafeAction(action):
			if got.Verdict != policy.VerdictAllow {
				t.Errorf("%s on a risk-refused item: %s (%s), want allow", action, got.Verdict, got.RuleID)
			}
		default:
			// Only cancel_write_off is left, and R4 is not its gate. R3 is,
			// and it escalates it at any amount above the floor.
			if got.Verdict != policy.VerdictEscalate || got.RuleID != policy.RuleHumanApproval {
				t.Errorf("%s on a risk-refused item: got %s/%s, want %s/%s", action,
					got.Verdict, got.RuleID, policy.VerdictEscalate, policy.RuleHumanApproval)
			}
		}
	}

	for _, status := range policy.TerminalSourceStatuses() {
		req := baseReq()
		req.SourceStatus = status
		got := p.Evaluate(policy.State{}, req)
		if got.Verdict != policy.VerdictEscalate || got.RuleID != policy.RuleNeverContact {
			t.Errorf("a %s resource: got %s/%s, want %s/%s", status, got.Verdict, got.RuleID,
				policy.VerdictEscalate, policy.RuleNeverContact)
		}
	}

	// Closing a cancelled invoice for fifty paise is what the write-off floor
	// is for, and R4 refusing it was the incoherence a review caught: the rule
	// whose reason says there is nothing left to collect was refusing the one
	// action that records exactly that. R4 now guards contact actions only, and
	// R3 still escalates any write-off above the floor.
	req := baseReq()
	req.Action = policy.ActionCancelWriteOff
	req.SourceStatus = "cancelled"
	req.AmountPaise, req.AmountDuePaise = 50, 50
	if got := p.Evaluate(policy.State{}, req); got.Verdict != policy.VerdictAllow {
		t.Errorf("writing off fifty paise on a cancelled invoice: %s (%s), want allow", got.Verdict, got.RuleID)
	}
	req.AmountPaise, req.AmountDuePaise = testWriteOffFloor+1, testWriteOffFloor+1
	if got := p.Evaluate(policy.State{}, req); got.RuleID != policy.RuleHumanApproval {
		t.Errorf("writing off above the floor on a cancelled invoice: rule = %s, want %s",
			got.RuleID, policy.RuleHumanApproval)
	}

	// An item with nothing outstanding is nobody to chase, and it is also
	// where a caller that forgot to fill AmountDuePaise lands.
	for _, due := range []int64{0, -1} {
		req := baseReq()
		req.AmountDuePaise = due
		got := p.Evaluate(policy.State{}, req)
		if got.Verdict != policy.VerdictEscalate || got.RuleID != policy.RuleNeverContact {
			t.Errorf("%d paise outstanding: got %s/%s, want %s/%s", due, got.Verdict, got.RuleID,
				policy.VerdictEscalate, policy.RuleNeverContact)
		}
	}

	// A live status is not terminal, and neither is an empty one.
	for _, status := range []string{"", "issued", "created", "partially_paid"} {
		live := baseReq()
		live.SourceStatus = status
		if got := p.Evaluate(policy.State{}, live); got.RuleID == policy.RuleNeverContact {
			t.Errorf("a %q resource was treated as terminal", status)
		}
	}
}

func TestPolicyPromiseHoldStopsContactAndNothingElse(t *testing.T) {
	p := newPolicy(t, testConfig())

	// Every action that is not safe, which is the contact actions plus the
	// write-off. Closing an item inside a window the merchant said it would
	// wait out is not a contact, but it is the same broken promise, and it
	// destroys the evidence of whether the customer kept theirs.
	for _, action := range append(append([]string{}, contactActions...), policy.ActionCancelWriteOff) {
		req := baseReq()
		req.Action = action
		req.AmountPaise, req.AmountDuePaise = 50, 50
		req.PromiseHoldUntil = start.Add(24 * time.Hour)
		got := p.Evaluate(policy.State{}, req)
		if got.Verdict != policy.VerdictDeny || got.RuleID != policy.RulePromiseHold {
			t.Errorf("%s under a live promise: got %s/%s, want %s/%s", action, got.Verdict, got.RuleID,
				policy.VerdictDeny, policy.RulePromiseHold)
		}
	}

	// Logging a second promise is how a renegotiation gets recorded, and
	// escalating is not something a hold may block.
	for _, action := range safeActions {
		req := baseReq()
		req.Action = action
		req.PromiseHoldUntil = start.Add(24 * time.Hour)
		if got := p.Evaluate(policy.State{}, req); got.Verdict != policy.VerdictAllow {
			t.Errorf("%s under a live promise: %s (%s), want allow", action, got.Verdict, got.RuleID)
		}
	}

	// The hold's end is exclusive: at the instant it expires, contact resumes.
	for _, tc := range []struct {
		name string
		hold time.Time
		want policy.Verdict
	}{
		{"no promise", time.Time{}, policy.VerdictAllow},
		{"a second before it expires", start.Add(time.Second), policy.VerdictDeny},
		{"exactly as it expires", start, policy.VerdictAllow},
		{"already expired", start.Add(-time.Hour), policy.VerdictAllow},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := baseReq()
			req.PromiseHoldUntil = tc.hold
			if got := p.Evaluate(policy.State{}, req); got.Verdict != tc.want {
				t.Errorf("verdict = %s (%s), want %s", got.Verdict, got.RuleID, tc.want)
			}
		})
	}
}

func TestPolicyQuietHoursDeniesNotificationsOutsideTheBand(t *testing.T) {
	dayPolicy := policy.New(testConfig(), clock.NewFake(start))
	nightPolicy := policy.New(testConfig(), clock.NewFake(night))

	for _, action := range policy.LawfulActions() {
		req := baseReq()
		req.Action = action

		if got := dayPolicy.Evaluate(policy.State{}, req); got.RuleID == policy.RuleQuietHours {
			t.Errorf("%s at noon was refused for quiet hours", action)
		}

		got := nightPolicy.Evaluate(policy.State{}, req)
		if policy.IsNotifyAction(action) {
			if got.Verdict != policy.VerdictDeny || got.RuleID != policy.RuleQuietHours {
				t.Errorf("%s at 02:00: got %s/%s, want %s/%s", action, got.Verdict, got.RuleID,
					policy.VerdictDeny, policy.RuleQuietHours)
			}
			continue
		}
		if got.RuleID == policy.RuleQuietHours {
			t.Errorf("%s is not a notification and was refused for quiet hours", action)
		}
	}

	// Minting a link in the small hours wakes nobody, and denying it would
	// mean the morning's message has no link to send.
	req := baseReq()
	req.Action = policy.ActionCreatePaymentLink
	if got := nightPolicy.Evaluate(policy.State{}, req); got.Verdict != policy.VerdictAllow {
		t.Errorf("creating a link at 02:00: %s (%s), want allow", got.Verdict, got.RuleID)
	}

	// The band is configurable, and a config that opens the whole day turns
	// the rule off rather than leaving it half on.
	cfg := testConfig()
	cfg.ContactWindow = quiet.AlwaysOpen()
	open := policy.New(cfg, clock.NewFake(night))
	if got := open.Evaluate(policy.State{}, baseReq()); got.Verdict != policy.VerdictAllow {
		t.Errorf("a notification at 02:00 under an always-open window: %s (%s)", got.Verdict, got.RuleID)
	}
}

func TestPolicyDisputedItemsAreNeverChased(t *testing.T) {
	p := newPolicy(t, testConfig())

	for _, action := range policy.LawfulActions() {
		req := baseReq()
		req.Action, req.Disputed = action, true
		got := p.Evaluate(policy.State{}, req)

		if policy.IsSafeAction(action) {
			if got.Verdict != policy.VerdictAllow {
				t.Errorf("%s on a disputed item: %s (%s), want allow", action, got.Verdict, got.RuleID)
			}
			continue
		}
		if got.Verdict != policy.VerdictEscalate {
			t.Errorf("%s on a disputed item: verdict = %s (%s), want escalate", action, got.Verdict, got.RuleID)
		}
	}

	// R13 sits below R3's write-off arm, so a disputed write-off escalates
	// under whichever of the two is reached first. What matters is that it
	// escalates and that an ordinary contact action names R13.
	req := baseReq()
	req.Disputed = true
	if got := p.Evaluate(policy.State{}, req); got.RuleID != policy.RuleDisputed {
		t.Errorf("a notification on a disputed item: rule = %s, want %s", got.RuleID, policy.RuleDisputed)
	}
}

func TestPolicyAmountCeilingReadsWhatIsStillOutstanding(t *testing.T) {
	p := newPolicy(t, testConfig())

	for _, tc := range []struct {
		name string
		due  int64
		want policy.Verdict
	}{
		{"one paise below the ceiling", testCeiling - 1, policy.VerdictAllow},
		{"exactly at the ceiling", testCeiling, policy.VerdictAllow},
		{"one paise above the ceiling", testCeiling + 1, policy.VerdictEscalate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := baseReq()
			req.AmountPaise, req.AmountDuePaise = tc.due, tc.due
			got := p.Evaluate(policy.State{}, req)
			if got.Verdict != tc.want {
				t.Errorf("%d paise due against a %d ceiling: verdict = %s (%s), want %s",
					tc.due, testCeiling, got.Verdict, got.RuleID, tc.want)
			}
			if tc.want == policy.VerdictEscalate && got.RuleID != policy.RuleHumanApproval {
				t.Errorf("rule = %s, want %s", got.RuleID, policy.RuleHumanApproval)
			}
		})
	}

	// The error this rule exists not to make: a large invoice that has been
	// nearly paid off is a small debt, and putting a person in front of it
	// wastes the escalation queue on nothing.
	req := baseReq()
	req.AmountPaise = testCeiling * 10
	req.AmountDuePaise = 100
	if got := p.Evaluate(policy.State{}, req); got.Verdict != policy.VerdictAllow {
		t.Errorf("a mostly-paid invoice escalated on its gross amount: %s (%s)", got.Verdict, got.RuleID)
	}

	// A request that fills only AmountPaise does not get its due amount
	// inferred from the gross one. That fold existed until a review caught what
	// it does to a fully collected item: a legitimate zero was read as "unset"
	// and reinflated to the original debt, after which every rule downstream
	// weighed a balance that had already been paid. AmountDuePaise is
	// authoritative, and a zero reaches R4 as nothing to chase.
	req = baseReq()
	req.AmountPaise, req.AmountDuePaise = testCeiling+1, 0
	got := p.Evaluate(policy.State{}, req)
	if got.Verdict != policy.VerdictEscalate || got.RuleID != policy.RuleNeverContact {
		t.Errorf("an unfilled AmountDuePaise: got %s/%s, want %s/%s", got.Verdict, got.RuleID,
			policy.VerdictEscalate, policy.RuleNeverContact)
	}
}

// TestPolicyWriteOffAlwaysNeedsAPersonAboveATinyFloor is R3's other arm, the
// one drafted as R14. A write-off is terminal and decides that money will not
// be collected, so the threshold is not the ceiling: it is a floor small enough
// that what runs unattended is rounding debris.
func TestPolicyWriteOffAlwaysNeedsAPersonAboveATinyFloor(t *testing.T) {
	p := newPolicy(t, testConfig())

	for _, tc := range []struct {
		name string
		due  int64
		want policy.Verdict
	}{
		{"below the floor", testWriteOffFloor - 1, policy.VerdictAllow},
		{"exactly at the floor", testWriteOffFloor, policy.VerdictAllow},
		{"one paise above the floor", testWriteOffFloor + 1, policy.VerdictEscalate},
		{"an ordinary debt, far under the escalation ceiling", testCeiling / 2, policy.VerdictEscalate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := baseReq()
			req.Action = policy.ActionCancelWriteOff
			req.AmountPaise, req.AmountDuePaise = tc.due, tc.due
			got := p.Evaluate(policy.State{}, req)
			if got.Verdict != tc.want {
				t.Errorf("writing off %d paise: verdict = %s (%s), want %s",
					tc.due, got.Verdict, got.RuleID, tc.want)
			}
			if tc.want == policy.VerdictEscalate && got.RuleID != policy.RuleHumanApproval {
				t.Errorf("rule = %s, want %s", got.RuleID, policy.RuleHumanApproval)
			}
		})
	}

	// A negative floor is how an operator says every write-off needs a person,
	// including one for nothing at all.
	cfg := testConfig()
	cfg.WriteOffFloorPaise = -1
	strict := policy.New(cfg, clock.NewFake(start))
	req := baseReq()
	req.Action = policy.ActionCancelWriteOff
	req.AmountPaise, req.AmountDuePaise = 0, 0
	if got := strict.Evaluate(policy.State{}, req); got.Verdict != policy.VerdictEscalate {
		t.Errorf("a zero write-off under a negative floor: %s (%s), want escalate", got.Verdict, got.RuleID)
	}
}

func TestPolicyMaxTouchesUsesThePerSourceCapAndEscalates(t *testing.T) {
	p := newPolicy(t, testConfig())

	for _, tc := range []struct {
		source string
		cap    int
	}{
		{policy.SourceFailedPayment, policy.MaxTouchesFailedPayment},
		{policy.SourceUnpaidOrder, policy.MaxTouchesUnpaidOrder},
		{policy.SourceOverdueInvoice, policy.MaxTouchesOverdueInvoice},
	} {
		t.Run(tc.source, func(t *testing.T) {
			req := baseReq()
			req.Source = tc.source
			req.SignalPresent = tc.source == policy.SourceFailedPayment
			if req.SignalPresent {
				req.Class = classify.ReauthRequired
			}
			req.AtRiskSince = start.Add(-30 * 24 * time.Hour)

			for touches := range tc.cap {
				got := p.Evaluate(policy.State{TouchesMade: touches}, req)
				if got.Verdict != policy.VerdictAllow {
					t.Errorf("%d of %d contacts spent: %s (%s), want allow", touches, tc.cap, got.Verdict, got.RuleID)
				}
				if want := tc.cap - touches; got.Remaining != want {
					t.Errorf("%d of %d spent: Remaining = %d, want %d", touches, tc.cap, got.Remaining, want)
				}
			}

			got := p.Evaluate(policy.State{TouchesMade: tc.cap}, req)
			if got.Verdict != policy.VerdictEscalate || got.RuleID != policy.RuleMaxTouches {
				t.Errorf("the cap spent: got %s/%s, want %s/%s", got.Verdict, got.RuleID,
					policy.VerdictEscalate, policy.RuleMaxTouches)
			}
			if got.Remaining != 0 {
				t.Errorf("Remaining = %d with the cap spent", got.Remaining)
			}
		})
	}

	// An invoice gets one more contact than an abandoned cart, and the table
	// is the only place that says so.
	if policy.MaxTouchesOverdueInvoice <= policy.MaxTouchesUnpaidOrder {
		t.Error("the per-source cap table no longer distinguishes an invoice from an unpaid order")
	}

	// A Config-wide override beats the table, which is how an operator turns
	// the whole run down without editing the source.
	cfg := testConfig()
	cfg.MaxTouchesPerItem = 1
	tight := policy.New(cfg, clock.NewFake(start))
	for _, source := range policy.Sources() {
		if got := tight.ParamsFor(source).MaxTouches; got != 1 {
			t.Errorf("%s ignored the config override: MaxTouches = %d, want 1", source, got)
		}
	}
}

func TestPolicyCooldownUsesThePerSourceInterval(t *testing.T) {
	p := newPolicy(t, testConfig())

	for _, tc := range []struct {
		source   string
		cooldown time.Duration
	}{
		{policy.SourceFailedPayment, policy.CooldownFailedPayment},
		{policy.SourceUnpaidOrder, policy.CooldownUnpaidOrder},
		{policy.SourceOverdueInvoice, policy.CooldownOverdueInvoice},
	} {
		t.Run(tc.source, func(t *testing.T) {
			req := baseReq()
			req.Source = tc.source
			req.SignalPresent = tc.source == policy.SourceFailedPayment
			if req.SignalPresent {
				req.Class = classify.ReauthRequired
			}

			for _, elapsed := range []time.Duration{0, time.Minute, tc.cooldown - time.Second} {
				state := policy.State{TouchesMade: 1, LastTouchAt: start.Add(-elapsed)}
				got := p.Evaluate(state, req)
				if got.Verdict != policy.VerdictDeny || got.RuleID != policy.RuleCooldown {
					t.Errorf("elapsed %s: got %s/%s, want %s/%s", elapsed, got.Verdict, got.RuleID,
						policy.VerdictDeny, policy.RuleCooldown)
				}
			}
			for _, elapsed := range []time.Duration{tc.cooldown, tc.cooldown + time.Second, 30 * 24 * time.Hour} {
				state := policy.State{TouchesMade: 1, LastTouchAt: start.Add(-elapsed)}
				if got := p.Evaluate(state, req); got.Verdict != policy.VerdictAllow {
					t.Errorf("elapsed %s: verdict = %s (%s), want allow", elapsed, got.Verdict, got.RuleID)
				}
			}
		})
	}

	// A zero LastTouchAt means this run has not contacted anyone about the
	// item, which is not a violation. Treating the zero time as "acted at the
	// epoch" would be a silent allow; treating it as "acted now" would deny
	// the first contact of every run.
	if got := p.Evaluate(policy.State{TouchesMade: 1}, baseReq()); got.Verdict != policy.VerdictAllow {
		t.Errorf("no prior contact: verdict = %s (%s), want allow", got.Verdict, got.RuleID)
	}

	// The interval is not a retry rate any more, and it must not be readable
	// as one. Half a minute between two messages to one customer is
	// harassment, and the old constant was 30 seconds.
	for _, d := range []time.Duration{
		policy.CooldownFailedPayment, policy.CooldownUnpaidOrder, policy.CooldownOverdueInvoice,
	} {
		if d < time.Hour {
			t.Errorf("a contact cooldown of %s is at machine scale, not at the scale of a follow-up", d)
		}
	}
}

func TestPolicyNotifyRateIsARunWideSendRate(t *testing.T) {
	p := newPolicy(t, testConfig())

	notify := baseReq()

	inside := policy.State{LastNotifyAt: start.Add(-time.Second)}
	if got := p.Evaluate(inside, notify); got.Verdict != policy.VerdictDeny || got.RuleID != policy.RuleNotifyRate {
		t.Errorf("inside the send window: got %s/%s, want %s/%s", got.Verdict, got.RuleID,
			policy.VerdictDeny, policy.RuleNotifyRate)
	}

	at := policy.State{LastNotifyAt: start.Add(-testNotifyWindow)}
	if got := p.Evaluate(at, notify); got.Verdict != policy.VerdictAllow {
		t.Errorf("at the send window: verdict = %s (%s), want allow", got.Verdict, got.RuleID)
	}

	// Creating a link is not a send, so the rate has nothing to say about it.
	link := baseReq()
	link.Action = policy.ActionCreatePaymentLink
	if got := p.Evaluate(inside, link); got.RuleID == policy.RuleNotifyRate {
		t.Errorf("minting a link was refused by the send rate: %+v", got)
	}

	// IsNotifyAction changed meaning at the pivot and the whole of R6 and R12
	// rest on it.
	for _, action := range []string{policy.ActionNotifyEmail, policy.ActionNotifySMS, policy.ActionResendLink} {
		if !policy.IsNotifyAction(action) {
			t.Errorf("%s sends a message and IsNotifyAction says otherwise", action)
		}
	}
	for _, action := range []string{
		policy.ActionCreatePaymentLink, policy.ActionLogPromise,
		policy.ActionEscalate, policy.ActionCancelWriteOff, policy.ActionDoNothing,
	} {
		if policy.IsNotifyAction(action) {
			t.Errorf("%s sends no message and IsNotifyAction says it does", action)
		}
	}
}

func TestPolicyActionBudgetDeniesPastTheGlobalCap(t *testing.T) {
	cfg := testConfig()
	cfg.ActionBudget = 2
	p := newPolicy(t, cfg)

	for _, tc := range []struct {
		spent int
		want  policy.Verdict
	}{{0, policy.VerdictAllow}, {1, policy.VerdictAllow}, {2, policy.VerdictDeny}, {3, policy.VerdictDeny}} {
		got := p.Evaluate(policy.State{ActionsThisRun: tc.spent}, baseReq())
		if got.Verdict != tc.want {
			t.Errorf("spent %d of 2: verdict = %s (%s), want %s", tc.spent, got.Verdict, got.RuleID, tc.want)
		}
		if tc.want == policy.VerdictDeny && got.RuleID != policy.RuleActionBudget {
			t.Errorf("spent %d of 2: rule = %s, want %s", tc.spent, got.RuleID, policy.RuleActionBudget)
		}
	}

	// The budget bounds side effects, and the safe actions have none outside
	// this program. A spent budget refusing an escalation would be the worst
	// refusal in the engine: Deny and Escalate are different rows in the
	// report, so an item that needed a person would leave the run as a denial
	// and never reach the number that counts escalations.
	for _, action := range safeActions {
		req := baseReq()
		req.Action = action
		got := p.Evaluate(policy.State{ActionsThisRun: 99}, req)
		if got.Verdict != policy.VerdictAllow {
			t.Errorf("%s with the budget spent: %s (%s), want allow", action, got.Verdict, got.RuleID)
		}
	}
}

// TestPolicyRuleOrderIsFixedWhenTwoRulesWouldFire pins the evaluation order.
// It is the contract the golden matrix rests on: without it, a refactor that
// reorders the rules changes every doubly-refused row's rule id and the diff
// looks like noise.
func TestPolicyRuleOrderIsFixedWhenTwoRulesWouldFire(t *testing.T) {
	cfg := testConfig()
	cfg.ActionBudget = 1
	p := policy.New(cfg, clock.NewFake(night))

	// Every field set to something a rule refuses. Each case removes the
	// higher-priority trip and expects the next rule down the list.
	req := policy.Request{
		RiskItemID:       "ri_order",
		Source:           "not-a-source",
		Action:           policy.ActionNotifyEmail,
		SignalPresent:    true,
		Class:            classify.Unclassified,
		AmountPaise:      testCeiling + 1,
		AmountDuePaise:   testCeiling + 1,
		SourceStatus:     "cancelled",
		Disputed:         true,
		PromiseHoldUntil: night.Add(time.Hour),
		TouchNo:          1,
	}
	state := policy.State{
		TouchesMade:        9,
		LastTouchAt:        night,
		LastNotifyAt:       night,
		ActionsThisRun:     9,
		KillSwitchEngaged:  true,
		IdempotencyKeySeen: true,
	}

	for _, tc := range []struct {
		name string
		mut  func(*policy.State, *policy.Request)
		want string
	}{
		{"everything trips", func(*policy.State, *policy.Request) {}, policy.RuleKillSwitch},
		{"no kill switch", func(s *policy.State, _ *policy.Request) { s.KillSwitchEngaged = false }, policy.RuleIdempotency},
		{"no replay", func(s *policy.State, _ *policy.Request) { s.IdempotencyKeySeen = false }, policy.RuleUnknownFailClosed},
		{"a known source, still unclassified", func(_ *policy.State, r *policy.Request) { r.Source = policy.SourceOverdueInvoice }, policy.RuleUnknownFailClosed},
		{"classified", func(_ *policy.State, r *policy.Request) { r.Class = classify.ReauthRequired }, policy.RuleNotYetDue},
		{"past the grace", func(_ *policy.State, r *policy.Request) { r.AtRiskSince = night.Add(-30 * 24 * time.Hour) }, policy.RuleNoContactChannel},
		{"an email address", func(_ *policy.State, r *policy.Request) { r.HasEmail = true }, policy.RuleNeverContact},
		{"a live resource", func(_ *policy.State, r *policy.Request) { r.SourceStatus = "issued" }, policy.RulePromiseHold},
		{"the promise expired", func(_ *policy.State, r *policy.Request) { r.PromiseHoldUntil = time.Time{} }, policy.RuleQuietHours},
		{"inside the contact band", func(_ *policy.State, r *policy.Request) { r.Action = policy.ActionCreatePaymentLink }, policy.RuleDisputed},
		{"not disputed", func(_ *policy.State, r *policy.Request) { r.Disputed = false }, policy.RuleHumanApproval},
		{"under the ceiling", func(_ *policy.State, r *policy.Request) { r.AmountPaise, r.AmountDuePaise = 1, 1 }, policy.RuleMaxTouches},
		{"contacts left", func(s *policy.State, _ *policy.Request) { s.TouchesMade = 0 }, policy.RuleCooldown},
		{"the cooldown elapsed", func(s *policy.State, _ *policy.Request) { s.LastTouchAt = night.Add(-30 * 24 * time.Hour) }, policy.RuleActionBudget},
	} {
		tc.mut(&state, &req)
		got := p.Evaluate(state, req)
		if got.RuleID != tc.want {
			t.Errorf("%s: rule = %s, want %s (verdict %s, %s)", tc.name, got.RuleID, tc.want, got.Verdict, got.Reason)
		}
	}

	// With every trip cleared and the budget restored, the same request
	// allows and says which rule let it through.
	cfg.ActionBudget = testBudget
	clear := policy.New(cfg, clock.NewFake(night))
	state.ActionsThisRun = 0
	if got := clear.Evaluate(state, req); got.Verdict != policy.VerdictAllow || got.RuleID != policy.RuleAllow {
		t.Errorf("nothing trips: got %s/%s (%s), want %s/%s", got.Verdict, got.RuleID, got.Reason,
			policy.VerdictAllow, policy.RuleAllow)
	}
}

// TestPolicyNeverAllowsAContactActionOnARiskRefusedItem walks the whole matrix
// input space. Nothing about an item the gateway's risk check refused can
// produce an allow for a contact action.
func TestPolicyNeverAllowsAContactActionOnARiskRefusedItem(t *testing.T) {
	for _, in := range matrixInputs() {
		if in.signal != signalRiskRefused || !policy.IsContactAction(in.action) {
			continue
		}
		state, req := in.build(in.now())
		if got := in.policy().Evaluate(state, req); got.Verdict == policy.VerdictAllow {
			t.Fatalf("%s allowed a contact action: %+v", in, got)
		}
	}
}

func TestPolicyNeverExceedsThePerSourceContactCap(t *testing.T) {
	for _, in := range matrixInputs() {
		if in.trigger != triggerTouchesSpent || !policy.IsContactAction(in.action) {
			continue
		}
		state, req := in.build(in.now())
		got := in.policy().Evaluate(state, req)
		if got.Verdict == policy.VerdictAllow {
			t.Fatalf("%s allowed a contact with the cap spent: %+v", in, got)
		}
		if got.Remaining != 0 {
			t.Fatalf("%s: Remaining = %d with the cap spent", in, got.Remaining)
		}
	}
}

func TestPolicyDecisionIsDeterministic(t *testing.T) {
	for _, in := range matrixInputs() {
		state, req := in.build(in.now())

		one := policy.New(testConfig(), clock.NewFake(in.now()))
		if a, b := one.Evaluate(state, req), one.Evaluate(state, req); !reflect.DeepEqual(a, b) {
			t.Fatalf("%s: two evaluations on one policy disagreed:\n%+v\n%+v", in, a, b)
		}

		two := policy.New(testConfig(), clock.NewFake(in.now()))
		if a, b := one.Evaluate(state, req), two.Evaluate(state, req); !reflect.DeepEqual(a, b) {
			t.Fatalf("%s: two policies disagreed:\n%+v\n%+v", in, a, b)
		}
	}
}

func TestPolicyDecisionAlwaysCarriesAKnownRuleID(t *testing.T) {
	known := policy.RuleIDs()
	if len(known) != 15 {
		t.Fatalf("RuleIDs returned %d ids, want 15: %v", len(known), known)
	}
	if len(slices.Compact(slices.Sorted(slices.Values(known)))) != len(known) {
		t.Fatalf("RuleIDs has a duplicate: %v", known)
	}

	for _, in := range matrixInputs() {
		state, req := in.build(in.now())
		got := in.policy().Evaluate(state, req)
		if got.RuleID == "" {
			t.Fatalf("%s produced a decision with no rule id: %+v", in, got)
		}
		if !slices.Contains(known, got.RuleID) {
			t.Fatalf("%s produced rule id %q, which is not in RuleIDs(): %v", in, got.RuleID, known)
		}
		if got.Verdict != policy.VerdictAllow && got.RuleID == policy.RuleAllow {
			t.Fatalf("%s refused with the allow rule on it: %+v", in, got)
		}
		if got.Reason == "" {
			t.Fatalf("%s produced a decision with no reason: %+v", in, got)
		}
	}
}

// TestPolicyEveryEscalatingRuleLeavesEscalationAvailable is the property that
// makes a fail-closed engine usable. If a rule that escalates also fired on the
// escalation itself, nothing could ever be handed to a person.
func TestPolicyEveryEscalatingRuleLeavesEscalationAvailable(t *testing.T) {
	for _, in := range matrixInputs() {
		if in.action != policy.ActionEscalate {
			continue
		}
		state, req := in.build(in.now())
		got := in.policy().Evaluate(state, req)
		switch got.RuleID {
		case policy.RuleKillSwitch, policy.RuleIdempotency, policy.RuleUnknownFailClosed, policy.RuleAllow:
			// A halt, a replay, and an item nothing recognises are the only
			// three things that may still refuse an escalation. The run's
			// action budget used to be a fourth, and a review caught it: a
			// spent budget silently turned items that needed a person into
			// denials that no escalation number ever counted.
		default:
			t.Fatalf("%s refused an escalation under %s: %s", in, got.RuleID, got.Reason)
		}
	}
}

func TestPolicyZeroConfigIsTheStandardPolicy(t *testing.T) {
	p := policy.New(policy.Config{}, clock.NewFake(start))

	cfg := p.Config()
	for _, tc := range []struct {
		field string
		got   any
		want  any
	}{
		{"NotifyWindow", cfg.NotifyWindow, policy.DefaultNotifyWindow},
		{"AmountCeilingPaise", cfg.AmountCeilingPaise, int64(policy.DefaultAmountCeilingPaise)},
		{"WriteOffFloorPaise", cfg.WriteOffFloorPaise, int64(policy.DefaultWriteOffFloorPaise)},
		{"ActionBudget", cfg.ActionBudget, policy.DefaultActionBudget},
		{"ContactWindow", cfg.ContactWindow, quiet.DefaultWindow()},
	} {
		if tc.got != tc.want {
			t.Errorf("a zero Config left %s at %v, want the default %v", tc.field, tc.got, tc.want)
		}
	}
	if cfg.KillSwitch {
		t.Error("a zero Config engaged the kill switch")
	}
	if cfg.ContactLocation == nil {
		t.Error("a zero Config left the contact zone nil")
	}

	// A zero MaxTouchesPerItem and a zero Cooldown are not defaults that got
	// missed. They mean the per-source table decides, which is the ordinary
	// case, and ParamsFor is where a caller reads what is actually in force.
	for _, source := range policy.Sources() {
		params := p.ParamsFor(source)
		if params.MaxTouches <= 0 {
			t.Errorf("a zero Config left %s with a %d contact cap", source, params.MaxTouches)
		}
		if params.Cooldown <= 0 {
			t.Errorf("a zero Config left %s with a %s cooldown", source, params.Cooldown)
		}
	}

	// And the thing that actually broke once: an ordinary action on an
	// ordinary item, through a policy nobody configured, has to be allowed.
	req := policy.Request{
		RiskItemID:     "ri_zero_config",
		Source:         policy.SourceOverdueInvoice,
		Action:         policy.ActionNotifyEmail,
		AmountPaise:    100000,
		AmountDuePaise: 100000,
		HasEmail:       true,
		AtRiskSince:    start.Add(-30 * 24 * time.Hour),
		TouchNo:        1,
	}
	if got := p.Evaluate(policy.State{}, req); got.Verdict != policy.VerdictAllow {
		t.Errorf("the standard policy refused an ordinary notification: %s (%s), %s",
			got.Verdict, got.RuleID, got.Reason)
	}
}

// TestDeprecatedFieldsStillDriveTheEngine covers the shim, and only the shim.
//
// The retry engine's spellings survive on Config, State, and Request so that
// packages another work package owns keep compiling across the pivot. They have
// to keep working while they are there, or the shim is worse than the build
// break it prevents.
func TestDeprecatedFieldsStillDriveTheEngine(t *testing.T) {
	cfg := testConfig()
	cfg.MaxAttemptsPerOrder = 1
	p := policy.New(cfg, clock.NewFake(start))

	if got := p.Config().MaxTouchesPerItem; got != 1 {
		t.Errorf("MaxAttemptsPerOrder did not fold into MaxTouchesPerItem: %d", got)
	}

	// The old Request spelling of the item id reaches the idempotency key.
	old := baseReq()
	old.RiskItemID, old.TouchNo = "", 0
	old.OrderID, old.AttemptNo = "ri_baseline", 1
	if got, want := p.Evaluate(policy.State{}, old), p.Evaluate(policy.State{}, baseReq()); got.IdempotencyKey != want.IdempotencyKey {
		t.Errorf("OrderID and AttemptNo hashed to %q, want the RiskItemID and TouchNo key %q",
			got.IdempotencyKey, want.IdempotencyKey)
	}

	// The old State spellings reach R1 and R2.
	if got := p.Evaluate(policy.State{AttemptsMade: 1}, baseReq()); got.RuleID != policy.RuleMaxTouches {
		t.Errorf("AttemptsMade did not fold into TouchesMade: %s (%s)", got.Verdict, got.RuleID)
	}
	if got := p.Evaluate(policy.State{LastActionAt: start.Add(-time.Second)}, baseReq()); got.RuleID != policy.RuleCooldown {
		t.Errorf("LastActionAt did not fold into LastTouchAt: %s (%s)", got.Verdict, got.RuleID)
	}

	// The deprecated action strings are not in the lawful set and have no rule
	// branch. They exist to compile, not to act.
	for _, action := range []string{
		policy.ActionNone, policy.ActionRetrySameInstrument,
		policy.ActionRequestReauth, policy.ActionRequestNewInstrument,
	} {
		if slices.Contains(policy.LawfulActions(), action) {
			t.Errorf("the deprecated action %q leaked into the lawful set", action)
		}
		if policy.IsContactAction(action) || policy.IsNotifyAction(action) || policy.IsSafeAction(action) {
			t.Errorf("the deprecated action %q is classified by one of the action predicates", action)
		}
	}
	if policy.DefaultMaxAttemptsPerOrder != policy.DefaultMaxTouchesPerItem {
		t.Error("the deprecated attempt cap and the touch cap are different numbers")
	}
}

// TestNoRetryActionExistsAnywhereInTheLawfulSet is the pivot as an assertion.
//
// Unattended re-presentment of a one-off Indian payment has no lawful
// counterpart on any rail. The engine does not gate a retry, it has no retry to
// gate, and this fails if a constant ever comes back.
func TestNoRetryActionExistsAnywhereInTheLawfulSet(t *testing.T) {
	for _, action := range policy.LawfulActions() {
		for _, banned := range []string{"retry", "reattempt", "represent", "recharge", "charge"} {
			if strings.Contains(action, banned) {
				t.Errorf("the lawful action %q reads as %q, which this engine may not do", action, banned)
			}
		}
	}
	if len(policy.LawfulActions()) != 8 {
		t.Errorf("the lawful set has %d actions, want the frozen 8: %v",
			len(policy.LawfulActions()), policy.LawfulActions())
	}
}
