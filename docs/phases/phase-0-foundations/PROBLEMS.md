# Phase 0 problems

Things that broke, what the cause turned out to be, and what fixed them. Date
every entry. A problem that got worked around rather than fixed says so.

## 2026-08-31: dependency go directives are ahead of the toolchain

`GOPROXY=off go mod download` fails with `module
github.com/modelcontextprotocol/go-sdk@v1.7.0 requires go >= 1.25.0 (running
go 1.24.6)`. Not fixed, deferred. See `DECISIONS.md` for the three ways out
and why none was taken during the bootstrap.

Fixed on 2026-08-31 by setting `go 1.25.0` in go.mod and `1.25.x` in CI. The
local go binary is still go1.24.6; `GOTOOLCHAIN` is the default `auto` and a
cached `golang.org/toolchain@v0.0.1-go1.25.0.linux-amd64` was already in the
module cache, so nothing had to be installed and `GOPROXY=off go mod download`
now exits 0. Cost: 10 minutes.

## 2026-08-31: the pre-commit hook will not let a compile-error red run be committed

Symptom: the TDD rule in `CLAUDE.md` wants the failing tests committed before
the implementation, and the plainest way there is tests written against
packages that hold nothing. The pre-commit hook runs `go vet ./...`, which
type-checks `_test.go` files, so every symbol those tests name is undefined and
vet exits non-zero. The commit is refused. `--no-verify` was not an option.

Cause: the hook is doing its job. A tree that does not compile is what it
exists to stop, and TDD wants one commit in that state.

Fix: split declaration from behaviour. The red commit carries the API surface,
meaning types, constants, interfaces, and function signatures, with every body
returning a zero value and no logic in any of them. The tree compiles, vet
passes, and 27 of the 28 tests fail on their assertions. The evidence is
better than a compile error would have been: each failure names the assertion
the test makes rather than the symbol it could not find.

Cost: 20 minutes, most of it deciding rather than typing.

## 2026-08-31: the phase 0 test count was 24 in the docs and 28 in the tables

Symptom: `PLAN.md` and `TESTS.md` both say 24 test functions. Counting the rows
of the seven tables in `TESTS.md` gives 28.

Cause: a bad total in the bootstrap docs. The per-group counts already written
into `PLAN.md` (2, 8, 5, 2, 5, 3, 3) sum to 28, so only the headline was wrong.

Fix: wrote the 28 functions the tables list, and corrected the headline in both
files with a note saying which number was right.

Cost: 5 minutes.

## 2026-08-31: `go test ./... | tee log | head` truncated the red log

Symptom: the captured red run held 124 lines and had no `internal/telemetry` in
it. Counting failures off that file gave 24 of 28, and three of the missing
four looked like tests that had somehow passed.

Cause: `head` exiting closed the pipe, `tee` took the signal, and the file
stopped at whatever had been written. `internal/telemetry` sorts last, so it
was the package that got cut.

Fix: redirect to the file and read it afterwards, rather than piping through
`head`. The real count is 27 of 28.

Cost: 10 minutes, all of it chasing a fourth passing test that did not exist.

## 2026-08-31: one test cannot be seen failing, and that is a property of the test

Symptom: `TestClassifierUnknownErrorCodeIsUnclassifiedAndNotRetryEligible`
passed against a classifier whose body was `return Unclassified`.

Cause: the test asserts fail-closed behaviour. A classifier that recognises
nothing returns `Unclassified` for everything, which is the answer the test
wants. There is no version of the empty package that fails it.

Fix: none, and none wanted. Faking a failure by weakening the assertion would
make the test worse. It is a regression guard: it fails the day someone makes
the default retry eligible. Recorded in the `## Red run` section of `TESTS.md`
rather than papered over.

Cost: 5 minutes.
