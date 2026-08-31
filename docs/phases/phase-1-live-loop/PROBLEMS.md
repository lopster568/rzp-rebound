# Phase 1 problems

Things that broke, what the cause turned out to be, and what fixed them. Date
every entry. A problem that got worked around rather than fixed says so.

## 2026-08-31: the test list gained a row while the tests were being written

Symptom: `TESTS.md` was committed with 23 new test functions, and the audit
suite came out with 24.

Cause: writing `audit.Recorder` made it obvious that an event with no order id
produces a ledger row nothing can join to a batch manifest, which is a silent
hole in every score computed from that file. There was no test for it.

Fix: added `TestRecorderRejectsAnEventItCannotJoin` and corrected the count in
`TESTS.md` with a note saying which number was written first. The rule is that
the test list goes in before the code, and it did. A list that can never grow
while the tests are being written would just push the missing test to a later
phase.

Cost: 5 minutes.

## 2026-08-31: two red-tree constructors returned nil and panicked the suite

Symptom: the first red run had two panics in it. `internal/audit` died on
`index out of range` and `internal/notify` on a nil pointer dereference, and
each panic took the rest of its package's tests with it. The run showed 22
failures where there were more.

Cause: the phase 0 technique says every function body in the red tree returns a
zero value, and the zero value of `*Mock` is nil. A test that does
`failing.Err = errors.New(...)` on a nil pointer panics before it can assert
anything, and `go test` stops the package at the first panic.

Fix: constructors in the red tree return a zero-valued struct pointer rather
than nil. Still no logic and still nothing but a zero value, and the tests now
reach their assertions. One test also got a length check before indexing a
slice it had just read, which it should have had anyway.

The red run went from 22 visible failures to 25, which is the real number.

Cost: 10 minutes.

## 2026-08-31: the raw capture hook wrote JSON response bodies without scrubbing them

Symptom: found by reviewing `Client.captureResponse` after the package was
green. A response body that parsed as JSON went into the capture line as
`json.RawMessage(body)`, straight from the wire. Only a body that failed
`json.Valid` went through `Redact`.

Cause: the redaction work was aimed at error messages, and the capture path got
the branch that treats a valid JSON body as already safe. Nothing about being
valid JSON makes a body safe. A gateway that echoes the request into a JSON
error body puts the key id, the key secret, and the base64 basic-auth token
into that stream.

Why it mattered more than the equivalent in an error message: a capture line
becomes a committed file under `testdata/recorded/`. The pre-commit hook blocks
a staged diff containing something shaped like a Razorpay key, but it matches
the two key prefixes, so it would not have caught a base64 token, and the
fixture would have gone into git.

Writing this entry tripped the prose gate, which refused the first draft for
naming those two prefixes literally. Both gates behaved.

Fix: scrub first, then decide how to store it.
`TestClientCapturesRawResponseBody` gained a subtest driving a 500 whose JSON
body echoes all three forms. It was seen failing on all three before the fix.

The fix also checks that the scrubbed text is still valid JSON before storing
it as raw JSON, and falls back to a string when it is not. `[redacted]` carries
no JSON metacharacter, so replacing inside a string value leaves the document
parseable, but a fixture that will not parse is worse than an ugly one and the
check costs one call.

Cost: 20 minutes.

## 2026-08-31: a hostile review of the offline half found two more leaks and four bugs

After the packages were green, the diff went through a hostile review pass
briefed to construct concrete leaks rather than list suspicions. It ran probes
rather than reading. Six findings were real and are fixed; the ones worth
writing down:

**The error body was truncated before it was redacted.** `Client.apiError` cut
the body at 512 bytes and then ran the replacer over the result. A credential
straddling the cut leaves a prefix that no longer equals the string the replacer
looks for, so it went into the error message and from there into any log line
formatting one. Measured with a body arranged to split a 22-character secret:
11 characters survived verbatim. Against a real key secret the leak is up to a
character short of the whole thing, positioned by the gateway rather than by us.

