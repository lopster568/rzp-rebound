# Phase 1 report, offline half

Written 2026-08-31, at the end of the offline half. The live half has not
started and this report claims nothing about it.

## Scope

Phase 1 has two halves. This one is everything that runs with no Razorpay
credential, no docker daemon, and no network. The live half is blocked on
test-mode keys and a reachable docker daemon, neither of which this machine had
on 2026-08-31. `PLAN.md` draws the line and the last section here hands the
live half its checklist.

Nothing in this phase touched a live Razorpay API. Every card in
`testdata/magic_cards.json` still carries `"verified": false`, and PRD questions
Q1 through Q5 are all still open.

## What shipped

Five packages went from a doc comment to a tested implementation, one package
gained a client, and the operator got three scripts.

| Package | What it is |
|---|---|
| `internal/razorpay` | `Client`, the live client behind `Port`: plain `net/http` per ADR-0002, HTTP Basic auth, configurable base URL, an `otelhttp` transport, 429 retry with capped exponential backoff on an injected wait, a concurrency semaphore, credential redaction on every error path, and a raw response capture hook. Plus `NewReplay`, the same `Client` with a fixture transport over `testdata/recorded/`. |
| `internal/poller` | Polls `FetchOrder` and `ListPaymentsForOrder` until the order is terminal or the budget runs out. Backoff reads an injected clock and waits through an injected function. A timeout reports the last state it saw. |
| `internal/audit` | `Recorder`, the dual sink. One event goes to attributes on the active span and to a JSONL ledger line carrying that span's trace id. Redaction by detail-key name and by value shape, and a monotonic sequence per order. |
| `internal/notify` | `NotifierPort`, `Mock`, and `Notifier`. Three audit-phrase constants, all of them about an API call. `Receipt.DeliveryConfirmed` is false on every path. |
| `internal/recovery` | `Orchestrator`, the first slice: poll, classify, act, read the outcome back out of the gateway. Three audit rows per order. |
| `internal/redact` | The card-shaped and key-shaped patterns, in one place, called by `internal/audit` and by the client's capture path. It has no test file of its own: it is covered through both callers, the same arrangement `internal/testcards` has under FR-CARDS-1. |
| `scripts/` | `jaeger-up.sh` and `jaeger-down.sh` wrap the compose file and wait on the query API. `seed-batch.sh` is a skeleton that exits non-zero. `make jaeger-up`, `make jaeger-down`, `make seed`, and `make verify-offline` wire them in. |

Commits, in order:

```
db671e8 docs(phase-1): plan and test list for the offline half, before any code
0c10bc7 test(phase-1): the offline half red, with declarations and no bodies
50f66a0 feat(razorpay): net/http client with basic auth, 429 backoff, spans, redaction, and a capture hook
ae0577b feat(razorpay): replay client over recorded fixtures, with one labelled synthetic file
0efb1a5 feat(poller): poll to a terminal order state on an injected clock, timeout reports last state
975aebf feat(audit): dual-sink recorder, span attributes and a JSONL ledger with redaction
c9cd785 feat(notify): notifier port, mock, and a receipt that reports an API call and nothing about a person
e890ede feat(recovery): orchestrator first slice, outcome read back from the gateway
666b392 build(scripts): jaeger up and down with a health wait, seed skeleton, make targets
d89c0c0 docs(phase-1): decisions, problems, and the offline-half report
aa3c251 fix(razorpay): scrub credentials out of captured JSON response bodies
1217c80 test(razorpay): assert no credential reaches an otelhttp span attribute
118226e fix: redact before truncating, close the card-pattern gap, and correct four review findings
```

The last three are the review round. `aa3c251` and `118226e` each close a
credential leak that the green suite had not been asking about, and
`PROBLEMS.md` has both with the measurement that found them.

`db671e8` holds `PLAN.md` and `TESTS.md` and no code. `0c10bc7` is the red
commit: it carries all 24 new tests, the second contract harness, and six files
of type declarations, constants, interfaces, and signatures whose bodies return
zero values. The red output pasted into `TESTS.md` came from that tree. Every
commit after it adds behaviour to a package whose tests were already failing.

## Test counts

| | Before | After |
|---|---|---|
| Test functions | 28 | 52 |
| Packages with tests | 6 | 10 |
| Packages | 11 | 12 |

