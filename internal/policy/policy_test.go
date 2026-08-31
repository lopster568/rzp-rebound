package policy_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/lopster568/rzp-recovery-agent/internal/redact"
)

// start is the instant every fake clock in this file reads. Any instant would
// do; a fixed one keeps a failure message readable.
var start = time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

const (
	testCooldown = 30 * time.Second
	testCeiling  = int64(400000)
)

// testConfig is the policy every test in this file evaluates against, unless
// it says otherwise.
func testConfig() policy.Config {
	return policy.Config{
		MaxAttemptsPerOrder: 3,
		Cooldown:            testCooldown,
		NotifyWindow:        testCooldown,
		AmountCeilingPaise:  testCeiling,
		ActionBudget:        500,
	}
}

func newPolicy(t *testing.T, cfg policy.Config) *policy.Policy {
	t.Helper()
	return policy.New(cfg, clock.NewFake(start))
}

// retryReq is a plain, allowable retry on an order that should pass every
// rule, so a test can change one field and see one rule fire.
func retryReq() policy.Request {
	return policy.Request{
		OrderID:     "order_baseline",
		Action:      policy.ActionRetrySameInstrument,
		Class:       classify.TransientRetryEligible,
		AmountPaise: testCeiling - 100,
		AttemptNo:   1,
	}
}

// allClasses is every class the classifier can return.
var allClasses = []classify.Class{
	classify.Unclassified,
	classify.TransientRetryEligible,
	classify.RetryEligible,
	classify.ReauthRequired,
	classify.NewInstrumentRequired,
	classify.NeverRetry,
}

// allActions is every action kind the policy branches on, minus none, which is
// not a proposal.
var allActions = []string{
	policy.ActionRetrySameInstrument,
	policy.ActionRequestReauth,
	policy.ActionRequestNewInstrument,
}

func TestPolicyKillSwitchFlagDeniesEveryAction(t *testing.T) {
	cfg := testConfig()
	cfg.KillSwitch = true
	p := newPolicy(t, cfg)

	for _, class := range allClasses {
		for _, action := range allActions {
			for attempts := range 4 {
				req := retryReq()
				req.Action = action
				req.Class = class
				got := p.Evaluate(policy.State{AttemptsMade: attempts}, req)
				if got.Verdict != policy.VerdictDeny || got.RuleID != policy.RuleKillSwitch {
					t.Errorf("class %s action %s attempts %d: got %s/%s, want %s/%s",
						class, action, attempts, got.Verdict, got.RuleID,
						policy.VerdictDeny, policy.RuleKillSwitch)
				}
			}
		}
	}
}

