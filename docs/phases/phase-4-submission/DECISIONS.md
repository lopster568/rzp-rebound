# Phase 4 decisions

Choices made while the work happened, with the alternative that was rejected.
Anything that outlives this phase goes to `docs/decisions/` instead.

## 1. The layer goes in a column, not in a label line above the table

ADR-0004 says every results row names its layer. Until this phase that was
implemented as a sentence under each table saying which layer it came from,
which is true of the table and not of a row. A row copied into a slide deck,
a form field, or a message carries no layer at all.

Every four-arm table in `RESULTS.md`, `README.md`, and `ARCHITECTURE.md` now
has `layer` as its first column, and `scripts/claims-check.sh` fails a results
table that has an `arm` column without one. The generated tables under
`results/tables/` already had the column; this makes the hand-written ones
match rather than the other way round.

The cost is a wider table on a page that already had fourteen columns.
Rejected: keeping the label line and trusting a reader to carry it, which is
the thing ADR-0004 exists to stop.

## 2. The gate is a Python file behind a shell script

Every other gate in `scripts/` is bash. This one is
`scripts/claims-check.sh` calling `scripts/claims_check.py`, because the work
is parsing CSV, markdown tables, and JSONL, and doing that in awk would be
three parsers nobody wants to review. `scripts/report.sh` already sets the
precedent of a shell entry point over a Python implementation.

Standard library only and no install, per ADR-0007, so a clone can still run
`make ci` with nothing fetched. `python3` is already a hard dependency of
`make test`, so this adds none.

## 3. The fact set was narrowed, and it was not narrowed to one number per claim

`PROBLEMS.md` 1 has why it was narrowed at all. The question left over is why
it stops where it stops.

The strongest version of check 4 would tie each sentence to the exact cell it
cites, the way check 1 ties a table cell. That needs an annotation per claim,
something like a marker naming the run, the arm, and the column beside every
number in prose. It would be exact, and it would be a second copy of the table
maintained by hand in the margins of the prose, and the first time it went
stale it would be wrong in a way nobody could see.

What is here instead is two checks with different strengths, stated rather than
blurred. Check 1 is exact and positional and it covers the tables, which is
where a judge and a reader take numbers from. Check 4 is a sweep that says
every number in the prose exists in a committed run, which catches a
fabrication and does not catch a real number in a wrong sentence. `TESTS.md`
says so under "What the gate does not check" rather than letting the green line
imply more than it proves.

## 4. `make verify-phase-4` drives no arm

The phase 2 and phase 3 gates seed a batch, run every arm, and end on the
containment assertion. The obvious thing would have been to make the phase 4
gate do that too, with the agent capped.

It does not, for two reasons. The agent arm costs a headless invocation per
order and the night's budget is already over at 62, so a gate that spends a
subscription to publish a document is the wrong trade. And phase 4 produces no
run: what can break here is a document disagreeing with a run that already
exists, which is what the gate checks. `verify-phase-2` and `verify-phase-3`
are unchanged and are still the gates that drive arms.

## 5. The claims gate joined `make ci` rather than staying a phase target

`verify-phase-4` is the phase gate, and `claims-check` is also a prerequisite
of `ci`, so it runs on every push and a green head in Actions means the
published numbers match the committed runs.

The argument against was that `ci` is the fast gate and the phase gates are the
slow ones. The gate reads six CSV files and a handful of ledgers and finishes
in well under a second, so there is no speed to protect. The argument for is
that a claims check nobody runs is a claims check that goes stale, and the
failure it catches, a published number drifting from its run, arrives through a
documentation commit rather than through a code change.

## 6. `RESULTS.md` keeps three limitations and points at the rest

The full list moved to `/HONEST-LIMITATIONS.md`, because two files carrying the
same ten items is two files that will disagree. Three stayed inline, the ones
that change how the two tables directly above them are read: the 0.568 ceiling,
classification accuracy carrying no information on the fake layer, and one run
per layer with a nondeterministic arm.

Rejected: moving all of them, which would leave a reader of `RESULTS.md` alone
looking at a recovery rate with no idea that 0.568 is the ceiling.

## 7. The demo script records trace ids and the command that resolves them

`docs/DEMO-SCRIPT.md` gives the two trace ids and tells the operator to run
`make trace-links RUN_DIR=results/runs/phase-3-fake` to turn them into URLs,
rather than printing a URL.

The host is whatever machine is running Jaeger, so a hardcoded URL is wrong on
every other machine and is also a piece of infrastructure detail in a public
repository. `RESULTS.md` already took this position for the same two ids. The
script adds the part that matters for a recording: Jaeger runs on in-memory
storage, so a container restart loses both traces, and the checklist says to
confirm both tabs load before the take and how to regenerate a pair if they do
not.
