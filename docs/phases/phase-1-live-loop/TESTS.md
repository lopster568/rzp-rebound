# Phase 1 tests, offline half

24 new test functions. The suite goes from 28 to 52. Each one is written and
seen failing before the code it covers exists.

This file said 23 and 51 when it was committed on 2026-08-31, before the tests
were written. `TestRecorderRejectsAnEventItCannotJoin` was added while writing
the audit suite, because a recorder that accepts an event with no order id
writes a row nothing can score. `PROBLEMS.md` has the entry.

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
| `TestClientEmitsClientSpanPerRequest` | Each request produces one span of kind client, recorded through an in-memory span recorder, and no span attribute carries the key id, the key secret, or the base64 basic-auth token. |
| `TestClientRedactsSecretFromErrorMessages` | An error carrying a response body that echoes the key id, the secret, and the base64 basic-auth token contains none of the three. |
| `TestClientCapsConcurrencyAtConfiguredLimit` | With the limit set to 2 and 6 calls in flight, the server never sees more than 2 at once. |
| `TestClientCapturesRawResponseBody` | With a capture writer configured, each response appends one JSON line carrying the method, the path, the status, and the body, and no request header. A subtest added on 2026-08-31 also drives a JSON error body that echoes the credentials and asserts none of the three forms survives into the line. See `PROBLEMS.md`. |

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
| `TestRecorderRejectsAnEventItCannotJoin` | An event with no order id or no kind is refused and writes nothing. A row that cannot be joined to a batch manifest cannot be scored. |

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

## Red run

Run on 2026-08-31 with go1.25.0, before any of the six new implementations had
a working body. Every new package holds nothing but type declarations,
constants, and functions returning zero values.

Same technique as phase 0, and for the same reason: the pre-commit hook runs
`go vet ./...`, which type-checks test files, so a tree whose tests name
symbols that do not exist cannot be committed. `PROBLEMS.md` in
`docs/phases/phase-0-foundations/` has the original entry.

One refinement over phase 0. The constructors in the red tree return a
zero-valued struct pointer rather than nil, because two of them returning nil
made the tests panic on a nil dereference, and a panic aborts the whole package
before the rest of its tests get to fail. Assertion failures name what the code
is supposed to do; a stack trace does not.

25 of the 52 test functions failed: 23 of the 24 new ones, plus the two
`TestPortContract_*` functions, which now fail on their new `client_httptest`
subtest while still passing on `fake`.

The 24th new test, `TestClassifierHandlesEveryRecordedErrorPayload`, passed.
That is the behaviour it was written to have: it excludes the `synthetic_`
fixture on purpose, no real capture exists yet, and it logs a skip and returns.
It starts asserting something the day the live half captures a failure. Making
it fail today would mean pointing it at the synthetic file, which would turn a
hand-written fixture into evidence about Razorpay.

