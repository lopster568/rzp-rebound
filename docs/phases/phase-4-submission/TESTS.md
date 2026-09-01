# Phase 4 tests

Phase 4 adds no Go test and no Python test, because it adds no behaviour. What
it adds is a gate, and a gate is only worth having once it has been seen
failing. This file is the record of that.

## What the gate checks

`scripts/claims-check.sh`, over `README.md`, `RESULTS.md`, `ARCHITECTURE.md`,
`HONEST-LIMITATIONS.md`, and `docs/DEMO-SCRIPT.md`.

| # | Check | What it would catch |
|---|---|---|
| 1 | Every cell of every results table against the CSV row for that layer, arm, and scope | A number that drifted from the run behind it, which is exactly what phase 2 found three of |
| 2 | A results table with an `arm` column and no `layer` column | ADR-0004 broken by a table that reads fine on its own and is unquotable in context |
| 3 | A results table header this file has no CSV column for | A new column arriving unchecked |
| 4 | Every number in prose against a fact set built from tracked artifacts, or an allowlist line with a reason | A fabricated number |
| 5 | An allowlist line with no reason on it | The allowlist turning into a place to put anything |
| 6 | `RESULTS.md` and `README.md` each carrying a live row under a heading that names test mode | A test-mode rate quoted with no label attached |
| 7 | The fact set having fewer than 100 entries | The gate passing because it loaded nothing |

## The red run

Four faults were injected one at a time into a clean tree, each reverted before
the next. Run on 2026-09-01.

**A cell that drifted.** `a3-rules` recovered changed from 18 to 19 in the
`RESULTS.md` fake table.

```
claims-check: RESULTS.md:68: a3-rules.recovered is '19', results/tables/phase-3-fake.csv says '18'
claims-check: 1 problem(s) across 5 file(s)
```

**A results table with no layer column.** The `layer` column stripped off the
`README.md` fake table.

```
claims-check: README.md:44: results table has an arm column and no layer column (ADR-0004)
claims-check: 1 problem(s) across 5 file(s)
```

**A fabricated number.** The sentence "and it recovered 26 orders on the way"
added to `RESULTS.md`.

```
claims-check: 5 file(s) clean against 423 facts from the committed runs
```

That is the gate passing a number no run produced, and it is the finding of
this phase. `PROBLEMS.md` 1 has the cause and the fix. After the fix, the same
injection and four more:

```
injected 26   -> claims-check: RESULTS.md:97: 26 is in no committed run and no allowlist line
injected 55   -> claims-check: RESULTS.md:97: 55 is in no committed run and no allowlist line
injected 99   -> claims-check: RESULTS.md:97: 99 is in no committed run and no allowlist line
injected 7000 -> claims-check: RESULTS.md:97: 7000 is in no committed run and no allowlist line
injected 0.777 -> claims-check: RESULTS.md:97: 0.777 is in no committed run and no allowlist line
```

**A live table with no test-mode heading.** `## Live layer, n=8, Razorpay TEST
MODE` shortened to `## Live layer, n=8`.

```
claims-check: RESULTS.md: no heading names Razorpay test mode above the live table
claims-check: 1 problem(s) across 5 file(s)
```

## The green run

```
$ make verify-phase-4
check-docs: 53 file(s) clean
claims-check: 5 file(s) clean against 305 facts from the committed runs
exit 0
```

The fact count fell from 423 to 305 with the fix, which is the fix working: 118
of those entries were second counts derived from per-invocation
`duration_ms` values and they were what let a two-digit number pass.

## What the gate does not check

Check 4 is a sweep for invented numbers, not a proof that a sentence is right.
A number that exists somewhere in a committed run passes it even when the
sentence around it is wrong, which is a real hole and it is why check 1 exists
and covers the tables cell by cell. Prose outside a table still rests on a
reader. `DECISIONS.md` 3 has why the fact set was not narrowed further.