func TestPolicyKillSwitchStateDeniesEveryAction(t *testing.T) {
	p := newPolicy(t, testConfig())

	for _, class := range allClasses {
		for _, action := range allActions {
			req := retryReq()
			req.Action = action
			req.Class = class
			got := p.Evaluate(policy.State{KillSwitchEngaged: true}, req)
			if got.Verdict != policy.VerdictDeny || got.RuleID != policy.RuleKillSwitch {
				t.Errorf("class %s action %s: got %s/%s, want %s/%s",
					class, action, got.Verdict, got.RuleID,
					policy.VerdictDeny, policy.RuleKillSwitch)
			}
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

	got := p.Evaluate(policy.State{AttemptsMade: 1, IdempotencyKeySeen: true}, retryReq())
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

	// The same request with the key unseen is a fresh action, not a replay.
	fresh := p.Evaluate(policy.State{AttemptsMade: 1}, retryReq())
	if fresh.IdempotentReplay {
		t.Error("IdempotentReplay is true on an unseen key")
	}
	if fresh.IdempotencyKey != got.IdempotencyKey {
		t.Errorf("the same request hashed two ways: %q and %q", fresh.IdempotencyKey, got.IdempotencyKey)
	}
}

func TestPolicyIdempotencyKeyIsSha256OfOrderActionAttempt(t *testing.T) {
	sum := sha256.Sum256([]byte("order_x|retry_same_instrument|2"))
	want := hex.EncodeToString(sum[:])

	if got := policy.IdempotencyKey("order_x", policy.ActionRetrySameInstrument, 2); got != want {
		t.Errorf("IdempotencyKey = %q, want %q", got, want)
	}

	// One field different is one key different, in all three fields.
	base := policy.IdempotencyKey("order_x", policy.ActionRetrySameInstrument, 2)
	for name, other := range map[string]string{
		"order":   policy.IdempotencyKey("order_y", policy.ActionRetrySameInstrument, 2),
		"action":  policy.IdempotencyKey("order_x", policy.ActionRequestReauth, 2),
		"attempt": policy.IdempotencyKey("order_x", policy.ActionRetrySameInstrument, 3),
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
		key := policy.IdempotencyKey("order_x", policy.ActionRetrySameInstrument, i)
		short := policy.ShortKey(key)
		if redact.Value(short) != short {
			t.Fatalf("attempt %d: the short key %q does not survive redaction", i, short)
		}
	}
}

func TestPolicyUnclassifiedEscalatesAndNeverRetries(t *testing.T) {
	p := newPolicy(t, testConfig())

	for _, action := range allActions {
		for attempts := range 4 {
			req := retryReq()
			req.Action = action
			req.Class = classify.Unclassified
			got := p.Evaluate(policy.State{AttemptsMade: attempts}, req)
			if got.Verdict != policy.VerdictEscalate || got.RuleID != policy.RuleUnknownFailClosed {
				t.Errorf("action %s attempts %d: got %s/%s, want %s/%s",
					action, attempts, got.Verdict, got.RuleID,
					policy.VerdictEscalate, policy.RuleUnknownFailClosed)
			}
		}
	}
}

func TestPolicyNeverRetryClassEscalates(t *testing.T) {
	p := newPolicy(t, testConfig())

	for _, action := range allActions {
		req := retryReq()
		req.Action = action
		req.Class = classify.NeverRetry
		got := p.Evaluate(policy.State{}, req)
		if got.Verdict != policy.VerdictEscalate || got.RuleID != policy.RuleNeverRetryClass {
			t.Errorf("action %s: got %s/%s, want %s/%s", action, got.Verdict, got.RuleID,
				policy.VerdictEscalate, policy.RuleNeverRetryClass)
		}
	}
}

func TestPolicyAmountAboveCeilingEscalates(t *testing.T) {
	p := newPolicy(t, testConfig())

	req := retryReq()
	req.AmountPaise = testCeiling + 1
	got := p.Evaluate(policy.State{}, req)
	if got.Verdict != policy.VerdictEscalate || got.RuleID != policy.RuleAmountCeiling {
		t.Errorf("got %s/%s, want %s/%s", got.Verdict, got.RuleID,
			policy.VerdictEscalate, policy.RuleAmountCeiling)
	}
}

// TestPolicyAmountAtCeilingIsAllowed is its own test because "above" is the
// whole of R3. An off-by-one here escalates every order at the round number an
// operator is most likely to configure.
func TestPolicyAmountAtCeilingIsAllowed(t *testing.T) {
	p := newPolicy(t, testConfig())

	for _, tc := range []struct {
		name   string
		amount int64
		want   policy.Verdict
	}{
		{"one paise below the ceiling", testCeiling - 1, policy.VerdictAllow},
		{"exactly at the ceiling", testCeiling, policy.VerdictAllow},
		{"one paise above the ceiling", testCeiling + 1, policy.VerdictEscalate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := retryReq()
			req.AmountPaise = tc.amount
			got := p.Evaluate(policy.State{}, req)
			if got.Verdict != tc.want {
				t.Errorf("%d paise against a %d ceiling: verdict = %s (%s), want %s",
					tc.amount, testCeiling, got.Verdict, got.RuleID, tc.want)
			}
		})
	}
}

func TestPolicyMaxAttemptsDeniesTheFourthAttempt(t *testing.T) {
	p := newPolicy(t, testConfig())

	for _, tc := range []struct {
		attempts int
		want     policy.Verdict
	}{{0, policy.VerdictAllow}, {1, policy.VerdictAllow}, {2, policy.VerdictAllow}, {3, policy.VerdictDeny}} {
		t.Run(fmt.Sprintf("attempts=%d", tc.attempts), func(t *testing.T) {
			got := p.Evaluate(policy.State{AttemptsMade: tc.attempts}, retryReq())
			if got.Verdict != tc.want {
				t.Errorf("verdict = %s (%s), want %s", got.Verdict, got.RuleID, tc.want)
			}
			if tc.want == policy.VerdictDeny && got.RuleID != policy.RuleMaxAttempts {
				t.Errorf("rule = %s, want %s", got.RuleID, policy.RuleMaxAttempts)
			}
		})
	}
}

func TestPolicyRemainingCountsAttemptsLeft(t *testing.T) {
	p := newPolicy(t, testConfig())

	for attempts, want := range map[int]int{0: 3, 1: 2, 2: 1, 3: 0, 4: 0, 9: 0} {
		got := p.Evaluate(policy.State{AttemptsMade: attempts}, retryReq())
		if got.Remaining != want {
			t.Errorf("attempts %d: Remaining = %d, want %d", attempts, got.Remaining, want)
		}
	}
}

func TestPolicyCooldownDeniesInsideTheWindow(t *testing.T) {
	p := newPolicy(t, testConfig())

	for _, elapsed := range []time.Duration{0, time.Second, testCooldown - time.Second} {
		state := policy.State{AttemptsMade: 1, LastActionAt: start.Add(-elapsed)}
		got := p.Evaluate(state, retryReq())
		if got.Verdict != policy.VerdictDeny || got.RuleID != policy.RuleCooldown {
			t.Errorf("elapsed %s: got %s/%s, want %s/%s", elapsed, got.Verdict, got.RuleID,
				policy.VerdictDeny, policy.RuleCooldown)
		}
	}
}

func TestPolicyCooldownAllowsExactlyAtTheWindow(t *testing.T) {
	p := newPolicy(t, testConfig())

	for _, elapsed := range []time.Duration{testCooldown, testCooldown + time.Second, time.Hour} {
		state := policy.State{AttemptsMade: 1, LastActionAt: start.Add(-elapsed)}
		if got := p.Evaluate(state, retryReq()); got.Verdict != policy.VerdictAllow {
			t.Errorf("elapsed %s: verdict = %s (%s), want %s", elapsed, got.Verdict, got.RuleID,
				policy.VerdictAllow)
		}
	}

	// A zero LastActionAt means this run has not acted on the order, which is
	// not a cooldown violation. Treating the zero time as "acted at the epoch"
	// would be a silent allow; treating it as "acted now" would deny the first
	// action of every run.
	if got := p.Evaluate(policy.State{AttemptsMade: 1}, retryReq()); got.Verdict != policy.VerdictAllow {
		t.Errorf("no prior action: verdict = %s (%s), want %s", got.Verdict, got.RuleID, policy.VerdictAllow)
	}
}

func TestPolicyNotifyRateAllowsOneNotificationPerWindow(t *testing.T) {
	p := newPolicy(t, testConfig())

	notify := retryReq()
	notify.Action = policy.ActionRequestReauth
	notify.Class = classify.ReauthRequired

	// Inside the window, refused by R6 rather than by R2, because the last
	// action and the last notification are tracked separately.
	inside := policy.State{LastNotifyAt: start.Add(-time.Second)}
	if got := p.Evaluate(inside, notify); got.Verdict != policy.VerdictDeny || got.RuleID != policy.RuleNotifyRate {
		t.Errorf("inside the window: got %s/%s, want %s/%s", got.Verdict, got.RuleID,
			policy.VerdictDeny, policy.RuleNotifyRate)
	}

	// At the window, allowed.
	at := policy.State{LastNotifyAt: start.Add(-testCooldown)}
	if got := p.Evaluate(at, notify); got.Verdict != policy.VerdictAllow {
		t.Errorf("at the window: verdict = %s (%s), want %s", got.Verdict, got.RuleID, policy.VerdictAllow)
	}

	// A retry inside the same window is not a notification, so R6 has nothing
	// to say about it.
	if got := p.Evaluate(inside, retryReq()); got.RuleID == policy.RuleNotifyRate {
		t.Errorf("a retry was refused by the notification rate rule: %+v", got)
	}

	if !policy.IsNotifyAction(policy.ActionRequestReauth) || !policy.IsNotifyAction(policy.ActionRequestNewInstrument) {
		t.Error("a reauth or new-instrument request is a notification and IsNotifyAction says otherwise")
	}
	if policy.IsNotifyAction(policy.ActionRetrySameInstrument) {
		t.Error("a retry is not a notification and IsNotifyAction says it is")
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
		got := p.Evaluate(policy.State{ActionsThisRun: tc.spent}, retryReq())
		if got.Verdict != tc.want {
			t.Errorf("spent %d of 2: verdict = %s (%s), want %s", tc.spent, got.Verdict, got.RuleID, tc.want)
		}
		if tc.want == policy.VerdictDeny && got.RuleID != policy.RuleActionBudget {
			t.Errorf("spent %d of 2: rule = %s, want %s", tc.spent, got.RuleID, policy.RuleActionBudget)
		}
	}
}

// TestPolicyRuleOrderIsFixedWhenTwoRulesWouldFire pins the evaluation order.
// It is the contract the golden matrix rests on: without it, a refactor that
// reorders the rules changes every doubly-refused row's rule id and the diff
// looks like noise.
func TestPolicyRuleOrderIsFixedWhenTwoRulesWouldFire(t *testing.T) {
	cfg := testConfig()
	// A cap of 1 against a run that has already spent 9. Zero would mean the
	// default, which is what every other field's zero means since the
	// 2026-08-31 change.
	cfg.ActionBudget = 1
	p := newPolicy(t, cfg)

	// Every field set to something a rule refuses. Each case removes the
	// higher-priority trip and expects the next rule down the list.
	notify := retryReq()
	notify.Action = policy.ActionRequestReauth
	notify.AmountPaise = testCeiling + 1
	notify.Class = classify.Unclassified

	full := policy.State{
		AttemptsMade:       9,
		LastActionAt:       start,
		LastNotifyAt:       start,
		ActionsThisRun:     9,
		KillSwitchEngaged:  true,
		IdempotencyKeySeen: true,
	}

	for _, tc := range []struct {
		name  string
		mutin func(*policy.State, *policy.Request)
		want  string
	}{
		{"everything trips", func(*policy.State, *policy.Request) {}, policy.RuleKillSwitch},
		{"no kill switch", func(s *policy.State, _ *policy.Request) { s.KillSwitchEngaged = false }, policy.RuleIdempotency},
		{"no replay", func(s *policy.State, _ *policy.Request) { s.IdempotencyKeySeen = false }, policy.RuleUnknownFailClosed},
		{"classified never retry", func(_ *policy.State, r *policy.Request) { r.Class = classify.NeverRetry }, policy.RuleNeverRetryClass},
		{"classified reauth", func(_ *policy.State, r *policy.Request) { r.Class = classify.ReauthRequired }, policy.RuleAmountCeiling},
		{"under the ceiling", func(_ *policy.State, r *policy.Request) { r.AmountPaise = 1 }, policy.RuleMaxAttempts},
		{"attempts left", func(s *policy.State, _ *policy.Request) { s.AttemptsMade = 0 }, policy.RuleCooldown},
		{"cooldown elapsed", func(s *policy.State, _ *policy.Request) { s.LastActionAt = start.Add(-time.Hour) }, policy.RuleNotifyRate},
		{"notify window elapsed", func(s *policy.State, _ *policy.Request) { s.LastNotifyAt = start.Add(-time.Hour) }, policy.RuleActionBudget},
	} {
		tc.mutin(&full, &notify)
		got := p.Evaluate(full, notify)
		if got.RuleID != tc.want {
			t.Errorf("%s: rule = %s, want %s (verdict %s)", tc.name, got.RuleID, tc.want, got.Verdict)
		}
	}

	// With every trip cleared and the budget restored, the same request
	// allows and says which rule let it through.
	cfg.ActionBudget = 500
	clear := policy.New(cfg, clock.NewFake(start))
	full.ActionsThisRun = 0
	if got := clear.Evaluate(full, notify); got.Verdict != policy.VerdictAllow || got.RuleID != policy.RuleAllow {
		t.Errorf("nothing trips: got %s/%s, want %s/%s", got.Verdict, got.RuleID,
			policy.VerdictAllow, policy.RuleAllow)
	}
}

// TestPolicyNeverAllowsActionOnNeverRetryClass walks the whole matrix input
// space. Nothing about a never-retry order can produce an allow.
func TestPolicyNeverAllowsActionOnNeverRetryClass(t *testing.T) {
	p := newPolicy(t, testConfig())

	for _, in := range matrixInputs() {
		if in.class != classify.NeverRetry {
			continue
		}
		state, req := in.build()
		if got := p.Evaluate(state, req); got.Verdict == policy.VerdictAllow {
			t.Fatalf("%s allowed an action: %+v", in, got)
		}
	}
}

func TestPolicyNeverExceedsMaxAttempts(t *testing.T) {
	p := newPolicy(t, testConfig())

	for _, in := range matrixInputs() {
		if in.attempts < policy.DefaultMaxAttemptsPerOrder {
			continue
		}
		state, req := in.build()
		got := p.Evaluate(state, req)
		if got.Verdict == policy.VerdictAllow {
			t.Fatalf("%s allowed a %d attempt against a cap of %d: %+v",
				in, in.attempts+1, policy.DefaultMaxAttemptsPerOrder, got)
		}
		if got.Remaining != 0 {
			t.Fatalf("%s: Remaining = %d with the cap spent", in, got.Remaining)
		}
	}
}

func TestPolicyDecisionIsDeterministic(t *testing.T) {
	for _, in := range matrixInputs() {
		state, req := in.build()

		// The same policy twice.
		one := policy.New(testConfig(), clock.NewFake(start))
		if a, b := one.Evaluate(state, req), one.Evaluate(state, req); !reflect.DeepEqual(a, b) {
			t.Fatalf("%s: two evaluations on one policy disagreed:\n%+v\n%+v", in, a, b)
		}

		// Two policies built the same way.
		two := policy.New(testConfig(), clock.NewFake(start))
		if a, b := one.Evaluate(state, req), two.Evaluate(state, req); !reflect.DeepEqual(a, b) {
			t.Fatalf("%s: two policies disagreed:\n%+v\n%+v", in, a, b)
		}
	}
}

func TestPolicyDenialAlwaysCarriesRuleID(t *testing.T) {
	p := newPolicy(t, testConfig())
	known := policy.RuleIDs()
	if len(known) != 10 {
		t.Fatalf("RuleIDs returned %d ids, want the 10 in PLAN.md: %v", len(known), known)
	}

	for _, in := range matrixInputs() {
		state, req := in.build()
		got := p.Evaluate(state, req)
		if got.RuleID == "" {
			t.Fatalf("%s produced a decision with no rule id: %+v", in, got)
		}
		if !slices.Contains(known, got.RuleID) {
			t.Fatalf("%s produced rule id %q, which is not in RuleIDs(): %v", in, got.RuleID, known)
		}
		if got.Verdict != policy.VerdictAllow && got.RuleID == policy.RuleAllow {
			t.Fatalf("%s refused with the allow rule on it: %+v", in, got)
		}
	}
}

// TestPolicyZeroConfigIsTheStandardPolicy covers the gap that let the first
// real batch run deny all 40 orders.
//
// Every other test in this file supplies a Config with all five fields set, so
// none of them exercised what happens when one is left out. Config's doc
// comment says the zero value is the standard policy and cmd/rzp/run.go takes
// it at its word, and for one afternoon on 2026-08-31 the standard policy
// permitted nothing because a zero ActionBudget meant a cap of zero.
func TestPolicyZeroConfigIsTheStandardPolicy(t *testing.T) {
	p := policy.New(policy.Config{}, clock.NewFake(start))

	cfg := p.Config()
	for _, tc := range []struct {
		field string
		got   any
		want  any
	}{
		{"MaxAttemptsPerOrder", cfg.MaxAttemptsPerOrder, policy.DefaultMaxAttemptsPerOrder},
		{"Cooldown", cfg.Cooldown, policy.DefaultCooldown},
		{"NotifyWindow", cfg.NotifyWindow, policy.DefaultCooldown},
		{"AmountCeilingPaise", cfg.AmountCeilingPaise, int64(policy.DefaultAmountCeilingPaise)},
		{"ActionBudget", cfg.ActionBudget, policy.DefaultActionBudget},
	} {
		if tc.got != tc.want {
			t.Errorf("a zero Config left %s at %v, want the default %v", tc.field, tc.got, tc.want)
		}
	}
	if cfg.KillSwitch {
		t.Error("a zero Config engaged the kill switch")
	}

	// And the thing that actually broke: an ordinary retry on an ordinary
	// order, through a policy nobody configured, has to be allowed.
	req := policy.Request{
		OrderID:     "order_zero_config",
		Action:      policy.ActionRetrySameInstrument,
		Class:       classify.TransientRetryEligible,
		AmountPaise: 100000,
		AttemptNo:   1,
	}
	if got := p.Evaluate(policy.State{AttemptsMade: 1}, req); got.Verdict != policy.VerdictAllow {
		t.Errorf("the standard policy refused an ordinary retry: %s (%s), %s",
			got.Verdict, got.RuleID, got.Reason)
	}
}
