# Phase 0 tests

24 test functions. Each one is written and seen failing before the code it
covers exists.

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

(failing output pasted by the implementation step before code was written)
