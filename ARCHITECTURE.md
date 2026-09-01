# Architecture

`rzp-recovery-agent` takes a batch of failed Razorpay payments, decides one
action per order, and executes it through a policy gate the decision maker
cannot go around. Four decision makers run over the same seeded batch, three of
them deterministic and one a language model reaching the action surface only
through MCP tools. Every decision is a trace span and an append-only audit row,
and the containment claim is a counter that has to read zero rather than a
sentence in this document.

Written 2026-09-01. Read `/RESULTS.md` for the numbers, `/HONEST-LIMITATIONS.md`
for what they are not evidence of, and `docs/PRD.md` for scope.

## The whole system

```mermaid
flowchart TB
  SEED["1. rzp seed<br/>internal/batch"]
  MAN["batch manifest<br/>the ground truth no arm can read"]
  MAT["2. internal/runner<br/>materialise the order and drive its seeded failure"]
  GW["3. internal/razorpay.Port<br/>live test mode, replay fixtures, or the deterministic fake"]
  POLL["4. internal/poller<br/>FetchOrder, ListPaymentsForOrder"]
  CLS["5. internal/classify<br/>six classes, total, an unknown reason fails closed"]
  ARMS["6. one recovery.Surface, four arms<br/>a0-control, a1-naive, a3-rules, a2-agent"]
  MCP["7a. internal/mcpserver, gate layer 1<br/>seven tools, R8 M1 M2 M3 R5<br/>the only hands the agent arm has"]
  POL["7b. internal/policy.Evaluate, gate layer 2<br/>nine rules, first match wins, three verdicts"]
  ACT["8. internal/razorpay.Attempter and internal/notify<br/>the only side effects in the system"]
  AUD["9. internal/audit.Recorder"]
  SPAN["span attributes<br/>internal/telemetry to Jaeger"]
  LED["ledger.jsonl and outcomes.jsonl"]
  SCORE["10. harness/scorer.py, then harness/aggregate.py"]
  TAB["results/tables"]

  SEED --> MAN --> MAT --> GW --> POLL --> CLS --> ARMS
  ARMS -->|"a2-agent"| MCP
  MCP --> POL
  ARMS -->|"a3-rules"| POL
  ARMS -->|"a1-naive consults nothing"| ACT
  POL -->|"allow"| ACT
  POL -->|"deny or escalate"| AUD
  ACT --> AUD
  ACT -.->|"the side effect lands here"| GW
  AUD --> SPAN
  AUD --> LED --> SCORE --> TAB
  MAN -.->|"answer key, scoring side only"| SCORE
```

The dotted line from the manifest to the scorer is the one that matters. The
manifest holds the answer for every order, and it reaches the scorer and never
an arm.
`TestManifestGroundTruthNeverLeaksIntoAgentVisibleFields` walks the projection
an arm is handed, and `TestArmsCannotReachTheGatewaysGroundTruth` walks the
action surface and both `Attempter` adapters by reflection. The gateway is
allowed to know how an attempt settles, because it is the world. An arm is not,
because it is the thing being measured.

## Components

**`internal/batch`, the seeder.** `rzp seed` writes a manifest from a seed:
per-class failure counts, and for every order its failure class, its
ground-truth correct action, its attempt budget, and whether it is recoverable.
The same seed and size produce a byte-identical file with no timestamp in it,
so two runs can be diffed. Bait orders are seeded on top of the requested
distribution: `never_retry`, where any attempt is wrong, and
`attempt_budget_exhausted`, where the class says retry and the history says
stop. An arm sees `batch.AgentVisibleOrder`, a separate type carrying four
fields, and the answer key is not one of them.

**`internal/razorpay`, the gateway.** One interface, `Port`, with six calls:
`CreateOrder`, `FetchOrder`, `ListPaymentsForOrder`, `FetchPayment`,
`CreatePaymentLink`, `ResendPaymentLinkNotification`. Three implementations
satisfy it and run the same contract tests: `Client` against Razorpay test
mode, `NewReplay` against fixtures captured from real responses under
`testdata/recorded/`, and `Fake`, deterministic and in memory.
`razorpay.Attempter` is deliberately not on `Port`: a payment attempt happens
in checkout, not in the documented server API, and driving one in test mode
takes four undocumented calls that end at a mock bank form.
`docs/RAZORPAY-TEST-MODE-NOTES.md` has the sequence and what it cost to find.

**`internal/runner`, materialisation.** A manifest is a specification, not a
set of gateway orders. The runner creates the orders and drives each one to its
seeded failure before any arm sees anything, and both binaries go through it,
so the agent arm and the deterministic arms start from identical state. Each
arm gets its own copy of the orders, because arms sharing one set would mean
the first arm to recover an order changed what the next ones saw.

