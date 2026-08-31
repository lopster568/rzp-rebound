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
96cab4e docs(phase-1): plan and test list for the offline half, before any code
bff7b18 test(phase-1): the offline half red, with declarations and no bodies
94ddf8c feat(razorpay): net/http client with basic auth, 429 backoff, spans, redaction, and a capture hook
6034a74 feat(razorpay): replay client over recorded fixtures, with one labelled synthetic file
0b1c937 feat(poller): poll to a terminal order state on an injected clock, timeout reports last state
f3ccf71 feat(audit): dual-sink recorder, span attributes and a JSONL ledger with redaction
aca9991 feat(notify): notifier port, mock, and a receipt that reports an API call and nothing about a person
1672062 feat(recovery): orchestrator first slice, outcome read back from the gateway
ffc9fe7 build(scripts): jaeger up and down with a health wait, seed skeleton, make targets
5dd5e56 docs(phase-1): decisions, problems, and the offline-half report
2205320 fix(razorpay): scrub credentials out of captured JSON response bodies
99dbbfa test(razorpay): assert no credential reaches an otelhttp span attribute
fcaf281 fix: redact before truncating, close the card-pattern gap, and correct four review findings
```

The last three are the review round. `2205320` and `fcaf281` each close a
credential leak that the green suite had not been asking about, and
`PROBLEMS.md` has both with the measurement that found them.

`96cab4e` holds `PLAN.md` and `TESTS.md` and no code. `bff7b18` is the red
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
Met, from commit `bff7b18`. The `## Red run` section carries the full output and
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

**CI green.** The last push, `fcaf281`, completed successfully on
`ubuntu-latest` with `go-version: "1.25.x"`, as did every push before it in
this phase.

## The review round

The offline half went through a hostile review pass after the packages were
green, briefed to construct concrete leaks rather than list suspicions. It
found six real defects. Two were credential leaks the green suite was not
asking about:
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

## The review round

The live half went through two hostile reviews before anything was pushed, run
in parallel and briefed separately: one on correctness, one told to construct
credential leaks rather than list suspicions. Both were told to reproduce a
finding or not report it.

They found ten defects worth fixing. `PROBLEMS.md` has all of them with their
reproductions; the three that would have shipped as real problems:

**Both clients followed redirects anywhere.** Neither set a `CheckRedirect`.
The attempter is the one that matters: two of its four calls carry `key_id` in
the query and the callback carries it as a path segment, so a 302 handed a
foreign host half a credential pair and a 307 replayed the form body, which is
the key id, the card number, and the CVV. Reproduced against a foreign test
server. Both clients now refuse a hop off their own origin and still follow the
same-origin callback the sequence depends on, which `make test-integration`
against live test mode confirms.

There is an uncomfortable lesson in the ordering. The span-attribute fix
earlier in this phase moved the key id out of the span and left it in the URL.
Go strips the `Authorization` header across a redirect and never strips a URL,
so that fix left the checkout path with the weaker credential placement and no
hop policy at all.

**`apiError` redacted before parsing, which silently disabled the not-found
mapping.** `redact.Value` replaces any run of 13 or more digits, and an
unquoted JSON number is that shape, so a millisecond epoch in the error
envelope left a document that no longer parsed. `Description` came back empty
and `mapNotFound` stopped recognising the case `ErrOrderNotFound` exists for.
The same class of ordering mistake as the truncate-before-redact bug from the
offline round.

**A 2xx with an empty body invented state on every read.** The tolerance was
written for the resend and applied to all six calls, so `ListPaymentsForOrder`
returned an empty slice and a nil error, which is a positive claim that an
order has no attempts on it.

Also worth recording: the rate-limit probe could not detect a 429 that the
backoff retried successfully, so the PRD Q5 claim in this report rested on an
instrument that could not have contradicted it. It now counts beneath the retry
loop, and the re-run produced the numbers quoted above.

Four credential leaks have now been found in this project, all four in code
whose tests were green: two in the offline round, the span attribute, and the
redirect. None of them was carelessness in the redaction code. Each lived on a
surface the redaction tests were not asking about, and each was found by
somebody told to build a leak rather than to read for one.

