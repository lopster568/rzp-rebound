# Phase 3 problems

Appended to while the work happened. Every entry says what the code did before,
how it was found, and what it costs.

## 1. Two invocations of one batch got the same gateway order id

**Found by running the two-order smoke on 2026-09-01 and reading the ledger.**
Not by reading the code, and not by any test: the tests drive one server at a
time, and the collision only exists between processes.

Every invocation is its own process with its own in-memory fake gateway, and
`razorpay.Fake` reads its rng for exactly one thing, generating ids. Two
invocations built from the same batch file therefore handed their first order
the same id. The smoke's ledger had two different manifest orders, both filed
under `order_itlu4O6ZE4ZZc0`.

The overall table row survives that, because it reads the whole ledger.
The per-class rows do not. `harness/aggregate.py` selects a class's ledger rows
by gateway order id, so with forty orders sharing one id every class row would
have carried every other class's policy counts.

`cmd/rzp-mcp` now offsets the fake's seed by an FNV hash of the manifest order
id. It is deterministic, so a rerun of the same order gets the same ids, and it
moves nothing else, because nothing else in the fake reads the rng.
`TestTwoInvocationsOfOneBatchGetDifferentGatewayIDs` is the regression test and
it was run against the old behaviour first.

## 2. Every audit row from the agent arm had an empty trace id

**Found by reading the ledger after the first trace capture.** FR-AUD-3 says
every row joins to a span by trace id, and every row from `a2-agent` had
`"trace_id": ""`. Nothing failed. The exporter was simply never built.

The cause took a probe to find. The CLI starts an MCP stdio server with the
parent environment, which is how `RAZORPAY_KEY_ID` and `RAZORPAY_KEY_SECRET`
reach the server process without ever being written to a file. It makes one
exception: it strips `OTEL_*` on the way to the child, because those configure
its own telemetry.

Measured on 2026-09-01 with a probe server that dumped its environment to a
file: 58 variables arrived, `RAZORPAY_KEY_ID` among them,
`OTEL_EXPORTER_OTLP_ENDPOINT` not. The same probe confirmed two other things
worth writing down. Entries in the mcp config's `env` map do reach the child,
and `${VAR}` in one of those values is expanded from the CLI's own environment.

`harness/claude_runner.mcp_config` now restates the three telemetry variables
in the config's `env` map, by value, and only the ones that are actually set.
None of them is a credential: they are a host and port and a service name. No
credential is written to the config file, because none needs to be.

## 3. `create_payment_link` and the resend it exists for fought over R6

The rules arm raises a link and sends it as one action and commits once. Over
MCP they are two tools, because the agent has to be able to raise a link, read
the id back, and then send it.

Committing the link creation as the notify action it was evaluated as moves
`LastNotifyAt`, and then `R6-NOTIFY-RATE` refuses the resend the link exists
for. Committing it as a non-notify action is worse: `store.Commit` counts
anything that is not a notification as a payment attempt, and a payment link is
not one, so every link would have inflated the order's attempt count and
FA-2 would have fired on orders where raising a link was correct.

`create_payment_link` commits nothing. `resend_payment_link_notification`
commits with the notify action. What bounds link raising is the invocation's
action budget in layer 1. `DECISIONS.md` has the trade written out.

## 4. `escalate_to_human` wrote a second `decision_recorded` row

The first version recorded the escalation reason as its own
`decision_recorded` row. `record_decision` had already written one for the same
order, so a single escalation produced two rows of a kind that means "the agent
stated a decision", and a reader counting decisions would have counted two.

The reason now goes on the action row as `escalation_reason`.

## 5. The containment sweep asserted something that is not true of escalation

`TestEveryActionToolConsultsPolicyBeforeSideEffect`'s second sweep drives every
action tool at a `never_retry` order and originally asserted that all four were
refused. `escalate_to_human` was allowed, and it should be: `R4-NEVER-RETRY-CLASS`
returns an escalate verdict, and handing that order to a person is the correct
move.

The sweep's real claim is that no side effect reaches the gateway, and the spies
carry that. The test now names the exception and says why, and still asserts
that escalate consulted the policy and reached nothing.

## 6. The leak test's first version failed on the agent's own vocabulary

`TestToolResponseNeverContainsGroundTruthFields` searched every tool response
for the manifest's `ground_truth_correct_action`. Every action tool echoes back
the action it was asked for, and `retry_same_instrument` is both a correct
action in the manifest and a string the agent sends, so the test failed on the
echo.

It also failed on `never_retry`, which is both a bait kind and a
`classify.Class`. The class is legitimately visible: it is what
`internal/classify` reads out of the gateway's error fields, and the rules arm
gets the same reading from the same function.

The value check is now precise about which values are answers and which are
observations, and the reasoning is in the test rather than in a commit message:

- The seeded card and the bait kind are answers, and the bait kind is skipped
  when `classify.ParseClass` recognises it as a class name.
- For a bait order, `do_nothing` is checked. Nothing the agent sends contains
  it and nothing the server computes produces it, so finding it means the
  answer key reached the wire.
- The field-name half is computed rather than listed: every json field on
  `batch.Order` that is not on `batch.AgentVisibleOrder`. A field added to the
  manifest is covered without anybody remembering to add it here.

## 7. The fake-layer run carries `attempt_no` 0 on its action rows

Found by reading `internal/mcpserver/handlers.go` while the fake-layer run was
already in flight. `finishAction` wrote `attempt_no` as a literal zero: the
attempt number was computed in `act` and never passed down. The
`policy_evaluated` row it sits next to carried the right value all along.

Nothing reads the field. `harness/scorer.py` computes FA-2 from
`attempts_seen` on the outcome row and `max_legit_attempts` in the manifest,
and `policy_counts` reads the kind, the verdict, and the side-effect flag. So
no published number moves either way.

The fix landed after the fake run started and before the live one. Restarting
the fake run would have cost forty more headless invocations against a
sixty-invocation budget for the night, so it was allowed to finish on one
consistent binary rather than being restarted or, worse, switched mid-run.

What that leaves is one disclosed inconsistency: in
`results/runs/phase-3-fake/a2-agent/ledger.jsonl` the `action_taken` and
`action_skipped` rows carry `attempt_no` 0, and in the live run they carry the
real value. Per ADR-0004 the two layers are never summed anyway, and this note
is here so a reader of the ledger is not left wondering.

## 8. The live layer would have printed its spans onto the MCP transport

Found by reading the code before the live run rather than by running it, which
is the only way this one could have been found cheaply: the symptom is a
connection failure with nothing naming the cause.

`internal/telemetry` falls back to the stdout exporter when no OTLP endpoint is
configured. That is correct for `rzp run`, whose stdout is a terminal. The live
gateway rig builds its provider through the same function, and `rzp-mcp`'s
stdout is the MCP transport, so a live invocation with no endpoint set would
have written spans into the JSON-RPC frame stream.

`rzp-mcp -layer live` now refuses to start without
`OTEL_EXPORTER_OTLP_ENDPOINT`, and the error says why and names
`scripts/jaeger-up.sh`, which prints the value.
`TestLiveLayerRefusesToServeWithNoOTLPEndpoint` covers it. The fake layer needs
no guard, because it builds no provider at all when the endpoint is unset.

## 9. Parallel tool calls could put eight payments on an order the cap allowed one

The worst thing found in this phase, and it was found by reading the code
rather than by any run.

`internal/store`'s doc comment has said since phase 2 that snapshot, evaluate,
and commit are three separate lock acquisitions, so two callers can both read
`AttemptsMade` at 2 against a cap of 3 and both commit, putting 4 attempts on
one order under `R1-MAX-ATTEMPTS`. Phase 2 recorded it as unreachable, and it
was: `rzp run` processes orders one at a time.

An MCP client issues tool calls in parallel. The SDK dispatches each request in
its own goroutine, so the sequence became reachable the moment the agent arm
existed, and nothing in the phase 3 test list was looking for it.

Measured. Against the unlocked code, eight concurrent `retry_payment` calls on
an order with one of its two permitted attempts already spent put **eight**
payments on it. Every one of those eight actions carried an `allow` verdict, so
`policy_violations_succeeded` would have read 0 and the containment column
would have said the run was clean. That is the failure mode worth being loudest
about: the metric that gates the build cannot see a rule that was consulted
correctly and then raced.

`Server.act` now holds one mutex from before the snapshot to after the commit.
It is a single lock rather than one per order because an invocation serves one
order; for a server built with several it is conservative rather than wrong.

`TestConcurrentActionToolCallsCannotBothPassTheAttemptCap` covers it. The first
version of that test was a probabilistic detector: against the unlocked code it
went red about twice in forty runs, because the window between the snapshot and
the commit is a few microseconds wide. It now widens that window with a 25ms
delay in the spy attempter and starts its eight callers on a barrier, which
takes it to red on every run against the unlocked code and green under `-race`
against the locked one. A test that usually passes against the bug it exists for
is a test nobody can act on.

The fake-layer run had already started when this landed, so it ran on the
unlocked binary. Its ledger was checked afterwards for the signature, an order
with more attempts than `R1-MAX-ATTEMPTS` permitted, and the result is in
`REPORT.md`.

## 10. The test list grew by six Python methods

Named in `TESTS.md` under "Changes to this list while the tests were written",
with what each one is for. The list stays a record of what was named before the
code existed.
