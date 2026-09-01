# Phase 5 report: realism hardening

Written 2026-09-01, at the end of the phase.

## What the phase was for

Phases 0 through 4 built a system that is rigorous about provenance downstream
of a run and silent about provenance upstream of one. Every published number is
checked against the CSV that produced it. Where the retry cap of 3, the cooldown
of 30 seconds, the ceiling of 450000 paise, the modelled costs of 200 and 5000
paise, the eight-string failure taxonomy, and the 28/24/24/24 batch mix came
from was: the author.

That asymmetry is worse than an unsourced number in a blog post, because the
surrounding rigour makes the unsourced numbers look checked.

The rule the phase wrote, and ADR-0008 records: **every constant is cited, with
a source, or declared a configured choice, with a reason. There is no third
category.**

## What shipped

| Change | Status |
|---|---|
| Documented live-mode failure taxonomy, per method: 15 card reasons and 8 UPI | cited, `razorpay.com/docs/errors/payments/` |
| `insufficient_fund` corrected to `insufficient_funds`, singular kept in a labelled test-mode table | cited, and a real bug fixed |
| `payment_risk_check_failed` replaces `testcards.PendingRiskBlockCode`, PRD Q2 closed | cited |
| `error.source` becomes a per-method enum, `error.step` stays a string with a test on the absence | cited |
| `internal/networkcodes`: Visa Category 1, the four codes moved out, MAC 03, the Category 2 cap, code 14 | cited, with the list itself marked reconstructed |
| R1 keeps 3, relabelled as a merchant policy under the Visa cap, with a test that fails above 15 | cited bound, configured value |
| R3 moves 450000 to 1500000, the RBI e-mandate additional-factor threshold | cited |
| R2 and R6 keep 30 seconds and are declared configured choices | configured choice, no citable value exists |
| Cost model rebuilt: 0, 50000, 20, and a carried 875 that multiplies by zero | cited |
| Three named batch profiles, one of them somebody else's numbers | one cited, one declared invented, one empty by design |
| `testdata/` rows all carry a label from a closed set, a source, and a date | mechanical, two tests |
| `docs/EVIDENCE.md`, ADR-0008, ADR-0005 and ADR-0006 backfilled | new |
| Three runs re-driven, four documents regenerated from them | new |

## The headline

Every published table moved, and not because the code got better at recovering
payments.

The `ethoca-card-mix-2017` profile seeds a batch from Mastercard and Ethoca's
published card-decline shares. Lost, stolen, and fraud declines are 35 percent
of that mix and they are orders no merchant should touch, so a citable
card-decline mix makes over a third of the batch bait.

On that batch the naive arm takes 14 forbidden actions and a modelled cost of
700000 paise. On the invented mix, same seed and same code, it takes 3 and
150000. Nothing about the arm changed. The batch did, and it changed because
half of it now comes from published research instead of from the author. That
one comparison is the phase's result.

The agent arm matched the rule set on all 40 orders again, on a second and
harder batch: same recoveries, same actions, no false action on either, the same
18 escalations splitting the same way. It proposed 22 things the policy refused
against the rule set's 18, and none of them reached the gateway.

## The finding the phase did not want

**Four of this phase's own citations were wrong on the first pass, and no gate
could have caught them.**

`docs/EVIDENCE.md` was written from a research brief rather than from the
primary documents. A second pass read Visa bulletin AI10325 as a PDF and re-read
Razorpay's error pages, and found that the bulletin does not contain the
Category 1 code list it was cited for, that the reattempt cap is stated for
Category 2 rather than Categories 2 and 3, that `error.source` is documented per
method and not as one flat nine-value list, and that the Ethoca article is dated
2017 rather than 2019. A fifth claim, about response codes 05 and 51, could not
be traced to any source and was dropped.

`scripts/claims_check.py` passed all of them. It checks that a published number
is in a committed run or on the allow-list with a reason on its line. Every one
of these was on the allow-list with a reason, and the reason was wrong.

So the exit criterion this phase wrote for itself was the wrong shape. "Every
constant names a source" is not the same as "the source says it", and the gap
between those two is where all four errors lived. What the phase can offer
against that is not a mechanism: it is
`VisaCategory1IsReconstructed`, a constant a test holds, which puts one
particular gap between a citation and its document into the code where it cannot
be skimmed past. `PROBLEMS.md` entry 1 has the full account, and the corrections
in `docs/EVIDENCE.md` are marked in place rather than silently applied.

