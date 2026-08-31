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

## What phase 1 changed, 2026-08-31

The decision stands unchanged. Two statements in the context above were true
when this was written and are not any more, and a reader should not take them
for current fact.

**`AttemptPayment` is no longer only on the fake.** PRD Q1 is answered.
`razorpay.Attempter` drives a test-mode payment attempt through four
undocumented checkout calls, so the live layer can now run a full recovery
cycle. It is still deliberately off `Port`, so the sentence "adding a layer
means adding a `contractHarnesses` entry" still holds and the `live` entry was
added exactly that way.

**The success card is not undocumented. There is no such card.** PRD Q3 turned
out to have a different answer than the one it was asked for: the outcome of a
test-mode attempt is chosen at the last checkout call by one form field
carrying `S` or `F`, and the card number never reaches it.

That last point makes the layer split matter more rather than less, and it is
worth being blunt about what it does to the `live` row of the table above. A
live-layer recovery rate is a rate for outcomes this project selected. It is
evidence that the loop runs end to end against the real API, that the wire
shapes are right, and that the state read back from the gateway is what it
says. It is not evidence that a recovery decision caused a recovery, and no
phase can make it one, because test mode has no mechanism that would decide
differently based on the decision.

`make demo` prints that caveat on every run rather than leaving it here, and
`docs/RAZORPAY-TEST-MODE-NOTES.md` has the walk behind it.

The rate limit in the `live` row's batch-size column is still unmeasured. No
429 came back at 1.4 requests per second on 2026-08-31, which bounds nothing.
