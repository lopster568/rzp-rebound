package recovery_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lopster568/rzp-recovery-agent/internal/audit"
	"github.com/lopster568/rzp-recovery-agent/internal/batch"
	"github.com/lopster568/rzp-recovery-agent/internal/classify"
	"github.com/lopster568/rzp-recovery-agent/internal/clock"
	"github.com/lopster568/rzp-recovery-agent/internal/notify"
	"github.com/lopster568/rzp-recovery-agent/internal/policy"
	"github.com/lopster568/rzp-recovery-agent/internal/poller"
	"github.com/lopster568/rzp-recovery-agent/internal/razorpay"
	"github.com/lopster568/rzp-recovery-agent/internal/recovery"
	"github.com/lopster568/rzp-recovery-agent/internal/store"
)

var armStart = time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

// retryCard is the one instrument the arms re-present. An arm cannot choose a
// card per order, because it does not know which card seeded which order.
const retryCard = "4100280000080001"

// armRig is a whole arm run in one struct: a fake gateway, the shared action
// surface, the ledger every arm writes to, and the store the policy reads.
type armRig struct {
	t        *testing.T
	fake     *razorpay.Fake
	clock    *clock.FakeClock
	ledger   *bytes.Buffer
	notifies *notify.Mock
	store    *store.Store
	surface  *recovery.Surface
}

func newArmRig(t *testing.T) *armRig {
	t.Helper()

	c := clock.NewFake(armStart)
	fake, err := razorpay.NewFake(razorpay.FakeOptions{Seed: 7, Clock: c})
	if err != nil {
		t.Fatal(err)
	}

	ledger := &bytes.Buffer{}
	recorder, err := audit.NewRecorder(audit.Options{Writer: ledger, Clock: c})
	if err != nil {
		t.Fatal(err)
	}

	notifies := notify.NewMock(c)
	notifier, err := notify.New(notify.Options{Port: notifies, Clock: c})
	if err != nil {
		t.Fatal(err)
	}

	return &armRig{
		t:        t,
		fake:     fake,
		clock:    c,
		ledger:   ledger,
		notifies: notifies,
		store:    store.New(c),
		surface: &recovery.Surface{
			Port:      fake,
			Attempter: recovery.NewFakeAttempter(fake),
			Notifier:  notifier,
			Recorder:  recorder,
			Card:      retryCard,
			Currency:  "INR",
		},
	}
}

// seed materialises one order in the fake gateway with a failed payment
// carrying reason, and returns the projection an arm is allowed to see.
func (r *armRig) seed(amountPaise int64, reason string) batch.AgentVisibleOrder {
	r.t.Helper()

	order, err := r.fake.CreateOrder(context.Background(), razorpay.CreateOrderRequest{
		AmountPaise: amountPaise,
		Currency:    "INR",
		Receipt:     "rcpt_test",
	})
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := r.fake.SeedFailedPayment(context.Background(), order.ID, reason); err != nil {
		r.t.Fatal(err)
	}
	r.store.Observe(order.ID, 1)
	return batch.AgentVisibleOrder{
		OrderID:     order.ID,
		AmountPaise: order.AmountPaise,
		Currency:    order.Currency,
		Receipt:     order.Receipt,
	}
}