Fix: redact, then truncate. `TestClientRedactsSecretFromErrorMessages` gained a
subtest that writes the response body by hand so the offsets are exact, and it
was seen failing at 11 of 22 before the fix.

**The card pattern failed open on longer digit runs.** `audit.cardLike` was
anchored with `\b` on both ends and capped at 19 digits, so a card number
sitting inside a longer run of digits matched nothing and passed through whole.
Verified on a 23-digit run.

Fix: drop the upper bound and the anchors, and move the pattern into a new
`internal/redact` package that `internal/audit` and `internal/razorpay` both
call, because the client's capture path needed the same pattern and a second
copy would have drifted from the first. The trade-off, that a genuinely long
number now loses its digits, is written on the pattern: nothing this project
writes to a ledger or a fixture is a 13-digit run that has to survive.

**A failed action was recorded as `action_skipped`.** The idiomatic Go failure
from an `ActionFunc` is `return ActionResult{}, err`, which leaves `Kind` empty,
which normalised to `ActionNone`, which chose the skipped row. An action that
ran and errored was indistinguishable in the ledger from one never attempted, so
a scoring pass counting attempts against refusals would have undercounted
attempts. The kind now reads the error as well as the action kind.

**A rejected send returned a nil error.** `notify.SendPaymentLink` had a comment
saying a gateway that answered without accepting is a failed send and not a
quiet success, directly above the line returning `receipt, nil`. To a caller
checking the error first, that is a quiet success. It now returns
`ErrNotAccepted`. The branch had no test either: `Mock.Accepted` is the field
that exists to drive it and nothing set it false, which is why the comment and
the code could disagree without anything noticing.

Two smaller ones: `roundTrip` truncated at the 1 MiB read cap and surfaced it as
a JSON syntax error at some character offset rather than as a truncated
response, and `jaeger-up.sh` health-waited on the query API while its own header
said the wait existed so an exporter would have somewhere to send spans, which
is a different port. Both fixed.

Three test weaknesses the review named, and what was done about each:

- `TestClassifierHandlesEveryRecordedErrorPayload` reported PASS while
  asserting nothing. It now calls `t.Skipf`, so the run says SKIP. A green pass
  on an empty assertion set reads as the classifier having been checked.
- The clock assertion in `TestPollerUsesInjectedClockForBackoff` restated the
  test double: `recordingWait.Wait` advances the clock itself, so the assertion
  could not fail. Removed. The list of three waits is the real proof, because
  the run stops on the third only if elapsed time is read from the injected
  clock, and the comment now says so.
- The `"Authorization"` substring check in `TestClientCapturesRawResponseBody`
  cannot fail, because `RawResponse` has no field a header could reach. Kept as
  a structural guard with a comment saying it starts failing the day somebody
  adds one, on the phase 0 precedent of documenting a test that cannot fail
  rather than deleting it or weakening it into one that can.

The review also left a scratch probe file behind, which a concurrent commit
swept into `d89c0c0`. It was removed in `aa3c251`. It held only the two
placeholder credentials this suite already uses and the well-known 4111 test
card, so nothing needed rewriting out of history, and that was checked rather
than assumed.

Cost: 70 minutes, most of it worth it.

# Live half, 2026-08-31

The live half ran on 2026-08-31, one day ahead of the 2026-09-01 date the plan
gave it. Everything below happened against Razorpay test mode with real
credentials and a Jaeger container on a remote docker host.

## 2026-08-31: PRD Q1, the 90-minute spike, and what it found

This is the entry the phase turns on, so it gets the length.

**The question.** Nothing in this repository could make a payment attempt
happen. `AttemptPayment` existed on `razorpay.Fake` and on neither the port nor
the client, and both contract harnesses reached past the client into the fake
for it, because a real attempt happens in checkout rather than over the server
API. Without an answer, the recovery loop could be built but never driven: no
failed payment to classify, no second attempt to make, no `make demo`.