Per package after: `razorpay` 17, `classify` 8, `audit` 5, `batch` 5, `poller`
4, `config` 3, `notify` 3, `telemetry` 3, `clock` 2, `recovery` 2.

The 24 new functions are 8 client, 2 replay, 4 poller, 5 audit, 3 notify, and 2
recovery. No new test function was added for the port contract: the two
existing `TestPortContract_*` functions gained a second harness and now run
twice each, with no assertion copied.

25 of the 52 failed in the red run: 23 of the 24 new ones plus the two contract
functions, which failed on their new `client_httptest` subtest while still
passing on `fake`. The 24th,
`TestClassifierHandlesEveryRecordedErrorPayload`, passed by design and
`TESTS.md` explains why.

## Exit criteria

**`make ci` passes with both key variables unset and the docker daemon
unreachable.** Met, through the `make verify-offline` target added for it, which
unsets the two variables itself so the claim does not depend on the developer's
environment.

```
$ env -u RAZORPAY_KEY_ID -u RAZORPAY_KEY_SECRET make verify-offline
preflight: toolchain
  ok    go go1.25.0
  ok    jq jq-1.7
  ok    claude CLI at /home/oni/.local/bin/claude
  FAIL  docker is installed but the daemon is not reachable
preflight: credentials (test mode)
  warn  RAZORPAY_KEY_ID is unset. Offline tests still run; live test-mode calls will not.
  warn  RAZORPAY_KEY_SECRET is unset. Offline tests still run; live test-mode calls will not.

error: preflight: 1 hard failure(s), 2 warning(s)
preflight reported problems, continuing (the offline half needs no docker and no keys)
ok  	github.com/lopster568/rzp-recovery-agent/internal/audit
ok  	github.com/lopster568/rzp-recovery-agent/internal/batch
ok  	github.com/lopster568/rzp-recovery-agent/internal/classify
ok  	github.com/lopster568/rzp-recovery-agent/internal/clock
ok  	github.com/lopster568/rzp-recovery-agent/internal/config
ok  	github.com/lopster568/rzp-recovery-agent/internal/notify
ok  	github.com/lopster568/rzp-recovery-agent/internal/poller
ok  	github.com/lopster568/rzp-recovery-agent/internal/razorpay
ok  	github.com/lopster568/rzp-recovery-agent/internal/recovery
ok  	github.com/lopster568/rzp-recovery-agent/internal/telemetry
check-docs: 24 file(s) clean
exit 0
```

The docker line is the same hard failure phase 0 tolerated, and it is the
criterion being demonstrated rather than a problem: the daemon was down and the
phase passed anyway.

**`TESTS.md` holds real red output from before the implementations existed.**
Met, from commit `0c10bc7`. The `## Red run` section carries the full output and
the two qualifications on it.

**The two `TestPortContract_*` functions run against a client-backed harness
with no assertion copied.** Met.

```
$ go test ./internal/razorpay/ -run TestPortContract -v
--- PASS: TestPortContract_CreateOrderThenFetchOrderRoundTrips
    --- PASS: .../fake
    --- PASS: .../client_httptest
--- PASS: TestPortContract_FailedPaymentCarriesErrorCodeAndSource
    --- PASS: .../fake
    --- PASS: .../client_httptest
```

What that harness can and cannot prove is written up in `DECISIONS.md` at
length, and the short version is on the doc comment of `fakeAPIServer` in
`internal/razorpay/apiserver_test.go`. It proves the client's plumbing. It
proves nothing about Razorpay, because both ends of the exchange marshal
through the same struct tags in `port.go`.

**Every wire shape the client guesses is marked pending fixture capture.** Met.
The endpoint path block in `client.go`, `createOrderBody`,
`createPaymentLinkBody`, and `paymentCollection` each carry the marker, and
`DECISIONS.md` lists them.

**No test needs a credential, a container, or the network.** Met. The whole
suite also passes under `-race`:

```
$ env -u RAZORPAY_KEY_ID -u RAZORPAY_KEY_SECRET go test ./... -count=1 -race
10 packages ok, 0 failures
```

**CI green.** The last push, `118226e`, completed successfully on
`ubuntu-latest` with `go-version: "1.25.x"`, as did every push before it in
this phase.

## The review round

