# Phase 0: foundations

Started 2026-08-31.

## Goal

Every seam the later phases plug into exists and is proven by a test that was
seen failing first. The whole phase runs offline: no Razorpay credentials, no
docker, no network. If phase 0 needs a key to pass, phase 0 is wrong.

The seams: a fake clock, an error-code classifier, a Razorpay port with a
deterministic fake behind it, a batch generator with a ground-truth manifest,
a tracer provider, and config loading that redacts secrets.

## Tests first

`TESTS.md` lists all 24 test functions with the assertion each one makes.
Order of work per group: write the tests, run them, paste the red output into
`TESTS.md`, then write the package until they pass.

1. `internal/clock` (2 tests). Nothing else can be tested deterministically
   until time is injectable.
2. `internal/classify` (8 tests). Reads `testdata/error_codes.json`. The
   totality test is the one that matters: an unknown code returns
   `Unclassified` and is not retry eligible, rather than panicking or
   defaulting to retry.
3. `internal/razorpay` fake (5 tests) and the port contract (2 tests). The
   contract tests are written against the interface so the live client can be
   run through the same suite in phase 1.
4. `internal/batch` (5 tests). Ground truth must never appear in a field the
   agent can read, or every later score is meaningless.
5. `internal/telemetry` (3 tests) and `internal/config` (3 tests).

## Tasks

- [x] Repo, gitignore, module, directory skeleton, package doc comments
- [x] `testdata/magic_cards.json` and `testdata/error_codes.json` from the
      Razorpay docs, every entry marked unverified
- [x] Prose gate, pre-commit hook, preflight, Makefile, CI
- [ ] Write the 24 tests listed in `TESTS.md`, run them, paste the red output
- [ ] Implement the six packages until the suite is green
- [ ] `make verify-phase-0` passes on a machine with no Razorpay keys
- [ ] Write `REPORT.md`

## Exit criteria

- `make verify-phase-0` passes with `RAZORPAY_KEY_ID` and
  `RAZORPAY_KEY_SECRET` unset and docker stopped.
- `TESTS.md` contains real red output from before the implementation existed.
- The classifier handles every code in `testdata/error_codes.json` and returns
  `Unclassified` for anything else.
- The batch manifest carries ground truth for every order, and no
  agent-visible field leaks it.
- `DECISIONS.md` records anything a later phase would otherwise have to
  reverse-engineer.

## Out of scope

No live API calls, no retry policy, no MCP server, no agent. Those are phases
1 through 3. `internal/policy`, `internal/store`, `internal/recovery`,
`internal/notify`, `internal/audit`, `internal/poller`, and
`internal/mcpserver` stay as package doc comments until then.
