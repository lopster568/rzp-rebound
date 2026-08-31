# Phase 2 report

Written 2026-08-31, at the end of phase 2. Every number here comes from a run
whose output is under `results/`.

## Scope

Phase 2's gate was a real three-arm results table with all metrics populated.
There is one per layer: `results/tables/sample-phase-2-fake.md` and
`results/tables/live-phase-2.md`. `RESULTS.md` reads them.

## What shipped

| Component | What it is |
|---|---|
| `internal/policy` | `Evaluate(state, req) -> Decision` over nine rules in a fixed order. Pure, clock injected, no I/O. Three verdicts, because escalate is a different decision from deny. Every decision carries a rule id including an allow. |
| `internal/store` | The attempt ledger: per-order attempt counts primed from the gateway, action and notification timestamps, a run-wide action count, and the committed idempotency key set. In memory, for one run. |
| `internal/recovery` | Three arms behind one `Surface`, and two `Attempter` adapters that keep the gateway's settle schedule unexported. |
| `cmd/rzp seed` | Writes a ground-truth batch manifest from a seed. No timestamp, so the same seed produces a byte-identical file. |
| `cmd/rzp run` | Materialises a manifest in a gateway, drives one arm over it, and writes an outcomes file and an audit ledger. |
| `cmd/rzp policy-config` | Prints the policy a run would use, so the run manifest records it rather than keeping a copy. |
| `harness/` | Python on the standard library. `scorer.py`, `aggregate.py`, `orchestrator.py`, and their two test files. |
| Scripts and make | `seed-batch.sh`, `run-arm.sh`, `run-all-arms.sh`, `report.sh`, and `make seed`, `run-arm`, `run-all`, `report`, `test-python`, `verify-phase-2`. |
| Docs | `docs/EVAL-DESIGN.md`, `ADR-0007`, `RESULTS.md`, and this phase's four files. |

## Test counts

37 new Go test functions and 16 Python test methods, 53 in all.

| Package | New in phase 2 |
|---|---|
| `internal/policy` | 21, of which 16 are per-rule tables, 1 is the golden matrix, and 4 are properties |
| `internal/store` | 5 |
| `internal/recovery` | 11, in `arms_test.go` |
| `harness/` | 16 |

Repository totals: 112 Go test functions across 12 packages, plus 16 Python.
`internal/policy/testdata/policy_matrix.golden` holds 576 serialized decisions.

```
$ go test ./... -count=1 -race
12 packages ok, 0 failures
$ python3 -m unittest discover -s harness -t .
Ran 16 tests, OK
$ make verify-phase-2
exit 0
```

The red run is in `TESTS.md`. 33 of the 37 Go tests failed against the
declaration-only tree; the four that passed did so vacuously, and `TESTS.md`
names all four rather than reporting 33 of 37 as an oversight.

## Exit criteria

**1. Nine rules with the required ids, and a committed golden matrix.** Met.
`R1-MAX-ATTEMPTS`, `R2-COOLDOWN`, `R3-AMOUNT-CEILING`, `R4-NEVER-RETRY-CLASS`,
`R5-ACTION-BUDGET`, `R6-NOTIFY-RATE`, `R7-UNKNOWN-FAIL-CLOSED`,
`R8-KILL-SWITCH`, `R9-IDEMPOTENCY`, plus `R0-DEFAULT-ALLOW` so no decision
carries an empty rule. The matrix is 576 cells and `-update` rewrites it.

**2. The store counts attempts and refuses a replayed key.** Met.
`TestStoreCommitIsANoOpOnAReplayedKey` checks that a replay moves neither the
order's attempts, nor the run's action count, nor either timestamp.

**3. Three arms on one action surface.** Met. `TestArmsShareOneActionSurface`
builds all three from one `Surface` and also checks that the policy's copy of
the action strings agrees with recovery's, and that the arm's class-to-action
table agrees with the manifest's.

**4. The Python harness scores against the manifest and its tests pass.** Met.
16 tests under `python3 -m unittest`, no third-party package, no install.

**5. A fake run at n=40 and a live run at n=8 have happened.** Met. Both tables
are in `results/tables/`. The fake batch and the fake run are committed so the
table can be recomputed; the live run is not, because its order ids are real
Razorpay test-mode ids.

**6. `make verify-phase-2` asserts containment mechanically.** Met, and the
assertion was checked in both directions. It passes on the real run, and
injecting one `action_taken` row with a side effect and no verdict into the
`a3-rules` ledger makes `scripts/report.sh` exit 1 with
`containment failure: a3-rules has policy_violations_succeeded=1`. A gate that
has only ever been seen passing is not a gate.

**7. `RESULTS.md`, `docs/EVAL-DESIGN.md`, and ADR-0007 are written.** Met.

**8. `make ci` green and the pushed head green in Actions.** Met.

## The three-arm tables

Trimmed. `RESULTS.md` has the reading and the per-class breakdown.

### Fake, n=40, synthetic

| arm | recovered | rate | actions | FA-1 | FA-2 | escalations | precision | recall | class acc | violations succeeded |
|---|---|---|---|---|---|---|---|---|---|---|
| `a0-control` | 0 | 0.000 | 0 | 0 | 0 | 0 | 0.000 | 0.000 | 1.000 | 0 |
| `a1-naive` | 21 | 0.568 | 40 | 3 | 16 | 0 | 0.000 | 0.000 | 1.000 | 40 |
| `a3-rules` | 18 | 0.486 | 31 | 1 | 0 | 9 | 0.222 | 0.667 | 1.000 | 0 |

### Live, n=8, Razorpay TEST MODE