```
?   	github.com/lopster568/rzp-recovery-agent/cmd/rzp	[no test files]
?   	github.com/lopster568/rzp-recovery-agent/cmd/rzp-mcp	[no test files]
--- FAIL: TestRecorderWritesLedgerLineAndSpanAttributesForSameEvent (0.00s)
    audit_test.go:95: wrote 0 ledger line(s), want 1
--- FAIL: TestLedgerLinesAreValidJSONAndCarryTraceID (0.00s)
    audit_test.go:159: wrote 0 ledger line(s), want 3
--- FAIL: TestRecorderRedactsCardAndKeyFieldsFromLedger (0.00s)
    audit_test.go:224: nothing in the ledger line was redacted: ""
    audit_test.go:230: wrote 0 ledger line(s), want 1
--- FAIL: TestRecorderAssignsMonotonicSequencePerOrder (0.00s)
    audit_test.go:285: order_a0000000001 has 0 row(s), want 3
--- FAIL: TestRecorderRejectsAnEventItCannotJoin (0.00s)
    audit_test.go:300: an event with no order id was recorded, so the row cannot be joined to a batch
    audit_test.go:303: an event with no kind was recorded
FAIL
FAIL	github.com/lopster568/rzp-recovery-agent/internal/audit	0.005s
ok  	github.com/lopster568/rzp-recovery-agent/internal/batch	(cached)
ok  	github.com/lopster568/rzp-recovery-agent/internal/classify	(cached)
ok  	github.com/lopster568/rzp-recovery-agent/internal/clock	(cached)
ok  	github.com/lopster568/rzp-recovery-agent/internal/config	(cached)
?   	github.com/lopster568/rzp-recovery-agent/internal/mcpserver	[no test files]
--- FAIL: TestMockNotifierRecordsSendAndReportsAPICallSucceeded (0.00s)
    notify_test.go:70: mock recorded 0 send(s), want 1
--- FAIL: TestReceiptDeliveryConfirmedIsAlwaysFalse (0.00s)
    --- FAIL: TestReceiptDeliveryConfirmedIsAlwaysFalse/api_call_succeeded (0.00s)
        notify_test.go:138: APICallSucceeded = false, want true
        notify_test.go:141: receipt carries no audit phrase
    --- FAIL: TestReceiptDeliveryConfirmedIsAlwaysFalse/api_call_failed (0.00s)
        notify_test.go:141: receipt carries no audit phrase
    --- FAIL: TestReceiptDeliveryConfirmedIsAlwaysFalse/medium_rejected_before_any_call (0.00s)
        notify_test.go:141: receipt carries no audit phrase
--- FAIL: TestNotifierNeverClaimsCustomerNotified (0.00s)
    notify_test.go:152: AuditPhrases returned nothing, so this test would assert over an empty set
FAIL
FAIL	github.com/lopster568/rzp-recovery-agent/internal/notify	0.005s
?   	github.com/lopster568/rzp-recovery-agent/internal/policy	[no test files]
--- FAIL: TestPollerReturnsOnTerminalOrderState (0.00s)
    poller_test.go:109: a paid order did not end the poll
    poller_test.go:115: order status = "", want "paid"
    poller_test.go:118: order id = "", want "order_BpLnfgDsc2WD8F"
    poller_test.go:121: polled 0 times for an already terminal order, want 1
--- FAIL: TestPollerTimesOutAndReportsLastKnownState (0.00s)
    poller_test.go:145: an order that never settled did not report a timeout
    poller_test.go:153: timed-out result carries order id "", want "order_KSiOW4eQ7sklpg"
    poller_test.go:156: last known order status = "", want "attempted"
    poller_test.go:159: timed-out result carries 0 payment(s), want 1
--- FAIL: TestPollerUsesInjectedClockForBackoff (0.00s)
    poller_test.go:193: backoff waits = [], want [100ms 200ms 400ms]
    poller_test.go:196: Result.Waited = 0s, want 700ms
    poller_test.go:202: clock reads 2026-08-31 09:00:00 +0000 UTC, want 2026-08-31 09:00:00.7 +0000 UTC
--- FAIL: TestPollerDetectsFailedPaymentOnOrderStillAttempted (0.00s)
    poller_test.go:223: an order at attempted with a failed payment under it reported no failed payment
FAIL
FAIL	github.com/lopster568/rzp-recovery-agent/internal/poller	0.005s
--- FAIL: TestClientSetsBasicAuthFromKeyPair (0.00s)
    client_test.go:95: the request carried no HTTP Basic auth header
--- FAIL: TestClientCreateOrderPostsExpectedPayload (0.00s)
    client_test.go:150: method = "", want POST
    client_test.go:153: path = "", want /v1/orders
    client_test.go:156: content type = "", want application/json
    client_test.go:160: body amount = <nil>, want 349900 (paise, not rupees)
    client_test.go:163: body currency = <nil>, want INR
    client_test.go:166: body receipt = <nil>, want rcpt_payload
    client_test.go:170: body notes = <nil>, want an object
--- FAIL: TestClientRetriesOn429WithBackoffUpToCap (0.00s)
    --- FAIL: TestClientRetriesOn429WithBackoffUpToCap/gives_up_after_the_attempt_limit (0.00s)
        client_test.go:206: a run of 429s returned no error
    --- FAIL: TestClientRetriesOn429WithBackoffUpToCap/succeeds_once_the_429s_stop (0.00s)
        client_test.go:252: id = "", want order_recovered001
        client_test.go:255: server saw 0 requests, want 3
        client_test.go:258: waited 0 times, want 2
--- FAIL: TestClientDoesNotRetryOn400 (0.00s)
    client_test.go:280: a 400 returned no error
--- FAIL: TestClientEmitsClientSpanPerRequest (0.00s)
    client_test.go:335: recorded 0 client spans for 2 requests, want 2
--- FAIL: TestClientRedactsSecretFromErrorMessages (0.00s)
    client_test.go:356: a 500 returned no error
--- FAIL: TestClientCapsConcurrencyAtConfiguredLimit (2.00s)
    client_test.go:434: only 0 request(s) reached the server, want 2 in flight at once
--- FAIL: TestClientCapturesRawResponseBody (0.00s)
    client_test.go:474: capture wrote 1 line(s), want exactly 1: ""
--- FAIL: TestPortContract_CreateOrderThenFetchOrderRoundTrips (0.00s)
    --- FAIL: TestPortContract_CreateOrderThenFetchOrderRoundTrips/client_httptest (0.00s)
        contract_test.go:162: CreateOrder returned an empty id
--- FAIL: TestPortContract_FailedPaymentCarriesErrorCodeAndSource (0.00s)
    --- FAIL: TestPortContract_FailedPaymentCarriesErrorCodeAndSource/client_httptest (0.00s)
        contract_test.go:201: AttemptPayment: razorpay: order not found: 
--- FAIL: TestReplayServesRecordedFailedPaymentPayload (0.00s)
    replay_test.go:33: id = "", want "pay_synthetic00001"
    replay_test.go:36: the replayed payment carries no order_id, so nothing can join it to an order
    replay_test.go:39: status = "", want "failed"
    replay_test.go:42: error_reason = "", want insufficient_fund
    replay_test.go:46: the replayed payment carries no error_code
    replay_test.go:49: the replayed payment carries no error_source
    replay_test.go:52: the replayed payment carries no error_step
    replay_test.go:62: a synthetic fixture was served to a client that did not ask for one: err = <nil>
FAIL
FAIL	github.com/lopster568/rzp-recovery-agent/internal/razorpay	2.011s
--- FAIL: TestOrchestratorClassifiesThenRecordsAuditEventPerOrder (0.00s)
    recovery_test.go:199: outcome order id = "", want "order_cfnPUA0TSUpxHz"
    recovery_test.go:202: class = "unclassified", want "retry_eligible"
    recovery_test.go:207: processing an order wrote no audit row
--- FAIL: TestOrchestratorRefetchesOrderStateForOutcomeRatherThanTrustingAction (0.00s)
    recovery_test.go:266: the outcome dropped what the action claimed, so the disagreement is invisible
    recovery_test.go:269: final order status = "", want "attempted"
    recovery_test.go:272: action kind = "", want "retry_same_instrument"
    recovery_test.go:278: the action never ran: calls were []
FAIL
FAIL	github.com/lopster568/rzp-recovery-agent/internal/recovery	0.004s
?   	github.com/lopster568/rzp-recovery-agent/internal/store	[no test files]
ok  	github.com/lopster568/rzp-recovery-agent/internal/telemetry	(cached)
?   	github.com/lopster568/rzp-recovery-agent/internal/testcards	[no test files]
FAIL
```

