# Phase 2 problems

Things that broke, what the cause turned out to be, and what fixed them. Date
every entry. A problem that got worked around rather than fixed says so.

## 2026-08-31: the test list gained one test and renamed another while the tests were being written

`TESTS.md` was committed with 36 Go test functions. The tree has 37, and one of
the 36 has a different name.

**The addition.** `TestArmsCannotReachTheGatewaysGroundTruth`. Writing the two
`Attempter` adapters made the hole obvious. The gateway has to know how an
attempt settles, on both layers: the fake reads a per-order recovery schedule
and the live attempter reads a per-order settle outcome, because test mode
picks the outcome from one form field rather than from the card. Both tables
come out of the manifest. Nothing stopped an arm from holding the concrete
adapter and reading one. The fix is that both fields are unexported with no
accessor, and the new test walks `recovery.Surface` and both adapters by
reflection to keep it that way.

**The rename.** `TestRulesArmRefusesBaitOrdersAndRecordsTheRuleID` became
`TestRulesArmRefusesTheNeverRetryBaitAndWalksIntoTheBudgetBait`, because the
original name asserts something that is not true. R1 is a flat cap of three
attempts per order. `batch.MaxLegitAttemptsFor` gives a retry-eligible order
two, and the attempt-budget-exhausted bait arrives with those two already
spent. No rule in the set reads the per-class budget, so the policy allows a
third attempt and the rules arm takes it. The bait catches the rules arm, which
is what bait is for.

The choice was to add a tenth rule so the name would come true, or to keep the
nine-rule set and let the test say what happens. The set is the phase's
specification and the finding is worth more than a clean-looking table, so the
test now pins the behaviour that exists and `REPORT.md` carries the finding.

Cost: 20 minutes.

## 2026-08-31: three property tests passed against a policy that decides nothing

Symptom: the red run had 33 failures across 37 tests. Four passed.

Cause: `TestPolicyNeverAllowsActionOnNeverRetryClass`,
`TestPolicyNeverExceedsMaxAttempts`, and `TestPolicyDecisionIsDeterministic`
all assert a negative. The red tree's `Evaluate` returns the zero `Decision`,
whose verdict is the empty string, which is not `allow`, and two zero values
compare equal. `TestArmsCannotReachTheGatewaysGroundTruth` walks struct fields
and the fields were unexported from the first line.

Not fixed, because there is nothing to fix. A property that forbids something
cannot go red against a stub that does nothing. What it changes is what the
green run proves: those four are guardrails against a future change, not
evidence that this implementation does anything. The other 33 are the
evidence. `TESTS.md` names all four rather than reporting 33 of 37 as if the
gap were an oversight.

Cost: 0, beyond writing it down.

## 2026-08-31: the arm test rig hung, because a fake clock never reaches a poll deadline

Symptom: `go test ./internal/recovery/` never returned. Killed at 120 seconds.

Cause: the rig gave the poller a fake clock and a `Wait` that returned
immediately without advancing it. `PollUntilTerminal` ends either when the
order reads `paid` or when `clock.Now().Sub(start) + backoff` passes `MaxWait`.
Every order in these tests sits at `attempted`, and with a clock that never
moves the second condition can never fire. The loop is infinite by
construction, and it is infinite for exactly the orders this phase is about.

Fix: the rig's `Wait` advances the fake clock by the duration it was asked to
wait, which is what the phase 1 rig already did. Interval, MaxBackoff, and
MaxWait are milliseconds here, so the clock drift one poll run adds stays four
orders of magnitude below the 30 second cooldown the R1 and R2 assertions
depend on.

Cost: 10 minutes, most of it waiting for the timeout.

## 2026-08-31: the standard policy denied every order in the first real batch run

Symptom: the first `rzp run` over the 40-order fake batch took zero actions.
Every line read `deny R5-ACTION-BUDGET`.

Cause: `Config.ActionBudget` zero meant a literal cap of zero. Every other field
on `Config` treats zero as "use the default", the doc comment on the type says
the zero value is the standard policy, and `cmd/rzp/run.go` built its policy
with `policy.Config{}`. So the standard policy was a policy that permits
nothing.

