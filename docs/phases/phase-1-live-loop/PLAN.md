# Phase 1: live loop

Started 2026-08-31, one day ahead of the 2026-09-01 target date in the PRD.

## Goal

Phase 1 has two halves and only one of them can run on 2026-08-31.

**The offline half.** Write every component the live loop is made of, and prove
each one with a test that needs no Razorpay credential, no docker daemon, and
no network. The Razorpay client runs against `httptest`. The poller runs
against `razorpay.Fake` on an injected clock. The audit recorder writes to a
buffer and an in-memory span recorder. The notifier runs against a mock and
against the fake.

**The live half.** Point the same client at Razorpay test mode, capture
fixtures, confirm the eight cards in `testdata/magic_cards.json`, and answer
PRD questions Q1 through Q5. It is blocked on two things this machine does not
have on 2026-08-31: test-mode API keys, and a reachable docker daemon for
Jaeger.

This document plans both. The offline half is the work; the live half is a
checklist the next session starts from.

## Why the split is safe

ADR-0004 already put the three measurement layers on separate tables and said
an unanswered live question costs batch size in the live table, not the build.
The offline half is exactly the part of phase 1 that ADR-0004 predicted would
still stand with the live path unresolved.

The one thing the split costs is confidence about wire shapes. Everything the
client encodes and decodes on 2026-08-31 comes from the struct tags already in
`internal/razorpay/port.go`, not from a response Razorpay sent. Every one of
those places is marked pending fixture capture in the source, and `DECISIONS.md`
lists them.

## Offline half: what gets built

### 1. `internal/razorpay` live client

Plain `net/http` per ADR-0002. No SDK, no generated client.

- HTTP Basic auth from the key pair in `internal/config`.
- Base URL configurable, so a test points it at an `httptest.Server`.
- `otelhttp` transport, so every request is a client span. The dependency comes
  back with `go get`; phase 0 tidied it away because nothing imported it.
- 429 retry with capped exponential backoff, through an injected wait function
  so no test sleeps.
- No retry on any other 4xx. The same request gets the same refusal.
- A concurrency semaphore, so a batch cannot open more sockets than configured.
- Secret redaction on every error path: the key id, the secret, and the base64
  basic-auth token are all scrubbed before an error string leaves the package.
- A raw response capture hook, an `io.Writer` seam that records one JSON line
  per response. It is what the live half records fixtures with.

### 2. `internal/razorpay` replay client

Serves recorded JSON from `testdata/recorded/`. It is the same `Client` with a
fixture `http.RoundTripper` under it, so replay exercises the real decode path
rather than a second one that could drift from it.

`testdata/recorded/` holds no real capture on 2026-08-31. One synthetic fixture
goes in so the mechanism has something to serve. It is named with a
`synthetic_` prefix, carries `"_meta": {"synthetic": true}`, and is shaped only
from fields that already exist in `port.go`. It is not evidence about Razorpay
and the file says so in its own `_meta`.

### 3. `internal/poller`

Polls `FetchOrder` and `ListPaymentsForOrder` until the order reaches a
terminal state or the maximum wait runs out. Backoff reads time through
`clock.Clock` and waits through an injected function, so the tests move time
instead of sleeping. A timeout reports the last state it saw rather than
throwing it away.

### 4. `internal/audit`

A dual-sink recorder. One event goes to two places: attributes on the active
span, and a JSONL ledger line carrying the trace id. That is what makes
FR-AUD-3 work, because a reviewer reading a ledger row can jump to the trace it
came from.

Redaction of key-shaped and card-shaped values happens on the way in, on both
sinks. A monotonic per-order sequence number puts the rows for one order in
order even when the file interleaves several.

### 5. `internal/notify`

A `NotifierPort`, a mock that records calls, and the real notifier that goes
through the port to `ResendPaymentLinkNotification`, so it is testable against
`razorpay.Fake` with no credential.

`Receipt.DeliveryConfirmed` is hardcoded false and the struct comment says why:
the only observable is an HTTP response. Audit strings come from package
constants, and a test asserts the forbidden phrasings never appear in any of
them.

### 6. `internal/recovery` first slice

An orchestrator that takes one agent-visible batch order, polls it, classifies
the failure, and records an audit event per stage. The outcome comes from
re-fetching the order state, never from what an action reported about itself.
Phase 2 puts the policy gate in front of the action.

### 7. Operator scripts

`scripts/jaeger-up.sh` and `scripts/jaeger-down.sh` wrap the compose file and
wait on the query API rather than returning the moment the container starts.
`scripts/seed-batch.sh` is a skeleton. `make jaeger-up`, `make jaeger-down`,
and `make seed` wire them in.

## Live half: out of scope on 2026-08-31

Blocked on test-mode keys and a reachable docker daemon. Listed here so the
boundary is written down rather than assumed.

- Integration tests behind a build tag, running against test mode.
- `scripts/capture-fixtures.sh` and any run of it.
- `scripts/verify-razorpay-auth.sh` and any run of it.
- The 90-minute spike on PRD Q1: how a second payment attempt is made on a
  failed order against the live API.
- Flipping any `"verified": false` in `testdata/magic_cards.json`.
- `make demo` end to end.
- `AUDIT-TRACE-SCHEMA.md`. It documents the span and ledger shape a real run
  produces, and no real run has happened.

## Tests first

`TESTS.md` lists the 23 new test functions with the assertion each one makes.
The suite goes from 28 to 51.

Order of work: write `PLAN.md` and `TESTS.md`, then the tests, then run them,
paste the red output into `TESTS.md`, then write the packages until green.

Phase 0 hit a wall here and the fix is on record in
`docs/phases/phase-0-foundations/PROBLEMS.md`: the pre-commit hook runs
`go vet ./...`, which type-checks test files, so a tree whose tests name
undefined symbols cannot be committed. The red commit therefore carries the API
surface, meaning types, constants, interfaces, and signatures with zero-value
bodies, and the tests fail on their assertions. Phase 1 uses the same
technique.

## Tasks

- [ ] `PLAN.md` and `TESTS.md`, before any code
- [ ] `go get` otelhttp back into go.mod
- [ ] Write the 23 tests, run them, paste the red output into `TESTS.md`
- [ ] `internal/razorpay` client
- [ ] `internal/razorpay` replay client and the one synthetic fixture
- [ ] `internal/poller`
- [ ] `internal/audit`
- [ ] `internal/notify`
- [ ] `internal/recovery` first slice
- [ ] Jaeger scripts, seed skeleton, Makefile targets
- [ ] `DECISIONS.md`, `PROBLEMS.md`, `REPORT.md`

## Exit criteria for the offline half

- `make ci` passes with `RAZORPAY_KEY_ID` and `RAZORPAY_KEY_SECRET` unset and
  the docker daemon unreachable.
- `TESTS.md` holds real red output from before the implementations existed.
- The two `TestPortContract_*` functions run against a second harness backed by
  the client, with no assertion copied.
- Every wire shape the client guesses is marked pending fixture capture in the
  source and listed in `DECISIONS.md`.
- No test needs a credential, a container, or the network.
- `REPORT.md` covers the offline half only and hands the live half a checklist
  in the order it has to be done.
