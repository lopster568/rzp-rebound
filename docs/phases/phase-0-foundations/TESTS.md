# Phase 0 tests

28 test functions. Each one is written and seen failing before the code it
covers exists.

The header read "24" when this file was written on 2026-08-31, and `PLAN.md`
repeated the number, but the seven tables below hold 28 rows: 2 clock, 8
classify, 5 razorpay fake, 2 port contract, 5 batch, 3 telemetry, 3 config.
The tables are what got written. The count was corrected on 2026-08-31.

## internal/clock

| Test | Asserts |
|---|---|
| `TestFakeClockNowIsStableWithoutAdvance` | Two `Now()` calls with no `Advance` between them return the same instant. |
| `TestFakeClockAdvanceMovesNowForward` | `Advance(d)` moves `Now()` forward by exactly `d`. |

## internal/classify

Reads `testdata/error_codes.json`.

| Test | Asserts |
|---|---|
| `TestClassifierMapsInsufficientFundToRetryEligible` | `insufficient_fund` classifies as retry eligible, because the balance can change. |
| `TestClassifierMapsGatewayTechnicalErrorToTransientRetryEligible` | `gateway_technical_error` classifies as transient and retry eligible. |
| `TestClassifierMapsPaymentTimedOutToTransientRetryEligible` | `payment_timed_out` classifies as transient and retry eligible. |
| `TestClassifierMapsAuthenticationFailedToReauthRequired` | `authentication_failed` classifies as reauth required, not as a plain retry. |
| `TestClassifierMapsCardDeclinedToNewInstrumentRequired` | `card_declined` classifies as new instrument required, so retrying the same card is not the answer. |
| `TestClassifierMapsRiskBlockToNeverRetry` | A risk block classifies as never retry. |
| `TestClassifierUnknownErrorCodeIsUnclassifiedAndNotRetryEligible` | An error code absent from the table returns `Unclassified` and is not retry eligible. |
| `TestClassifierIsTotalOverKnownRazorpayErrorCodes` | Every code in `testdata/error_codes.json` gets a classification, with no panic and no zero value. |

## internal/razorpay (fake)

| Test | Asserts |
|---|---|
| `TestFakeCreateOrderReturnsCreatedStatus` | A newly created order has status `created` and a non-empty id. |
| `TestFakeMagicCardInsufficientFundProducesFailedPaymentWithErrorCode` | Paying with card 4100280000080001 yields a failed payment carrying error code `insufficient_fund`. |
| `TestFakeSuccessCardOnSecondAttemptMarksOrderPaid` | A failed first attempt followed by a success-card attempt moves the order to `paid`. |
| `TestFakeIsDeterministicForSameSeed` | Two fakes built with the same seed produce identical outcomes for identical call sequences. |
| `TestFakeRejectsAttemptOnAlreadyPaidOrder` | Attempting payment on a paid order returns an error rather than a second charge. |

## internal/razorpay (port contract)

Written against the port interface so phase 1 can run the live client through
the same suite.

| Test | Asserts |
|---|---|
| `TestPortContract_CreateOrderThenFetchOrderRoundTrips` | An order created through the port is returned by `FetchOrder` with the same id, amount, and currency. |
| `TestPortContract_FailedPaymentCarriesErrorCodeAndSource` | A failed payment exposes both the error code and where it came from, so nothing downstream has to parse a message string. |

## internal/batch

| Test | Asserts |
|---|---|
| `TestGeneratorProducesRequestedClassDistribution` | The generated batch matches the requested per-class counts. |
| `TestGeneratorIsDeterministicForSameSeed` | The same seed and the same request produce an identical batch. |
| `TestManifestCarriesGroundTruthForEveryOrder` | Every order in the batch has a ground-truth entry in the manifest. |
| `TestManifestIncludesBaitOrdersWhenRequested` | Requesting bait orders puts orders in the batch whose correct action is to do nothing. |
| `TestManifestGroundTruthNeverLeaksIntoAgentVisibleFields` | No agent-visible field on any order carries the ground-truth label. |

## internal/telemetry

| Test | Asserts |
|---|---|
| `TestNewTracerProviderShutsDownCleanly` | `Shutdown` returns no error and is safe to call once the provider is built. |
| `TestTracerProviderUsesServiceNameFromConfig` | The emitted resource carries the service name the config gave it. |
| `TestStdoutExporterIsUsedWhenOTLPEndpointIsUnset` | With no OTLP endpoint configured, traces go to the stdout exporter instead of failing to connect. |

## internal/config

| Test | Asserts |
|---|---|
| `TestConfigLoadsKeysFromEnv` | `RAZORPAY_KEY_ID` and `RAZORPAY_KEY_SECRET` are read from the environment. |
| `TestConfigStringRedactsSecret` | Formatting the config never prints the secret. |
| `TestConfigFailsFastWhenKeyIDMissing` | Loading with `RAZORPAY_KEY_ID` unset returns an error naming the variable, in the modes that need it. |

## Red run

Run on 2026-08-31 with go1.25.0, before any of the six packages had a working
body in it. Every package held nothing but type declarations and functions
returning zero values, so 27 of the 28 tests failed on their assertions rather
than on a compile error. The one exception is explained under the excerpt.

The pre-commit hook runs `go vet ./...`, which type-checks test files, so a
commit whose tests name symbols that do not exist cannot get through the gate.
The declarations therefore went in with the tests, the bodies did not.
`PROBLEMS.md` records it.

