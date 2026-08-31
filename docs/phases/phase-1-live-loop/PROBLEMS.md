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
