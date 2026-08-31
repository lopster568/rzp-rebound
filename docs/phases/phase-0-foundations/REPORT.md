# Phase 0 report

Written 2026-08-31, at the end of the phase.

## What shipped

Six packages, 28 tests, all offline. No Razorpay call was made, no key was set,
and docker was not running for any of it.

| Package | What it is |
|---|---|
| `internal/clock` | `Clock` interface, a wall-clock implementation, and `FakeClock` with `Advance`. |
| `internal/testcards` | Loads `testdata/magic_cards.json`. The only card table in the tree, read by both the fake gateway and the batch seeder. Holds the two pending constants. |
| `internal/classify` | Six-value `Class` enum and a total `Classify`. Reads `error.reason`, falls back to `error.code` only when reason is empty, returns `Unclassified` for anything else. |
| `internal/razorpay` | `Port` interface over orders, payments, payment links, and a notification resend, plus typed structs and a deterministic in-memory `Fake`. |
| `internal/batch` | Seeded generator, ground-truth `Manifest`, and `AgentVisibleOrder`, the four-field projection an agent gets. |
| `internal/telemetry` | `NewTracerProvider`: OTLP over gRPC when an endpoint is set, stdout when not, batch span processor, resource with service name, `Shutdown` that runs once. |
| `internal/config` | Environment loading with a fake-gateway default, fail-fast when live access is asked for without keys, and a `String()` that prints neither credential. |

Commits, in order:

```
b4e35ac build(phase-0): bump go.mod to 1.25.0 so the pinned deps resolve
6f6db67 test(phase-0): all seams red
5c63b19 feat(clock): real and fake Clock
9a93106 feat(classify,testcards): error code to recovery class, fail closed
c4f7f37 feat(razorpay): port interface and the deterministic in-memory fake
c910fe0 feat(batch): seeded generator, ground-truth manifest, agent-visible projection
75de246 feat(telemetry): tracer provider, OTLP when configured and stdout when not
b39bf24 feat(config): env loading that fails fast and never prints a credential
9b5bb05 docs(phase-0): green run, decisions, problems, and the phase report
bc49432 refactor(classify,testcards): drop three exported functions nothing calls
```

`6f6db67` is the red commit. It holds all 28 tests and the six packages with
nothing but type declarations and zero-value function bodies in them, and the
red output in `TESTS.md` was produced from that tree. Every commit after it
adds behaviour to a package whose tests were already failing.

## Exit criteria

**`make verify-phase-0` passes with both key variables unset and docker
stopped.** Met.

```
$ env -u RAZORPAY_KEY_ID -u RAZORPAY_KEY_SECRET make verify-phase-0
preflight: toolchain
  ok    go go1.25.0
  ok    jq jq-1.7
  ok    claude CLI at /home/oni/.local/bin/claude
  FAIL  docker is installed but the daemon is not reachable
preflight: credentials (test mode)
  warn  RAZORPAY_KEY_ID is unset. Offline tests still run; live test-mode calls will not.
  warn  RAZORPAY_KEY_SECRET is unset. Offline tests still run; live test-mode calls will not.
error: preflight: 1 hard failure(s), 2 warning(s)
preflight reported problems, continuing (phase 0 needs no docker and no keys)
ok  	github.com/lopster568/rzp-recovery-agent/internal/batch
ok  	github.com/lopster568/rzp-recovery-agent/internal/classify
ok  	github.com/lopster568/rzp-recovery-agent/internal/clock
ok  	github.com/lopster568/rzp-recovery-agent/internal/config
ok  	github.com/lopster568/rzp-recovery-agent/internal/razorpay
ok  	github.com/lopster568/rzp-recovery-agent/internal/telemetry
check-docs: 16 file(s) clean
exit 0
```

The docker line is the preflight hard failure the target is written to
tolerate. It is the criterion being demonstrated, not a problem: the daemon was
down and the phase passed anyway.

**`TESTS.md` contains real red output from before the implementation existed.**
Met, with one qualification worth stating plainly.

The output in the `## Red run` section came from commit `6f6db67`, where no
package had a working body. It is assertion failures rather than compile
errors, because the pre-commit hook runs `go vet ./...`, which type-checks test
files, so a tree where the tests name symbols that do not exist cannot be
committed without `--no-verify`. `PROBLEMS.md` has the entry.

