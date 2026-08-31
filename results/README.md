# Results

Where a run's output goes, and which parts of it are committed.

| Path | What it holds | Tracked |
|---|---|---|
| `batches/` | Ground-truth batch manifests from `rzp seed`. The answer key. | One, `b-1234-40.json`, so the committed table can be recomputed. |
| `runs/<run_id>/manifest.json` | The run record: seed, arms, batch, layer, git sha, policy, key id prefix, and the shuffled cell order. | With its run. |
| `runs/<run_id>/<arm>/outcomes.jsonl` | One row per order: the class, the action, the gateway-read final status, the policy verdict, and the cost. | With its run. |
| `runs/<run_id>/<arm>/ledger.jsonl` | The audit trail, one row per decision, each carrying a trace id. | With its run. |
| `tables/<run_id>.{csv,md}` | The scored comparison table. | Yes, both layers. |

Two runs are tracked out of the several that exist: the fake-layer
`sample-phase-2-fake` and its batch, so anyone can rerun the scorer and get the
committed table back. The live-layer run is not, because its order ids are real
Razorpay test-mode ids. Its scored table is, because a table row carries no
order id.

Everything else under `runs/` and `batches/` is gitignored. It is evidence for
whoever ran it.

`RESULTS.md` at the repository root reads the tables.
`docs/EVAL-DESIGN.md` defines every column.

## Rerunning the committed table

```
make run-all BATCH=results/batches/b-1234-40.json LAYER=fake SEED=42
make report
```

Or `make verify-phase-2`, which seeds, runs, reports, and then fails if
`policy_violations_succeeded` is not 0 for `a3-rules`.

The demo ledgers under `runs/demo-*.jsonl` are from phase 1's `make demo` and
predate the per-run directory layout.
