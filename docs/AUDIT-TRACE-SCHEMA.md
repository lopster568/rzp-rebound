# Audit trail and trace schema

What one run of `make demo` actually emits, written from a run rather than from
the test assertions. Every span name, attribute key, and ledger field below was
read out of Jaeger and out of the JSONL file that run wrote, on 2026-08-31.

The run it describes is trace `84775a556f3c0aec9fcd504d00fb77b4`, 30 spans,
against Razorpay test mode with Jaeger on the build machine. It wrote
`results/runs/demo-1788205925.jsonl`, four rows.

## The two sinks and what joins them

`internal/audit.Recorder` writes one event to two places: attributes on the
span active in the context, and a JSONL line carrying that span's trace id.
They are two views of one event, and `trace_id` is the join. A reviewer reading
a row can open the trace; a scoring pass can read the file with no trace
backend running.

## Trace shape

One run is one trace. Everything nests under a single root, so a reviewer
opening the link a demo prints sees the whole cycle rather than four
disconnected traces.

```
demo.recovery_loop                        cmd/rzp, the root
├── HTTP POST                             create the order (otelhttp, on Client)
├── razorpay.checkout.attempt             the first attempt, driven to a decline
│   ├── razorpay.checkout.create_payment
│   ├── razorpay.checkout.authenticate
│   ├── razorpay.checkout.gateway
│   └── razorpay.checkout.settle
└── recovery.process_order                internal/recovery, carries every audit row
    ├── HTTP GET                          poll: fetch order, list payments
    ├── HTTP POST                         create the payment link
    ├── HTTP POST                         resend the notification
    ├── razorpay.checkout.attempt         the second attempt
    │   └── (the same four step spans)
    └── HTTP GET                          read the order state back
```

`HTTP GET` and `HTTP POST` come from `otelhttp` on `razorpay.Client`. The
`razorpay.checkout.*` spans are opened by `razorpay.Attempter`, which does not
use `otelhttp`. Why is in the next section.

## Span attributes

### `recovery.process_order`

The audit recorder writes onto whatever span is active, and in a demo run that
is this one, so it carries every row's attributes.

| Key | Source | Example |
|---|---|---|
| `rzp.order_id` | `audit.AttrOrderID` | `order_TWUzLYqCv75Ji3` |
| `rzp.audit.kind` | `audit.AttrKind` | `outcome_observed` |
| `rzp.audit.sequence` | `audit.AttrSequence` | `4` |
| `rzp.failure_class` | `audit.AttrClass` | `unclassified` |
| `rzp.proposed_action` | `audit.AttrProposedAction` | `retry_same_instrument` |
| `rzp.policy.verdict` | `audit.AttrPolicyVerdict` | not emitted yet, phase 2 fills it |
| `rzp.policy.rule` | `audit.AttrPolicyRule` | not emitted yet, phase 2 fills it |
| `rzp.detail.*` | one per `Event.Detail` key | see below |

Detail keys are namespaced under `rzp.detail.` so a detail called `kind` cannot
overwrite the event's own kind. The demo run emitted these:

`amount_paid_paise`, `api_call_succeeded`, `attempt_via`, `attempts_seen`,
`audit_phrase`, `claimed_recovered`, `classified_as`, `delivery_confirmed`,
`final_order_status`, `medium`, `notification_audit_phrase`,
`notification_delivery_confirmed`, `outcome_selected`, `payment_link_id`,
`policy_gate`, `poll_timed_out`, `polled_order_status`, `recovered`,
`retry_eligible`, `second_attempt_told_bank`, `second_payment_id`.

Values are strings. A span attribute takes the last write for a key, so a run
of several orders through one span would overwrite; the ledger keeps every row
separately, which is the other reason both sinks exist.

### `razorpay.checkout.attempt` and its four step spans

| Key | On | Example |
|---|---|---|
| `rzp.order_id` | the parent | `order_TWUzLYqCv75Ji3` |
| `rzp.payment_id` | the parent, set after the first call | `pay_TWUzMUNPkjt3iL` |
| `rzp.attempt.outcome` | the parent | `F` or `S` |
| `rzp.checkout.step` | each step span | `create_payment`, `authenticate`, `gateway`, `settle` |
| `rzp.checkout.http_status` | each step span | `200` |

**There is no URL attribute here, and that is deliberate.** Two of the four
checkout calls carry `key_id` as a query parameter, and the callback the last
one redirects to carries it as a path segment, because that is how Razorpay's
own pages are built. `otelhttp` records `url.full`, so instrumenting this
sequence put the key id into six span attributes of one demo run. It was seen
in Jaeger on 2026-08-31 and fixed by taking `otelhttp` off the attempter rather
than by scrubbing afterwards: the span never receives the URL.

