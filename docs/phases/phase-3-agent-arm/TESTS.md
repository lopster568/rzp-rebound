# Phase 3 tests

Written before the phase 3 test code, which is written before the phase 3
implementation. The `## Red run` section at the bottom holds the failing output
of the whole list, captured before any function body was written.

20 Go test functions, 17 in `internal/mcpserver` and 3 in `cmd/rzp-mcp`, plus
12 Python test methods across two new files, as planned. The tree ended at 23
Go and 21 Python. The section at the bottom records every addition rather than
editing the tables below, so this list stays a record of what was named before
the code existed.

The technique is the phase 0 and phase 2 one: the declarations go in with the
tests and the bodies do not. A red run that will not compile cannot be
committed, because the pre-commit hook runs `go vet`.

## `internal/mcpserver`

### Layer (a): containment, the four tests ADR-0003 and the phase 3 brief name

| # | Test | What it pins |
|---|---|---|
| 1 | `TestEveryActionToolConsultsPolicyBeforeSideEffect` | The phase 3 test. It lists the tools through the server's own registry over a live session, so the set it walks is exactly the set the model sees, then calls every one of them against a spy `Port` and a spy `Attempter` under a state the policy must deny. Any mutating call on either spy fails the test. A tool the test has no argument builder for also fails, so adding an ungated tool turns the suite red two ways: through the missing builder, and through the spy if it reaches the gateway. |
| 2 | `TestMiddlewareOpensSpanForEveryToolCall` | Layer 1. Every `tools/call` produces exactly one span named for the tool, carrying the tool name and the verdict, whether the call was allowed or refused. Recorded through an in-memory span exporter, not asserted from the handler side. |
| 3 | `TestKillSwitchDeniesAllToolsImmediately` | R8 through the middleware. With the kill switch engaged, every registered tool refuses, no spy is touched, and the refusal carries `R8-KILL-SWITCH`. |
| 4 | `TestToolResponseNeverContainsGroundTruthFields` | The leak test, extended from the manifest projection to the tool-response surface. Every tool is called on a batch whose ground truth is known, and the marshaled response is searched for the manifest's ground-truth field names and for the values behind them: the correct action, the bait kind, the recoverable flag, the attempt budget, and the prior attempt count. It also walks the response types by reflection and fails on a field reachable from `batch.Order`. |
| 5 | `TestActionToolsRefuseUntilDecisionRecorded` | The decision gate. Every action tool refuses for an order with no `record_decision` behind it, and the same call succeeds once one exists. The refusal carries its own rule id, so it is a countable refusal rather than a silent no. |

### Layer (b): the tools themselves

| # | Test | What it pins |
|---|---|---|
| 6 | `TestServerServesExactlyTheSevenNamedTools` | The tool set is a closed list. `ListTools` returns the seven names in `PLAN.md` and nothing else, so a capability cannot arrive without a line in this table. |
| 7 | `TestRecordDecisionRequiresOrderActionAndReasoning` | A decision with an empty order id, an unknown action, or empty reasoning is refused. The agent has to say what it is doing and why before it does it. |
| 8 | `TestRecordDecisionWritesReasoningToTheAuditTrail` | The stated reasoning reaches the ledger as its own row, joined to the order and carrying the trace id. |
| 9 | `TestReadToolsNeedNoDecisionAndReachNoSideEffect` | `list_failed_payments` and `get_payment_detail` work before any decision is recorded and touch no mutating call. Gating a read behind a decision would make the agent guess. |
| 10 | `TestEscalateToHumanIsRecordedAsAnEscalationNotAFailure` | Escalation is a decision with an outcome, scored in the precision and recall pair. The outcome row comes back with `escalated` true and `action_kind` none. |
| 11 | `TestRetryPaymentDrivesTheSameAttempterTheArmsUse` | FR-REC-4. The agent's retry goes through `recovery.Attempter`, the interface the other three arms hold, so the four-arm table compares decisions and not capabilities. |
| 12 | `TestCreatePaymentLinkAndResendGoThroughThePortAndTheNotifier` | The other two side effects reach `razorpay.Port` and `notify.Notifier`, and the recorded phrase is that the notification API call succeeded. |

### Layer (c): the middleware's own rules