# Phase 1 tests, live half

Written 2026-08-31, the same day as the offline half. Seven new test functions,
taking the suite from 52 to 59.

Six of the seven pin a wire shape or a behaviour that the offline half had
guessed and that a live call corrected. They all run against `httptest` or a
fake, so they stay in `make ci` with no credential: a fact learned from
Razorpay becomes an offline assertion, which is the only way it keeps holding.

The seventh set, in `internal/razorpay/live_test.go`, is behind the
`integration` build tag and does spend real API calls.

## internal/config

| Test | Asserts |
|---|---|
| `TestConfigReadsJaegerUIURLAndBuildsATraceURL` | `RZP_JAEGER_UI_URL` is read, `TraceURL` joins it to a trace id without a double slash, an empty trace id produces no link at all, and an unset variable falls back to the port compose publishes. |

## internal/razorpay (live wire shapes)

All four run against `httptest`.

| Test | Asserts |
|---|---|
| `TestClientTreatsMissingResourceBadRequestAsNotFound` | A 400 whose `error.description` is "The id provided does not exist" maps to `ErrOrderNotFound` and `ErrPaymentNotFound`, and a 400 for a malformed id does not. |
| `TestClientCreatePaymentLinkSendsNestedNotifyObject` | The payment-link body carries a nested `notify` object and no flat `notify_sms` or `notify_email`. |
| `TestClientResendReadsSuccessFieldFromResponseBody` | `Accepted` comes from the `success` field when the body has one, including when it is false, and falls back to the status code when it does not. |
| `TestOrderDecodesNotesWhetherRazorpaySendsAnObjectOrAnEmptyArray` | An order decodes with `notes` as an object, an empty object, an empty array, or null, and a non-empty array is still an error. |