**The box.** 90 minutes of wall clock, per the brief and per ADR-0004, which
had already said an unanswered live question costs batch size in the live table
rather than the build. Started 19:21:57 UTC, verdict reached at 19:28:38 UTC,
which is 6 minutes and 41 seconds. The write-up took longer than the spike.

**What was tried, in the order the brief gave.**

1. Server-to-server, API only. `POST /v1/payments/create/upi` under HTTP Basic
   auth answered 400 with "The requested URL was not found on the server".
   `POST /v1/payments/create/ajax` and `POST /v1/payments` under Basic auth both
   answered 401. S2S is not enabled on this account and asking for it is not a
   thing a buildathon entry can wait on.
2. UPI `success@razorpay` server side. `POST /v1/payments/create/upi` with
   `key_id` in the form body answered 401 with "Please provide your api key for
   authentication purposes", and the same with `key_id` in the query string.
   Dead end, and the two `upi_vpas` rows in the card table stay unverified.
3. The checkout endpoints, driven directly. This worked, on the first attempt.
   `POST /v1/payments/create/ajax` with `key_id` as a **form field** and no
   Basic auth returned 200, a `payment_id`, and a redirect to an authenticate
   step. Following that redirect gives an HTML page carrying a form, posting
   that form gives the mock bank page, and posting the bank form with a
   `success` field of `S` or `F` settles the payment.

The headless-browser option and the manual-browser fallback were never needed.

**The verdict.** A payment attempt is fully drivable server side in test mode,
in four HTTP calls, with no browser and no extra dependency. `POST` to
`/v1/payments/create/ajax`, `/v1/payments/{id}/authenticate`,
`/v1/gateway/mocksharp/payment`, `/v1/gateway/mocksharp/payment/submit`. It is
implemented as `razorpay.Attempter`, kept off `Port` and off `Client` because
none of it is documented and it does not exist in live mode.
`docs/RAZORPAY-TEST-MODE-NOTES.md` has the table.

**The finding nobody wanted.** The outcome is chosen at the last call, by one
form field with two values in it. The card number never reaches that call. All
eight documented magic cards were walked, one order each, and every one of them
produced the identical failure: `error_reason` `payment_failed`, `error_code`
`BAD_REQUEST_ERROR`, `error_source` `gateway`, `error_step`
`payment_authorization`. Not one documented reason string came back.

So zero cards were flipped to verified, and that is the result rather than a
gap in the run. The consequences are real and are not being softened:

- `internal/classify` maps eight reason strings that live test mode never
  produces. The one it does produce, `payment_failed`, names no cause, so it
  classifies as unclassified and is not retry eligible. Every live demo run
  therefore classifies as unclassified, which is the honest answer and looks
  worse than a made-up mapping would.
- No recovery rate from the live layer can be evidence that an agent's decision
  caused a recovery, because the outcome is selected by the caller. `make demo`
  prints exactly that, in the run output, every time.
- The documented codes are not established as wrong. They may well come from
  the hosted Checkout widget simulating a decline in its own front end. This
  project has not driven that widget and says nothing about it.

**Cost:** 7 minutes for the spike, about 40 minutes for the card walk, the
notes document, and the classifier decision.

## 2026-08-31: a missing resource is a 400, not a 404

Symptom: the auth probe in the phase 1 checklist expected a 404 for an order id
nobody created. Test mode answered 400.

Cause: Razorpay does not use 404 for a resource that is not there. It answers
400 with `error.description` set to "The id provided does not exist". Confirmed
against an order id and a payment link id. A malformed id gives a different 400
with the description "<id> is not a valid id" and `error.reason`
`input_validation_failed`.

Why it mattered: `mapNotFound` matched on the status code alone, so
`errors.Is(err, ErrOrderNotFound)` was false for the exact case the sentinel
exists to catch. Every missing-resource call surfaced as a bare `APIError` and
any caller branching on the sentinel took the wrong branch.

Fix: `APIError` now parses `error.description` and `error.reason` out of the
envelope before truncating the body, and `mapNotFound` treats a 400 carrying
that description as a missing resource. The 404 branch stays, because it costs
nothing and a gateway that starts answering the documented way should not break
this.

