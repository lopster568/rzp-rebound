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
