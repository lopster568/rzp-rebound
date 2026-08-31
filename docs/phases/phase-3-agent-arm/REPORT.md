# Phase 3 report

Written 2026-09-01, at the end of phase 3. Every number here comes from a run
whose output is under `results/`.

## Scope

Phase 3's gate was an LLM arm that drives the recovery loop through MCP tools
and nothing else, gated server side by the same policy, and scored by the same
harness over the same batches. The output is a four-arm table per layer:
`results/tables/phase-3-fake.md` and `results/tables/phase-3-live.md`.
`RESULTS.md` reads them.

## What shipped

| Component | What it is |
|---|---|
| `internal/mcpserver` | Seven tools, receiving middleware in front of all of them, and one path every action tool takes with `policy.Evaluate` as its first statement. |
| `cmd/rzp-mcp` | One process per order: materialise, serve MCP on stdio, read the order back, append an outcome row. |
| `internal/runner` | The batch file, the gateway rig, the materialiser, and the outcome row, extracted from `cmd/rzp` so both binaries build a batch order through the same code. |
| `harness/claude_runner.py` | One headless invocation: the argv, the mcp config, the envelope, the infra retry, the unscorable classification. |
| `harness/agent_runner.py` | Drives `a2-agent` over a batch, one invocation per order, writes `invocations.jsonl`. |
| `harness/arm_config.py` | The settings for all four arms in one place, so "identical except the decision maker" is checked. |
| `prompts/agent_system.md` | The charter. Its sha256 fills the run manifest field phase 2 left as a placeholder. |
| Scripts and make | `agent-smoke.sh`, `trace-links.sh`, the agent flags on `run-all-arms.sh`, and `make verify-phase-3`, `agent-smoke`, `trace-links`. |
| Docs | `PLAN.md`, `TESTS.md`, `PROBLEMS.md`, `DECISIONS.md`, this file, and the phase 3 half of `RESULTS.md`, `docs/EVAL-DESIGN.md`, `docs/PRD.md`, and `harness/README.md`. |

## Test counts

24 new Go test functions and 21 new Python test methods, 45 in all. `TESTS.md`
named 20 and 12 before any of them existed, and records each addition with the
defect that produced it.

| Package | New in phase 3 |
|---|---|
| `internal/mcpserver` | 18, of which 5 are the containment layer ADR-0003 names |
| `cmd/rzp-mcp` | 5, all driving the compiled binary or its helpers |
| `internal/telemetry` | 1, from a phase 0 requirement this phase found was false |
| `harness/` | 21, across `test_claude_runner.py`, `test_arm_config.py`, `test_agent_runner.py`, and two added to `test_aggregate.py` |

The red run is in `TESTS.md`. All 20 Go tests that existed at the red commit
failed, none vacuously, because every one of them builds a server first and
`mcpserver.New` returned an error. Of the 18 Python methods at that commit, 17
errored and one passed vacuously, and `TESTS.md` names which one and why.

## Exit criteria

**1. Seven tools, exactly the names in `PLAN.md`, served by a compiled binary
over stdio.** Met. `TestServerServesExactlyTheSevenNamedTools` and
`TestCompiledServerListsItsToolsOverStdio`.

**2. `TestEveryActionToolConsultsPolicyBeforeSideEffect` passes and an ungated
tool turns it red.** Met. It lists the tools through the server's own registry
over a live session, so the set it walks is exactly the set the model sees, and
calls every one against spies that fail the test on a mutating call. A tool
with no argument builder fails it too, so a new ungated tool turns the suite red
two ways.

**3. A subprocess test drives the real binary end to end.** Met. `TestMain`
builds `./cmd/rzp-mcp` and five tests drive it over `mcp.CommandTransport`.

**4. `a2-agent` rows in both tables, with cost columns.** Met.

**5. `policy_violations_succeeded` is 0 for `a2-agent`.** Met on both layers,
and `harness/aggregate.py`'s `CONTAINED_ARMS` now gates both arms rather than
one, so a leaked action tool fails the build.

**6. Two Jaeger trace URLs.** Met. Trace ids are in `RESULTS.md`, read out of
the run's own ledger by `make trace-links`.

**7. `RESULTS.md` carries the four-arm tables and places `a2-agent` in the
trade.** Met, and the placement is that on this batch it lands exactly on top
of `a3-rules`.

**8. `make ci` green and the pushed head green in Actions.** Met.

## The four-arm tables

Trimmed. `RESULTS.md` has the reading and the per-class breakdown.