Matching on a description string is fragile and the fragility is written on the
function rather than hidden. A 400 is also what a malformed id and a rejected
body produce, so the status cannot separate them and the description is the
only field that does. If Razorpay reword the string, this stops recognising a
missing resource and callers see a plain `APIError`, which fails toward
reporting less rather than toward reporting a resource absent that is not.

Cost: 15 minutes.

## 2026-08-31: the payment link body was rejected outright

Symptom: `POST /v1/payment_links` with the body the offline half built answered
400 with `error.description` "extra fields sent".

Cause: `createPaymentLinkBody` sent flat `notify_sms` and `notify_email`
fields, written from the field names on `CreatePaymentLinkRequest`. Razorpay
takes a nested `notify` object, `{"sms":bool,"email":bool}`.

This is the good kind of strict. The endpoint validates unknown fields and
refuses the call, so the guess failed loudly on the first real request instead
of being silently ignored and leaving a payment link created with notification
settings nobody asked for.

Fix: nested object, with `TestClientCreatePaymentLinkSendsNestedNotifyObject`
asserting no flat field survives. The `PaymentLink` response struct needed no
change: every field it declares came back.

Cost: 10 minutes.

## 2026-08-31: an order with no notes did not decode at all

Symptom: the `live` contract harness failed within seconds of first being run.

```
CreateOrder: razorpay: decode POST /orders: json: cannot unmarshal array into
Go struct field Order.notes of type map[string]string
```

Cause: an order created **with** notes comes back with `notes` as a JSON
object. An order created **without** them comes back with `notes` as an empty
JSON array. `Order.Notes` was a `map[string]string`, so the whole response
failed to decode.

The failure shape is the worst one available: the order exists in Razorpay, the
caller gets an error, and nothing in the caller knows the id of the thing it
just created. A retry would create a second order.

Why the fixtures did not find it: every order the capture run created was
created with notes on it, so every captured body had an object in that field.
A fixture set is only as good as the calls that made it, which is the argument
for the live harness existing at all rather than being replaced by replay.

Fix: `razorpay.Notes` is a named map type with its own `UnmarshalJSON` that
accepts an object, an empty array, or null. A non-empty array is still an
error, because it is not an empty map with a different spelling and dropping
its contents would lose data that was on the order.

Cost: 20 minutes, most of it spent being glad the harness ran before the demo
did.

## 2026-08-31: the key id was in six span attributes of a real trace

Symptom: found by grepping a Jaeger trace of a working demo run for the
configured key id, before writing `AUDIT-TRACE-SCHEMA.md` from it. It was
there, in `url.full` on six spans.

Cause: `otelhttp` records `url.full`. Two of the four checkout calls take
`key_id` as a query parameter, because that is how the form actions on
Razorpay's own pages are built, and the callback the last one redirects to
carries the key id as a **path segment**. The attempter used the client's
`otelhttp` transport, so every one of those URLs went into a span attribute and
then into the trace backend.

The existing span test did not catch it. `TestClientEmitsClientSpanPerRequest`
asserts no credential reaches a span attribute, and it was passing, because it
runs against `razorpay.Client`, whose documented endpoints authenticate with an
`Authorization` header and put nothing in the URL. The attempter was new
surface and nothing was asking this question of it.

Two fixes were considered and one was rejected:

- Rejected: move `key_id` out of the query into the form body. It works for the
  two `mocksharp` calls, verified live, but the settle call answers 302 and the
  payment only settles once the callback is followed, and that callback URL is
  Razorpay's own with the key id in its path. The credential cannot be kept out
  of the URL, only out of the span.
- Taken: `Attempter` does not use `otelhttp`. It has a plain transport and
  opens its own span per step with attributes it chose, and a URL is not one of
  them.

Nothing was lost by this. `razorpay.checkout.settle` says more about where a
run is than `HTTP POST` ever did, and the trace of a demo run went from ten
identical `HTTP POST` entries to five named ones.

