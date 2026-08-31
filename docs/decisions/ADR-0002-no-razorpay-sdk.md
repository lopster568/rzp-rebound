# ADR-0002: no Razorpay SDK, a typed net/http client instead

| | |
|---|---|
| Status | Accepted |
| Date | 2026-08-31 |
| Applies from | Phase 1 |

## Context

Phase 1 writes the live client behind `razorpay.Port`. The port is six calls:
`CreateOrder`, `FetchOrder`, `ListPaymentsForOrder`, `FetchPayment`,
`CreatePaymentLink`, `ResendPaymentLinkNotification`.

Five things have to be true of whatever sits behind them:

1. An `otelhttp` span per request, because PRD goal 3 wants every decision and
   every call in the trace.
2. Backoff on 429, because no rate limit for test mode is documented anywhere
   in this repository (PRD 9.1).
3. Fixture capture into `testdata/recorded/`, so the replay layer of ADR-0004
   has something to replay.
4. An `httptest` seam, so the two `TestPortContract_*` functions can run
   against the live client's code path offline.
5. Redaction, so no credential reaches a log line or a span attribute (NFR-3).

`github.com/razorpay/razorpay-go` was read on 2026-08-31, at `master`, to see
how much of that it gives:

- Every method in `resources/order.go` returns `(map[string]interface{},
  error)`. `Create`, `Fetch`, `All`, `Update`, and `Payments` all have that
  signature, and so does every `Get`, `Post`, `Patch`, `Put`, and `Delete` on
  `requests.Request`.
- `requests/request.go` contains no match for 429, retry, or backoff.
- `Request.HTTPClient` is an exported `*http.Client`, so a transport can be
  injected.
- `Request.File` calls `log.Fatal` on error, which exits the process. Nothing
  here uploads files, so it is not a blocker, and it is a sample of the error
  discipline in the library.

Whatever the transport, an untyped map has to become an `Order` or a `Payment`
before `internal/classify` can read `error.reason` off it. That typed layer is
work this project does either way.

## Decision

A plain `net/http` client in `internal/razorpay`, satisfying the `Port` that
already exists, with the typed structs already written in `port.go`.

## Consequences

- `Order`, `Payment`, `PaymentLink`, and `NotifyReceipt` are the only shapes in
  the system. Nothing pulls a string out of an `interface{}`, and a renamed
  Razorpay field fails at decode with a message naming the field rather than
  arriving downstream as an empty string.
- One `http.RoundTripper` carries the span, the backoff, the redaction, and the
  fixture capture. All four are configured once.
- The contract tests already run against the fake through
  `contractHarnesses`. The live client joins as a second entry and inherits the
  assertions with nothing copied.
- We own request signing, error decoding, and every API change Razorpay ships.
  Six endpoints is a small enough surface to carry that.
- No dependency added. `go.mod` stays at the five OpenTelemetry modules plus
  the MCP SDK when phase 3 brings it back.

## Alternatives considered

**Use the SDK and wrap it.** The typed layer still gets written, so the SDK
buys request signing and costs a dependency, an extra map allocation per call,
and a second error vocabulary. Fixture capture would happen at the
`Request.HTTPClient` level anyway, which is where our own transport already is.

**Use the SDK raw.** Every call site reads fields out of
`map[string]interface{}` with no compiler help, and `classify.Failure` gets
built from three type assertions that fail silently on a schema change. The
classifier is the component whose correctness the whole eval rests on.

**Generate a client from an OpenAPI spec.** No spec is in `testdata/`, so one
would have to be sourced and trusted first. For six endpoints the generator and
its configuration are more code than the client.