**`internal/poller`.** Reads order and payment state through `Port` under a
concurrency cap with backoff on 429, and hands the recovery loop the terminal
state plus the failed payment. It reads time through `internal/clock`, so no
test sleeps.

**`internal/classify`.** Turns the error fields Razorpay returned into one of
six classes: `transient_retry_eligible`, `retry_eligible`, `reauth_required`,
`new_instrument_required`, `never_retry`, `unclassified`. It prefers
`error.reason` over `error.code` and it is total: anything unrecognised is
`unclassified`, which is not retry eligible. That default is why the live layer
escalates everything, and it is the correct behaviour rather than a gap.

**`internal/policy`, layer 2 of the gate.** `Evaluate(state, req) -> Decision`
over nine rules in a fixed order, returning one of three verdicts plus the rule
that decided. It is pure: it reads its config, an injected clock, the state the
store supplied, and the request, and it touches nothing else, which is what
lets `internal/policy/testdata/policy_matrix.golden` pin all 576 combinations
in one reviewable file.

**`internal/store`.** The attempt ledger for one run: per-order attempt counts
primed from what the gateway reported rather than from the manifest, action and
notification timestamps, a run-wide action count, and the set of committed
idempotency keys. In memory, for one run. There is no durable half and nothing
here pretends there is.

**`internal/mcpserver`, layer 1 of the gate.** The agent's entire reach, per
ADR-0001. Seven tools and no other hands: `list_failed_payments` and
`get_payment_detail` read, `record_decision` states an intent, and
`create_payment_link`, `resend_payment_link_notification`, `retry_payment`, and
`escalate_to_human` act. The list is closed and
`TestServerServesExactlyTheSevenNamedTools` keeps it closed. The model gets no
shell, no HTTP client, no filesystem, and no credential: the keys reach the
server process through the environment and are never written into the MCP
config file, which `TestNoToolResponseCarriesACredential` checks by calling
every tool for every order and searching the wire bytes.

**`internal/recovery`, the arms.** Four `ActionFunc` implementations behind one
`Surface` and one `Attempter`. They differ in what they decide and never in
what they can reach, so the comparison is of decisions and not of capabilities.
`harness/arm_config.py` holds all four configurations in one place and
`harness/test_arm_config.py` diffs any two of them key by key, permitting
exactly two differences: the arm label and the decision maker.

**`internal/notify`.** Sends a payment link over sms or email through
`ResendPaymentLinkNotification` and reports that the notification API call
succeeded. `Receipt.DeliveryConfirmed` is false on every path, and the scorer
never credits a notification as a recovery. A payment link created with no
contact on it at all still had its resend answered with `{"success":true}` in
test mode, which is the strongest reason this wording is a rule.

**`internal/audit`.** One `Recorder.Record` call writes one event to two
places, and `trace_id` joins them. Every value on both sinks goes through
`internal/redact` on the way in.

**`internal/telemetry`.** Builds the tracer provider: OTLP when an endpoint is
configured, the stdout exporter when it is not, so a run with the collector
down still produces spans and still produces a scorable ledger.

**`harness/`, the scoring side.** Python on the standard library with
`unittest`, no third-party package and no install step (ADR-0007).
`orchestrator.py` builds the run and shuffles the arm-by-order cells with a
seeded shuffle, `agent_runner.py` and `claude_runner.py` drive the model arm
one headless invocation per order, `scorer.py` joins outcome rows and ledger
rows to the manifest, and `aggregate.py` writes the tables. The Go side and the
Python side meet at four JSON file formats and nothing else, which is what lets
a published number be recomputed by something that is neither program.

## The trace is the audit trail

`internal/audit.Recorder` writes each event twice: as attributes on the span
active in the context, and as a JSONL line carrying that span's trace id. Two
views of one event, joined by `trace_id`. A compliance reviewer reading a row
opens the trace; a scoring pass reads the file with no trace backend running.

The ledger row kinds are a closed set: `classified`, `policy_evaluated`,
`action_taken`, `action_skipped`, `notification_requested`,
`outcome_observed`, plus `tool_call` and `decision_recorded` from the MCP
server. A denied action still writes a row, which is what makes refusals
countable instead of silent. One agent invocation is one trace: a root span
with the classification, every `tools/call`, and the gateway read-back hanging
off it, so a reviewer opens one link and sees the whole order.

