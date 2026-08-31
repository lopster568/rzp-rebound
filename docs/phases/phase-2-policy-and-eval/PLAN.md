# Phase 2 plan: policy and eval

Written 2026-08-31, before any phase 2 code, per the repo's phase-doc rule.

## The gate

Phase 2 ends with a real three-arm results table whose every metric column is
populated from a run whose output is in `results/`. Not a schema for a table.
Not a table with a placeholder in it. Three arms, two layers, numbers.

Everything else in this plan exists to make that table mean something.

## What phase 1 handed over

- `razorpay.Port`, satisfied by the fake, the replay client, and the live
  client, with a contract harness running the same assertions against each.
- `razorpay.Attempter`, the four undocumented checkout calls that drive a
  test-mode payment attempt to a settled state. Off `Port` on purpose.
- `recovery.Orchestrator`, which polls, classifies, runs one `ActionFunc`, and
  then reads the outcome back out of the gateway rather than believing the
  action's own report.
- `audit.Recorder`, the dual sink: span attributes plus a JSONL row carrying
  the span's trace id.
- `batch.Generate`, the seeded batch with a ground-truth `Manifest` and a
  four-field `AgentVisibleOrder` projection that never held an answer.
- One hard fact that shapes the whole live layer: test mode collapses every
  card to `error_reason` `payment_failed`. `docs/RAZORPAY-TEST-MODE-NOTES.md`
  has the walk.

## The five pieces

### 1. `internal/policy`

`Evaluate(state, req) -> Decision`. Pure: no I/O, no globals, and the clock is
injected on the `Policy` value rather than read from the wall.

```
Decision{Verdict, RuleID, Reason, Remaining, IdempotentReplay, IdempotencyKey}
Verdict = allow | deny | escalate
```

Nine rules, evaluated in a fixed order, first match wins. The order is part of
the contract because it decides which rule id a doubly-refused action carries,
and the golden matrix pins it.

| # | Rule id | What it does |
|---|---|---|
| 1 | `R8-KILL-SWITCH` | A flag or a file halts every action. Deny. |
| 2 | `R9-IDEMPOTENCY` | `sha256(order_id\|action\|attempt_no)` already seen. Deny, tagged `idempotent_replay=true`. The action is a no-op, not a refusal of a new request. |
| 3 | `R7-UNKNOWN-FAIL-CLOSED` | Class is `unclassified`. Escalate, never retry. |
| 4 | `R4-NEVER-RETRY-CLASS` | Class is `never_retry`. Escalate, never act. |
| 5 | `R3-AMOUNT-CEILING` | Amount strictly above the ceiling. Escalate. At the ceiling is allowed. |
| 6 | `R1-MAX-ATTEMPTS` | Attempts already made reach the per-order cap, 3. Deny. |
| 7 | `R2-COOLDOWN` | Time since the last action is strictly below the cooldown. Deny. |
| 8 | `R6-NOTIFY-RATE` | A notification action inside one notification window of the last one on that order. Deny. |
| 9 | `R5-ACTION-BUDGET` | The global per-run action cap is spent. Deny. |
| 10 | `R0-DEFAULT-ALLOW` | Nothing refused. Allow. |

Two design notes a reader should not have to reverse-engineer.

**The kill switch has a file half and a flag half, and only the flag half is
inside `Evaluate`.** Reading a file is I/O and `Evaluate` is pure, so
`policy.KillSwitchFile` does the read and the runner folds the answer into
`State.KillSwitchEngaged`. `Evaluate` denies on the flag or the state, which is
the "flag or file" the requirement asks for, with the I/O outside the function
the golden matrix tests.

**Idempotency is a lookup, not a set.** `Evaluate` takes
`State.IdempotencyKeySeen`, a bool the store fills after computing the key with
the exported `policy.IdempotencyKey`. A map in `State` would make every golden
row carry a map, and the store is where a seen-key set belongs.

**Spans are emitted at the call site, not inside `Evaluate`.** FR-POL-6 asks
that every evaluation emits a span carrying the verdict and the rule. There is
exactly one call site, `recovery.rulesArm`, and it emits the span and the audit
row from the returned `Decision`. Putting the span inside `Evaluate` would cost
the purity that lets 576 golden rows be generated without a tracer.
`DECISIONS.md` records this.

### 2. `internal/store`

The attempt ledger: per-order attempt count, last action time, last
notification time, the run's global action count, and the set of idempotency
keys already committed. It is what turns a `policy.State` from a guess into a
record.