## internal/razorpay (fake, corrected by a live fact)

| Test | Asserts |
|---|---|
| `TestFakeSplitsErrorCodeAndReasonTheWayRazorpayDoes` | The fake puts the coarse class in `error_code` and the specific reason in `error_reason`, with the observed `error_source` and `error_step`. |

## internal/razorpay (Attempter)

Five test functions against a `checkoutBackend` that replays the observed
five-call sequence.

| Test | Asserts |
|---|---|
| `TestAttempterWalksTheFiveCallCheckoutSequence` | The four calls happen in order, the bank form's fields are carried forward rather than rebuilt, and the card number reaches the first call and no call after it. |
| `TestAttempterSendsTheOutcomeTheCallerAsksFor` | The `success` field the mock bank receives is the one the caller asked for, for both values. |
| `TestAttempterRejectsAnOutcomeItCannotDrive` | An empty or invented outcome is `ErrUnsupportedAttemptOutcome` rather than a silent default. |
| `TestAttempterRedactsCredentialsFromItsErrors` | An error quoting a page that echoes both credentials carries neither. |
| `TestAttempterKeepsTheKeyIDOutOfEverySpanAttribute` | No span attribute from an attempt carries the key id or the secret, and the four steps are named spans. |

## internal/classify

| Test | Asserts |
|---|---|
| `TestClassifierLeavesTheObservedLiveReasonUnclassified` | `payment_failed` classifies as unclassified and is not retry eligible, `BAD_REQUEST_ERROR` alone stays never_retry, and `testdata/error_codes.json` lists the observed reason under `_meta.pending`. |

## internal/razorpay (integration tag, spends real calls)

Run with `make test-integration`. Never reached by `make ci`.

| Test | Asserts |
|---|---|
| `TestPortContract_*` under the `live` harness | The two existing contract functions, against Razorpay test mode, with no assertion copied. |
| `TestLiveMissingOrderIsReportedAsNotFound` | A missing order comes back as `ErrOrderNotFound` against the real API. |
| `TestLiveFailedPaymentCarriesTheObservedReason` | A real failed payment carries the four observed error fields, and the order stays at `attempted`. |
| `TestLiveSecondAttemptCanPayAnAttemptedOrder` | A second attempt on an attempted order can move it to `paid`, read back from the gateway. |
| `TestLiveResendReportsAnAPICallAndNothingAboutAPerson` | A payment link with no contact on it still has its resend accepted. |
| `TestLiveRateLimitObservation` | A burst survives without corrupting a response. It measures rather than asserts, and is skipped unless `RZP_RATE_LIMIT_PROBE` is set. |

## Red runs

### internal/config

```
=== RUN   TestConfigReadsJaegerUIURLAndBuildsATraceURL
    config_test.go:163: JaegerUIURL = "", want the value from RZP_JAEGER_UI_URL
    config_test.go:171: TraceURL = "", want "http://build-machine:16686/trace/4bf92f3577b34da6a3ce929d0e0e4736"
    config_test.go:188: JaegerUIURL with RZP_JAEGER_UI_URL unset = "", want "http://localhost:16686"
--- FAIL: TestConfigReadsJaegerUIURLAndBuildsATraceURL (0.00s)
```

### internal/razorpay, the wire shapes and the Attempter