### Fake, n=40, synthetic

| arm | recovered | rate | actions | FA-1 | FA-2 | escalations | precision | recall | evaluations | refusals | violations succeeded |
|---|---|---|---|---|---|---|---|---|---|---|---|
| `a0-control` | 0 | 0.000 | 0 | 0 | 0 | 0 | n/a | 0.000 | 0 | 0 | 0 |
| `a1-naive` | 21 | 0.568 | 40 | 3 | 16 | 0 | n/a | 0.000 | 0 | 0 | 40 |
| `a2-agent` | 18 | 0.486 | 31 | 1 | 0 | 9 | 0.222 | 0.667 | 59 | 16 | 0 |
| `a3-rules` | 18 | 0.486 | 31 | 1 | 0 | 9 | 0.222 | 0.667 | 40 | 9 | 0 |

### Live, n=8, Razorpay TEST MODE

| arm | scorable | recovered | rate | actions | FA-1 | FA-2 | escalations | precision | recall | evaluations | refusals | violations succeeded |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| `a0-control` | 8 | 0 | 0.000 | 0 | 0 | 0 | 0 | n/a | 0.000 | 0 | 0 | 0 |
| `a1-naive` | 8 | 4 | 0.667 | 8 | 2 | 2 | 0 | n/a | 0.000 | 0 | 0 | 8 |
| `a2-agent` | 7 | 0 | 0.000 | 0 | 0 | 0 | 7 | 0.286 | 1.000 | 8 | 8 | 0 |
| `a3-rules` | 8 | 0 | 0.000 | 0 | 0 | 0 | 8 | 0.250 | 1.000 | 8 | 8 | 0 |

No row is summed across the two tables, per ADR-0004.

### Cost of the arm that has one

| layer | invocations | failed | infra retries | input tokens | output tokens | usd reported | wall clock |
|---|---|---|---|---|---|---|---|
| fake | 40 | 0 | 0 | 542 | 56621 | 3.94 | 835s |
| live | 8 | 0 | 0 | 98 | 11696 | 0.66 | 172s |

`failed` counts invocations that did not complete, which is zero on both
layers. It is not the same as the one unscorable outcome row on the live layer,
which came from a completed invocation whose server was killed before the
read-back.

## Findings

**The agent and the rules arm agreed on all 40 fake-layer orders.** Same
recoveries, same actions, same false action on the same bait order, same nine
escalations splitting the same way. Given the same classification, the same
policy, and the same tools, the model reached the decision the rule set reaches,
forty times out of forty, at 3.94 usd and 14 minutes against under a second.

That is the phase's headline and it is not a flattering one for the agent.
`RESULTS.md` says it in those words rather than looking for a column where the
agent wins.

**What differed is what the two arms asked for.** `a3-rules` made 40 policy
evaluations, one per order, because it proposes exactly what its class table
dictates. `a2-agent` made 59 and had 16 refused against nine. Seven orders were
decided twice: proposed, refused, re-decided. That is the number ADR-0003 is
reaching for when it says an agent that never proposes anything out of bounds
has not been tested against a policy, and none of the 16 reached the gateway.

**The agent escalated all eight live orders without proposing a retry on any of
them.** Test mode collapses every card to `payment_failed`, which names no
cause, so `classify.Classify` returns `unclassified` and
`R7-UNKNOWN-FAIL-CLOSED` fires. A model asked to recover revenue and shown
eight failed payments it could have retried chose not to, eight times, on a
gateway that gave it nothing to reason from. One batch, one gateway, not a
general claim.

**Containment held on both layers for both gated arms, and the metric that
gates the build could not have seen the failure mode that mattered most.**
Eight parallel `retry_payment` calls on an order with one permitted attempt
left put eight payments on it, every one carrying an allow verdict, so
`policy_violations_succeeded` would have read 0 and the column would have
called the run clean. `PROBLEMS.md` 9 has it. The fix is one mutex held across
the whole action path; the test that pins it goes red on every run against the
unlocked code.

**FR-POL-4 stopped being unreachable.** Phase 2 recorded the order allowlist as
not met because a deterministic arm iterates the manifest and cannot name
anything else. A model can name any string, so the rule became reachable and
was built as `M2-ORDER-ALLOWLIST` in layer 1.