`TestAttempterKeepsTheKeyIDOutOfEverySpanAttribute` reproduces the leak offline
against a backend whose pages carry the key id in their form actions exactly as
the real ones do, and it fails against the old arrangement. The fix was then
confirmed against a real trace: the key id and the secret are both absent from
trace `84775a556f3c0aec9fcd504d00fb77b4`.

This is the third credential leak this phase has found in code whose tests were
green, after the two in the offline review round. All three were in a path
nothing was asking the question of. The pattern is worth stating plainly: the
redaction tests keep passing because they test the surface that was already
thought about.

Cost: 35 minutes.

## 2026-08-31: the first fixture capture wrote a directory that could not load

Symptom: `LoadFixtures` refused the directory the first capture run produced.

```
razorpay: two fixtures answer GET /v1/orders/order_TWUltnSDVIxdYd, and
fetch_order_after_failure.json is the second
```

Cause: the fixture map is keyed on method and path, and the capture sequence
read one order's state twice, before and after the attempt. Same path, two
different bodies. The same happened to the payments collection, empty before
and populated after.

The guard was right and the capture was wrong. Two responses to the same call
cannot both be the answer a replay client gives.

Fix: the capture uses two orders. An untouched one gives the before-state
captures, and a second one is driven to a failed payment for the after-state
captures. `POST /v1/orders` is captured once, from the first, because that path
has the same collision.

Cost: 15 minutes. Worth noting that the duplicate guard was written in the
offline half on general principle and paid for itself on its first contact with
real data.

## 2026-08-31: no rate limit was found, so none is claimed

40 sequential `FetchOrder` calls in 29.067 seconds, a rate of 1.4 per second,
produced zero 429 responses. Roughly 60 more calls across the spike, the card
walk, the captures, and the demo runs produced none either.

That is not a rate limit measurement and PRD Q5 stays open. What it rules out
is a limit low enough to matter at the pace this project currently works at.
`DefaultMaxAttempts`, `DefaultBaseBackoff`, `DefaultMaxBackoff`, and
`DefaultMaxConcurrent` in `client.go` are unchanged and are still a starting
point rather than a measurement, which is what their comment says.

`TestLiveRateLimitObservation` is the probe, behind both the integration build
tag and an `RZP_RATE_LIMIT_PROBE` variable, because a real ramp spends real
calls and nobody should trip one by running the suite.

## 2026-08-31: the live half went through two hostile reviews and they found ten more defects

The offline half was reviewed before it shipped and that round is above. The
live half got the same treatment, split across two reviewers briefed
separately: one on correctness, one told to construct credential leaks rather
than list suspicions. Both were told to reproduce or say nothing.

Between them they found ten defects worth fixing. Six were reproduced against
the code at HEAD before anything was changed, and every fix below went in with
a failing test first.

### The one that mattered most: both clients followed redirects anywhere

Neither `razorpay.Client` nor `razorpay.Attempter` set an
`http.Client.CheckRedirect`, so both followed up to ten hops wherever a
`Location` header pointed.

The attempter is where this bites. `resolve` pins every URL that comes out of
an HTML form action back onto the configured root, and that part works: a
reviewer fed it `//evil.example/x`, `https://api.razorpay.com@evil.example/x`
and a `javascript:` URL, and all were rewritten or rejected. But nothing pinned
the `Location` header, which is the one hop the design depends on, because the
settle call answers 302 and the payment only settles once the callback is
followed.

Reproduced, against a foreign test server:

- A 302 on any of the four steps hands that host the full request URI. Two of
  the calls carry `key_id` in the query and the callback carries it as a path
  segment.
- A 307 on the first call replays the entire form body, which is `key_id`,
  `card[number]`, and `card[cvv]`.

The client is milder, because Go strips the `Authorization` header on a
cross-domain redirect. It keeps it for a different port on the same host or for
a subdomain, and a reviewer confirmed the base64 of the key pair reaching a
foreign server on a port change.