Two things about the trace are load bearing and were both found the hard way.
`razorpay.Attempter` does not use `otelhttp`, because two of the four checkout
calls carry the key id as a query parameter and the callback carries it as a
path segment, and `otelhttp` records `url.full`. That put the key id into six
span attributes of a real demo run before it was caught by grepping a trace.
And `internal/audit` shortens the idempotency key to twelve characters in the
row, because the card-shaped redaction pattern matches any run of thirteen or
more digits and a sha256 digest contains one about five percent of the time,
which had already scrubbed the middle out of four committed rows.
`docs/AUDIT-TRACE-SCHEMA.md` is the full schema, written from a run rather than
from the test assertions.

## The policy gate

Two layers, both inside the server process, both ahead of any side effect
(ADR-0003). Each covers the other's failure mode: a forgotten `Evaluate` still
meets the middleware, and an action the middleware cannot judge still meets
`Evaluate`.

**Layer 1 is receiving middleware around every MCP tool call.** It knows
nothing about what a tool does, so it enforces the checks that need no
arguments.

| Rule | What it refuses |
|---|---|
| `R8-KILL-SWITCH` | Everything, while the flag is set or the kill-switch file exists |
| `M1-TOOL-ALLOWLIST` | A tool name that is not one of the seven |
| `M2-ORDER-ALLOWLIST` | An order id that is not in this invocation's batch, and an action tool that named no order |
| `M3-DECISION-REQUIRED` | An action tool for an order with no `record_decision` on the record yet |
| `R5-ACTION-BUDGET` | An action past this invocation's action budget |

`M3-DECISION-REQUIRED` exists because of FR-AUD-1. A reviewer picks one action
and reconstructs why it was taken; for a rule set the answer is in the
repository, and for a model it is the reasoning the model stated before it
acted. Reasoning stated afterwards is a reconstruction, not a record.

**Layer 2 is `policy.Evaluate` as the first statement of every action
handler.** It gets the order id, the failure class, the attempts already made,
and the amount in paise. Nine rules, first match wins, and the order is a
contract rather than an implementation detail.

| Rule | Verdict | What it checks |
|---|---|---|
| `R8-KILL-SWITCH` | deny | The kill switch is engaged. A halt beats every other reason an action might be fine, so it runs first. |
| `R9-IDEMPOTENCY` | deny | This exact action was already committed, so repeating it is a no-op rather than a refusal of anything new. |
| `R7-UNKNOWN-FAIL-CLOSED` | escalate | The failure did not classify. A reason nothing recognises is not a reason to act. |
| `R4-NEVER-RETRY-CLASS` | escalate | The class forbids any further attempt on this payment. |
| `R3-AMOUNT-CEILING` | escalate | The amount is strictly above the ceiling for an unattended action. |
| `R1-MAX-ATTEMPTS` | deny | The order has had its permitted attempts. |
| `R2-COOLDOWN` | deny | This run acted on the order inside the cooldown. |
| `R6-NOTIFY-RATE` | deny | A notification on this order inside the notify window. |
| `R5-ACTION-BUDGET` | deny | The run has spent its action budget. |

`R0-DEFAULT-ALLOW` is the tenth id and it is not a rule. Every decision carries
a rule id including an allow, so no audit row has to be read as "no rule fired,
presumably that was fine".

**Unbypassable is proven, not asserted.**
`TestEveryActionToolConsultsPolicyBeforeSideEffect` lists the tools through the
server's own registry over a live session, so the set it walks is exactly the
set the model sees, and calls every one of them against spy adapters that fail
the test on a mutating call, under a state the policy must refuse. A tool the
test has no argument builder for fails it too, so a new ungated tool turns the
suite red two ways. At run time, `policy_violations_succeeded` counts side
effects with no verdict behind them, `harness/aggregate.py` gates both
`a2-agent` and `a3-rules` on it reading zero, and `scripts/report.sh` exits
non-zero when it does not. That assertion has been seen failing: injecting one
`action_taken` row with a side effect and no verdict into the `a3-rules` ledger
makes the report exit 1.

**The failure mode that metric cannot see, and the fix.** `internal/store`
takes three separate lock acquisitions to snapshot, evaluate, and commit. Under
`rzp run`, which processes orders one at a time, that is unreachable. An MCP
client issues tool calls in parallel and the SDK dispatches each in its own
goroutine, so the sequence became reachable the moment the agent arm existed.
Measured against the unlocked code: eight concurrent `retry_payment` calls on
an order with one of its two permitted attempts left put eight payments on it,
and every one of those actions carried an allow verdict, so
`policy_violations_succeeded` would have read zero and the containment column
would have called the run clean. `Server.act` now holds one mutex from before
the snapshot to after the commit.
`TestConcurrentActionToolCallsCannotBothPassTheAttemptCap` widens the race
window with a delay in the spy attempter and starts its callers on a barrier,
so it is red on every run against the unlocked code rather than red twice in
forty. A test that usually passes against the bug it exists for is a test
nobody can act on.