**Six of the nine policy rules still never fire in a run, and none of the three
middleware rules did.** The fake run's evaluations carry `R0-DEFAULT-ALLOW`,
`R3-AMOUNT-CEILING`, and `R4-NEVER-RETRY-CLASS`; the live run's carry
`R7-UNKNOWN-FAIL-CLOSED`. The agent tripped no allowlist and no decision gate,
because it followed the procedure every time. An agent that never names an
order it was not given is a good result and it is not evidence that the
allowlist works, which is what the tests are for.

## Corrections made during the phase

Twelve things were wrong. `PROBLEMS.md` has all of them with how each was
found. Four are worth pulling forward, because all four were invisible in a
green suite.

1. **Parallel tool calls could put eight payments on an order the cap allowed
   one.** Every action carried an allow verdict, so the containment column
   would have read 0. Found by reading the code.
2. **The first live agent arm came back entirely unscorable**, because the
   CLI's exit cancelled the context the gateway read-back was on. Nothing
   showed on the fake layer, because `razorpay.Fake` ignores the context it is
   handed: a whole layer of a bug hidden by a double more permissive than the
   thing it doubles.
3. **Two invocations of one batch gave their first order the same gateway id**,
   which would have made every per-class ledger count carry every other class's
   rows. Found by running the two-order smoke and reading the ledger.
4. **Every audit row from the agent arm had an empty trace id**, because the
   CLI strips `OTEL_*` from the server's environment. FR-AUD-3 silently not
   met, with nothing failing.
5. **`OTEL_SERVICE_NAME` beat the configured service name**, so FR-TEL-2 had
   been false since phase 0. Nothing caught it because nothing here had ever
   set the variable, and the first thing that did was this phase's own gate.
   The failure looked like the harness breaking a phase 0 test rather than a
   test finding a bug.

## Budget

62 headless invocations for the night against a cap of about 60, and the two
over are disclosed rather than rounded away.

| What | Invocations |
|---|---|
| Two-order smoke, first end-to-end run | 2 |
| Trace capture check | 1 |
| Environment probe, which found correction 4 | 1 |
| Fake layer, n=40 | 40 |
| Live layer, first attempt, lost to correction 2 | 8 |
| Live layer, re-run on the fixed binary | 8 |
| `make verify-phase-3`, run twice, capped at 1 each | 2 |

The eight that correction 2 cost are why the number is over. They are counted
here rather than written off as a failed attempt, because they were spent. The
gate ran twice because its first attempt failed before reaching the agent, on
the telemetry bug in correction 5 below, and spent nothing.

No batch size was reduced. The preferred plan in `PLAN.md`, fake n=40 plus live
n=8, ran in full, and the stratified fallback was not needed.

## Still not done

- **One run per layer, and this arm is not deterministic.** The other three
  reproduce from a seed and `a2-agent` does not. It was sampled once per order
  with no repeats, so there is no spread, and a second run could land somewhere
  else. This is the limitation the PRD risk table named for this phase and it
  is not fixed.
- **The agent never tripped the three middleware rules in a run.** They are
  proven by unit tests, not by these tables.
- **One live outcome row is unscorable.** The invocation completed and the
  agent escalated; the server was killed before its read-back finished. Counted
  and explained, not scored.
- **FR-BATCH-6, the paid-order bait, is still not built.** Two bait kinds ship
  and both fire.
- **FR-STORE-2 is still half done.** No durable store.
- **PRD Q8 is still open.** A budget-aware rule was deliberately not added in
  the same phase as the agent, because two changes at once confound each other.
  The attempt-budget-exhausted bait caught both gated arms again, identically.
- **PRD Q5 stays open.** No 429 at concurrency 2 across either phase.
- **The action budget can overshoot under concurrency.** The middleware checks
  the budget before the handler spends it, so two calls admitted at once can
  both spend. `act`'s lock stops the attempt cap being raced; it does not make
  the invocation budget exact. The bound that matters, R1, is exact.
- **`ActionOutput.Action` means two things.** On a middleware refusal it is the
  tool name and on a handler refusal it is the policy action. Cosmetic, in a
  field the model reads and no metric does, and it was left alone rather than
  changed between the two layers' runs.

## What phase 4 inherits

- Four arms behind one action surface, and a scoring harness that needed no new
  path for the fourth.
- A containment claim with an actor that can push on it, and a gate that has
  been seen failing in both directions.
- Two Jaeger traces that show the whole thing: one refusal with its rule id and
  one recovery, each one invocation, each one trace.
- A cost model for the agent arm in tokens, usd, and wall clock, so the next
  question, whether an agent is worth what it costs here, has numbers on both
  sides of it.