| # | Test | What it pins |
|---|---|---|
| 13 | `TestOrderAllowlistDeniesAnOrderOutsideTheBatch` | FR-POL-4, which phase 2 recorded as not met and unreachable. A model can name any string, so the rule is reachable now. An action naming an order the invocation was not given is refused before any handler runs. |
| 14 | `TestActionBudgetDeniesPastTheInvocationCap` | R5 in the layer that cannot be skipped. Once the invocation has spent its action budget, every further action tool is refused whatever the per-order state says. |
| 15 | `TestUnknownToolNameIsRefusedByTheAllowlist` | A `tools/call` naming something not in the seven is refused by the middleware rather than falling through to the SDK's own not-found path, so the refusal is on the record. |
| 16 | `TestEveryToolCallStampsTheAuditTrailWithItsTraceID` | FR-AUD-3 across the MCP surface. Every ledger row a tool call produces carries the trace id of that call's span, so a reviewer goes from a row to a trace. |
| 17 | `TestNoToolResponseCarriesACredential` | FR-MCP-2. The process holds the keys; the model holds tool names. No response, and no error string in a response, contains the configured key id or secret. |

### Layer (d): the compiled binary over stdio

Driven through `TestMain`, which builds `./cmd/rzp-mcp` once into the test's
temp directory and drives the real process over `mcp.CommandTransport`. The
pattern is `~/loadline/interposer/interposer_test.go`: a subprocess test that
exercises the artefact that ships rather than a server built in the test.

| # | Test | What it pins |
|---|---|---|
| 18 | `TestCompiledServerListsItsToolsOverStdio` | The binary starts, speaks MCP on stdin and stdout, and lists the seven tools. Nothing about the transport is mocked. |
| 19 | `TestCompiledServerRefusesAnActionWithNoDecisionRecorded` | The decision gate holds in the shipped binary, not only in a server built inside a test. |
| 20 | `TestCompiledServerWritesAnOutcomeRowReadBackFromTheGateway` | The end of the loop. After the client disconnects, the process reads the order back with `FetchOrder` and appends one outcome row whose `final_order_status` came from the gateway and not from anything the agent said. |

## `harness/`, under `python3 -m unittest`

### `harness/test_claude_runner.py`

| # | Test | What it pins |
|---|---|---|
| 1 | `test_command_carries_the_strict_flag_the_model_and_the_budget` | The invocation is the one `PLAN.md` documents. `--strict-mcp-config` is asserted by name: without it the run inherits whatever MCP servers the operator has configured, and the containment claim would be about an unwritten tool set. |
| 2 | `test_mcp_config_names_only_the_rzp_server` | The generated config has exactly one server, pointed at the compiled `rzp-mcp` binary with this invocation's arguments. |
| 3 | `test_envelope_parsing_reads_cost_tokens_and_duration` | The `--output-format json` envelope yields usd, input and output tokens, and wall clock, which are the cost columns for `a2-agent`. |
| 4 | `test_an_infra_error_is_retried_once_and_then_unscorable` | A transport or startup failure is retried once. A second failure is unscorable, which is counted and explained rather than scored as a miss. |
| 5 | `test_a_budget_exhausted_invocation_is_unscorable` | Hitting `--max-budget-usd` is an infrastructure limit, not a decision the arm made, so it does not go in a denominator. |
| 6 | `test_every_invocation_writes_a_row_including_the_unscorable_ones` | `invocations.jsonl` gets a row per invocation whatever happened, so the cost total is the cost actually spent. |
| 7 | `test_prompt_sha256_is_the_digest_of_the_prompt_file` | The manifest field phase 2 left as a placeholder is filled from the file that ran, computed rather than copied. |

### `harness/test_arm_config.py`

| # | Test | What it pins |
|---|---|---|
| 8 | `test_a2_config_matches_the_other_arms_except_the_decision_maker` | The assertion the four-arm table rests on. Two configs are diffed key by key and the only permitted difference is `decision_maker`. |
| 9 | `test_every_arm_gets_the_same_layer_batch_and_policy` | Named separately because these three are the ones a hurried change would move. |
| 10 | `test_a_different_agent_budget_fails_the_identity_assertion` | The test that proves test 8 can fail. A config built with a bigger budget for `a2-agent` makes the diff non-empty. |

### `harness/test_aggregate.py`, added to the existing file

| # | Test | What it pins |
|---|---|---|
| 11 | `test_agent_cost_columns_sum_the_invocation_rows` | Tokens, usd, and wall clock for `a2-agent` come from `invocations.jsonl` and are sums over the rows, including the unscorable ones. |
| 12 | `test_agent_cost_columns_are_not_a_number_for_the_deterministic_arms` | An arm that made no model invocation has no token count, and the cell reads `n/a` rather than 0, for the reason the escalation rates read `n/a`: a zero there is a claim about something that did not happen. |

## Red run

Captured 2026-09-01, before any function body in `internal/mcpserver`,
`cmd/rzp-mcp`, `harness/claude_runner.py`, or `harness/arm_config.py` was
written. The declarations went in with the tests and the bodies did not.

