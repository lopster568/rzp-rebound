# ADR-0006: state comes back by polling, not a webhook listener

| | |
|---|---|
| Status | Accepted |
| Date | 2026-09-01 |
| Applies from | Phase 1 |

## Context

This decision was made on 2026-08-31, when `internal/poller` was built during
phase 1's live loop work. It has no ADR of its own until now: phase 5's
numbering sweep on 2026-09-01 found the gap between ADR-0004 and ADR-0007,
and this file closes it.

`razorpay.Port` is a pull interface. Its six calls, `CreateOrder`,
`FetchOrder`, `ListPaymentsForOrder`, `FetchPayment`, `CreatePaymentLink`, and
`ResendPaymentLinkNotification`, all return current state on request; nothing
in `Port`, and nothing anywhere in this repository, receives an inbound call
from Razorpay. The recovery loop has to learn when an order's state changes,
and the two shapes on the table are push, an endpoint Razorpay calls back
into, or pull, asking the gateway. `internal/poller` is the pull shape:
`Poller.PollUntilTerminal` reads `FetchOrder` and `ListPaymentsForOrder`
through `Port` on a doubling backoff until the order is terminal or a budget
runs out (FR-POLL-1).

Several constraints already in this project rule out the push shape, beyond
simply not having built one yet:

`NFR-2` requires the full unit suite run offline with no credentials and no
docker (`make verify-phase-0`, `make verify-offline`). A webhook receiver
only proves something when a real caller reaches it; testing one offline
means faking an inbound HTTP request against your own handler, which checks
the handler and nothing about whether Razorpay would ever send that request.
A poll runs through the same `Port` interface the fake, replay, and live
gateways all implement (ADR-0002), so the identical `TestPortContract_*`
assertions already run against all three, and `Poller`'s injected
`clock.Clock` and `WaitFunc` seam is what makes "no test sleeps" possible for
a backoff loop at all.

`NFR-1` requires a seed and a spec to reproduce a batch and a gateway run
exactly (`TestGeneratorIsDeterministicForSameSeed`,
`TestFakeIsDeterministicForSameSeed`). A push notification arrives on Razorpay's
own schedule, from a process this project does not control, which a seed
cannot fix. A poll is initiated by this process on an interval it controls, so
the fake layer's clock advances deterministically under a test double and the
live layer's wait is bounded by `MaxWait` rather than open ended.

ADR-0004's three measurement layers all satisfy one `Port`, and a captured
fixture replays as the same call it was captured from: `NewReplay` is the same
`Client` with a fixture transport over `testdata/recorded/`, nothing more
(phase 1 `DECISIONS.md`). A webhook has no equivalent story in that design.
Capturing one delivery and replaying it into a listener that would also have
to run during a batch of runner-driven attempts is a second inbound path next
to the poll, and a fourth thing to add to the `contractHarnesses` table
(phase 0 `DECISIONS.md`) for a signal the pull interface already delivers.

Section 6's product surface runs everything from `make`, on the operator's own
machine: `make seed`, `make run-all`, `make report`, `make demo`. None of them
starts a server that has to be reachable from outside that machine. A webhook
receiver needs a publicly reachable endpoint, which this surface has no room
for and never provisioned.

Finally, `Orchestrator.ProcessOrder` already re-fetches the order after every
action, on every code path, specifically because the project does not trust a
claim about an outcome from anywhere other than a synchronous read of the
gateway: "recovery is read from the gateway, never from the arm"
(`ARCHITECTURE.md`). `ActionResult.ClaimedRecovered` is kept next to
`Recovered` on the `Outcome` for exactly this reason, so a disagreement shows
up in the row instead of being resolved silently toward the claim (phase 1
`DECISIONS.md`). A webhook delivery is the same shape of claim, arriving out
of band, and the project already rejected trusting that shape of claim once.

## Decision

Every order and payment state read this project needs comes from
`internal/poller.Poller.PollUntilTerminal`, calling `FetchOrder` and
`ListPaymentsForOrder` through `razorpay.Port` on a doubling backoff
(`DefaultInterval` 500 milliseconds, doubling up to `DefaultMaxBackoff` 5
seconds), bounded by `DefaultMaxWait` of 2 minutes. Nothing in this project
runs an HTTP listener for Razorpay to call back into, and `Port` has no call
that would receive one.

A poll timeout is a result field, `Result.TimedOut`, not an error; the poller
returns the last order and payments it saw either way. PRD section 7 counts a
timeout as an unscorable outcome that stays in the denominator rather than a
run that waited forever with nothing to show for it (phase 1 `DECISIONS.md`).

`Orchestrator.ProcessOrder` calls `FetchOrder` after the action on every code
path: after a successful action, a failed one, and `DoNothing`. What the
ledger and the trace both carry is that read, never a claim from the action
that ran.

## Consequences

- The live layer's rate limit is unmeasured. No 429 came back at 1.4 requests
  per second over 40 sequential calls on 2026-08-31 (PRD Q5, still open), and
  polling spends real API calls under a concurrency cap of two
  (`LiveMaxConcurrent`) with 429 backoff, in a way a passive listener would
  not. That is a cost this design pays for the offline testability above, and
  it is a cost a webhook does not have.
- The live rig treats a poll as a read of current state, not a wait for one,
  because `materialise` already drove each order's seeded failure
  synchronously before any arm sees it: three reads at a 500 millisecond
  interval and a 1200 millisecond budget are enough to see it
  (`internal/runner/rig.go`). That shortcut only holds because this project
  controls both ends, the seeder and the poller, of every order it drives. A
  poller reading a real customer's payment in production could not assume
  that and would need the full two-minute budget, which is itself unmeasured
  beyond the one walk this project has done.
- A poll can be as stale as one interval; a webhook fires the instant
  Razorpay's own state changes. The design accepts that latency because every
  order's outcome is bounded inside one run by `MaxWait`, and the project's
  own surface is a batch score written to `results/tables/`, not a
  real-time, customer-facing recovery path (non-goal: "Dashboards or UI
  beyond Jaeger and a markdown table in `results/`").
- Whether Razorpay's webhook payload for a failed-then-recovered payment
  carries the same fields `FetchOrder` and `ListPaymentsForOrder` do is
  something nothing in this project has checked. Adopting one later is a new
  fixture-capture question in the shape of PRD Q1 or Q4, not a small code
  change: `Port`'s six calls are all synchronous request and response, and a
  webhook receiver is a new component with no place in that interface, not a
  seventh call on it.

## Alternatives considered

**A webhook receiver, listening for `payment.failed` and `order.paid` and
feeding the same recovery loop.** This is the alternative the decision is
named against. Rejected on the grounds in Context: it needs a reachable
endpoint the make-driven, offline-first product surface has no room for; it
cannot be exercised by the same `Port` contract-harness pattern that gives
the fake, replay, and live layers one shared assertion suite; and it would be
a second "trust an external claim and reconcile it" surface next to
`ClaimedRecovered`, which this project already treats as unreliable enough to
verify by reading the gateway every time rather than trust once.

**A streaming or long-polling subscription, if Razorpay offered one.** Not on
`razorpay.Port`, and outside the non-goal that scopes this project to
"[r]ecovery strategies beyond the six calls in `razorpay.Port`" staying out.
Nothing here was investigated against a subscription API, because the project
scoped its gateway surface to those six documented calls before the question
came up.
