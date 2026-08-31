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

After the packages were green, the diff went through a review agent briefed to
construct concrete leaks rather than list suspicions. It ran probes rather than
reading. Six findings were real and are fixed; the ones worth writing down:

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