One limit is documented rather than fixed, in `PROBLEMS.md`: any encoded form
of the key id defeats both the literal replacement and the shape patterns, and
every downstream guard uses the same patterns. There is no evidence Razorpay
returns one, and encoding-aware redaction is an unbounded problem, so it is
written down next to the existing statement that a key secret has no shape a
pattern can find.

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

# Phase 1 report, live half

Written 2026-08-31, the same day as the offline half. The live half was pulled
forward from its 2026-09-01 date because both blockers cleared: test-mode
credentials landed in `.env`, and a docker daemon became reachable on another
machine over ssh.

Everything below ran against Razorpay test mode with real credentials. Nothing
below involves live mode, real money, or a message sent to a person.

## The checklist, item by item

The offline report handed this half ten numbered steps. All ten ran.

**0. Unblock.** Done. `.env` gained the test-mode key pair, and
`DOCKER_SSH_HOST` moved the docker check to the build machine. `make preflight`
reports zero hard failures and zero warnings:

```
preflight: toolchain
  ok    go go1.25.0
  ok    jq jq-1.7
  ok    claude CLI at /home/oni/.local/bin/claude
  ok    docker daemon reachable on <build machine> (Docker version 29.6.1, build 8900f1d)
preflight: credentials (test mode)
  ok    RAZORPAY_KEY_ID is set
  ok    RAZORPAY_KEY_SECRET is set

preflight: passed with 0 warning(s)
```

Jaeger came up on the build machine through `make jaeger-up`, and a span from
`internal/telemetry` landed in it and was read back by trace id before any
other work started.

**1. Prove the credentials.** Done, as `rzp auth-probe` and `make auth-probe`.
It found the first real fact of the phase immediately: the checklist expected a
404 for an order that does not exist and test mode answers 400. `PROBLEMS.md`
has the entry and `mapNotFound` was fixed for it.

**2. Confirm the endpoint paths and the order wire shape.** Done. All six
endpoint paths in `client.go` were correct. The `Order` json tags were correct.
`paymentCollection` was correct. `createPaymentLinkBody` was not, and neither
was `Order.Notes`, and both are below.

**3. `scripts/capture-fixtures.sh`.** Done. Nine real fixtures in
`testdata/recorded/`, listed further down, plus a credential scan the script
runs over what it just wrote.

**4. The PRD Q1 spike, timeboxed to 90 minutes.** Done in 6 minutes 41 seconds
of wall clock, 19:21:57 to 19:28:38 UTC. It is the star entry in `PROBLEMS.md`
and it produced `docs/RAZORPAY-TEST-MODE-NOTES.md`.

**5. Capture one failed payment and settle PRD Q4.** Done. Q4 is settled:
`error_code` carries the coarse class and `error_reason` carries the specific
reason, both populated. `ErrorSourcePendingFixture` and
`ErrorStepPendingFixture` are gone.

**6. Walk the card table.** Done, all eight, one order each. Zero flipped to
verified, which is the finding rather than an unfinished job.

**7. Capture a payment link.** Done. The request body was wrong and is fixed.
The response field set was right. The resend response turned out to carry a
`success` field, which `NotifyReceipt.Accepted` now reads.

**8. Measure the rate limit.** Attempted and unmeasured. No 429 at 1.4 requests
per second over 40 sequential calls, and none across roughly 60 further calls
that day. PRD Q5 stays open, and the client's four retry constants are
unchanged.

**9. The `live` contract harness.** Done. Both `TestPortContract_*` functions
run against Razorpay test mode with no assertion copied, behind the
`integration` build tag.

**10. `make demo` and `AUDIT-TRACE-SCHEMA.md`.** Both done, and the schema
document is written from a run rather than from the test assertions.

## What shipped

| Piece | What it is |
|---|---|
| `internal/razorpay.Attempter` | The PRD Q1 answer. Drives a test-mode payment attempt to a settled state in four checkout calls, with its own spans and no `otelhttp`. |
| `internal/razorpay.Notes` | A named map type that decodes `notes` whether Razorpay sends an object, an empty array, or null. |
| `APIError.Description` and `.Reason` | Parsed out of the error envelope, so `mapNotFound` can recognise the 400 that means a resource is missing. |
| `cmd/rzp` | Three subcommands: `auth-probe`, `capture`, `demo`. The phase 0 scaffold that printed "not implemented yet" is gone. |
| `scripts/capture-fixtures.sh` | Captures fixtures and then scans them for credentials. |
| Remote docker in `scripts/` | `DOCKER_SSH_HOST` in `jaeger-up.sh`, `jaeger-down.sh`, `preflight.sh`, and `lib.sh`. |
| `config.JaegerUIURL` and `TraceURL` | So a run can print a link to its own trace when the UI is on another machine. |
| `docs/RAZORPAY-TEST-MODE-NOTES.md` | The verified and unverified table for test mode. |
| `docs/AUDIT-TRACE-SCHEMA.md` | The span and ledger schema, from a real run. |

