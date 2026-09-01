# Phase 5 problems

What broke, how it was found, and what it cost. Appended while the work
happened.

## 1. This phase shipped four wrong citations on its first pass

The worst finding in phase 5, and it is about phase 5.

`docs/EVIDENCE.md` was written from a research brief rather than from the
primary documents. A second verification pass on 2026-09-01 read the Visa
bulletin as a PDF and re-read Razorpay's error pages, and four claims were
wrong:

1. The Visa Category 1 code list was cited to bulletin AI10325. The bulletin
   establishes the four categories and does not enumerate Category 1. The twelve
   codes are a processor reconstruction of a member-gated table.
2. The reattempt cap was recorded as covering Categories 2 and 3. The bulletin
   states it for Category 2.
3. `error.source` was modelled as one flat nine-value enumeration including
   `issuer`. It is documented per method, five values for cards and eight for
   UPI, and `issuer` is on neither.
4. The Ethoca card-decline article was dated 2019. It is dated 2017-04-28.

A fifth was a claim rather than a citation: "response codes 05 and 51 are
together about 80 percent of declines" is repeated widely, is not in the article
it was attributed to, and could not be traced to any primary source. It is
dropped, not softened.

**How it was found:** by someone re-reading the primary documents rather than by
any gate. Nothing in this repository could have caught it. `claims_check.py`
checks that a published number is in a run or on the allow-list with a reason,
and every one of these was on the allow-list with a reason that was wrong.

**What it cost:** one red-and-green cycle across `internal/networkcodes`,
`internal/classify`, and `internal/batch`, plus problem 2 below.

**What changed as a result.** The corrections are marked in place with a
`Corrected 2026-09-01` line saying what the text used to say, rather than
silently applied. `VisaCategory1IsReconstructed` is a constant a test holds, so
the reconstruction is visible in the code and not only in prose. And the phase's
own exit criteria were the wrong shape: they required every constant to name a
source, and naming a source is not the same as the source saying it.

## 2. Correcting the profile's vintage orphaned a run from its input

Renaming `ethoca-card-mix-2019` to `ethoca-card-mix-2017` changed the batch id,
because the id carries the profile name. The four-arm run had already been
driven at 40 headless invocations against a cap of 45, so re-driving it was not
available.

Editing the run manifest's `batch_id` and `batch_path` to point at the renamed
file would have been editing a run artifact to fit a story, which is the one
thing this repository does not do.

**Resolution:** both files stay. `scripts/verify-batches.sh` rebuilds the
correctly named one from the profile and diffs it, and then asserts that the
old-named one differs from it in nothing but `batch_id`. The orders are
byte-identical because the shares never changed and only the label did.
`DECISIONS.md` entry 13 and `HONEST-LIMITATIONS.md` item 30 carry it.

**What it would have cost to do properly:** 40 more invocations, which is 82
against a cap of 45.

## 3. The agent arm's ledger lost its trace ids and nothing noticed

`results/runs/phase-3-fake/a2-agent/ledger.jsonl` carries a trace id on all 443
rows. `results/runs/phase-5-fake-ethoca/a2-agent/ledger.jsonl` carries one on 0
of 404. Every arm of `phase-5-live` is traced, and the three deterministic
fake-layer arms carried no trace id in phase 3 either, so the regression is the
agent arm on the fake layer specifically.

**How it was found:** `make trace-links RUN_DIR=results/runs/phase-5-fake-ethoca`
printed "none in this ledger" for both the refusal and the recovery, while
producing a fresh pair for `docs/DEMO-SCRIPT.md`.

**What it cost:** the demo script's two trace ids stay from the phase 3 run, and
that document now says which run they come from.

**The cause was not isolated, and it is written down as unexplained.** Two facts
about the machine are recorded as candidates rather than as answers: the
configured `OTEL_EXPORTER_OTLP_ENDPOINT` had no scheme and something in the
exporter path logged one `parse url` failure, and Docker was unreachable from
that shell so no collector was listening. Neither explains why a span would
carry no id at all.

**The finding underneath it is the one worth keeping.** Every gate stayed green.
`make ci` passes, `make claims-check` passes, every table cell matches its CSV,
and the containment counter reads 0, because no metric reads a trace id. An
audit design whose central claim is "every decision is a trace span" lost its
link from a published row to a trace and nothing in the suite noticed.
`.env.example` now warns about the endpoint form and `HONEST-LIMITATIONS.md`
item 36 has the counts.

## 4. A cited share and a rounding rule do not get along

The batch seeder gave the leftover from apportionment to the first declared
class. That is fine when every share is the author's own and nothing rides on
any single one being exact.

With the ethoca profile it put the cited 44 percent insufficient-funds share at
20 of 40 orders, which is 50 percent. The rounding rule was silently moving a
number read off somebody's published research.

**Resolution:** largest-remainder apportionment, in `batch.apportion`, which
holds every share within one order of its quota.

**What it cost:** `uniform-invented` at n=37 non-bait now splits 10/9/9/9 where
it used to split 13/8/8/8. That is a real change to the mix that was supposed to
be the thing holding still for the comparison, which is why the comparison in
`/RESULTS.md` is between two phase 5 runs rather than between a phase 5 run and
a phase 3 one. `HONEST-LIMITATIONS.md` item 38.

## 5. The eval's own bait went missing from the headline batch

Both gated arms report 0 false actions on `phase-5-fake-ethoca`. On the phase 3
batch each had exactly one, on the `attempt_budget_exhausted` bait order that no
rule reads the per-class budget for.

That is not the trap being avoided. It is the trap not being set: the ethoca
profile's bait is entirely `never_retry`, because that is what its cited lost,
stolen, and fraud share is, so the bait kind that catches a gated arm is not in
that batch at all.

It would have been easy to publish "0 false actions for both gated arms" as a
result. `/RESULTS.md` states the opposite in the section that reports it, PRD Q8
stays open, and `phase-5-fake-uniform` keeps the two-kind rotation so the case is
still in a published run.

## 6. A documented reason had been recorded as undocumented since phase 1

`payment_failed`, the only reason Razorpay test mode ever returned, is
documented on Razorpay's live-mode card error page as the bank declining without
giving a specific reason. Phase 1 recorded it as a string on no documentation
page and half the live-layer narrative rested on that.

**How it was found:** the same 2026-09-01 re-verification pass as problem 1.

**What it cost:** nothing in the code, and a paragraph in four documents. The
classification does not change and `DECISIONS.md` entry 11 argues why the
documented suggested action supports the existing fail-closed answer rather than
overturning it. `testdata/error_codes.json` now labels the row `documented-live`
while keeping it in `_meta.pending`, and the label test was extended to allow
that combination, which is what pending has always meant.

## 7. The claims gate could not tell two fake-layer runs apart

`PUBLISHED_CSV` mapped a layer to one CSV, and phase 5 publishes two fake-layer
runs from one seed with two failure mixes. A second fake table in `RESULTS.md`
would have been checked against the first one's CSV.

Worse, a documentation audit found the same headline table in four documents,
and the gate as written would not have caught a stale one: the old run's CSV
stays committed and still verifies, so a table left behind at phase 3 numbers
passed.

**Resolution:** results tables may carry a `run` column, which picks the CSV
exactly, and the layer fallback now points at the current headline run. A table
left at an older run's numbers fails. All four documents were regenerated and
the gate was seen failing on every one of them first.