```
--- FAIL: TestAttempterWalksTheFiveCallCheckoutSequence (0.00s)
    attempt_test.go:184: payment id = "", want the id the create call returned
    attempt_test.go:190: calls = [], want [create_ajax authenticate mocksharp_payment mocksharp_submit]
--- FAIL: TestAttempterSendsTheOutcomeTheCallerAsksFor (0.00s)
    --- FAIL: TestAttempterSendsTheOutcomeTheCallerAsksFor/S (0.00s)
        attempt_test.go:240: the bank was sent success="", want "S"
    --- FAIL: TestAttempterSendsTheOutcomeTheCallerAsksFor/F (0.00s)
        attempt_test.go:240: the bank was sent success="", want "F"
--- FAIL: TestAttempterRedactsCredentialsFromItsErrors (0.00s)
    attempt_test.go:285: a 500 on the first call returned no error
--- FAIL: TestPortContract_FailedPaymentCarriesErrorCodeAndSource (0.00s)
    --- FAIL: TestPortContract_FailedPaymentCarriesErrorCodeAndSource/fake (0.00s)
        contract_test.go:213: a failed payment carries no error_source
        contract_test.go:216: a failed payment carries no error_step
    --- FAIL: TestPortContract_FailedPaymentCarriesErrorCodeAndSource/client_httptest (0.00s)
        contract_test.go:213: a failed payment carries no error_source
        contract_test.go:216: a failed payment carries no error_step
--- FAIL: TestClientTreatsMissingResourceBadRequestAsNotFound (0.00s)
    --- FAIL: TestClientTreatsMissingResourceBadRequestAsNotFound/missing_order (0.00s)
        livewire_test.go:117: err = razorpay: GET /orders/order_AAAAAAAAAAAAAA returned 400: {...}, want it to wrap razorpay: order not found
    --- FAIL: TestClientTreatsMissingResourceBadRequestAsNotFound/missing_payment (0.00s)
        livewire_test.go:117: err = razorpay: GET /payments/pay_AAAAAAAAAAAAAA returned 400: {...}, want it to wrap razorpay: payment not found
--- FAIL: TestClientCreatePaymentLinkSendsNestedNotifyObject (0.00s)
    livewire_test.go:151: the body carries a flat notify_sms field, which test mode rejects as an extra field
    livewire_test.go:154: the body carries a flat notify_email field, which test mode rejects as an extra field
    livewire_test.go:158: the body has no nested notify object: map[amount:100000 currency:INR description:probe nested notify_email:false notify_sms:true reference_id:ref_nested_01]
--- FAIL: TestClientResendReadsSuccessFieldFromResponseBody (0.00s)
    --- FAIL: TestClientResendReadsSuccessFieldFromResponseBody/success_false (0.00s)
        livewire_test.go:215: Accepted = true, want false for body {"success":false}
--- FAIL: TestFakeSplitsErrorCodeAndReasonTheWayRazorpayDoes (0.00s)
    livewire_test.go:253: error_code = "insufficient_fund", want "BAD_REQUEST_ERROR", the coarse class Razorpay returns
FAIL
```

One qualification on that run, on the phase 0 precedent of recording a test
that could not fail rather than quietly deleting it.
`TestAttempterRejectsAnOutcomeItCannotDrive` passed in the red tree and should
not have: the red `ErrUnsupportedAttemptOutcome` was a nil `error` variable, and
`errors.Is(nil, nil)` is true, so the assertion could not fail until the
sentinel existed. It fails correctly against an `Attempt` that accepts a bad
outcome, which is what it is for.

### internal/classify

```
=== RUN   TestClassifierLeavesTheObservedLiveReasonUnclassified
    classify_test.go:236: payment_failed is the only failure reason live test mode produces and ../../testdata/error_codes.json does not list it
    classify_test.go:240: payment_failed is not in _meta.pending, so the totality test will demand a class for a reason that names no cause
--- FAIL: TestClassifierLeavesTheObservedLiveReasonUnclassified (0.00s)
```

### The notes decoder, found by the live harness rather than written from a plan

`TestOrderDecodesNotesWhetherRazorpaySendsAnObjectOrAnEmptyArray` was written
after the live contract harness failed on a real call. The harness failure came
first:

```
    contract_test.go:159: CreateOrder: razorpay: decode POST /orders: json: cannot unmarshal array into Go struct field Order.notes of type map[string]string
--- FAIL: TestPortContract_CreateOrderThenFetchOrderRoundTrips/live (0.43s)
```

Then the offline reproduction, which is the test that stays:

```
--- FAIL: TestOrderDecodesNotesWhetherRazorpaySendsAnObjectOrAnEmptyArray (0.05s)
    --- FAIL: .../empty_array (0.00s)
        livewire_test.go:313: FetchOrder with notes []: razorpay: decode GET /orders/...: json: cannot unmarshal array into Go struct field Order.notes of type map[string]string
```