## Test counts

| | Offline half | Live half | After review |
|---|---|---|---|
| Test functions, default build | 52 | 64 | 70 |
| Test functions, integration tag | 0 | 5 | 5 |
| Packages with tests | 10 | 10 | 10 |

The twelve default-build functions the live half added are 1 config, 5
attempter, 4 live wire shape, 1 fake, and 1 classify. The review round added
six more. Every one runs with no credential, no container, and no network,
because a fact learned from Razorpay only keeps holding if it becomes an
offline assertion.

The five integration functions plus the two contract functions under the `live`
harness are what spend real API calls.

## Fixture inventory

Nine real captures, none of them hand written, all scrubbed on the way out by
the client's capture hook and scanned afterwards.

| File | Call | Status |
|---|---|---|
| `create_order.json` | `POST /v1/orders` | 200 |
| `fetch_order.json` | `GET /v1/orders/{id}` | 200 |
| `list_payments_empty.json` | `GET /v1/orders/{id}/payments` | 200 |
| `fetch_failed_payment.json` | `GET /v1/payments/{id}` | 200 |
| `list_payments_after_failure.json` | `GET /v1/orders/{id}/payments` | 200 |
| `fetch_order_after_failure.json` | `GET /v1/orders/{id}` | 200 |
| `create_payment_link.json` | `POST /v1/payment_links` | 200 |
| `resend_payment_link_notification.json` | `POST /v1/payment_links/{id}/notify_by/email` | 200 |
| `fetch_missing_order.json` | `GET /v1/orders/{id}` | 400 |

`synthetic_failed_payment_insufficient_fund.json` is still there, still named
and marked synthetic, and still excluded from every measuring test.

`TestClassifierHandlesEveryRecordedErrorPayload` stopped skipping. It now reads
the real failed payment and logs the finding: the reason and code on it
classify as unclassified, so the table is missing an entry, which is exactly
the report it should give.

The checkout responses are captured during a capture run and no file is written
for them. Two of those pages carry the key id in a form action, and a fixture
built from an HTML page serves nothing the replay client can use.

## The spike verdict

A payment attempt is fully drivable server side in Razorpay test mode, in four
HTTP calls, with no browser. The full write-up is in `PROBLEMS.md` and the
sequence is in `docs/RAZORPAY-TEST-MODE-NOTES.md`.

The finding that matters more than the mechanism: the outcome of an attempt is
chosen at the last call by one form field carrying `S` or `F`, and the card
number never reaches it. All eight documented magic cards produced the
identical failure. `error_reason` `payment_failed`, `error_code`
`BAD_REQUEST_ERROR`, `error_source` `gateway`, `error_step`
`payment_authorization`, with no variation.

So zero cards carry `"verified": true`. Each row in
`testdata/magic_cards.json` now records what came back instead, with the date.
The two `upi_vpas` rows are unverified too, with the reason: the UPI creation
endpoint could not be reached server side at all.

## `make demo`