```
$ go test ./internal/mcpserver/ ./cmd/rzp-mcp/ -count=1
--- FAIL: TestEveryActionToolConsultsPolicyBeforeSideEffect (0.00s)
    --- FAIL: .../refused_in_the_middleware
        mcpserver_test.go:495: build the mcp server: mcpserver: not implemented
    --- FAIL: .../refused_by_policy.Evaluate_in_the_handler
    --- FAIL: .../every_action_row_carries_a_verdict
--- FAIL: TestMiddlewareOpensSpanForEveryToolCall (0.00s)
[... 17 of 17 in internal/mcpserver ...]
FAIL	github.com/lopster568/rzp-recovery-agent/internal/mcpserver	0.005s
rzp-mcp: serving is not implemented yet
--- FAIL: TestCompiledServerListsItsToolsOverStdio (0.01s)
    main_test.go:262: connect to the compiled server: connection closed:
        calling "initialize": client is closing: EOF
--- FAIL: TestCompiledServerRefusesAnActionWithNoDecisionRecorded (0.00s)
--- FAIL: TestCompiledServerWritesAnOutcomeRowReadBackFromTheGateway (0.00s)
FAIL	github.com/lopster568/rzp-recovery-agent/cmd/rzp-mcp	0.613s

$ python3 -m unittest discover -s harness -t .
Ran 34 tests
FAILED (errors=17)
```

All 20 Go test functions failed. None of them passed vacuously, because every
one of them builds a server first and `mcpserver.New` returned an error, and
the three subprocess tests could not open a session against a binary that
exits rather than serving.

Of the 18 new Python test methods, 17 errored and one passed vacuously:
`test_an_unknown_arm_is_an_error` asserts that `decision_maker` raises, and a
stub that raises `NotImplementedError` satisfies it. That is recorded rather
than counted as a win, the same way phase 2 named the four Go tests that
passed against its stub tree. A test that only forbids something cannot go red
against a function that does nothing.

## Changes to this list while the tests were written

The list above named 20 Go tests and 12 Python. The tree ended at 23 Go and 18
Python.

The three extra Go tests all came out of a defect. Two were written after the
implementation, against a bug a run had already found, and were checked against
the old behaviour before being kept. The third was written against code that
was in the tree and wrong.

| Test | Package | Why |
|---|---|---|
| `TestTwoInvocationsOfOneBatchGetDifferentGatewayIDs` | `cmd/rzp-mcp` | Two invocations of one batch gave their first order the same gateway id, which would have made every per-class ledger count carry every other class's rows. `PROBLEMS.md` 1. Run against the old behaviour first. |
| `TestLiveLayerRefusesToServeWithNoOTLPEndpoint` | `cmd/rzp-mcp` | The live tracer falls back to the stdout exporter and stdout is the MCP transport. `PROBLEMS.md` 8. |
| `TestConcurrentActionToolCallsCannotBothPassTheAttemptCap` | `internal/mcpserver` | Eight parallel tool calls put eight payments on an order the cap allowed one, every one carrying an allow verdict, so the containment column would have read clean. `PROBLEMS.md` 9. It goes red on every run against the unlocked code and green under `-race` against the locked one. |
| `TestOutcomeContextSurvivesTheSessionsCancellation` | `cmd/rzp-mcp` | The first live agent arm came back entirely unscorable, because the CLI's exit cancelled the context the gateway read-back was on. `PROBLEMS.md` 10. |

The nine extra Python methods are the six below plus the three in
`test_agent_runner.py`, which is a file that did not exist when this list was
written: the driver had no logic worth testing until a live run showed that its
check for the server's outcome row was racing the server writing it
(`PROBLEMS.md` 11).

The six from the original two files:

| Test | File | Why |
|---|---|---|
| `test_an_unknown_arm_is_an_error` | `test_arm_config.py` | A typo in an arm id must not fall through to a default config. |
| `test_the_agent_is_the_only_llm_decision_maker` | `test_arm_config.py` | Pins which arm is the one under test, so a config that quietly made two arms agentic fails. |
| `test_a_nonzero_exit_with_an_envelope_is_read_from_the_envelope` | `test_claude_runner.py` | The first of the four transplanted lessons: parse `subtype` before `returncode`. It was in the module doc and had no test. |
| `test_a_retry_that_succeeds_is_scorable` | `test_claude_runner.py` | The other half of the retry test. Without it, a runner that always returned unscorable would pass. |
| `test_unparseable_output_is_unscorable_rather_than_an_answer` | `test_claude_runner.py` | Output that is not JSON is not an empty answer. |
| `test_the_clean_settings_file_is_written_every_call` | `test_claude_runner.py` | The third transplanted lesson, which cost the source project 56 trials. |