Fix: zero means `DefaultActionBudget`, like every other field. A run that wants
to act on nothing sets the kill switch, which is the control built for that and
which names itself in the audit row.

What it cost beyond the fix: the unit tests did not catch it, because every one
of them sets `ActionBudget` explicitly. A rule tested only with its value
supplied does not test what happens when it is not. `DECISIONS.md` has the
entry.

Cost: 15 minutes.

## 2026-08-31: a quarter of the batch escalated on amount, and it swamped the escalation numbers

Symptom: the second run took actions, and 13 of 40 orders came back
`escalate R3-AMOUNT-CEILING`. Escalation precision was dominated by orders
whose ground truth said retry.

Cause: `DefaultAmountCeilingPaise` was 400000 and `batch.Generate` produces
amounts between 50000 and 500000. A ceiling in the middle of the amount
distribution escalates on amount routinely, so the metric that is supposed to
be about classification was measuring the ceiling.

Fix: 450000, which sits above the top decile. Recorded in `DECISIONS.md` and in
the constant's doc comment with the number it was before, because a threshold
moved after seeing a result has to be disclosed rather than quietly changed.
The rule still fires on 7 of 40, and `aggregate.py` gained an
`escalation_rules` column so an amount escalation is not read as a
classification mistake.

Cost: 20 minutes, including the column.

## 2026-08-31: every correct payment link scored as a false action

Symptom: the fake-layer table gave `a3-rules` 12 over-attempt false actions, on
the 12 orders where raising a payment link was the ground-truth correct action.

Cause: FA-2 was `acted and attempts_seen >= max_legit_attempts`.
`batch.MaxLegitAttemptsFor` counts payment attempts and gives a reauth-required
order 1, which the failure that put it in the batch has already spent. A
payment link is a notification API call and spends none of that budget, so
comparing one against the other is a category error, and it charged the arm for
doing the right thing.

Fix: FA-2 now also requires the action to be `retry_same_instrument`. The
narrowing had no test, so `test_action_past_the_attempt_budget_is_fa2` was
extended to pin it: a notification never scores FA-2, and FA-1 still fires on
bait whatever the action kind.

Cost: 20 minutes.

## 2026-08-31: the cost column counted a run's polls and none of its attempts

Symptom: the fake-layer table gave `a0-control`, which takes no action at all,
and `a1-naive`, which makes 40 payment attempts, the same 360 gateway calls.

Cause: the call counter was a decorator around `razorpay.Port`. A payment
attempt does not go through `Port` on either layer: the live one drives four
undocumented checkout calls and the fake one calls `AttemptPayment`, and
neither is a `Port` method. On the live layer that understated the naive arm by
four calls per order, roughly a third of its real cost.

Fix: `AttemptRecord.GatewayCalls`, reported by whichever adapter made the
calls, added to the row's total in `cmd/rzp/run.go`. The live adapter reports
`len(AttemptOutcome.Steps)`, which records a step when it is sent rather than
when it comes back, so a request that reached Razorpay and then failed to
decode is still counted as paid for.

The same pass found that the fake layer was not counting its materialisation
calls at all while the live layer was, so the run total meant one thing on one
layer and something else on the other. Both count them now.

Cost: 25 minutes, and the live layer was rerun so the published number is the
corrected one.

## 2026-08-31: three sentences in RESULTS.md did not match the table they described

Symptom: found by reading the committed CSV back against the prose rather than
trusting the prose.

Three errors, none of them in a table cell and all of them in a sentence a
reader would quote. "Six of the seven false escalations" was all seven. "Gives
up 14 percent of the recoverable set" mixed two denominators: three orders is
14 percent of what the naive arm recovered and 8 percent of the recoverable
set. And "that is the whole of the recoverable set" claimed 21 was all 37
recoverable orders when it is all 21 a retry can reach.

Fix: all three corrected against the CSV, in their own commit so the correction
is visible in the history rather than folded into the change that introduced
them.

The general lesson is the one the repository already has a rule for: a number
in prose is a claim, and it has to be checked against the run that produced it
even when the same person wrote both an hour apart.

Cost: 15 minutes.