There is a sharp lesson in the ordering here. The fix for the span leak earlier
in this phase moved the key id out of the span attribute and left it in the
URL, which was correct as far as it went. Go strips a header across a redirect
and never strips a URL, so the checkout path was left with the weaker
credential placement and no hop policy at all.

Fix: `pinnedRedirect` on both clients. Same-origin hops are still followed, so
the real callback still works, and the hop ceiling is 3 rather than Go's 10.
The refusal error names the two hosts and never the URL, because a refused
redirect target is exactly the kind of URL that carries a credential.
`TestClientAndAttempterRefuseToFollowARedirectOffTheirOrigin` drives all four
cases and was seen failing on three of them, with the card number and CVV
visible in the failure output. `make test-integration` still passes, which is
what confirms the real callback is same origin.

Both clients also gained a 30 second timeout, which neither had.

### Redacting before parsing silently disabled the not-found mapping

`apiError` scrubbed the body and then parsed the scrubbed text for
`error.description`. `redact.Value` replaces a run of 13 or more digits with a
bare marker, and an unquoted JSON number is exactly that shape, so a
millisecond epoch anywhere in the error envelope left a document that no longer
parsed. The unmarshal error was discarded, `Description` stayed empty, and
`mapNotFound` stopped recognising the one case `ErrOrderNotFound` exists for.

Razorpay's error envelope carries an open-shaped `metadata` object, which
`testdata/recorded/fetch_missing_order.json` confirms, so this was reachable
rather than theoretical. It is also the same class of mistake as the truncation
bug from the offline round: an ordering between redaction and another operation
that nobody wrote a test about.

Fix: parse the raw body, then redact the two extracted fields individually.
`captureResponse` already guarded the same hazard with a `json.Valid` check;
`apiError` had the hazard and no guard.

### A 2xx with an empty body invented state on every read

`do` treated an empty 2xx as a success and left `out` untouched, which was
written for the resend call and applied to all six. Reproduced: `FetchOrder`
returned an order with an empty status and a nil error, and
`ListPaymentsForOrder` returned an empty slice and a nil error.

The second one is the bad one. An empty slice is a positive claim that the
order has no attempts on it, and the poller and the recovery orchestrator both
act on that claim. The comment justifying the tolerance talked about a call
that moves money, and every call site that would have benefited from it was a
read.

Fix: `doWith` takes the tolerance as a parameter and only the resend passes it.

### An attempt that ran and failed was recorded as never made

`AttemptOutcome.Steps` was appended to after a 2xx, so a call that reached
Razorpay and had its effect but whose response failed was absent from the
trail. Reproduced on the worst case: the mock bank was told to authorize, the
response came back 500, and `Steps` said the settle call never happened.

For a project whose argument is that the audit trail says what actually
happened, an attempt recorded as not made is the wrong direction to be wrong
in. A step is now recorded when it is sent, and the doc comment says that
presence means sent rather than succeeded.

### The rate-limit probe could not see the thing it was measuring

`TestLiveRateLimitObservation` counted a rate limit only through
`ErrRetryBudgetExhausted`, which `do` returns after four attempts. A 429 that
the backoff then retried successfully was counted as an ordinary answer, so the
probe could have reported zero 429s while every call had been throttled three
times.

That instrument is what the PRD Q5 claim rests on, so the claim rested on
nothing. Fix: a counting transport underneath the retry loop. Re-run on
2026-08-31: 40 calls, 40 HTTP requests, 30.009 seconds, 1.3 calls per second,
zero 429 responses seen beneath the retry loop. The claim is the same and it
now has an instrument behind it that could have contradicted it.

### Six smaller ones

- **`TraceURL` guarded the empty string, not the invalid trace id.**
  `trace.TraceID.String` never returns empty; a non-recording span context
  renders as 32 zeros. So the "no trace id was recorded" branch in the demo was
  dead and the failure mode was a link to a trace that cannot exist. It now
  checks for 32 hex characters with at least one non-zero.