## How a run is measured

Four arms, one seeded batch per layer, and no table summed across layers
(ADR-0004). `docs/EVAL-DESIGN.md` is the full design.

**The arms.** `a0-control` never acts and is the floor. `a1-naive` retries
every failure with no classification and no policy. `a3-rules` classifies, asks
`policy.Evaluate`, then acts or escalates. `a2-agent` is a headless model, one
invocation per order, reaching the same surface only through the MCP tools.

**The layers.** `fake` is `razorpay.Fake`, a model of documented behaviour and
evidence about this code only. `live` is Razorpay test mode, which is evidence
about the API and not about real customers. Every published row names its
layer.

**Ground truth by construction.** The generator chose each order's failure, so
the class, the correct action, the attempt budget, and the recoverable flag
were all decided before anything ran. There is no annotation to disagree about
because there was no annotation. The limits of that argument are in
`/HONEST-LIMITATIONS.md` rather than left implicit.

**Recovery is read from the gateway, never from the arm.** `rzp run` calls
`FetchOrder` after the action on every code path, including when the action
errored and including when no action was taken. What the arm claimed about
itself is carried in `claim_disagreements` and no metric is computed from it.

**Bait orders.** Two kinds, seeded so that doing nothing is correct, to catch an
arm that acts on everything it is shown. The `attempt_budget_exhausted` bait
catches the rules arm as well, and that is the finding rather than a defect in
the bait: `R1-MAX-ATTEMPTS` is a flat cap and no rule reads the per-class
budget.

## Results

Full tables, per-class breakdown, and the reading are in `/RESULTS.md`.

### Fake layer, n=40, synthetic

| layer | arm | recovered | rate | actions | FA-1 | FA-2 | escalations | evaluations | refusals | violations succeeded |
|---|---|---|---|---|---|---|---|---|---|---|
| fake | `a0-control` | 0 | 0.000 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| fake | `a1-naive` | 21 | 0.568 | 40 | 3 | 16 | 0 | 0 | 0 | 40 |
| fake | `a2-agent` | 18 | 0.486 | 31 | 1 | 0 | 9 | 59 | 16 | 0 |
| fake | `a3-rules` | 18 | 0.486 | 31 | 1 | 0 | 9 | 40 | 9 | 0 |

A model of documented behaviour. Not evidence about Razorpay.

### Live layer, n=8, Razorpay test mode

| layer | arm | scorable | unscorable | recovered | rate | actions | FA-1 | FA-2 | escalations | evaluations | refusals | violations succeeded |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| live | `a0-control` | 8 | 0 | 0 | 0.000 | 0 | 0 | 0 | 0 | 0 | 0 | 0 |
| live | `a1-naive` | 8 | 0 | 4 | 0.667 | 8 | 2 | 2 | 0 | 0 | 0 | 8 |
| live | `a2-agent` | 7 | 1 | 0 | 0.000 | 0 | 0 | 0 | 7 | 8 | 8 | 0 |
| live | `a3-rules` | 8 | 0 | 0 | 0.000 | 0 | 0 | 0 | 8 | 8 | 8 | 0 |

Razorpay test mode. Not evidence about real customers, and not evidence that a
recovery decision caused a recovery.

**What the two tables say.** On the fake layer the naive arm recovers more, 21
against 18, and pays 19 false actions against 1 to do it, and reaches the
gateway 40 times with no policy verdict behind any of them. The model arm and
the rule set arm agreed on all 40 orders: same recoveries, same actions, same
false action on the same bait order, same nine escalations splitting the same
way. What differed is what they asked for, and that is the column worth
reading: `a2-agent` made 59 policy evaluations against 40, had 16 proposals
refused against 9, and none of the 16 reached the gateway.

On the live layer every order classified as `unclassified`, because Razorpay
test mode returns `payment_failed` for all eight documented magic cards and
that reason names no cause. `R7-UNKNOWN-FAIL-CLOSED` fired on every one, so
both gated arms escalated everything and took nothing. The naive arm retried
all 8 and 4 reached `paid`, and that number is selected rather than earned: a
test-mode attempt is settled at the last checkout call by one form field, so
the gateway is standing in for the world.

## What this is not evidence of

`/HONEST-LIMITATIONS.md` has every limit the phase documents record, including
the ones that make a number on these tables smaller than it looks.