### The span leak, same order of events

`TestAttempterKeepsTheKeyIDOutOfEverySpanAttribute` was written after the key
id was found in a real Jaeger trace. The offline reproduction:

```
--- FAIL: TestAttempterKeepsTheKeyIDOutOfEverySpanAttribute (0.03s)
    attempt_test.go:363: span "HTTP POST" attribute url.full carries the key id
    attempt_test.go:363: span "HTTP POST" attribute url.full carries the key id
    attempt_test.go:382: no span named "razorpay.checkout.attempt". recorded: [HTTP POST]
    attempt_test.go:382: no span named "razorpay.checkout.create_payment". recorded: [HTTP POST]
    attempt_test.go:382: no span named "razorpay.checkout.authenticate". recorded: [HTTP POST]
    attempt_test.go:382: no span named "razorpay.checkout.gateway". recorded: [HTTP POST]
    attempt_test.go:382: no span named "razorpay.checkout.settle". recorded: [HTTP POST]
```

Both of those are the honest order for a bug a live call found: the live
failure is the discovery, and the offline test is what stops it coming back
without a credential in the loop.

## The review round, 2026-08-31

Six more test functions came out of two hostile reviews of the live half, taking
the default build from 64 to 70. Each was written as a failing assertion first
and seen failing against the code as it was. `PROBLEMS.md` has the findings.

| Test | Asserts |
|---|---|
| `TestClientAndAttempterRefuseToFollowARedirectOffTheirOrigin` | Neither client follows a redirect off its configured origin, on a 302 or a 307, and a foreign host receives neither credential nor the card number. A same-origin redirect is still followed. |
| `TestClientStillRecognisesAMissingResourceWhenTheBodyCarriesALongNumber` | A missing-resource 400 still maps to the not-found sentinel when the error envelope carries a 13-digit number, and the body a caller sees is still scrubbed. |
| `TestClientDoesNotInventStateFromAnEmptyResponseBody` | Five read and create calls error on an empty 2xx body rather than returning a zero value, for both 200 and 204. The resend still reports accepted. |
| `TestAttemptOutcomeRecordsAStepThatRanAndThenFailed` | A settle call the mock bank acted on appears in `Steps` even when its response failed. |
| `TestAttempterDecodesEntitiesInHiddenInputValues` | An escaped ampersand in a hidden input value is decoded before the field is carried forward, as it already was for form actions. |
| `TestOrderDecodesNotesThatAreNotStrings` | An order with numeric, boolean, and null notes decodes rather than failing the whole response. |

`TestConfigReadsJaegerUIURLAndBuildsATraceURL` gained two cases rather than a
new function: the all-zero trace id and a malformed one both produce no link.

Red output for the two that carry the most:

```
--- FAIL: TestClientAndAttempterRefuseToFollowARedirectOffTheirOrigin/attempter_307_replays_the_form_body_off_origin
    a foreign host received the key id: /v1/payments/create/ajax form=...card%5Bcvv%5D=123...card%5Bnumber%5D=4100280000080001...
    a foreign host received the card number: /v1/payments/create/ajax form=...
--- FAIL: TestClientAndAttempterRefuseToFollowARedirectOffTheirOrigin/attempter_302_hands_over_the_request_uri
    a foreign host received the key id: /callback/sig/key_id_placeholder form=
--- FAIL: TestClientAndAttempterRefuseToFollowARedirectOffTheirOrigin/client_refuses_an_off-origin_redirect
    the client followed a redirect off its configured origin and reported no error
    the foreign host was contacted 1 time(s)
```

```
--- FAIL: TestClientDoesNotInventStateFromAnEmptyResponseBody/OK
    FetchOrder returned an order and no error for an empty body
    CreateOrder returned an order and no error for an empty body
    ListPaymentsForOrder returned 0 payment(s) and no error for an empty body, which is a positive claim that the order has no attempts
    FetchPayment returned a payment and no error for an empty body
    CreatePaymentLink returned a link and no error for an empty body
```

The card number in that first block is the well-known Razorpay test card and
the key id is this suite's placeholder, so nothing in the output is a real
credential. That it reads like one is the point of the test.
