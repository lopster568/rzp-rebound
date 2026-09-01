# Razorpay test mode, verified and unverified

What this project has actually observed from Razorpay test mode, and what it
has not. Everything here was produced on 2026-08-31 against a fresh test-mode
account with `RAZORPAY_KEY_ID` and `RAZORPAY_KEY_SECRET` set from `.env`.

Nothing in this file is a reading of the documentation. Every "verified" row
below was seen coming back over the wire on the date in its column, and the
capture that produced it is under `testdata/recorded/` where one exists.

Test mode only. None of these numbers, cards, or endpoints is evidence about
live-mode behaviour, and none of the outcomes below involves real money or a
real person.

## The mechanism, in five calls

This is the answer to PRD Q1: how a payment attempt is driven against an order
programmatically, with no browser. It was found by the 2026-08-31 spike and it
is what `internal/razorpay.Attempter` implements.

| Step | Call | Auth | Notes |
|---|---|---|---|
| 1 | `POST /v1/orders` | HTTP Basic, key pair | Returns the order at `created`. |
| 2 | `POST /v1/payments/create/ajax` | `key_id` as a **form field**, no Basic auth | Form encoded. Returns `payment_id` and a redirect to the authenticate step. The order moves to `attempted` here. |
| 3 | `POST /v1/payments/{id}/authenticate` | `key_id` as a form field | `{id}` has no `pay_` prefix. Returns an HTML page whose `form1` holds the fields step 4 needs. |
| 4 | `POST /v1/gateway/mocksharp/payment` | `key_id` in the query string | The fields come from `form1`. Returns the mock bank page. |
| 5 | `POST /v1/gateway/mocksharp/payment/submit` | `key_id` in the query string | Fields from the bank page plus `success`, which is `S` or `F`. This is what settles the payment. |

Steps 2 through 5 are the checkout front end's own calls, driven directly. They
are not part of the documented server API, which is why `Attempter` is a
separate type from `Client` and is not on `Port`. `DECISIONS.md` for phase 1
has the reasoning.

**The outcome is chosen at step 5, not by the card number.** `success=S` gives a
captured payment and an order at `paid`. `success=F` gives a failed payment and
an order still at `attempted`.

## Verified on 2026-08-31

| Fact | Observed |
|---|---|
| A failed payment's `error_code` | `BAD_REQUEST_ERROR` |
| A failed payment's `error_reason` | `payment_failed` |
| A failed payment's `error_source` | `gateway` |
| A failed payment's `error_step` | `payment_authorization` |
| A failed payment's `error_description` | `Payment failed` |
| A failed payment's `status` | `failed`, with the order still at `attempted` and `amount_paid` 0 |
| A successful payment's `status` | `captured`, with the order at `paid` and `amount_paid` equal to the order amount |
| Both error fields are populated at once | Yes. `error_code` carries the coarse class and `error_reason` carries the specific reason. This settles PRD Q4. |
| Missing resource status code | `400`, not `404`, with `error.description` `The id provided does not exist`. Confirmed on both an order id and a payment link id. |
| Malformed id status code | `400`, with `error.description` `<id> is not a valid id` and `error.reason` `input_validation_failed`. |
| Wrong secret status code | `401`, with `error.description` `Authentication failed`. |
| Order response fields | `id`, `amount`, `amount_paid`, `amount_due`, `currency`, `receipt`, `status`, `attempts`, `created_at`, `notes` all present and matching the json tags in `port.go`. `entity` and `offer_id` are also returned and ignored. |
| Payments-for-order envelope | `{"entity":"collection","count":N,"items":[...]}`. The `count` and `items` tags in `paymentCollection` are correct. |
| Payment link request body | `notify` is a nested object, `{"sms":bool,"email":bool}`. The flat `notify_sms` and `notify_email` fields the offline half guessed are rejected with `400` and `error.description` `extra fields sent`. |
| Payment link response fields | `id` (a `plink_` prefix), `short_url`, `status`, `amount`, `currency`, `reference_id`, `created_at` all present and matching `PaymentLink`. |
| Resend notification response | `200` with the body `{"success":true}`. |
| Resend notification with nothing to send to | `200` with `{"success":true}`. See the honesty note below. |

## The honesty note on the resend call

A payment link created with `notify.sms` false and no customer contact on it at
all still answered `POST /v1/payment_links/{id}/notify_by/sms` with `200` and
`{"success":true}`.

That is the strongest evidence this project has for the rule it already had:
what the resend call reports is that an API call succeeded. It is not evidence
that a message was sent, and it is certainly not evidence that a person read
one. `notify.Receipt.DeliveryConfirmed` stays a false constant, and the audit
phrase stays "notification API call succeeded".

`razorpay.NotifyReceipt.Accepted` now reads the `success` field from the body
instead of inferring acceptance from the status code, because the field turned
out to exist. That is a narrower claim about a wider observation, not a
stronger one.

## Not verified, and why