`TestAttempterKeepsTheKeyIDOutOfEverySpanAttribute` is the regression test, and
it fails against the old arrangement.

### `HTTP GET` and `HTTP POST`

`otelhttp` v0.69.0 defaults on `razorpay.Client`: `http.request.method`,
`http.response.status_code`, `network.protocol.version`, `server.address`,
`url.full`, plus `otel.scope.*`. The `url.full` on these is safe and is checked
rather than assumed: the documented API authenticates with an `Authorization`
header, which `otelhttp` does not record, so no credential reaches the URL.
`TestClientEmitsClientSpanPerRequest` asserts it.

### Resource attributes

`service.name` comes from `OTEL_SERVICE_NAME` or defaults to
`rzp-recovery-agent`. `telemetry.sdk.language`, `telemetry.sdk.name`, and
`telemetry.sdk.version` come from the SDK. No resource attribute carries
anything from `.env` other than the service name.

## Ledger schema

One JSON object per line, written by `internal/audit.Recorder`. The demo writes
to `results/runs/demo-<unix>.jsonl`, which is gitignored: a run's output is
evidence for whoever ran it, not a committed artefact.

| Field | Type | Notes |
|---|---|---|
| `sequence` | int | Starts at 1 and increases by 1 per order, independently per order. A gap means a row was refused. |
| `order_id` | string | Required. It is what joins a row to a batch manifest, and an event without one is rejected. |
| `kind` | string | One of `classified`, `policy_evaluated`, `action_taken`, `action_skipped`, `notification_requested`, `outcome_observed`. The set is closed. |
| `class` | string, omitted when empty | The failure class. |
| `proposed_action` | string, omitted when empty | `none`, `retry_same_instrument`, `request_reauth`, `request_new_instrument`. |
| `policy_verdict` | string, omitted when empty | Phase 2. |
| `policy_rule` | string, omitted when empty | Phase 2. |
| `trace_id` | string | The join to the trace. Empty when no span was active, which still writes the row: a run whose collector was down has to produce a scoreable file. |
| `span_id` | string | The span the row was written onto. |
| `recorded_at` | string | RFC3339 with nanoseconds, UTC. |
| `detail` | object of string to string, omitted when empty | Free-form context, redacted on the way in. |

A real row from the demo run:

```json
{"sequence":4,"order_id":"order_TWUzLYqCv75Ji3","kind":"outcome_observed",
 "class":"unclassified","proposed_action":"retry_same_instrument",
 "trace_id":"84775a556f3c0aec9fcd504d00fb77b4","span_id":"5c894213bd90ebc4",
 "recorded_at":"2026-08-31T19:52:53.631773875Z",
 "detail":{"amount_paid_paise":"100000","claimed_recovered":"true",
           "final_order_status":"paid","recovered":"true"}}
```

`recovered` and `claimed_recovered` sit next to each other on purpose.
`recovered` comes from a `FetchOrder` after the action; `claimed_recovered` is
what the action said about itself. When they disagree, the row shows the
disagreement rather than resolving it in favour of either side.

## Redaction

Every value on both sinks goes through `internal/redact` on the way in:

- A detail key on the credential or card list has its value dropped whatever
  the value looks like: `api_key`, `card_number`, `contact`, `cvv`, `email`,
  `key_secret`, `phone`, and the rest of `redactedKeys` in `internal/audit`.
- Any remaining value has card-shaped and key-shaped runs replaced with
  `[redacted]`.

What that cannot do is on the function and is worth repeating here: a Razorpay
key secret is a bare alphanumeric string with no shape to match, so no pattern
finds one in ordinary text. The control for a secret is the package holding it
scrubbing before the string leaves, which `razorpay.Client.Redact` does on
every error and every captured body, and which `razorpay.Attempter` does on
every error of its own. The patterns are a backstop.

## Reading a run

1. Run `make demo`. It prints the ledger path and a trace URL.
2. Open the trace URL. `demo.recovery_loop` is the root.
3. Open the ledger. Every row carries the trace id in the URL you just opened.
   The demo asserts this before printing: a row with a different trace id fails
   the run rather than being printed next to a link it does not belong to.

The trace and the ledger are not both evidence. Numbers come from `results/`,
never from the span store, which is why `jaeger-down.sh` removes volumes
without ceremony.
