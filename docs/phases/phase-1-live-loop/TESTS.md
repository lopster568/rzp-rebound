# Phase 1 tests, offline half

23 new test functions. The suite goes from 28 to 51. Each one is written and
seen failing before the code it covers exists.

Every test here runs with no Razorpay credential, no docker daemon, and no
network. The tests for the live half are not in this file, because the code
they cover is not written yet.

## internal/razorpay (client)

Runs against `httptest.Server`, so the client's own code path is exercised
without a key or a socket to Razorpay.

| Test | Asserts |
|---|---|
| `TestClientSetsBasicAuthFromKeyPair` | Every request carries HTTP Basic auth built from the configured key id and secret, and no credential appears in the URL or the query string. |
| `TestClientCreateOrderPostsExpectedPayload` | `CreateOrder` POSTs to the orders path with `amount`, `currency`, `receipt`, and `notes` in a JSON body, and decodes the response into `Order`. |
| `TestClientRetriesOn429WithBackoffUpToCap` | A 429 is retried, the wait between attempts grows exponentially, no wait exceeds the configured cap, and the call gives up after the configured attempt limit. |
| `TestClientDoesNotRetryOn400` | A 400 produces exactly one request and an error. A refusal repeated is still a refusal. |
| `TestClientEmitsClientSpanPerRequest` | Each request produces one span of kind client, recorded through an in-memory span recorder. |
| `TestClientRedactsSecretFromErrorMessages` | An error carrying a response body that echoes the key id, the secret, and the base64 basic-auth token contains none of the three. |
| `TestClientCapsConcurrencyAtConfiguredLimit` | With the limit set to 2 and 6 calls in flight, the server never sees more than 2 at once. |
| `TestClientCapturesRawResponseBody` | With a capture writer configured, each response appends one JSON line carrying the method, the path, the status, and the body, and no request header. |

## internal/razorpay (replay)

| Test | Asserts |
|---|---|
| `TestReplayServesRecordedFailedPaymentPayload` | The replay client returns the failed payment recorded in `testdata/recorded/synthetic_failed_payment_insufficient_fund.json`, with the error fields the port contract requires. |
| `TestClassifierHandlesEveryRecordedErrorPayload` | Every real capture under `testdata/recorded/` classifies to a named class and carries an error code or an error reason. Files with the `synthetic_` prefix are excluded, and the test passes with a logged skip while no real capture exists. It becomes meaningful after the live half runs. |

## internal/razorpay (port contract)

No new test functions. `contractHarnesses` gains a second entry, so the two
existing `TestPortContract_*` functions run against the client as well as the
fake, with no assertion copied. What that second harness can and cannot prove
is written up in `DECISIONS.md`.

## internal/poller

All four run against `razorpay.Fake` with a `clock.FakeClock` and an injected
wait function. Nothing sleeps.

| Test | Asserts |
|---|---|
| `TestPollerReturnsOnTerminalOrderState` | Polling stops as soon as the order reads `paid`, and the result carries that order. |
| `TestPollerTimesOutAndReportsLastKnownState` | An order that never reaches a terminal state times out, and the result still carries the last order and payments the poller saw. |
| `TestPollerUsesInjectedClockForBackoff` | The waits the poller asks for follow capped exponential backoff, and the clock moves by exactly those durations. |
| `TestPollerDetectsFailedPaymentOnOrderStillAttempted` | An order sitting at `attempted` with a failed payment under it is reported as a failed payment, not as a terminal state. |

## internal/audit

| Test | Asserts |
|---|---|
| `TestRecorderWritesLedgerLineAndSpanAttributesForSameEvent` | One `Record` call puts the same order id, event kind, and sequence on the active span and in the ledger line. |
| `TestLedgerLinesAreValidJSONAndCarryTraceID` | Every emitted line unmarshals, and its `trace_id` equals the trace id of the span that was active when it was recorded. |
| `TestRecorderRedactsCardAndKeyFieldsFromLedger` | A card number, a key id, and a secret passed in the event detail are replaced before either sink sees them. |
| `TestRecorderAssignsMonotonicSequencePerOrder` | Sequence numbers start at 1 and increase by 1 per order, independently for each order, with events interleaved. |

## internal/notify

| Test | Asserts |
|---|---|
| `TestMockNotifierRecordsSendAndReportsAPICallSucceeded` | The mock records the link id and medium it was called with, and the receipt reports that the notification API call succeeded. |
| `TestReceiptDeliveryConfirmedIsAlwaysFalse` | `DeliveryConfirmed` is false on the success path, on the API-error path, and on the unsupported-medium path. |
| `TestNotifierNeverClaimsCustomerNotified` | None of the forbidden phrasings appears in any audit string the package can emit, checked over every exported audit phrase and every receipt both paths produce. |

## internal/recovery

| Test | Asserts |
|---|---|
| `TestOrchestratorClassifiesThenRecordsAuditEventPerOrder` | Processing one order writes an audit event carrying the order id and the class the failure classified to. |
| `TestOrchestratorRefetchesOrderStateForOutcomeRatherThanTrustingAction` | An action that claims the order was recovered while the gateway still reads `attempted` produces an outcome of not recovered, and `FetchOrder` is called after the action. |
