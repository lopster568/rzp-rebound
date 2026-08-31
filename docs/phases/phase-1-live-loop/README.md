# Phase 1: live loop

Two halves. Start with `PLAN.md`.

- The offline half, worked on 2026-08-31: the Razorpay client, the replay
  client, the poller, the audit recorder, the notifier, the first slice of the
  recovery orchestrator, and the Jaeger scripts. Every test in it runs with no
  credential, no docker daemon, and no network.
- The live half, blocked on 2026-08-31 on Razorpay test-mode keys and a
  reachable docker daemon: fixture capture, card-table confirmation, the PRD
  Q1 spike, and the end-to-end demo.

`PLAN.md` marks the boundary. `REPORT.md` covers the offline half and hands the
live half its checklist.