| Question | State on 2026-08-31 |
|---|---|
| PRD Q2: the risk-block error code | Still unknown. No call in the spike produced a risk block, and nothing in the test account can obviously force one. `testcards.PendingRiskBlockCode` stands. |
| PRD Q3: the card that forces a success | There is no such card. The outcome is chosen at step 5 of the mechanism above, and the same card number produced both a capture and a failure. `testcards.PendingSuccessCard` stands, and its doc comment now records this rather than an open question. |
| PRD Q5: the rate limit | Not measured. The deliberate probe on 2026-08-31 made 40 calls in 30.009 seconds, a rate of 1.3 calls per second, and the counting transport underneath the retry loop saw 40 HTTP requests and zero 429 responses. Roughly 100 further calls that day across the spike, the captures, and the demo runs produced none either. That rules out a limit low enough to matter at this pace and is not a measurement of the limit, so `DefaultMaxAttempts`, `DefaultBaseBackoff`, `DefaultMaxBackoff`, and `DefaultMaxConcurrent` in `client.go` are still a starting point. A number for the limit needs a deliberate ramp, which is phase 2 work. The count comes from beneath the retry loop on purpose: counting at the call boundary, which is what the probe did first, cannot see a 429 the backoff absorbed. |
| The eight documented magic cards | None of them reproduced its documented error code. All eight rows are in the table below. |
| Whether a 5xx means the call did not happen | No 5xx was observed at all, so the conservative no-retry rule in `Client.do` stands untested rather than confirmed. |
| Whether UPI `success@razorpay` can be driven server side | No. `POST /v1/payments/create/upi` answered `400` with `The requested URL was not found on the server` under Basic auth, and `401` with `Please provide your api key for authentication purposes` with `key_id` in the body or the query string. The two `upi_vpas` rows in `testdata/magic_cards.json` stay unverified. |
| Whether `POST /v1/standard_checkout/payments/create/ajax` is an alternative | No. It answered `401` with the same form the working endpoint accepts. |

### Amended 2026-09-01, by reading rather than by running

Two rows above were overtaken by phase 5, which read Razorpay's live-mode error
documentation instead of driving another call. Both are corrections to this
document rather than new observations, and the column above still says what was
true on 2026-08-31.

**PRD Q2 is closed and the answer was never going to come from a spike.** The
risk-block reason is `payment_risk_check_failed`, documented on Razorpay's
live-mode card error page. `testcards.PendingRiskBlockCode` is retired and
`internal/classify` carries the documented string. Nothing this project has run
has still ever produced a risk block, so the reason is documented and not
observed. The decision trigger written for that question looked for the answer
in a fixture capture, and the answer was on a page nobody had read.

**`payment_failed` is not an undocumented string either.** Razorpay documents it
on the same page as the bank declining without providing a specific reason, with
a suggested action of contacting the bank or trying a different card. Everything
this document says about it being the only reason test mode produces is
unchanged and still observed. What changed is that it is a documented generic
decline rather than a mystery. It still classifies as `unclassified`, and the
documented suggested action is the reason: "try a different card" rules out a
same-instrument retry, which is what the fail-closed default already delivers.
Phase 5 `DECISIONS.md` entry 11.

## The magic card table

All eight cards documented in `testdata/magic_cards.json` were driven through
the mechanism above with `success=F` on 2026-08-31, one order each. Every one
of them produced the identical failure.

| Card | Documented reason | Observed reason | Verified |
|---|---|---|---|
| `4100280000090000` | `payment_timed_out` | `payment_failed` | false |
| `4100280000080001` | `insufficient_fund` | `payment_failed` | false |
| `4100280000070002` | `payment_cancelled` | `payment_failed` | false |
| `4100280000060003` | `card_declined` | `payment_failed` | false |
| `4100280000030006` | `card_disabled_for_online_payments` | `payment_failed` | false |
| `4100280000010008` | `card_number_invalid` | `payment_failed` | false |
| `4100280000020007` | `gateway_technical_error` | `payment_failed` | false |
| `4100280000000009` | `authentication_failed` | `payment_failed` | false |

Every row also carried `error_code` `BAD_REQUEST_ERROR`, `error_source`
`gateway`, and `error_step` `payment_authorization`, with no variation across
the eight.

So zero cards were flipped to `"verified": true`, and that is the finding
rather than a gap in the run. The card number does not reach the mock bank
gateway in a form that changes what it returns: step 5 takes a single `success`
field with two values in it, and the reason string is the same one whichever
card started the flow.

What this does not establish is that the documented codes are wrong. They may
well be produced by the hosted Checkout widget, which simulates the decline in
its own front end before the payment ever reaches this path. This project has
not driven that widget and says nothing about it. What it can say is that the
one mechanism that works without a browser does not reproduce them, and that
any recovery logic keyed on those eight strings has nothing behind it in test
mode.

The consequence for `internal/classify` is direct and is written up in
`docs/phases/phase-1-live-loop/DECISIONS.md`: the only failure reason test mode
actually produces is `payment_failed`, which carries no cause, so it classifies
`unclassified` and is not retry eligible. The eight-entry reason table is
exercised by the fake gateway and by nothing live.

## Two credential warnings about the checkout steps

**The pages carry the key id.** The HTML page returned by
`POST /v1/payments/{id}/authenticate` carries the key id inside a form action
URL, and so does the mock bank page after it. Anything that captures a response
from those two steps has to run it through `Client.Redact` before it is written
anywhere, which is what the capture hook already does for every response body.

**So do the URLs, which is worse.** Steps 4 and 5 take `key_id` as a query
parameter, and the callback that step 5 redirects to carries the key id as a
path segment. Two consequences, both found the hard way and both written up in
`PROBLEMS.md`:

- An instrumented HTTP transport records the URL. `otelhttp` records
  `url.full`, so tracing these calls put the key id into span attributes of a
  real run. `razorpay.Attempter` therefore does not use `otelhttp` and emits
  its own spans, which carry no URL.
- A redirect hands the URL to whatever host the `Location` header names. Go
  strips the `Authorization` header across a domain change and never strips a
  URL, so a credential in a URL survives a hop that a credential in a header
  would not. Both clients now refuse a redirect off the origin they were
  pointed at, and follow the same-origin callback the sequence needs.

The general shape is worth stating: on this path the credential is in the URL
and not in a header, so every control that assumes it is in a header does not
apply.
