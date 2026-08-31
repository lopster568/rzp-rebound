# Phase 1: live loop

Two halves, both worked on 2026-08-31. Start with `PLAN.md`.

- The offline half: the Razorpay client, the replay client, the poller, the
  audit recorder, the notifier, the first slice of the recovery orchestrator,
  and the Jaeger scripts. Every test in it runs with no credential, no docker
  daemon, and no network.
- The live half: fixture capture, the card-table walk, the PRD Q1 spike,
  `cmd/rzp`, and `make demo` end to end against Razorpay test mode. It was
  blocked on test-mode keys and a reachable docker daemon, and both cleared the
  same day.

`PLAN.md` marks the boundary. `REPORT.md` covers both halves in order: the
offline half first, then the live half with each checklist item and each exit
criterion.

Two documents came out of the live half and live outside this directory,
because they outlast the phase:

- `docs/RAZORPAY-TEST-MODE-NOTES.md`, what test mode was actually observed
  doing, verified against unverified, with dates.
- `docs/AUDIT-TRACE-SCHEMA.md`, the span and ledger schema, written from a run.
