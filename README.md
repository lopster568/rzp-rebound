# rzp-recovery-agent

Most agents demo actions. This one proves them.

It takes a batch of failed Razorpay payments, classifies why each one failed,
decides one action per order, and executes it through a policy gate the
decision maker cannot go around. Four arms run over the same seeded batch: do
nothing, retry everything, a nine-rule engine, and a language model whose only
hands are seven MCP tools. Every decision is an OpenTelemetry span and an
append-only audit row, and every published number comes from a run whose output
is committed under `results/`.

Built for the Razorpay Buildathon, Track 3, against Razorpay test mode.

## Quickstart

```
make preflight && make demo
```

`preflight` reports the toolchain, docker, and credentials in one screen and
hard-fails on a missing tool. `demo` drives one order end to end against
Razorpay test mode: create it, fail it, classify, evaluate, act, read the state
back, and print the ledger path and the Jaeger trace URL. It needs test-mode
keys in `.env` (`.env.example` has the shape) and Jaeger up, which
`make jaeger-up` starts and prints the endpoint for.

Everything else runs offline against the deterministic fake gateway, with no
credentials and no docker:

```
make seed SEED_ARGS="--seed 1234 --n 40 --bait 3"
make run-all BATCH=results/batches/b-1234-40.json LAYER=fake SEED=42
make report
```

`make report` writes the comparison table to `results/tables/` and exits
non-zero if any gated arm reached a side effect without a policy verdict.

## Results

### Fake layer, n=40, synthetic

| layer | arm | recovered | rate | FA-1 | FA-2 | escalations | refusals | violations succeeded |
|---|---|---|---|---|---|---|---|---|
| fake | `a0-control` | 0 | 0.000 | 0 | 0 | 0 | 0 | 0 |
| fake | `a1-naive` | 21 | 0.568 | 3 | 16 | 0 | 0 | **40** |
| fake | `a2-agent` | 18 | 0.486 | 1 | 0 | 9 | **16** | **0** |
| fake | `a3-rules` | 18 | 0.486 | 1 | 0 | 9 | 9 | **0** |

### Live layer, n=8, Razorpay test mode

| layer | arm | scorable | unscorable | recovered | rate | FA-1 | FA-2 | escalations | refusals | violations succeeded |
|---|---|---|---|---|---|---|---|---|---|---|
| live | `a0-control` | 8 | 0 | 0 | 0.000 | 0 | 0 | 0 | 0 | 0 |
| live | `a1-naive` | 8 | 0 | 4 | 0.667 | 2 | 2 | 0 | 0 | **8** |
| live | `a2-agent` | 7 | 1 | 0 | 0.000 | 0 | 0 | 7 | 8 | **0** |
| live | `a3-rules` | 8 | 0 | 0 | 0.000 | 0 | 0 | 8 | 8 | **0** |

A test-mode number is not evidence about real customers, and no row here is
summed or averaged across layers.

Three things to read off those tables. The naive arm recovers more on the fake
layer and pays 19 false actions against 1 to do it, with no policy verdict
behind any of its 40 actions. The model arm and the rule engine reached
identical decisions on all 40 fake-layer orders, and what separated them is
that the model proposed 16 things the policy refused and none of them reached
the gateway. And on the live layer both gated arms escalated everything,
because Razorpay test mode returns `payment_failed` for all eight documented
magic cards, that reason names no cause, and the fail-closed rule is doing its
job.

`/RESULTS.md` has the full tables, the per-class breakdown, the cost of the
model arm, and the reading.

## Where to look

| Document | What is in it |
|---|---|
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | The diagram, every component, the two-layer policy gate, the trace-as-audit-trail design |
| [`RESULTS.md`](RESULTS.md) | Four arms, two layers, and what each number is and is not |
| [`HONEST-LIMITATIONS.md`](HONEST-LIMITATIONS.md) | Every limit the build found, collected |
| [`docs/PRD.md`](docs/PRD.md) | Scope, requirements with their covering tests, open questions |
| [`docs/EVAL-DESIGN.md`](docs/EVAL-DESIGN.md) | How a run is measured and why each metric exists |
| [`docs/EVIDENCE.md`](docs/EVIDENCE.md) | Where every constant came from, what kind of source it has, and what still cannot be made real without production data |
| [`docs/RAZORPAY-TEST-MODE-NOTES.md`](docs/RAZORPAY-TEST-MODE-NOTES.md) | What test mode actually returns, observed rather than read |
| [`docs/AUDIT-TRACE-SCHEMA.md`](docs/AUDIT-TRACE-SCHEMA.md) | Span names, attributes, and ledger fields, written from a run |
| [`docs/phases/`](docs/phases/) | The process trail: plan, test list, problems, decisions, and report per phase |
| [`docs/decisions/`](docs/decisions/) | The ADRs |
| [`docs/DEMO-SCRIPT.md`](docs/DEMO-SCRIPT.md) | The five minute walkthrough |

The phase directories are the honest record. `PROBLEMS.md` in each one says
what broke, how it was found, and what it cost, including the four credential
leaks and the parallel-tool-call race the containment metric could not see.

## Stack

Go 1.25 (`internal/` packages, two binaries), the Model Context Protocol Go
SDK, OpenTelemetry with Jaeger, Razorpay test mode over plain `net/http` with
no SDK, and a Python standard-library harness for scoring. No third-party
Python package and no install step.
