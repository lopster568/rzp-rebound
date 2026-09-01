# Phases

Each phase directory holds `PLAN.md` and `TESTS.md` written before the code,
`PROBLEMS.md` and `DECISIONS.md` appended during the work, and `REPORT.md`
written at the end.

| Phase | Date | Goal | Status |
|---|---|---|---|
| [0 foundations](phase-0-foundations/) | 2026-08-31 | Every seam the later phases plug into exists and is proven by tests that run offline with no credentials. | done, 28 tests green, see [REPORT.md](phase-0-foundations/REPORT.md) |
| [1 live loop](phase-1-live-loop/) | 2026-08-31 | Drive a real Razorpay test-mode order to a documented failure and back to paid, and confirm the magic-card table against live responses. | done, both halves, see [REPORT.md](phase-1-live-loop/REPORT.md). The loop closes against test mode. None of the eight documented cards reproduced its code, which is the finding rather than a gap. |
| [2 policy and eval](phase-2-policy-and-eval/) | 2026-08-31 | Retry policy plus a batch harness that scores decisions against a ground-truth manifest. | done, 38 new Go tests and 16 Python, see [REPORT.md](phase-2-policy-and-eval/REPORT.md). Nine rules, three arms, and a three-arm table on two layers in [RESULTS.md](../../RESULTS.md). |
| [3 agent arm](phase-3-agent-arm/) | done 2026-09-01 | An agent drives the recovery loop over MCP and is scored on the same batches. | 7 tools, 2 gate layers, 45 tests, a four-arm table on two layers. The agent matched the rules arm on all 40 fake-layer orders. |
| [4 submission](phase-4-submission/) | done 2026-09-01 | Demo, writeup, and the numbers that back them. | Repository done, see [REPORT.md](phase-4-submission/REPORT.md). `/ARCHITECTURE.md`, `/README.md`, `/HONEST-LIMITATIONS.md`, `docs/DEMO-SCRIPT.md`, and a claims gate that reads every published table cell back against the CSV that produced it. The pitch video and the submission form are Roshan's and are not done. |