```
rzp demo, against Razorpay TEST MODE
  gateway  live test mode
  traces   otlp
  ledger   results/runs/demo-1788205925.jsonl

1. created  order_TWUzLYqCv75Ji3  100000 paise  status=created
2. attempt  pay_TWUzMUNPkjt3iL  card ****0001  mock bank told: decline
3. loop     poll, classify, act, then read the order back out of the gateway
   -> link    plink_TWV00SXTYlbemP created
   -> notify  notification API call succeeded (delivery_confirmed=false)
   -> retry   pay_TWV02Lh6yabti2  mock bank told: authorize
4. class    the failure classified as unclassified
5. acted    retry_same_instrument
6. outcome  order order_TWUzLYqCv75Ji3 reads paid from the gateway, recovered=true (the action claimed true)

audit rows written: 4, every one carrying trace_id 84775a556f3c0aec9fcd504d00fb77b4
   1 classified               unclassified
   2 notification_requested   unclassified
   3 action_taken             unclassified
   4 outcome_observed         unclassified

What this run is and is not evidence of:
  The order state above was read back from Razorpay after the action, not
  reported by the action. That part is real.
  The outcome of each attempt was chosen by this command and sent to the mock
  bank as a single form field (first attempt: decline, second attempt: authorize).
  Test mode has no other mechanism, so no recovery rate from this layer is
  evidence that the agent's decision was the reason a payment recovered.
  See docs/RAZORPAY-TEST-MODE-NOTES.md and ADR-0004.

trace: http://<build machine>:16686/trace/84775a556f3c0aec9fcd504d00fb77b4
```

That trace holds 30 spans under one root: the order creation, both attempts
with their four named checkout steps each, the poll, the payment link, the
resend, and the read-back. All four ledger rows carry that trace id, and the
demo checks that before printing rather than printing a link the rows might not
belong to.

The class reading `unclassified` is not a defect in the run. It is the only
honest answer to the only reason string test mode produces, and
`DECISIONS.md` has the three options that were weighed.

## Exit criteria

`PLAN.md` wrote its exit criteria for the offline half only, and listed the
live half as a scope boundary rather than a criterion list. Each of those
boundary items is taken here as the criterion it became.

**Integration tests behind a build tag, running against test mode.** Met.
`internal/razorpay/live_test.go` carries `//go:build integration`, and
`make test-integration` runs it.

```
--- PASS: TestPortContract_CreateOrderThenFetchOrderRoundTrips/live (0.83s)
--- PASS: TestPortContract_FailedPaymentCarriesErrorCodeAndSource/live (8.12s)
--- PASS: TestLiveMissingOrderIsReportedAsNotFound (0.89s)
--- PASS: TestLiveFailedPaymentCarriesTheObservedReason (7.26s)
--- PASS: TestLiveSecondAttemptCanPayAnAttemptedOrder (14.43s)
--- PASS: TestLiveResendReportsAnAPICallAndNothingAboutAPerson (1.36s)
--- SKIP: TestLiveRateLimitObservation (0.00s)
ok  	github.com/lopster568/rzp-recovery-agent/internal/razorpay	32.891s
```

**`scripts/capture-fixtures.sh` and a run of it.** Met. Nine fixtures, zero
credentials found by the scan.

**`scripts/verify-razorpay-auth.sh` and a run of it.** Met in substance and not
in form. It is `rzp auth-probe` and `make auth-probe` rather than a shell
script, because the checklist asked for the probe to go through
`razorpay.Client` and a shell script wrapping a Go program to do that is a
wrapper with nothing in it. `scripts/capture-fixtures.sh` is a script because
it has real work of its own after the Go run: the credential scan.

**The 90-minute spike on PRD Q1.** Met, in 6 minutes 41 seconds.

**Flipping any `"verified": false` in `testdata/magic_cards.json`.** Not met,
and correctly not met. Zero cards reproduced their documented code, so zero
were flipped. Every row records what came back instead.

**`make demo` end to end.** Met, output above.

**`AUDIT-TRACE-SCHEMA.md`.** Met, written from trace
`84775a556f3c0aec9fcd504d00fb77b4` and the ledger it wrote.

**`make ci` still green with no keys and no docker.** Met. The live half added
no test that needs either.

```
check-docs: 26 file(s) clean
```

## The PRD questions

| Question | State |
|---|---|
| Q1: how a second payment attempt is made on a failed order | **Answered.** Four checkout calls, `razorpay.Attempter`. |
| Q2: the risk-block error code | Still open. Nothing in the run produced a risk block. `testcards.PendingRiskBlockCode` stands. |
| Q3: the card that forces a success | **Answered, and the answer is that there is no such card.** The outcome is chosen at the last call. `PendingSuccessCard` stays, with its doc comment rewritten from an open question into a finding. |
| Q4: whether the reason is in `error.code`, `error.reason`, or both | **Answered.** Both, with the coarse class in one and the specific reason in the other. |
| Q5: the rate limit | Still open. No 429 was seen at the rates used, which is not a measurement. |