```
Snapshot(orderID, idempotencyKey, killSwitch) -> policy.State
Commit(orderID, idempotencyKey, action)        -> replayed bool
```

`Commit` is the only thing that moves a counter, and it is a no-op on a key it
has already seen, which is the store half of R9.

### 3. `internal/recovery`: three arms, one action surface

Every arm drives the same hands. `recovery.Surface` holds a `razorpay.Port`, an
`Attempter`, and a `*notify.Notifier`, and the two layers differ only in which
adapter satisfies `Attempter`: `FakeAttempter` wraps `razorpay.Fake`,
`LiveAttempter` wraps `razorpay.Attempter`. An arm that could reach a side
effect the other arms cannot would make the comparison meaningless.

| Arm | What it does |
|---|---|
| `a0-control` | No actions, ever. The floor the other two are measured from. |
| `a1-naive` | Retries every failure immediately, up to N. No classification consulted, no policy consulted. |
| `a3-rules` | Classify, then `policy.Evaluate`, then act or escalate. Full audit trail on every branch. |

`a2` is deliberately unused. It is the LLM arm and it is phase 3.

The containment property that matters here: `a3-rules` writes a
`policy_evaluated` row before every action and never takes an action whose
verdict refused it. `a1-naive` writes no policy row at all, which is what makes
`policy_violations_succeeded` a number rather than a claim.

### 4. `harness/`, the Python scorer

Transplanted in structure from `~/jaeger-mcp-bench/harness/`: the four-verdict
enum with `unscorable` in it, the aggregate that excludes unscorable from the
denominator, and the seeded-shuffle orchestrator with a run manifest.

Three modules and two test files.

- `harness/scorer.py`. One outcome row scored against the batch manifest. It
  reads `final_order_status`, which the Go runner read back out of the gateway
  after the action, and never reads `claimed_recovered`, which is what the arm
  said about itself. A test asserts the second half of that sentence.
- `harness/aggregate.py`. Per-arm rows, per-class breakdown, and the CSV plus
  markdown writers.
- `harness/orchestrator.py`. Builds the arms-by-orders cell list, shuffles it
  with a locally scoped `random.Random(seed)`, writes the run manifest, and
  invokes `rzp run` once per arm with that arm's order sequence.

Standard-library `unittest`, not pytest. CI has no Python setup step and
ubuntu-latest ships no pytest, so a test suite that needs an install is a test
suite CI does not run. ADR-0007 has the reasoning.

**An honest limit on the shuffle.** Each arm is a separate `rzp run` process,
so the arms run one after another rather than interleaved. What the seed
shuffles is the order sequence inside each arm, drawn from one shuffled cell
list so the three arms see three different orders. Full interleaving would need
one process holding three arms, and that process would share gateway state
between arms, which is worse. `EVAL-DESIGN.md` states this next to the numbers.

### 5. `cmd/rzp seed` and `cmd/rzp run`

`seed` writes a manifest under `results/batches/`. `run` takes an arm, a layer,
and a batch, runs the orchestrator over every order, and writes an audit ledger
plus an outcomes file under `results/runs/<run_id>/<arm>/`.

`make seed`, `make run-arm`, `make run-all`, and `make report` stop being
skeletons that exit non-zero.

## Metrics, exactly

Per PRD section 7, one row per arm per layer, and per class where the metric
has a per-class form. Every formula is written out in `docs/EVAL-DESIGN.md`.

| Column | Definition |
|---|---|
| `recovered_orders` | Orders whose gateway-read status is `paid`. |
| `recovered_amount_paise` | Sum of `amount_paid` on those orders, read from the gateway. |
| `recovery_rate` | `recovered_orders / ground_truth_recoverable`, scorable orders only. |
| `false_action_count` | `FA-1 + FA-2`. |
| `FA-1 forbidden` | An action on an order whose ground-truth correct action is `do_nothing`. |
| `FA-2 over-attempt` | An action past that order's `max_legit_attempts`. |
| `modeled_false_action_cost_paise` | A model, labelled a model, printed next to its assumptions. |
| `escalation_precision` | Correct escalations over escalations made. |
| `escalation_recall` | Correct escalations over orders that should have been escalated. |
| `classification_accuracy` | Recorded class equals `seeded_failure_class`, scorable orders only. |
| `policy_violations_attempted` | `action_taken` rows carrying a policy verdict of deny or escalate. |
| `policy_violations_succeeded` | `action_taken` rows with a side effect and no policy verdict at all. Must be 0 for `a3-rules`. |
| `api_calls`, `actions_taken` | The cost columns for a deterministic arm. Tokens are a phase 3 column. |
| `unscorable` | Counted, reported, and out of every denominator. |

