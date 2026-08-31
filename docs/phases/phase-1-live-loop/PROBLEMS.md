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
