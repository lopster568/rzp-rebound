# Phase 2 decisions

Choices a later phase would otherwise have to reverse-engineer. Date every
entry.

## 2026-08-31: policy.Evaluate is pure and the span is emitted at the call site

FR-POL-6 says every evaluation emits a span carrying the verdict and the rule.
The span is emitted by `recovery.Arm.rules`, which is the only caller, not
inside `Evaluate`.

`Evaluate` reads its config, its injected clock, the state, and the request,
and touches nothing else. That is what lets 576 golden matrix cells be
generated in one function call with no tracer, no context, and no fixture, and
it is what makes `TestPolicyDecisionIsDeterministic` a statement about the
policy rather than about a test harness. A tracer inside `Evaluate` would mean
every rule test needed a span recorder to run at all.

The requirement is still met, because there is exactly one call site and it
writes both the span and the audit row from the returned `Decision`. What
changes is where a future second caller has to remember to do it, and
`TestRulesArmRecordsAPolicyVerdictBeforeEverySideEffect` is what catches one
that does not.

## 2026-08-31: the kill switch is split, and only the flag half is inside Evaluate

R8 is "a flag or a file halts all actions". Reading a file is I/O, so
`policy.KillSwitchFile(path)` does the read and the runner folds the answer
into `State.KillSwitchEngaged`. `Evaluate` denies on `cfg.KillSwitch` or on
`state.KillSwitchEngaged`.

`KillSwitchFile` treats a missing file as not engaged and anything else that
stops the path being readable as an error. A kill switch that fails open when
its own storage is unreadable is not a kill switch.

## 2026-08-31: R1 stays a flat cap and the budget bait catches the rules arm

`batch.MaxLegitAttemptsFor` gives a class 3, 2, 1, or 0 attempts.
`policy.Config.MaxAttemptsPerOrder` is a flat 3 for every order, by
requirement. Nothing in the nine rules reads the per-class budget.

The attempt-budget-exhausted bait order is a retry-eligible order arriving with
its two attempts already spent. R1 allows a third, so the rules arm acts, and
the scorer counts a forbidden false action against it.

That could have been closed by adding a tenth rule. It was not. The nine-rule
set is this phase's specification, and a bait order that catches the arm it was
not aimed at is worth more than a table with a zero in it.
`TestRulesArmRefusesTheNeverRetryBaitAndWalksIntoTheBudgetBait` pins the
behaviour, and `REPORT.md` carries the finding. It is also the first piece of
evidence PRD Q6 has ever had: the four numbers in `MaxLegitAttemptsFor` are
scored against and enforced by nothing.

## 2026-08-31: a zero ActionBudget means the default, not a cap of zero

The first version had `Config.ActionBudget` zero mean a literal cap of zero,
so a run could say "evaluate everything and act on nothing". The first
fake-layer batch run showed the cost: `policy.New(policy.Config{}, clock)` is
documented as the standard policy, and the standard policy denied all 40
orders under `R5-ACTION-BUDGET`.

Zero now means `DefaultActionBudget`, the same as every other field on
`Config`. A run that wants to act on nothing sets the kill switch, which is the
control built for that and which says so in its rule id.

## 2026-08-31: the amount ceiling moved from 400000 to 450000 paise, after seeing a run

`DefaultAmountCeilingPaise` was 400000. `batch.Generate` produces amounts
between 50000 and 500000, so the first fake-layer run escalated 13 of 40 orders
on amount alone, all of them orders whose ground truth said retry. Every
escalation number in the table was then dominated by the ceiling rather than by
the classification, which is the thing the arms differ on.

The constant is now 450000, which sits above the top decile rather than in the
middle of the distribution. The number it was before is recorded here and in
the constant's doc comment, because changing a threshold after seeing a result
is exactly the move that needs to be disclosed rather than quietly made.

The rule still fires. The phase 2 report gives the escalation split by rule, so
an amount-ceiling escalation is not silently counted as a classification
mistake.

## 2026-08-31: each arm materialises its own copy of the batch

A batch manifest is a specification, not a set of gateway orders.
`rzp run` creates its own orders from it, with the seeded failure history
already on them, and records both the manifest id and the gateway id on every
outcome row.

Three arms sharing one set of orders would mean the first arm to recover one
changed what the next two saw, and the three-arm table would be measuring the
running order. On the live layer it also means the run creates three real
orders per manifest order, which is the reason the live batch is 8 and not 40.

## 2026-08-31: the gateway knows how an attempt settles, and the arm cannot read it

Live test mode picks a payment outcome at the last checkout call from one form
field, and the card never reaches it. The fake picks it from the card, which
cannot model a cause clearing on a repeat of the same instrument. So on both
layers something has to stand in for the world deciding, and on both layers
that something reads the manifest.

It lives on the gateway side: `Fake.SeedRecoversAfter` and the unexported
outcome map inside `recovery.LiveAttempter`. An arm holds the `Attempter`
interface and there is no accessor on either adapter.
`TestArmsCannotReachTheGatewaysGroundTruth` walks `recovery.Surface` and both
adapters by reflection to keep it that way.

The line this draws: the gateway is allowed to know the answer, because it is
the world. The arm is not, because it is the thing being measured.

## 2026-08-31: the harness is Python on the standard library, with unittest

ADR-0007 has the full argument for Python in a Go repo. The narrower choice
recorded here is `unittest` over pytest: `.github/workflows/ci.yml` has no
Python setup step and `ubuntu-latest` ships no pytest, so a suite that needs an
install is a suite CI does not run. `make test-python` is
`python3 -m unittest discover`, which needs nothing.

## 2026-08-31: the shuffle is inside an arm, not across arms

`harness/orchestrator.py` builds the full arms-by-orders cell list and shuffles
it with a locally scoped `random.Random(seed)`, then runs the arms one after
another with each arm's orders in their shuffled relative order.

The arms are separate processes, so interleaving them would need one process
holding all three, and that process would share gateway state between arms,
which is the thing the previous decision exists to prevent. The run manifest
records the full shuffled `cell_order` as well as the per-arm sequences, so
what actually happened is on the record rather than implied.
`EVAL-DESIGN.md` states the limit next to the numbers.

## 2026-08-31: one batch manifest and one fake-layer run are tracked in git

`.gitignore` gained two negations. `results/batches/b-1234-40.json` and
`results/runs/sample-phase-2-fake/` are committed, so the table under
`results/tables/` can be recomputed by anyone who clones the repository. A
results table with no inputs behind it is a number nobody can check.

Both are synthetic: the manifest comes out of `batch.Generate` from a seed and
the run is the fake layer. The live-layer run stays untracked, because its
order ids are real Razorpay test-mode ids.

The negations use the star form (`results/runs/*` then
`!results/runs/sample-phase-2-fake/`). A rule that excludes the directory
itself stops git looking inside it, so the exception would never be reached.