The offline half went through a review agent after the packages were green,
briefed to construct concrete leaks rather than list suspicions. It found six
real defects. Two were credential leaks the green suite was not asking about:
`Client.apiError` truncated the error body before redacting it, so a secret
straddling the 512-byte cut left up to a character short of the whole thing in
every error message, measured at 11 of 22 characters; and the card pattern was
bounded at 19 digits, so a card inside a longer run of digits passed through
whole. The other four were a failed action recorded as a skipped one, a rejected
notification returning a nil error, a silent 1 MiB response truncation, and a
health check waiting on the wrong port.

Each fix was written as a failing assertion first and seen failing.
`PROBLEMS.md` has the findings with their measurements, and `DECISIONS.md` has
the three that changed a design rather than a line.

A green suite is not a reviewed one. Both of the leaks lived in code whose tests
passed, in the same commits that added tests specifically about redaction.

## Additional evidence

`internal/redact` is new and holds the two patterns that used to live in
`internal/audit`. What it cannot do is on the function: a Razorpay key secret is
a bare alphanumeric string with no shape to match, so the control for a secret
is the package that holds it scrubbing before the string leaves, which
`razorpay.Client.Redact` does on every error and every captured body. The
regexes are a backstop for recognisable shapes and are documented as one.

`go.mod` gained
`go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp v0.69.0` and its
transitive `github.com/felixge/httpsnoop v1.0.4`. Phase 0 tidied otelhttp away
because nothing imported it; `Client` does now. `go mod tidy` is clean and
`go.sum` is committed. The MCP SDK is still out, and comes back in phase 3.

## Still unverified

Everything phase 0 listed under this heading is still here, and this phase
added to it rather than clearing any of it.

- All eight cards in `testdata/magic_cards.json` still carry
  `"verified": false`. Nothing here observed a Razorpay response.
- `testcards.PendingRiskBlockCode` and `testcards.PendingSuccessCard` still
  stand in for facts the documentation does not give (PRD Q2, Q3).
- `razorpay.ErrorSourcePendingFixture` and `ErrorStepPendingFixture` are still
  what a failed payment carries in this system.
- Whether Razorpay puts the reason string in `error.code`, `error.reason`, or
  both is still open (PRD Q4). The one synthetic fixture fills both, which
  settles nothing on purpose.
- New this phase: six endpoint paths, two request bodies, and one list envelope
  in `internal/razorpay/client.go` are written from the struct tags in
  `port.go` and have never been checked against a live response.
- New this phase: whether a Razorpay 5xx means the call did not happen. The
  client does not retry 5xx because of it, which is documented as a
  conservative choice rather than a measured one.
- `testdata/recorded/` holds one file and nobody captured it. It is named
  `synthetic_`, its `_meta.synthetic` is true, and `LoadFixtures` refuses to
  load a file whose name and `_meta` disagree, so renaming it does not make it
  a capture.

`results/` is still empty of runs. No number in this report is a recovery rate,
a latency, or a rate limit, because no run has produced one.

## What the live half needs, in order

Start here. Each step names what unblocks it and what closes it.

**0. Unblock.** Two things this machine did not have on 2026-08-31:
`RAZORPAY_KEY_ID` and `RAZORPAY_KEY_SECRET` for test mode in `.env`, and a
running docker daemon. `make preflight` reports both, and it has to come back
with zero hard failures before step 1.

**1. Prove the credentials reach Razorpay before spending anything else on
them.** Write `scripts/verify-razorpay-auth.sh`: one `FetchOrder` for an id
that does not exist, through `razorpay.Client`, expecting a 404 rather than a
401. That separates a wrong key from a wrong path, and it is the cheapest call
in the API. If it returns 401, stop and fix the key. If it returns something
neither 401 nor 404, that is the first real fact about the API and it goes in
`PROBLEMS.md`.

**2. Confirm the endpoint paths and the order wire shape.** `CreateOrder` then
`FetchOrder` against test mode, with `RawCapture` pointed at a file. Compare
the captured body to the `Order` struct tags. Every mismatch is a decode bug
waiting to return an empty string, and it fixes `pathOrders` and
`pathOrderByID` at the same time.