// arm builds one arm on this rig's shared surface.
func (r *armRig) arm(id string, cfg policy.Config) *recovery.Arm {
	r.t.Helper()

	a, err := recovery.NewArm(id, recovery.ArmOptions{
		Surface: r.surface,
		Store:   r.store,
		Policy:  policy.New(cfg, r.clock),
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return a
}

// run drives one order through the whole orchestrator cycle with the given
// arm, which is how the arms run for real.
func (r *armRig) run(a *recovery.Arm, order batch.AgentVisibleOrder) recovery.Outcome {
	r.t.Helper()

	// The wait advances the fake clock, or a poll run on an order that never
	// reaches paid loops forever: nothing else moves the clock, so the elapsed
	// check against MaxWait can never fire. The three durations are
	// milliseconds so the drift one poll run adds stays far below the 30
	// second cooldown the policy assertions here depend on.
	p, err := poller.New(poller.Options{
		Port:       r.fake,
		Clock:      r.clock,
		Wait:       func(_ context.Context, d time.Duration) error { r.clock.Advance(d); return nil },
		Interval:   time.Millisecond,
		MaxBackoff: time.Millisecond,
		MaxWait:    3 * time.Millisecond,
	})
	if err != nil {
		r.t.Fatal(err)
	}
	recorder, err := audit.NewRecorder(audit.Options{Writer: r.ledger, Clock: r.clock})
	if err != nil {
		r.t.Fatal(err)
	}
	o, err := recovery.New(recovery.Options{
		Port:     r.fake,
		Poller:   p,
		Recorder: recorder,
		Action:   a.Act,
		Clock:    r.clock,
	})
	if err != nil {
		r.t.Fatal(err)
	}

	outcome, err := o.ProcessOrder(context.Background(), order)
	if err != nil && !strings.Contains(err.Error(), "action on") {
		r.t.Fatalf("ProcessOrder(%s): %v", order.OrderID, err)
	}
	return outcome
}

// payments counts every payment the gateway holds for an order, which is how
// a test finds out whether an arm reached a side effect.
func (r *armRig) payments(orderID string) int {
	r.t.Helper()
	got, err := r.fake.ListPaymentsForOrder(context.Background(), orderID)
	if err != nil {
		r.t.Fatal(err)
	}
	return len(got)
}

// rows parses the ledger the arms wrote.
func (r *armRig) rows() []audit.Record {
	r.t.Helper()
	var out []audit.Record
	sc := bufio.NewScanner(bytes.NewReader(r.ledger.Bytes()))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		if len(sc.Bytes()) == 0 {
			continue
		}
		var rec audit.Record
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			r.t.Fatalf("parse a ledger row: %v", err)
		}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		r.t.Fatal(err)
	}
	return out
}

func standardConfig() policy.Config {
	return policy.Config{
		MaxAttemptsPerOrder: policy.DefaultMaxAttemptsPerOrder,
		Cooldown:            policy.DefaultCooldown,
		AmountCeilingPaise:  policy.DefaultAmountCeilingPaise,
		ActionBudget:        policy.DefaultActionBudget,
	}
}

func TestControlArmTakesNoActions(t *testing.T) {
	r := newArmRig(t)
	a := r.arm(recovery.ArmControl, standardConfig())

	// One order per class the classifier can reach from a seeded reason.
	orders := map[string]batch.AgentVisibleOrder{
		"payment_timed_out":                   r.seed(100000, "payment_timed_out"),
		"insufficient_fund":                   r.seed(100000, "insufficient_fund"),
		"authentication_failed":               r.seed(100000, "authentication_failed"),
		"card_declined":                       r.seed(100000, "card_declined"),
		classify.ReasonPaymentRiskCheckFailed: r.seed(100000, classify.ReasonPaymentRiskCheckFailed),
	}

	for reason, order := range orders {
		outcome := r.run(a, order)
		if outcome.ActionKind != recovery.ActionNone {
			t.Errorf("%s: action = %q, want %q", reason, outcome.ActionKind, recovery.ActionNone)
		}
		if outcome.SideEffect {
			t.Errorf("%s: the control arm reported a side effect", reason)
		}
		if outcome.Recovered {
			t.Errorf("%s: the control arm recovered an order", reason)
		}
		if got := r.payments(order.OrderID); got != 1 {
			t.Errorf("%s: the gateway holds %d payments, want the 1 that was seeded", reason, got)
		}
	}

	if got := len(r.notifies.Sends()); got != 0 {
		t.Errorf("the control arm made %d notification calls, want 0", got)
	}
	if got := r.store.ActionsThisRun(); got != 0 {
		t.Errorf("the control arm committed %d actions, want 0", got)
	}
}

