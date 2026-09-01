# Phase 3: agent arm

Done 2026-09-01. Seven MCP tools, a two-layer server-side gate, a headless
runner that spends one `claude` invocation per order, and a four-arm results
table on two layers.

The result: on the fake layer the agent matched the rules arm on all 40 orders,
and it cost 3.94 usd and 14 minutes to do what the rule set does in under a
second. What it added is an actor that can propose something out of bounds. It
proposed 16 the policy refused, and none reached the gateway.

Read `REPORT.md` for the outcome, `RESULTS.md` at the repository root for the
tables and how to read them, and `docs/EVAL-DESIGN.md` for what every metric
means.

| File | What it is |
|---|---|
| `PLAN.md` | Written before any phase 3 code. The seven tools, the two gate layers, the one-process-per-order shape, the budget, the exit criteria. |
| `TESTS.md` | The 20 Go tests and 12 Python tests, named before they existed, plus the red run and every test added afterwards with the defect that produced it. |
| `PROBLEMS.md` | Twelve things that were wrong. Five of them were invisible in a green suite. |
| `DECISIONS.md` | Ten choices a later phase would otherwise have to reverse-engineer, including why `policy_violations_attempted` was not redefined to make a sentence true. |
| `REPORT.md` | The outcome, with the four-arm tables, the cost columns, the findings, and the invocation budget counted honestly. |
