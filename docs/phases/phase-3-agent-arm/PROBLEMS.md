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

## 7. The test list grew by six Python methods

Named in `TESTS.md` under "Changes to this list while the tests were written",
with what each one is for. The list stays a record of what was named before the
code existed.