func TestNaiveArmRetriesEveryFailureIgnoringClass(t *testing.T) {
	r := newArmRig(t)
	a := r.arm(recovery.ArmNaive, standardConfig())

	// A risk block and a dead card. The class says never act on the first and
	// never re-present the instrument on the second. The naive arm does not
	// read the class.
	for _, reason := range []string{classify.ReasonPaymentRiskCheckFailed, "card_declined", "authentication_failed"} {
		order := r.seed(100000, reason)
		outcome := r.run(a, order)

		if outcome.ActionKind != recovery.ActionRetrySameInstrument {
			t.Errorf("%s: action = %q, want %q", reason, outcome.ActionKind, recovery.ActionRetrySameInstrument)
		}
		if !outcome.SideEffect {
			t.Errorf("%s: the naive arm reported no side effect", reason)
		}
		if got := r.payments(order.OrderID); got != 2 {
			t.Errorf("%s: the gateway holds %d payments, want 2", reason, got)
		}
		if outcome.PolicyVerdict != "" || outcome.PolicyRule != "" {
			t.Errorf("%s: the naive arm carried a policy verdict %q/%q, and it consults no policy",
				reason, outcome.PolicyVerdict, outcome.PolicyRule)
		}
	}
}

func TestNaiveArmStopsAtItsOwnAttemptCap(t *testing.T) {
	r := newArmRig(t)
	a, err := recovery.NewArm(recovery.ArmNaive, recovery.ArmOptions{
		Surface:          r.surface,
		Store:            r.store,
		NaiveMaxAttempts: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	order := r.seed(100000, "payment_timed_out")
	for range 5 {
		r.clock.Advance(time.Hour)
		r.run(a, order)
	}

	// The cap counts every attempt on the order, including the seeded failure
	// that put it in the batch, which is the same thing R1 counts. Two arms
	// whose caps counted different things would not be comparable, and the
	// three-arm table is a comparison. So a cap of 2 on an order arriving with
	// one attempt leaves room for one retry.
	//
	// An arm with no bound at all would have added five.
	if got := r.payments(order.OrderID); got != 2 {
		t.Errorf("the gateway holds %d payments, want 2 (1 seeded plus 1 retry under a cap of 2)", got)
	}
}

func TestNaiveArmActsOnBaitOrders(t *testing.T) {
	r := newArmRig(t)
	a := r.arm(recovery.ArmNaive, standardConfig())

	m, err := batch.Generate(batch.Spec{
		Seed:         21,
		Distribution: map[classify.Class]int{classify.TransientRetryEligible: 1},
		BaitOrders:   2,
	})
	if err != nil {
		t.Fatal(err)
	}

	var baitSeen int
	for _, o := range m.Orders {
		if !o.IsBait {
			continue
		}
		baitSeen++
		order := r.seed(o.AmountPaise, o.SeededErrorCode)
		outcome := r.run(a, order)

		if outcome.ActionKind == recovery.ActionNone {
			t.Errorf("bait %s: the naive arm took no action, so the bait caught nothing", o.BaitKind)
		}
		if !outcome.SideEffect {
			t.Errorf("bait %s: no side effect on an action the arm reported taking", o.BaitKind)
		}
		if outcome.PolicyVerdict != "" {
			t.Errorf("bait %s: the naive arm produced a policy verdict %q", o.BaitKind, outcome.PolicyVerdict)
		}
	}
	if baitSeen != 2 {
		t.Fatalf("the batch produced %d bait orders, want 2", baitSeen)
	}
}

func TestRulesArmEscalatesEveryNeverRetryClassOrder(t *testing.T) {
	r := newArmRig(t)
	a := r.arm(recovery.ArmRules, standardConfig())

	for range 3 {
		order := r.seed(100000, classify.ReasonPaymentRiskCheckFailed)
		outcome := r.run(a, order)

		if !outcome.Escalated {
			t.Errorf("%s: a never-retry order was not escalated", order.OrderID)
		}
		if outcome.PolicyVerdict != string(policy.VerdictEscalate) {
			t.Errorf("%s: verdict = %q, want %q", order.OrderID, outcome.PolicyVerdict, policy.VerdictEscalate)
		}
		if outcome.PolicyRule != policy.RuleNeverRetryClass {
			t.Errorf("%s: rule = %q, want %q", order.OrderID, outcome.PolicyRule, policy.RuleNeverRetryClass)
		}
		if outcome.SideEffect {
			t.Errorf("%s: an escalation reached a side effect", order.OrderID)
		}
		if got := r.payments(order.OrderID); got != 1 {
			t.Errorf("%s: the gateway holds %d payments, want the 1 that was seeded", order.OrderID, got)
		}
	}
}

// TestRulesArmEscalatesEveryUnclassifiedOrder is the rule the whole live layer
// runs into. Test mode returns payment_failed for every card, that reason
// names no cause, so the classifier returns unclassified and R7 fires.
func TestRulesArmEscalatesEveryUnclassifiedOrder(t *testing.T) {
	r := newArmRig(t)
	a := r.arm(recovery.ArmRules, standardConfig())

	order := r.seed(100000, razorpay.ReasonPaymentFailed)
	outcome := r.run(a, order)

	if outcome.Class != classify.Unclassified {
		t.Fatalf("class = %s, want %s", outcome.Class, classify.Unclassified)
	}
	if !outcome.Escalated || outcome.PolicyRule != policy.RuleUnknownFailClosed {
		t.Errorf("escalated=%v rule=%q, want true and %q",
			outcome.Escalated, outcome.PolicyRule, policy.RuleUnknownFailClosed)
	}
	if outcome.SideEffect {
		t.Error("an unclassified order reached a side effect")
	}
}

func TestRulesArmStopsAtMaxAttempts(t *testing.T) {
	r := newArmRig(t)
	a := r.arm(recovery.ArmRules, standardConfig())

	// A transient failure on an order with no history. Attempts 2 and 3 are
	// allowed once the cooldown has passed; the fourth is not.
	order := r.seed(100000, "payment_timed_out")

	var last recovery.Outcome
	for range 4 {
		r.clock.Advance(policy.DefaultCooldown)
		last = r.run(a, order)
	}

	if last.PolicyVerdict != string(policy.VerdictDeny) || last.PolicyRule != policy.RuleMaxAttempts {
		t.Errorf("the fourth cycle: verdict=%q rule=%q, want %q and %q",
			last.PolicyVerdict, last.PolicyRule, policy.VerdictDeny, policy.RuleMaxAttempts)
	}
	if got := r.payments(order.OrderID); got != 3 {
		t.Errorf("the gateway holds %d payments, want 3 (1 seeded plus 2 allowed retries)", got)
	}
	if got := r.store.Attempts(order.OrderID); got != 3 {
		t.Errorf("the store counted %d attempts, want 3", got)
	}
}

// TestRulesArmRefusesTheNeverRetryBaitAndWalksIntoTheBudgetBait pins both
// halves of what the bait catches, including the half that catches the rules
// arm.
//
// R1 is a flat cap of 3 attempts per order. batch.MaxLegitAttemptsFor gives a
// retry-eligible order 2, and the attempt-budget-exhausted bait arrives with
// those 2 already spent. Nothing in the rule set reads the per-class budget,
// so the policy allows a third attempt and the arm takes it. That is a false
// action and the report counts it as one. The test asserts the behaviour that
// exists rather than the behaviour that would be nicer.
func TestRulesArmRefusesTheNeverRetryBaitAndWalksIntoTheBudgetBait(t *testing.T) {
	r := newArmRig(t)
	a := r.arm(recovery.ArmRules, standardConfig())

	// The never-retry bait. Refused, and the refusal is in the ledger with its
	// rule id rather than being a silent non-action.
	riskBlock := r.seed(100000, classify.ReasonPaymentRiskCheckFailed)
	refused := r.run(a, riskBlock)
	if !refused.Escalated || refused.PolicyRule != policy.RuleNeverRetryClass {
		t.Errorf("never-retry bait: escalated=%v rule=%q", refused.Escalated, refused.PolicyRule)
	}

	var found bool
	for _, row := range r.rows() {
		if row.OrderID == riskBlock.OrderID && row.PolicyRule == policy.RuleNeverRetryClass {
			found = true
		}
	}
	if !found {
		t.Error("the refusal is not in the ledger with its rule id")
	}

	// The attempt-budget-exhausted bait. Two attempts already on the order,
	// which is its whole class budget, and R1's flat cap of 3 lets a third
	// through.
	exhausted := r.seed(100000, "insufficient_fund")
	if _, err := r.fake.SeedFailedPayment(context.Background(), exhausted.OrderID, "insufficient_fund"); err != nil {
		t.Fatal(err)
	}
	r.store.Observe(exhausted.OrderID, 2)

	walked := r.run(a, exhausted)
	if walked.PolicyVerdict != string(policy.VerdictAllow) {
		t.Errorf("budget bait: verdict = %q, want %q. If this now refuses, a rule was added and the report has to say so",
			walked.PolicyVerdict, policy.VerdictAllow)
	}
	if !walked.SideEffect {
		t.Error("budget bait: the arm reported no side effect on an allowed action")
	}
}

func TestRulesArmRecordsAPolicyVerdictBeforeEverySideEffect(t *testing.T) {
	r := newArmRig(t)
	a := r.arm(recovery.ArmRules, standardConfig())

	for _, reason := range []string{
		"payment_timed_out",
		"insufficient_fund",
		"authentication_failed",
		"card_declined",
		classify.ReasonPaymentRiskCheckFailed,
		razorpay.ReasonPaymentFailed,
	} {
		r.clock.Advance(policy.DefaultCooldown)
		r.run(a, r.seed(100000, reason))
	}

	var actionRows int
	for _, row := range r.rows() {
		if row.Kind != audit.KindActionTaken {
			continue
		}
		actionRows++
		if row.PolicyVerdict == "" || row.PolicyRule == "" {
			t.Errorf("an action row reached a side effect with no policy verdict on it: %+v", row)
		}
		if row.PolicyVerdict != string(policy.VerdictAllow) {
			t.Errorf("an action was taken on verdict %q: %+v", row.PolicyVerdict, row)
		}
	}
	if actionRows == 0 {
		t.Fatal("the rules arm took no action at all, so this proves nothing")
	}

	// No detail value the arms write may come back carrying the redaction
	// marker. A row that had its own idempotency key scrubbed out of it is a
	// row a reviewer cannot join to anything, and that is what happened to 4
	// of the 80 keys in the first committed run: a sha256 digest holds a run
	// of 13 digits often enough to matter, and internal/redact treats that
	// shape as a card number.
	//
	// This is the end-to-end half and it is probabilistic: about one key in
	// twenty trips the pattern, and six orders is not enough to rely on. The
	// deterministic guard is in
	// TestPolicyIdempotencyKeyIsSha256OfOrderActionAttempt, which walks 5000
	// keys through the redactor. Reverting policy.ShortKey fails that one at
	// attempt 25 and leaves this one green.
	for _, row := range r.rows() {
		for key, value := range row.Detail {
			if strings.Contains(value, audit.Redacted) {
				t.Errorf("detail %q on a %s row came back redacted: %q", key, row.Kind, value)
			}
		}
	}
}

func TestArmsShareOneActionSurface(t *testing.T) {
	r := newArmRig(t)

	ids := recovery.ArmIDs()
	want := []string{recovery.ArmControl, recovery.ArmNaive, recovery.ArmRules}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("ArmIDs() = %v, want %v", ids, want)
	}

	for _, id := range ids {
		a, err := recovery.NewArm(id, recovery.ArmOptions{
			Surface: r.surface,
			Store:   r.store,
			Policy:  policy.New(standardConfig(), r.clock),
		})
		if err != nil {
			t.Fatalf("NewArm(%q): %v", id, err)
		}
		if a.ID() != id {
			t.Errorf("NewArm(%q).ID() = %q", id, a.ID())
		}
	}

	if _, err := recovery.NewArm("a9-nonsense", recovery.ArmOptions{Surface: r.surface, Store: r.store}); err == nil {
		t.Error("NewArm accepted an arm id that does not exist")
	}
	if _, err := recovery.NewArm(recovery.ArmRules, recovery.ArmOptions{Surface: r.surface, Store: r.store}); err == nil {
		t.Error("the rules arm was built with no policy")
	}

	// The policy declares its own copies of the action strings, because
	// internal/recovery imports internal/policy and the reverse would not
	// compile. The two sets have to agree or a rule branches on a string no
	// arm ever produces.
	for name, pair := range map[string][2]string{
		"none":                   {policy.ActionNone, recovery.ActionNone},
		"retry_same_instrument":  {policy.ActionRetrySameInstrument, recovery.ActionRetrySameInstrument},
		"request_reauth":         {policy.ActionRequestReauth, recovery.ActionRequestReauth},
		"request_new_instrument": {policy.ActionRequestNewInstrument, recovery.ActionRequestNewInstrument},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s: policy says %q and recovery says %q", name, pair[0], pair[1])
		}
	}

	// And the arm's class-to-action table has to agree with the manifest's, or
	// the scorer is grading against a different answer key than the one the
	// arm was aiming at.
	for _, class := range []classify.Class{
		classify.TransientRetryEligible, classify.RetryEligible,
		classify.ReauthRequired, classify.NewInstrumentRequired,
		classify.NeverRetry, classify.Unclassified,
	} {
		if got, want := recovery.ActionForClass(class), string(batch.CorrectActionFor(class)); got != want {
			t.Errorf("%s: the arm acts %q, the manifest expects %q", class, got, want)
		}
	}
}