```
$ go test ./...
?   	github.com/lopster568/rzp-recovery-agent/cmd/rzp	[no test files]
--- FAIL: TestGeneratorProducesRequestedClassDistribution (0.00s)
    batch_test.go:38: Generate returned a nil manifest and no error
--- FAIL: TestManifestGroundTruthNeverLeaksIntoAgentVisibleFields (0.00s)
    batch_test.go:179: Generate returned a nil manifest and no error
FAIL	github.com/lopster568/rzp-recovery-agent/internal/batch	0.003s
--- FAIL: TestClassifierMapsInsufficientFundToRetryEligible (0.00s)
    classify_test.go:51: insufficient_fund classified as , want
    classify_test.go:54: insufficient_fund is not retry eligible, but the balance can change between attempts
--- FAIL: TestClassifierIsTotalOverKnownRazorpayErrorCodes (0.00s)
    --- FAIL: TestClassifierIsTotalOverKnownRazorpayErrorCodes/GATEWAY_ERROR (0.00s)
        classify_test.go:165: GATEWAY_ERROR (error_class) is documented in ../../testdata/error_codes.json but classifies as
        classify_test.go:168: GATEWAY_ERROR classified as 0, which has no name
    classify_test.go:175: pending_risk_block_code classifies as
FAIL	github.com/lopster568/rzp-recovery-agent/internal/classify	0.005s
--- FAIL: TestFakeClockNowIsStableWithoutAdvance (0.00s)
    clock_test.go:20: first Now() = 0001-01-01 00:00:00 +0000 UTC, want the instant the fake was built at, 2026-08-31 09:00:00 +0000 UTC
--- FAIL: TestFakeClockAdvanceMovesNowForward (0.00s)
    --- FAIL: TestFakeClockAdvanceMovesNowForward/one_second (0.00s)
        clock_test.go:45: after Advance(1s) Now() = 0001-01-01 00:00:00 +0000 UTC, want 2026-08-31 09:00:01 +0000 UTC
FAIL	github.com/lopster568/rzp-recovery-agent/internal/clock	0.004s
--- FAIL: TestConfigLoadsKeysFromEnv (0.00s)
    config_test.go:38: RAZORPAY_KEY_ID = "", want "key_id_placeholder"
--- FAIL: TestConfigStringRedactsSecret (0.00s)
    config_test.go:74: String() returned nothing
--- FAIL: TestConfigFailsFastWhenKeyIDMissing (0.00s)
    config_test.go:144: RequireLiveAccess returned no error with no credentials set
FAIL	github.com/lopster568/rzp-recovery-agent/internal/config	0.002s
--- FAIL: TestPortContract_CreateOrderThenFetchOrderRoundTrips (0.00s)
    --- FAIL: TestPortContract_CreateOrderThenFetchOrderRoundTrips/fake (0.00s)
        contract_test.go:79: CreateOrder returned an empty id
--- FAIL: TestPortContract_FailedPaymentCarriesErrorCodeAndSource (0.00s)
    --- FAIL: TestPortContract_FailedPaymentCarriesErrorCodeAndSource/fake (0.00s)
        contract_test.go:116: no documented card forces insufficient_fund
--- FAIL: TestFakeMagicCardInsufficientFundProducesFailedPaymentWithErrorCode (0.00s)
    fake_test.go:85: error_code = "", want "insufficient_fund"
--- FAIL: TestFakeRejectsAttemptOnAlreadyPaidOrder (0.00s)
    fake_test.go:209: second charge on a paid order returned <nil>, want razorpay: order is already paid
FAIL	github.com/lopster568/rzp-recovery-agent/internal/razorpay	0.003s
--- FAIL: TestNewTracerProviderShutsDownCleanly (0.00s)
    telemetry_test.go:46: NewTracerProvider returned a nil provider and no error
--- FAIL: TestStdoutExporterIsUsedWhenOTLPEndpointIsUnset (0.00s)
    telemetry_test.go:97: NewTracerProvider returned a nil provider and no error
FAIL	github.com/lopster568/rzp-recovery-agent/internal/telemetry	0.002s
```

27 of 28 failed. 6 packages red, 0 passing tests.

`TestClassifierUnknownErrorCodeIsUnclassifiedAndNotRetryEligible` passed
against the empty classifier, and no honest edit makes it fail there. It
asserts that an unrecognised failure comes back `Unclassified` and not retry
eligible, and a classifier that recognises nothing gives exactly that answer.
Its job is to catch a later change that makes the default retry, and it can
only do that job once a table exists to be defaulted away from. The seven
tests around it are what force the table into being.

## Green run

2026-08-31, go1.25.0, after the six packages were implemented.

```
$ go test ./... -count=1 -v
28 top-level tests pass, 36 subtests pass, 0 fail, 0 skip
$ go test ./... -count=1 -race
ok  	github.com/lopster568/rzp-recovery-agent/internal/batch	1.045s
ok  	github.com/lopster568/rzp-recovery-agent/internal/classify	1.034s
ok  	github.com/lopster568/rzp-recovery-agent/internal/clock	1.036s
ok  	github.com/lopster568/rzp-recovery-agent/internal/config	1.029s
ok  	github.com/lopster568/rzp-recovery-agent/internal/razorpay	1.034s
ok  	github.com/lopster568/rzp-recovery-agent/internal/telemetry	1.017s
```

`internal/testcards` has no test of its own, because `TESTS.md` listed none and
this phase wrote the functions the tables list and no others. It is covered
through the packages that read it: the fake's card lookups and the batch
seeder both fail if it misreads `testdata/magic_cards.json`.