## Still unverified

- All eight magic cards and both UPI VPAs. Every row now says what came back
  instead and on what date.
- PRD Q2 and Q5.
- Whether a Razorpay 5xx means the call did not happen. No 5xx was observed at
  all across the whole day, so the conservative no-retry rule in `Client.do` is
  untested rather than confirmed.
- Whether the documented card reasons are producible through the hosted
  Checkout widget. This project has not driven that widget and says nothing
  about it.
- The orders-list envelope. It was probed and answers with the same
  `entity`/`count`/`items` shape as the payments collection, and it has no
  fixture because `Client` has no method that calls it. Adding untested client
  surface to produce a fixture would have been the wrong trade.

`results/` now has runs in it for the first time, under `results/runs/`, which
is gitignored. No number in this report is a recovery rate.

## The review round

The live half went through two hostile reviews before anything was pushed, run
in parallel and briefed separately: one on correctness, one told to construct
credential leaks rather than list suspicions. Both were told to reproduce a
finding or not report it.

They found ten defects worth fixing. `PROBLEMS.md` has all of them with their
reproductions; the three that would have shipped as real problems:

**Both clients followed redirects anywhere.** Neither set a `CheckRedirect`.
The attempter is the one that matters: two of its four calls carry `key_id` in
the query and the callback carries it as a path segment, so a 302 handed a
foreign host half a credential pair and a 307 replayed the form body, which is
the key id, the card number, and the CVV. Reproduced against a foreign test
server. Both clients now refuse a hop off their own origin and still follow the
same-origin callback the sequence depends on, which `make test-integration`
against live test mode confirms.

There is an uncomfortable lesson in the ordering. The span-attribute fix
earlier in this phase moved the key id out of the span and left it in the URL.
Go strips the `Authorization` header across a redirect and never strips a URL,
so that fix left the checkout path with the weaker credential placement and no
hop policy at all.

**`apiError` redacted before parsing, which silently disabled the not-found
mapping.** `redact.Value` replaces any run of 13 or more digits, and an
unquoted JSON number is that shape, so a millisecond epoch in the error
envelope left a document that no longer parsed. `Description` came back empty
and `mapNotFound` stopped recognising the case `ErrOrderNotFound` exists for.
The same class of ordering mistake as the truncate-before-redact bug from the
offline round.

**A 2xx with an empty body invented state on every read.** The tolerance was
written for the resend and applied to all six calls, so `ListPaymentsForOrder`
returned an empty slice and a nil error, which is a positive claim that an
order has no attempts on it.

Also worth recording: the rate-limit probe could not detect a 429 that the
backoff retried successfully, so the PRD Q5 claim in this report rested on an
instrument that could not have contradicted it. It now counts beneath the retry
loop, and the re-run produced the numbers quoted above.

Four credential leaks have now been found in this project, all four in code
whose tests were green: two in the offline round, the span attribute, and the
redirect. None of them was carelessness in the redaction code. Each lived on a
surface the redaction tests were not asking about, and each was found by
somebody told to build a leak rather than to read for one.

One limit is documented rather than fixed, in `PROBLEMS.md`: any encoded form
of the key id defeats both the literal replacement and the shape patterns, and
every downstream guard uses the same patterns. There is no evidence Razorpay
returns one, and encoding-aware redaction is an unbounded problem, so it is
written down next to the existing statement that a key secret has no shape a
pattern can find.

## What phase 2 inherits

Unchanged from the offline half, plus:

- Jaeger is left **running** on the build machine, as the brief asked. `make
  jaeger-up` and `make jaeger-down` both honour `DOCKER_SSH_HOST`.
- `razorpay.Attempter` is the seam a batch run drives attempts through. It is
  not on `Port` and not behind the policy gate, and `recovery.ActionFunc` is
  still where ADR-0003's gate plugs in.
- The classifier's eight documented reasons are exercised by the fake and by
  nothing live. Any phase 2 scoring that mixes fake-layer and live-layer
  numbers would be summing across ADR-0004's layers, which the ADR forbids.
- `internal/policy` and `internal/store` are still doc comments.
- `scripts/seed-batch.sh` still needs the `rzp seed` subcommand. `cmd/rzp` now
  has a subcommand dispatcher for it to slot into.
