# Phase 4 problems

Things that broke, what the cause turned out to be, and what fixed them. Date
every entry. A problem that got worked around rather than fixed says so.

## 2026-09-01: the claims gate passed a number no run produced

Symptom: the first version of `scripts/claims_check.py` was checked in the way
the phase 2 report says a gate has to be checked, by injecting the failure it
exists to catch. Three of the four injections went red. The fourth, a sentence
reading "and it recovered 26 orders on the way", came back clean against 423
facts.

Cause: the fact set admitted every numeric leaf of every tracked JSON and JSONL
file, and it also derived seconds and minutes from any key ending in `_ms`.
`results/runs/phase-3-fake/a2-agent/invocations.jsonl` carries a `duration_ms`
per invocation, forty of them, so forty second-counts between 5 and 60 went
into the set. 26069 milliseconds put 26 in it. With those there, almost any
two-digit number a person could type was already a fact.

The general shape is one this repository has hit before: a check whose input
set is wide enough passes everything, and it reads as a green gate rather than
as an absent one.

Fix, in two parts, both narrowing the input rather than the check.

- The millisecond scaling now happens only for CSV columns.
  `agent_wall_clock_ms` in a results table is a number a sentence quotes as
  seconds or minutes; a per-invocation duration in a ledger is not.
- A raw JSON or JSONL leaf under 1000 does not enter the fact set at all.
  Small numbers have to come from a table cell or from a count the script
  computes on purpose, which is rows per ledger kind, policy evaluations per
  rule, and the per-class composition of a batch. Amounts in paise and
  timestamps stay in, which is what lets `docs/DEMO-SCRIPT.md` cite the 456700
  paise amount that appears on a span.

The fact set went from 423 entries to 305. Re-injecting 26, and then 55, 99,
7000, and 0.777, turns it red on every one. `TESTS.md` has all five.

Cost: 25 minutes, and worth it. A gate that had shipped in the first state
would have been a line in `make ci` that proved nothing.

## 2026-09-01: `make demo` was listed as unbuilt in the PRD and had existed since phase 1

Symptom: PRD section 6 gives `make demo` the status "Planned, phase 4", and
`docs/AUDIT-TRACE-SCHEMA.md` was written from a `make demo` run on 2026-08-31
and tells a reader to run it.

Cause: the row's description is the phase 4 command that was planned, a scripted
seed plus every arm plus a report. What was built in phase 1 is a different
thing with the same name: one order driven end to end against test mode, which
is the right shape for a demo and the wrong shape for the row.

Fix: the row now says what the target does and marks it as working since phase
1, and the multi-arm sequence it used to describe is `make run-all` plus
`make report`, which already have rows of their own. No code changed. The
correction is here rather than only in the diff, because a status column that
disagreed with a document in the same repository for two phases is worth one
paragraph.

Cost: 10 minutes.

## 2026-09-01: nothing else

The rest of the phase was writing. The three documents that carry numbers were
drafted against `results/tables/phase-3-fake.csv` and
`results/tables/phase-3-live.csv` open alongside them, and the gate then found
nothing in them, which is the outcome the gate is supposed to have on a careful
draft rather than evidence that it does not work. `TESTS.md` has what it does
on a careless one.
