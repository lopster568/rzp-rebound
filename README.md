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
make showcase
```

The guided tour, in four acts: the problem this exists for, the recovery loop
running live against Razorpay test mode, the impact table parsed out of the
committed CSV at run time, and the two Jaeger trace links. It waits for Enter
between acts, and `NO_PAUSE=1 make showcase` runs it straight through. The live
act needs credentials; without them it says exactly what is missing, prints the
command that fixes it, and carries on rather than printing output that did not
happen.

The two commands it is built on:

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

### Fake layer, n=40, on a published card-decline mix

Batch seeded from Mastercard and Ethoca's published card-decline shares:
insufficient funds 44 percent, lost or stolen 26 percent, fraud 9 percent. That
makes 35 percent of the batch orders no merchant should touch, which is the
source's share and not the author's.

| run | layer | arm | recovered | rate | FA-1 | FA-2 | modelled cost | escalations | refusals | violations succeeded |
|---|---|---|---|---|---|---|---|---|---|---|
| phase-5-fake-ethoca | fake | `a0-control` | 0 | 0.000 | 0 | 0 | 0 | 0 | 0 | 0 |
| phase-5-fake-ethoca | fake | `a1-naive` | 20 | 0.769 | **14** | 6 | **700000** | 0 | 0 | **40** |
| phase-5-fake-ethoca | fake | `a2-agent` | 16 | 0.615 | 0 | 0 | 0 | 18 | **22** | **0** |
| phase-5-fake-ethoca | fake | `a3-rules` | 16 | 0.615 | 0 | 0 | 0 | 18 | 18 | **0** |

### Fake layer, n=40, on the invented mix, for comparison

Same seed, same code, same day. The only thing that changed is where the
failure mix came from.

| run | layer | arm | recovered | rate | FA-1 | FA-2 | modelled cost | escalations | refusals | violations succeeded |
|---|---|---|---|---|---|---|---|---|---|---|
| phase-5-fake-uniform | fake | `a0-control` | 0 | 0.000 | 0 | 0 | 0 | 0 | 0 | 0 |
| phase-5-fake-uniform | fake | `a1-naive` | 19 | 0.514 | 3 | 18 | 150000 | 0 | 0 | **40** |
| phase-5-fake-uniform | fake | `a3-rules` | 15 | 0.405 | 0 | 0 | 0 | 9 | 9 | **0** |

### Live layer, n=8, Razorpay test mode

| run | layer | arm | scorable | unscorable | recovered | rate | FA-1 | FA-2 | escalations | refusals | violations succeeded |
|---|---|---|---|---|---|---|---|---|---|---|---|
| phase-5-live | live | `a0-control` | 8 | 0 | 0 | 0.000 | 0 | 0 | 0 | 0 | 0 |
| phase-5-live | live | `a1-naive` | 8 | 0 | 4 | 0.667 | 2 | 2 | 0 | 0 | **8** |
| phase-5-live | live | `a3-rules` | 8 | 0 | 0 | 0.000 | 0 | 0 | 8 | 8 | **0** |

A test-mode number is not evidence about real customers, and no row here is
summed or averaged across layers.

Four things to read off those tables. The naive arm recovers the most on the
fake layer and pays 20 false actions to do it, with no policy verdict behind any
of its 40 actions. Its forbidden actions go from 3 on the invented mix to 14 on
the published one, because a real card-decline mix is a third lost, stolen, and
fraud, and blind retry acts on all of it. The model arm and the rule engine
reached identical decisions on all 40 orders of the headline batch, and what
separated them is that the model proposed 22 things the policy refused and none
of them reached the gateway. And on the live layer the rules arm escalated
everything, because Razorpay test mode returns `payment_failed` for all eight
documented magic cards and that reason names no cause a policy can act on.

`/RESULTS.md` has the full tables, the per-class breakdown, the cost of the
model arm, and the reading. `docs/EVIDENCE.md` has where every cited number
came from and what kind of source it has.

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