**And the correction itself stopped one file short.** An adversarial review pass
after everything was pushed and Actions was green found that the citation fixes
had landed in `internal/networkcodes` and in the prose but not in
`internal/policy`, where `citedValues[RuleMaxAttempts]` carried the wrong
category for four commits. Four more stale strings alongside it: two documents
still calling `error.source` a nine-value enumeration, two renamed test names,
and the profile's old vintage in four places.

No gate could catch those either, and for a sharper reason than problem 1. The
phase built its gates against the class of error it had already been bitten by,
a number that drifts from its run. What it produced was a *word* that drifts
from its source: a category name, a count written in English, a test name, a
profile name. `PROBLEMS.md` entry 8.

## The other findings

**The agent arm's ledger lost its trace ids and every gate stayed green.** 443
of 443 rows traced on the phase 3 fake run, 0 of 404 on the phase 5 one, while
the live run is fully traced and `make ci` passes throughout. No metric reads a
trace id, so an audit design whose central claim is "every decision is a trace
span" lost the link from a published row to a trace and nothing noticed. The
cause was not isolated and is recorded as unexplained. `PROBLEMS.md` entry 3.

**A cited share and a rounding rule do not get along.** The seeder gave the
apportionment leftover to the first declared class, which put the cited 44
percent insufficient-funds share at 50 percent of a 40 order batch. Largest
remainder fixed it, at the cost of `uniform-invented` splitting 10/9/9/9 where
it used to split 13/8/8/8. `PROBLEMS.md` entry 4.

**The eval's own bait went missing from the headline batch.** Both gated arms
report 0 false actions, and that is the `attempt_budget_exhausted` trap not
being set rather than being avoided: the ethoca profile's bait is entirely
never-retry because that is what its cited share is. PRD Q8 stays open,
`/RESULTS.md` says so in the section that reports the zero, and the uniform run
keeps the bait kind that sets the trap.

**Correcting a vintage orphaned a run from its input.** Renaming the profile
changed the batch id after the run had spent 40 invocations. Both batch files
stay and `scripts/verify-batches.sh` proves they differ in nothing but
`batch_id`, rather than a run manifest being edited to fit the new name.

## What is measurably better and what is not

Better: every constant in `internal/policy`, `internal/classify`,
`internal/batch`, and `harness/aggregate.py` now says where it came from, and
`TestEveryRuleDeclaresItsCitationStatus` fails a rule that says nothing. The
failure taxonomy is Razorpay's live-mode vocabulary rather than eight strings off
a test-card page. The cost model prices a notification, which it never did, and
stops charging for a failed attempt, which is free in India. `make verify-phase-5`
rebuilds every committed batch from its profile and diffs it.

Not better, and stated in `docs/EVIDENCE.md` section 8: whether the documented
reasons are returned at volume, whose failure mix this is, recovery causality in
test mode, retry-timing uplift which is excluded because every published figure
for it is marketing with no methodology, and the two code paths covered by unit
tests and by no run.

## Budget

42 headless invocations of a cap of 45: 2 on a smoke test and 40 on the
four-arm ethoca run. `phase-5-fake-uniform` and `phase-5-live` are deterministic
arms only and spent none. The agent arm reported 3.367192 usd across 40
invocations, on a subscription, so it is not an amount anyone was billed.

## Exit criteria

| # | Criterion | Met |
|---|---|---|
| 1 | Every constant cited or declared a configured choice | Yes, and a test walks the rule ids |
| 2 | `internal/networkcodes` exists with sources in its header | Yes, and the reconstructed list says it is reconstructed |
| 3 | Every `testdata/` row labelled with a source and a date | Yes, two tests |
| 4 | `docs/EVIDENCE.md` exists, linked, every external number on the allow-list with its source | Yes, and the file is inside the claims gate |
| 5 | ADR-0008 records the rule | Yes, and ADR-0005 and ADR-0006 were backfilled to close the numbering hole |
| 6 | Three runs committed, `RESULTS.md` reading from them, phase 4 gate semantics preserved | Yes. The gate gained a `run` column and now fails a stale table, which it did not before |
| 7 | `make ci` green | Yes |
