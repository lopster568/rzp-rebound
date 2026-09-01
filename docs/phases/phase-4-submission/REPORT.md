# Phase 4 report

Written 2026-09-01, at the end of the repository half of phase 4. The pitch
video and the submission form are not part of it and are named at the bottom.

## Scope

Phase 4's job was to publish what phase 3 measured, and to add no number. Every
figure in every document this phase wrote already existed in
`results/tables/phase-3-fake.csv`, `results/tables/phase-3-live.csv`, or a
committed run under `results/runs/phase-3-fake/`, and a script now checks that
rather than a reviewer.

## What shipped

| Artifact | What it is |
|---|---|
| `/ARCHITECTURE.md` | The required submission artifact. A mermaid diagram GitHub renders, one paragraph per component naming its package, the two-layer gate with every rule id and verdict, the trace-as-audit-trail design, the eval design, and both results tables. |
| `/README.md` | The landing page. The claim, five lines, one command, both tables, the links, the stack. The repository had no README until this phase. |
| `/HONEST-LIMITATIONS.md` | Twenty-nine limits collected out of four phase directories, grouped by what they qualify. |
| `/RESULTS.md` | A `layer` column on both four-arm tables, the naive trade stated with its containment column, and the limitations list replaced by a pointer plus the three that bear on the tables. |
| `docs/DEMO-SCRIPT.md` | A five minute shot list with the on-screen commands, the two trace ids, and the recording checklist. |
| `scripts/claims-check.sh`, `scripts/claims_check.py`, `scripts/claims-allow.txt` | The gate. |
| `make claims-check`, `make verify-phase-4` | The gate wired in, and `claims-check` added as a prerequisite of `make ci`. |
| Docs | `PLAN.md`, `TESTS.md`, `PROBLEMS.md`, `DECISIONS.md`, this file, and the phase 4 edits to `docs/PRD.md` and `docs/phases/README.md`. |

## Exit criteria

**1. The five documents are written and every four-arm table carries a `layer`
column.** Met. `RESULTS.md`, `README.md`, and `ARCHITECTURE.md` each have their
tables with the layer on the row, and the gate refuses a results table without
one.

**2. The claims gate exists and has been seen failing.** Met, four ways: a
drifted cell, a table with no layer column, a fabricated number, and a live
table whose heading stopped naming test mode. The third of those passed on the
first version of the gate, which is `PROBLEMS.md` 1 and the finding of this
phase. `TESTS.md` has every red output.

**3. `make verify-phase-4` exits 0.** Met.

```
$ make verify-phase-4
check-docs: 53 file(s) clean
claims-check: 5 file(s) clean against 305 facts from the committed runs
exit 0
```

**4. `make ci` green and the pushed head green in Actions.** Met, and `ci` now
runs the claims gate too, so a green head means the published numbers match the
committed runs.

**5. The phase directory holds all five files.** Met.

## Findings

**A gate whose input set is wide enough passes everything.** The first version
of the claims check admitted every numeric leaf of every tracked JSONL file and
derived seconds from any key ending in `_ms`. Forty per-invocation
`duration_ms` values put forty small second counts into the fact set, so a
made-up "26 orders" was already a fact and the gate came back clean. Narrowing
the input, rather than the check, took the fact set from 423 entries to 305 and
turned five separate injections red. That is the same shape as the phase 2
finding that a containment metric could be cleared by the arm it measured: the
number was fine and what it was computed over was not.

**`make demo` had been marked planned for two phases while it worked.** PRD
section 6 said "Planned, phase 4" and `docs/AUDIT-TRACE-SCHEMA.md` was written
from one of its runs on 2026-08-31. The row described a scripted multi-arm
sequence that is really `make run-all` plus `make report`, and the target that
exists drives one order end to end. Corrected in the PRD with the reason in
`PROBLEMS.md` 2.

**NFR-5 has no measurement and now says so.** It asks for a full run in under
twenty minutes and was assigned to this phase, and this phase deliberately
drives no arm. The closest number that exists is 835 seconds for the agent
arm's 40 invocations on the fake layer, which is a sum of invocations rather
than a run. The requirement is marked not measured instead of being given a
number nobody timed.

## What phase 4 did not do

- **No run.** No arm was driven and no headless invocation was spent, so no
  number in `results/` moved. `DECISIONS.md` 4.
- **No new rule, tool, or metric.** PRD Q8 is still open.
- **No repeat runs for a spread on `a2-agent`.** Still the honest fix, still
  another forty invocations, still not done.
- **NFR-5 unmeasured.** See above.

## Owned by Roshan, and not done

Two things, and neither is a repository change.

- **Record the five minute pitch video.** `docs/DEMO-SCRIPT.md` is the shot
  list, the narration, and the recording checklist. Two items on that checklist
  are load bearing rather than tidy: no Razorpay key in any frame, and no agent
  tooling on screen. Jaeger holds its traces in memory, so both trace tabs have
  to be confirmed loading before the take.
- **Submit the Google Form.** It takes one submission and allows no edits
  afterwards, so it goes in when the repository and the video link are both
  final.

The project name is also his. The repository ships as `rzp-recovery-agent` and
the name appears in title lines rather than through the prose, so a rename is a
`sed` and a `gh repo rename`.