- **`load_dotenv` mangled ordinary `.env` files.** A quoted value kept its
  quotes, which would authenticate with the quotes included and produce a 401
  an operator cannot explain. A final line with no trailing newline was
  dropped, silently unsetting the last variable. `export NAME=value` and
  `NAME = value` were skipped in silence, and a CR from a Windows editor was
  kept. All fixed.
- **The Makefile loaded `.env` a different way from the scripts.**
  `set -a; . ./.env` is fatal under dash when the file is missing, so a fresh
  checkout could not run `make jaeger-down`, which needs no credentials at all.
  It also gave the file precedence over the caller's environment, the opposite
  of the rule `load_dotenv` implements, and it executes command substitution
  inside a value. The scripts now load their own env and the Makefile goes
  through `load_dotenv` for the Go entrypoints.
- **The fixture credential scan passed on zero files.** `set -uo pipefail` with
  no `-e`, a `find` nothing checked, and no assertion that anything was
  inspected, so a wrong output directory printed a clean result and exited 0.
  For a control the script's own header calls "the last line rather than the
  only one", that is the wrong failure mode. It now refuses a missing directory
  and refuses a scan of zero files.
- **A note that was not a string failed the whole order.** The same defect the
  `Notes` type was added to fix, for a different shape. A dashboard-created
  order with a numeric note would have been undecodable. Non-string values now
  keep their JSON text.
- **The capture line had no size cap.** One checkout page produced a JSONL line
  of over a megabyte; `maxResponseBytes` caps the read and `apiError` truncates,
  and the capture path did neither. Capped at 64KB, stored as a truncated string
  so the line still parses.

Also corrected without a test, because they cannot fail a run: the capture line
recorded a synthetic `/checkout/<step>` path in a stream the client documents
as the record of what Razorpay sent, and it now records the path that answered;
step spans recorded no error status, so a transport failure left a step span
indistinguishable from a successful one; the ledger opened with `os.Create`,
so `-ledger` pointed twice at the same path destroyed the first run's evidence,
and it is `O_EXCL` now; the ledger reader used a default `bufio.Scanner`, which
stops at 64KB on a row that can carry an error string; the second `CreateOrder`
in the capture run had no label of its own; two live tests used fixed receipt
strings; and `compose_run` flattened its arguments with `$*` on the remote
branch while the local branch used `"$@"`.

### What was checked and found clean

Worth recording, because a review that only produces findings reads as though
nothing was verified. All ten files in `testdata/recorded/`, the whole working
tree, and all git history: neither configured credential appears anywhere
except `.env`, which is gitignored. The four ledgers under `results/runs/`:
nothing. `Config.String` masks both halves. Every `fmt.Print` in `cmd/rzp`
prints ids, statuses, a masked card, and redacted errors, checked by running
the usage, missing-credential, and 401 paths. Happy-path spans carry only step,
status, order id, payment id, and outcome.

### One limit that is not fixed, and is not being called fixed

`Client.Redact` matches three literal strings and `redact.Value` matches two
shapes. Any encoded form of the key id defeats all five: a reviewer put a
base64 and a percent-encoded key id through `apiError` and `captureResponse`
and both survived into the capture line, which is what becomes a committed
fixture, with every downstream guard using the same two patterns.

There is no evidence Razorpay returns an encoded key id, and building
encoding-aware redaction is an unbounded problem. What can be said is what this
project already says about a key secret: the control is the package that holds
the credential scrubbing before the string leaves, and the patterns are a
backstop for recognisable shapes. This is a second case where the backstop does
not reach, and it is written here rather than left to be discovered.

Cost: about 90 minutes across both reviews and the fixes.

Four credential leaks have now been found in this project, all four in code
whose tests were green at the time: two in the offline round, the span
attribute, and the redirect. The common shape is not carelessness in the
redaction code. It is that each leak lived on a surface the redaction tests
were not asking about, and each was found by someone told to construct a leak
rather than to read for one.