**3. Write `scripts/capture-fixtures.sh` around the capture hook.** It sets
`RawCapture`, runs a call sequence, and turns each captured line into a
`testdata/recorded/` file in the `Fixture` shape: `_meta` with `captured_at`,
`request` with method and path, `response` with status and body. No
`synthetic_` prefix, and `_meta.synthetic` false, which `LoadFixtures` enforces
against the filename in both directions.

**4. The PRD Q1 spike, timeboxed to 90 minutes.** Drive one test-mode order
from `created` to `paid` and write down the exact call sequence in
`DECISIONS.md`. `AttemptPayment` is on the fake and not on `Port` because a real
attempt happens in checkout, and both contract harnesses currently reach past
the client into the fake for it. If 90 minutes does not settle it, stop: per
ADR-0004 that costs batch size in the live table, not the build. Record what
was tried.

**5. Capture one failed payment and settle PRD Q4.** Pay an order with
`4100280000080001`, the documented `insufficient_fund` card, and capture the
payment. Read which of `error.code` and `error.reason` actually carries
`insufficient_fund`, and what `error.source` and `error.step` hold. That closes
Q4, replaces `ErrorSourcePendingFixture` and `ErrorStepPendingFixture` with real
values, and gives `TestClassifierHandlesEveryRecordedErrorPayload` its first
real input, at which point it stops logging a skip and starts asserting.

**6. Walk the card table.** All eight cards, one order each, capture each
failure. Flip `"verified": true` only on the cards whose documented code came
back, and leave the rest false with a note saying what came back instead. A
card that does something other than its documentation is a finding worth more
than a green flag. While you are there, look for the risk-block code (Q2) and
the success card (Q3), and replace `PendingRiskBlockCode` and
`PendingSuccessCard` if either turns up.

**7. Capture a payment link.** `CreatePaymentLink` then the resend call.
Correct the `PaymentLink` field set and `CreatePaymentLinkRequest` in
`port.go`, correct `createPaymentLinkBody` in `client.go`, and check whether
the resend response carries anything that would make `NotifyReceipt.Accepted`
better than the 2xx it currently comes from. Whatever the response says,
`Receipt.DeliveryConfirmed` stays false: no HTTP response reports a person
reading a message.

**8. Measure the rate limit (PRD Q5).** Run calls at increasing concurrency
through `Client` with backoff on, and record the request rate that produced the
first 429, with the date. `DefaultMaxAttempts`, `DefaultBaseBackoff`,
`DefaultMaxBackoff`, and `DefaultMaxConcurrent` in `client.go` are a starting
point, not a measurement, and this is what moves them.

**9. Add the `live` contract harness.** Register a `live` entry in
`contractHarnesses` and run it with
`RZP_CONTRACT_HARNESSES=live go test ./internal/razorpay/`. It stays out of the
default set on purpose, because it spends real API calls. Put the integration
tests behind a build tag so `make ci` never reaches for a key.

**10. `make demo` and `AUDIT-TRACE-SCHEMA.md`.** Both need a real run behind
them. The schema document describes the span attributes and ledger fields a run
actually produced, and the attribute keys are already constants in
`internal/audit`, so it is a matter of running the loop with Jaeger up and
writing down what came out. It was deliberately not drafted from the test
assertions, because a schema document that describes a test rather than a run
is the kind of thing that gets quoted later.

Two things to carry into that work. First, `results/` is where every number
goes, and a number without a run behind it does not go in a document. Second,
every table gets its layer, per ADR-0004, and nothing sums across layers.

## What phase 2 inherits

Unchanged from the phase 0 report, plus:

- `internal/policy` is still a doc comment. ADR-0003 has the two-layer design
  and `recovery.ActionFunc` is the seam it plugs into.
- `internal/store` is still a doc comment. The orchestrator holds no state
  between orders.
- `scripts/seed-batch.sh` needs the `rzp seed` subcommand behind it.
  `internal/batch` already has everything it will call.
- The PRD's requirement tables still say "Planned, phase 1" for FR-RZP-8
  through FR-RZP-10, FR-POLL-1, FR-POLL-2, FR-NOT-1, FR-NOT-2, and FR-AUD-1
  through FR-AUD-3. The offline half covers the testable part of several of
  those. Those rows get their covering tests filled in when phase 1 closes,
  which is also when the PRD freezes, and not before: half of FR-RZP-9 and all
  of FR-RZP-10 need the live half.