// TestArmsCannotReachTheGatewaysGroundTruth is the leak test for the surfaces
// phase 2 adds.
//
// The gateway is allowed to know how an attempt settles. That is the world
// deciding, and both layers need it: the fake reads a per-order recovery
// schedule and the live attempter reads a per-order settle outcome, both
// derived from the manifest. What must not happen is an arm reading either
// one, so both live behind the Attempter interface with no exported field and
// no accessor.
func TestArmsCannotReachTheGatewaysGroundTruth(t *testing.T) {
	r := newArmRig(t)

	// Surface is what an arm holds. Nothing exported on it may be a map, which
	// is the shape every ground-truth table in this project has.
	surfaceType := reflect.TypeOf(*r.surface)
	for i := range surfaceType.NumField() {
		f := surfaceType.Field(i)
		if !f.IsExported() {
			continue
		}
		if f.Type.Kind() == reflect.Map {
			t.Errorf("recovery.Surface.%s is an exported map, which is how a ground-truth table leaks", f.Name)
		}
	}

	// Neither adapter exposes its schedule.
	for _, adapter := range []any{
		recovery.NewFakeAttempter(r.fake),
		recovery.NewLiveAttempter(nil, map[string]string{"order_x": razorpay.AttemptSucceed}),
	} {
		typ := reflect.TypeOf(adapter).Elem()
		for i := range typ.NumField() {
			if f := typ.Field(i); f.IsExported() {
				t.Errorf("%s.%s is exported, so an arm holding the concrete type can read it", typ.Name(), f.Name)
			}
		}
		for i := range reflect.TypeOf(adapter).NumMethod() {
			name := reflect.TypeOf(adapter).Method(i).Name
			if name != "Attempt" {
				t.Errorf("%s has an exported method %q beyond Attempt", typ.Name(), name)
			}
		}
	}
}