27 of the 28 failed. The exception,
`TestClassifierUnknownErrorCodeIsUnclassifiedAndNotRetryEligible`, passed
against the empty classifier, because it asserts fail-closed behaviour and a
classifier that recognises nothing gives exactly that. No honest edit makes it
fail there.

**The classifier handles every code in `testdata/error_codes.json` and returns
`Unclassified` for anything else.** Met.

```
$ go test ./internal/classify/ -run TestClassifierIsTotalOverKnownRazorpayErrorCodes -v
--- PASS: TestClassifierIsTotalOverKnownRazorpayErrorCodes (0.00s)
    --- PASS: .../payment_timed_out
    --- PASS: .../insufficient_fund
    --- PASS: .../payment_cancelled
    --- PASS: .../card_declined
    --- PASS: .../card_disabled_for_online_payments
    --- PASS: .../card_number_invalid
    --- PASS: .../gateway_technical_error
    --- PASS: .../authentication_failed
    --- PASS: .../BAD_REQUEST_ERROR
    --- PASS: .../GATEWAY_ERROR
```

All ten documented codes plus the pending risk-block constant. The test also
reads an optional `_meta.pending` list from the file and skips anything on it.
That list is empty, so nothing was skipped.

**The batch manifest carries ground truth for every order, and no
agent-visible field leaks it.** Met.
`TestManifestCarriesGroundTruthForEveryOrder` checks a class, an action, an
amount, an attempt budget, and the agreement between the recoverable flag and
the action, on every order including bait.
`TestManifestGroundTruthNeverLeaksIntoAgentVisibleFields` walks
`AgentVisibleOrder` by reflection and then greps the marshalled projection for
every ground-truth value in the manifest.

**`DECISIONS.md` records anything a later phase would have to
reverse-engineer.** Met. Ten entries added in this phase, covering the
toolchain bump, the two pending constants, the reason-before-code precedence,
the pending-fixture error fields, the contract-harness table, the separate
projection type, the bait design, and the timestamp-free manifest.

## Additional evidence

```
$ go test ./... -count=1 -race
6 packages ok, 0 failures
$ make ci
exit 0
$ gh run list --limit 1
completed  success  docs(phases): repoint the commit citations at the SHAs that exist  ci  main  push  33438407103
```

The run id above is the newest green `ci` run on `main` as of 2026-08-31,
on commit `fb250b1`. The workflow and the Go version are unchanged from the
run that first proved this phase green.

CI runs on `ubuntu-latest` with `go-version: "1.25.x"`.

## Still unverified

Every one of the eight cards in `testdata/magic_cards.json` still carries
`"verified": false`. Nothing in this phase touched a live API, so nothing
earned the flag. The fake reproduces what the docs say those cards do, which is
a different claim from what they do.

Two constants stand in for facts the docs do not give:

- `testcards.PendingRiskBlockCode`, because no risk-block code is documented.
- `testcards.PendingSuccessCard`, because no success card is documented.

Two more mark fields the fake fills without knowing the real values:
`razorpay.ErrorSourcePendingFixture` and `razorpay.ErrorStepPendingFixture`.
The `PaymentLink` struct and `CreatePaymentLinkRequest` carry the same warning
in their doc comments.

## What phase 1 inherits

1. Confirm the eight card codes against live test-mode responses and flip the
   `verified` flags on the ones that hold.
2. Find the risk-block code and the success-card number, put them in
   `testdata/`, and replace the two pending constants.
3. Capture a real failed payment and replace `ErrorSourcePendingFixture` and
   `ErrorStepPendingFixture`. Settle whether Razorpay puts the reason string in
   `error.code`, `error.reason`, or both. The fake currently fills both.
4. Capture a payment-link response and correct `PaymentLink` and
   `CreatePaymentLinkRequest`.
5. Write the live client, add it to `contractHarnesses` in
   `internal/razorpay/contract_test.go` as a second entry, and supply
   `AttemptPayment` for it. The two contract tests then run against it with no
   assertion copied.
6. Bring back `github.com/modelcontextprotocol/go-sdk` and `otelhttp` with
   `go get` when the code that imports them is written.

Phase 2 inherits the third bait kind, an order that is already paid, and the
four attempt budgets in `batch.MaxLegitAttemptsFor`, which are an eval choice
made without data.
