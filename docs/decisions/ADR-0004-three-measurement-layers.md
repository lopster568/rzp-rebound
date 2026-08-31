# ADR-0004: three measurement layers, never merged in one table

| | |
|---|---|
| Status | Accepted |
| Date | 2026-08-31 |
| Applies from | Phase 1 |

## Context

Every number this project reports comes from a gateway, and the three gateways
available are not equally believable.

The live one also carries the project's biggest unknown. `AttemptPayment` sits
on the fake and not on `Port`, because a real payment attempt happens in
checkout and the API call sequence that reproduces it is open (PRD Q1). The
success card is not documented either (PRD Q3). A build whose headline number
requires the live path to work perfectly is a build that an unanswered API
question can sink on the last day.

Merging the three into one rate would hide exactly the thing a reader needs:
which gateway produced the number.

## Decision

Three layers. Every results row names its layer, and no table sums or averages
across them.

| Layer | Gateway | Batch size | What a number from it is worth |
|---|---|---|---|
| `live` | Razorpay test mode | Smallest, bounded by the rate limit measured in phase 1 | Evidence about the API, in test mode, labelled as test mode |
| `replay` | Fixtures in `testdata/recorded/` | Whatever was captured | Exactly as good as the capture behind it, and offline and deterministic |
| `fake` | `razorpay.Fake` | Any size, instant | A model of documented behaviour, and evidence about our code only |

The fake and the batch seeder read the same card table. `internal/testcards`
loads `testdata/magic_cards.json` once and both callers go through it. Two
copies would drift, and a drift there corrupts every score without announcing
itself: the gateway fails a payment one way while the ground-truth manifest
records another, and every classification score after that is measured against
the wrong answer.

## Consequences

- Q1 costs batch size in the live table, not the build. The replay and fake
  layers produce their numbers whatever happens to the live path, and the
  report says which layer each row came from.
- Three tables where a reader might want one. Nobody can quote a blended rate,
  which is the point of the split.
- A claim is only as strong as the layer on its row, and the layer is on the
  row, so the qualification travels with the number.
- Fake and live disagreeing about a failure class is a finding worth writing
  down. Because both read one card table, the disagreement is about Razorpay
  rather than about two copies of a fixture.
- The fake's numbers are a model. PRD 9.2's labelling rule applies to every one
  of them, and none of them is evidence about Razorpay.
- Adding a layer means adding a `contractHarnesses` entry in
  `internal/razorpay/contract_test.go` and a column to the report, not a new
  scoring path.

## Alternatives considered

**Live only.** The most credible numbers, and the whole result then sits behind
Q1, Q3, and a rate limit nobody has measured yet.

**Fake only.** Fast, free, and offline, and it proves nothing about Razorpay.
The recovery rate would measure our own fake against our own manifest.

**One blended number with a footnote about methodology.** A footnote does not
survive being quoted. The blended rate does, and it would be the least honest
number in the report.

**Two layers, live and fake.** Drops replay, and with it the ability to run a
captured live failure offline in CI. That capture is the only artifact that is
both real and deterministic.

**Separate card tables per layer, so each matches its own gateway.** This is
the drift the shared table exists to prevent. If a layer needs a different
table, that is a fixture question answered in `testdata/`, not two tables in
two packages.
