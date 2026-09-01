# Phase 4 plan: submission

Written 2026-09-01, before any phase 4 file. Target date 2026-09-04 per PRD 12,
against a claimed buildathon deadline of 2026-09-05.

## Goal

Phase 3 produced the numbers. Phase 4 produces the artifacts a judge reads in
ten minutes, and it adds no result. Every number that appears in a phase 4
document already exists in `results/tables/` or in a committed run under
`results/runs/`, and a script checks that rather than a reviewer.

## What gets written

| Artifact | What it is |
|---|---|
| `/ARCHITECTURE.md` | Required by the submission form. System overview, a mermaid diagram GitHub renders, one paragraph per component naming its package, the trace-as-audit-trail design, the two-layer policy gate, the eval design, and the four-arm results with their layer labels. |
| `/RESULTS.md` | Already written at the end of phase 3. Phase 4 puts a `layer` column on every four-arm table so a row carries its layer when it is quoted out of context, and moves the limitations list to its own file so it has one home. |
| `/HONEST-LIMITATIONS.md` | Every limit the phase docs record, consolidated. Nothing new is discovered here; what is new is that a reader does not have to walk four phase directories to find them. |
| `/README.md` | The landing page. The claim, five lines of what it does, one command, both headline tables with their layers, and links to everything above. |
| `docs/DEMO-SCRIPT.md` | A five minute shot list with the on-screen commands and the two trace ids, plus the recording checklist. |
| `scripts/claims-check.sh` | The gate. Reads every four-arm table out of the four published documents and checks each cell against the CSV that produced it, then checks every remaining number in the prose against a fact set built from the committed artifacts. |
| `make verify-phase-4` | `make ci` then `scripts/claims-check.sh`. |

## The one thing this phase exists to prevent

A number in a published document that does not match the run behind it.

Phase 2 already found three of those in the first draft of `RESULTS.md`, all
three in sentences a reader would quote and none of them in a table cell.
`PROBLEMS.md` for phase 2 has them. The rule the repository wrote afterwards is
that a number in prose is a claim and has to be checked against the CSV, and
until now the only thing enforcing it has been whoever was reading.

`scripts/claims-check.sh` makes that mechanical. It runs in three parts:

1. **Table cells.** Any markdown table whose header carries `layer` and `arm`
   is a results table. Every cell in it is looked up in the CSV row for that
   layer, arm, and scope, through a fixed column mapping, and compared
   numerically. A table with an `arm` column and no `layer` column fails,
   which is the ADR-0004 rule enforced rather than remembered.
2. **Prose numbers.** Every number outside a code fence, outside backticks,
   and outside a table has to appear in the fact set or in
   `scripts/claims-allow.txt` with a reason on its line. The fact set is built
   from the CSVs, the committed ledgers, and the batch manifests, including
   the rounded forms a sentence uses: a usd figure to two decimals, a
   millisecond wall clock as seconds and as minutes.
3. **Labels.** `RESULTS.md` and `README.md` each have to carry a table whose
   rows read `live` and a heading that names Razorpay test mode.

It has to be seen failing before it is trusted, on the phase 2 precedent that a
gate which has only ever passed is not a gate. `TESTS.md` records that run.

## Exit criteria

1. `/ARCHITECTURE.md`, `/RESULTS.md`, `/HONEST-LIMITATIONS.md`, `/README.md`,
   and `docs/DEMO-SCRIPT.md` are written, and every four-arm table in them
   carries a `layer` column.
2. `scripts/claims-check.sh` exists, has been seen failing against a
   deliberately wrong number, and exits 0 against the tree as published.
3. `make verify-phase-4` exits 0.
4. `make ci` green, and the pushed head green in Actions.
5. `docs/phases/phase-4-submission/` holds `PLAN.md`, `TESTS.md`,
   `PROBLEMS.md`, `DECISIONS.md`, and `REPORT.md`.

## What this phase does not do

- **No new run.** The agent arm costs a headless invocation per order and the
  night's budget is already over at 62. `make verify-phase-4` deliberately does
  not drive the arms; `make verify-phase-2` and `make verify-phase-3` are the
  gates that do, and they are unchanged.
- **No new rule, tool, or metric.** PRD Q8 stays open. A budget-aware rule
  would change the tables, and a phase whose job is to publish the tables is
  the wrong phase to change them in.
- **No repeat runs for a spread on `a2-agent`.** That is the honest fix for the
  limitation and it costs another forty invocations. It stays unfixed and named.

## Owned by Roshan, not by this phase

- Recording the five minute pitch video. `docs/DEMO-SCRIPT.md` is the shot list
  and the recording checklist; the recording is his.
- Submitting the Google Form. It accepts one submission with no later edits, so
  it goes in when the repository and the video link are both final.
- Picking the project name. The repository ships under `rzp-recovery-agent`.
