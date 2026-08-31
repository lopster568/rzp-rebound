# ADR-0007: the scoring harness is Python, in a Go repository

| | |
|---|---|
| Status | Accepted |
| Date | 2026-08-31 |
| Applies from | Phase 2 |

## Context

Everything that runs is Go: the gateway clients, the policy, the arms, the
audit ledger. The scoring pass is a different job. It reads a batch manifest
and three JSONL files, joins them, computes about twenty rates and counts, and
writes a CSV and a markdown table.

Two things pull it away from Go. It is the component that has to be arguable
with rather than merely correct, because every number in the report comes out
of it, and a reviewer reading a formula should not have to read a type
signature first. And there is a working harness of exactly this shape already
written, in `~/jaeger-mcp-bench/harness/`: a verdict enum with an `unscorable`
member, an aggregate that keeps unscorable out of the denominator, and a
seeded-shuffle orchestrator that writes a run manifest. Those three ideas are
the ones worth carrying over, and they were learned the expensive way.

A third thing pushes back. A second language in a repository is a second
toolchain, a second lint story, a second way for CI to break, and a second
thing a reviewer has to have installed.

## Decision

The scoring harness is Python, under `harness/`, on the standard library only.

- No third-party packages. No `requirements.txt`, no lockfile, no virtual
  environment, no `pip install` in any instruction.
- Tests are `unittest`, run by `python3 -m unittest discover -s harness -t .`.
- `make test` runs the Go tests and the Python tests. `make ci` runs `make
  test`. `.github/workflows/ci.yml` needs no Python setup step, because
  `ubuntu-latest` already has `python3` and the harness needs nothing else.
- Nothing in Go imports anything from `harness/`, and nothing in `harness/`
  imports anything Go. They meet at four file formats, all JSON, all documented
  in `docs/EVAL-DESIGN.md`: the batch manifest, the run manifest, the per-arm
  outcomes file, and the per-arm audit ledger.

## Consequences

- The formulas are readable as formulas. `recovery_rate` is a division on one
  line next to the definition of its denominator.
- The interface is files, so either half can be rewritten without touching the
  other, and a run's output can be scored again later, by hand, or by something
  that is neither of these two programs. That is what makes a published number
  checkable rather than assertable.
- Two toolchains. `make lint` covers Go only; the Python side is covered by its
  tests and by `python3 -m py_compile`, which is thinner than `go vet` and this
  document is not pretending otherwise.
- No pytest means no fixtures and no parametrize, so the tests are more
  verbose than the ones they were transplanted from.
- A reviewer who wants to check a number needs `python3` and nothing else.
- The standard-library rule has to hold. The first dependency added here turns
  a clone-and-run repository into an install-first one, and the CI workflow
  would need a step it currently does not have.

## Alternatives considered

**Score in Go.** One toolchain, one lint story, one test command, and the
scorer would be covered by `go vet` and the race detector like everything else.
Rejected on two counts: it would rewrite the three ideas being transplanted
rather than transplanting them, and a formula in Go carries type conversions
between the integer counts and the float rates that obscure the arithmetic
being argued about. This is the closest alternative and a later phase could
still take it, because the interface is files.

**Score in the runner itself, and skip the file formats.** Fastest to build and
the least checkable. A number that only exists inside the process that produced
it cannot be recomputed by a reader, and the whole point of `results/` is that
it can.

**Python with pandas.** The aggregate is a group-by over a few hundred rows.
Pandas would shorten it and would make CI install a dependency to score a
table that fits on a screen.

**Vendor the jaeger-mcp-bench harness wholesale.** It scores a different
experiment: LLM answers against a Jaeger trace store, with a per-task handler
table and a non-answer detector. What transplants is the shape, not the code,
and copying 700 lines to use 100 of them would leave the other 600 to rot.

**Jupyter notebooks for the analysis.** Not reproducible from a command line,
not diffable in review, and not runnable in CI.