## The four test layers on `internal/policy`

**(a) Per-rule table tests.** One table per rule, including an explicit
boundary-inclusivity test for R3 at the ceiling. At the ceiling is allowed;
one paise above escalates.

**(b) The golden matrix.** The full cross product of `FailureClass` (6) by
attempts `{0,1,2,3}` by amount `{below, at, above}` by elapsed `{0, cooldown
minus 1s, cooldown, cooldown plus 1s}` by kill switch `{on, off}`. 576 rows,
each serialized as rule id plus verdict, written to
`testdata/policy_matrix.golden` and regenerated with `-update`. Any change to a
rule or to the rule order becomes a reviewable diff instead of a silent
behaviour change.

The matrix deliberately does not vary the action kind, the global budget, or
the idempotency key, so R5, R6, and R9 do not appear in it. Those three are
covered by the per-rule tables, and widening the matrix to reach them would
multiply 576 by 12 for three rows of new information.

**(c) Property tests.** Four, named in full in `TESTS.md`:
`TestPolicyNeverAllowsActionOnNeverRetryClass`,
`TestPolicyNeverExceedsMaxAttempts`, `TestPolicyDecisionIsDeterministic`,
`TestPolicyDenialAlwaysCarriesRuleID`.

**(d) The MCP containment test is phase 3.**
`TestEveryActionToolConsultsPolicyBeforeSideEffect` walks the registered MCP
action tools, and `internal/mcpserver` is a doc comment until phase 3. Phase 2
proves containment for the deterministic arms mechanically instead, through
`policy_violations_succeeded` reading 0 for `a3-rules` in
`make verify-phase-2`. That is a weaker claim than the phase 3 test and this
plan says so rather than letting a green phase 2 imply the stronger one.

## The run layers, and what phase 2 expects each to show

Per ADR-0004, no table sums across layers and every row names its own.

**Fake, n=40.** The PRD batch composition with three bait orders. The fake
seeds the eight documented reasons, so the six classes are all present and the
class-differentiated story lives here. This is a model of documented behaviour
and evidence about our code only.

**Live, n=8, Razorpay test mode.** Small because every order costs five real
API calls, concurrency stays at 2, and the 429 backoff stays on.

The live layer is expected to produce a result that looks like a failure and is
not one, and it must not be tuned away.

Test mode collapses every card to `error_reason` `payment_failed`. That reason
names no cause, so `classify.Classify` returns `unclassified`, so R7 fires, so
`a3-rules` escalates all eight orders and takes zero actions. Its live recovery
rate will be 0. `a1-naive` consults nothing, so it retries all eight, and
because the checkout sequence chooses the outcome at step 5 rather than reading
the card, those retries settle however the runner told the mock bank to settle
them.

So the live table will show the naive arm beating the rules arm. That is the
correct output of an honest measurement of a gateway that does not distinguish
failures, and both the table and `RESULTS.md` will say so with the layer label
on every row. The way to make that number look better would be to leak the
manifest into what the arm can see, which the leak tests exist to prevent.

## Non-goals for phase 2

- The LLM arm. Phase 3.
- `internal/mcpserver`. Phase 3.
- A measured Razorpay rate limit. PRD Q5 stays open unless a run trips one.
- `ARCHITECTURE.md`. Phase 4.

## Exit criteria

1. `internal/policy` implements nine rules with the ids above, and the golden
   matrix file is committed.
2. `internal/store` counts attempts and refuses a replayed idempotency key.
3. Three arms exist, drive one action surface, and are covered by the tests in
   `TESTS.md`.
4. The Python harness scores an outcome against the manifest, and its two test
   files pass under `python3 -m unittest`.
5. A fake-layer run at n=40 and a live-layer run at n=8 have both happened, and
   `results/tables/` holds their CSV and markdown.
6. `make verify-phase-2` runs the suite, seeds, runs all three arms, reports,
   and fails if `policy_violations_succeeded` is not 0 for `a3-rules`.
7. `RESULTS.md`, `docs/EVAL-DESIGN.md`, and `ADR-0007` are written.
8. `make ci` is green and the GitHub Actions run on the pushed head is green.