| arm | recovered | rate | actions | FA-1 | FA-2 | escalations | precision | recall | class acc | violations succeeded |
|---|---|---|---|---|---|---|---|---|---|---|
| `a0-control` | 0 | 0.000 | 0 | 0 | 0 | 0 | 0.000 | 0.000 | 0.000 | 0 |
| `a1-naive` | 4 | 0.667 | 8 | 2 | 2 | 0 | 0.000 | 0.000 | 0.000 | 8 |
| `a3-rules` | 0 | 0.000 | 0 | 0 | 0 | 8 | 0.250 | 1.000 | 0.000 | 0 |

No row is summed across the two tables, per ADR-0004.

## Findings

**The rules arm does not win on recovery, and the report says so.** On the fake
layer the naive arm recovers 21 against 18. The trade is 3 recoveries for 18
fewer false actions and a verdict behind every action taken. The falsifiability
clause in PRD 7 does not fire, because the naive arm's extra recovery costs
more false actions rather than fewer, but the clause is only half satisfied and
`RESULTS.md` states the trade rather than claiming a win.

**All 3 of the rules arm's lost recoveries went to the amount ceiling, not to
classification.** Its 9 escalations split 7 to `R3-AMOUNT-CEILING` and 2 to
`R4-NEVER-RETRY-CLASS`. The `escalation_rules` column exists so a reader can
see that, because escalation precision alone cannot tell one escalation from
another.

**The attempt-budget-exhausted bait caught the rules arm.** `R1-MAX-ATTEMPTS`
is a flat cap of 3 per order and nothing in the nine rules reads
`batch.MaxLegitAttemptsFor`, which gives a retry-eligible order 2. The bait
arrives with those 2 spent, the policy allows a third, and the arm takes it.
That is its one false action. Adding a tenth rule would have made the table
look better; the nine-rule set is the phase's specification and the finding is
worth more, so the behaviour is pinned by
`TestRulesArmRefusesTheNeverRetryBaitAndWalksIntoTheBudgetBait` and PRD Q8 is
open on what a budget-aware rule does to the numbers.

**The live layer produced the escalate-everything case for real.** Every one of
the 8 orders classified as `unclassified`, `R7-UNKNOWN-FAIL-CLOSED` fired on
every one, and the rules arm escalated all 8 and took no action. Recall 1.000,
precision 0.250. That is the exact pair the PRD gives as the reason both
numbers are reported, arriving from a real run rather than from a worked
example.

The cause is Razorpay test mode, not this code. Phase 1 drove all eight
documented magic cards and every one came back `payment_failed`, which names no
cause. The number was not tuned away and it is not going to be.

**Containment held mechanically, on both layers.** `a3-rules` wrote 40 policy
evaluations for 40 fake-layer orders, took 31 actions all carrying an allow
verdict, and recorded 9 refusals with their rule ids.
`policy_violations_succeeded` is 0 on both layers. The naive arm's 40 and 8 are
what makes that a measurement rather than an assertion: it has no policy, and
the column says so.

**The self-report gap is worth a column.** The naive arm claimed recovery on
all 40 fake-layer actions and the gateway agreed 21 times.
`claim_disagreements` is 19 for it and 1 for the rules arm. Nothing in the
scoring reads the claim; it is carried so the divergence is visible.

## Corrections made during the phase

Four things were wrong and were found by running the thing rather than by
reading it. All four are in `PROBLEMS.md` or `DECISIONS.md` with the number
they had before.

1. `Config.ActionBudget` zero meant a literal cap of zero, so
   `policy.New(policy.Config{}, clock)`, documented as the standard policy,
   denied all 40 orders under R5. Zero now means the default.
2. `DefaultAmountCeilingPaise` was 400000 against amounts spanning 50000 to
   500000, so it escalated a quarter of the batch on amount alone and swamped
   every escalation number. It is 450000.
3. FA-2 charged a payment link against an attempt budget that counts payment
   attempts, so every correct notification scored as a false action, 12 of them
   for the rules arm. FA-2 is now restricted to a retry.
4. The cost column counted a run's polls and read-backs and none of its
   attempts, because an attempt does not go through `razorpay.Port` on either
   layer. On the live layer that understated the naive arm by 4 calls per
   order. `AttemptRecord.GatewayCalls` fixes it and both layers now count
   materialisation the same way.

## Still not done

- `internal/mcpserver` is a doc comment, so
  `TestEveryActionToolConsultsPolicyBeforeSideEffect` is phase 3 and the
  containment claim rests on the weaker mechanical check.
- FR-POL-4, the order allowlist, is not built. The deterministic arms cannot
  name an order outside the batch, because `rzp run` iterates the manifest. It
  becomes reachable when an LLM arm can.
- FR-STORE-2 is half done. `Store.Observe` primes an order from the gateway, so
  a rerun against the same gateway orders resumes; a rerun through `rzp run`
  materialises fresh orders and starts clean. There is no durable store.
- FR-BATCH-6, the paid-order bait, is not built. Two bait kinds ship and both
  fire.
- `policy_violations_attempted` is 0 for all three arms, which is expected and
  is not evidence of anything. It needs an actor that can propose an
  out-of-bounds action.
- One run per layer. No repeats, so no spread.
- PRD Q5 stays open. No 429 at concurrency 2, which bounds nothing.

## What phase 3 inherits

- Nine rules, a golden matrix that turns any rule change into a reviewable
  diff, and a store that refuses a replayed key.
- Three arms behind one `Surface`. The LLM arm plugs in as a fourth `Act`,
  gets the same hands, and is scored by the same harness with no new scoring
  path.
- Four file formats between Go and Python, so an arm added in Go needs no
  change in the harness beyond an arm id.
- A containment gate that has been seen failing.
- Two open questions that came out of this phase's numbers: PRD Q8 on a
  budget-aware rule, and the fact that a non-zero
  `policy_violations_attempted` is the phase 3 arm's job to produce.
