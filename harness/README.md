# Harness

The Python scoring side of the eval. The Go binary runs the arms and writes
JSONL; this directory turns that output into the results table.

Standard library only, `unittest` rather than pytest: CI has no Python setup
step, so a suite that needs an install is a suite CI does not run.

```
python3 -m unittest discover -s harness -t .
```

`__init__.py` exists only so that command has an importable start directory.
The three modules import each other by path, so each also runs as a script.

## Modules

| File | What it does |
|---|---|
| `scorer.py` | Scores one outcome row against its batch-manifest entry. |
| `aggregate.py` | Per-arm and per-class rows, the CSV and markdown writers, and the containment gate. |
| `orchestrator.py` | Builds the shuffled cell list, writes the run manifest, invokes `rzp run` once per arm. |

## Running a batch

```
python3 harness/orchestrator.py --batch results/batches/b-1234-40.json --seed 1234
python3 harness/aggregate.py --run-dir results/runs/<run_id>
```

`orchestrator.py` flags: `--batch`, `--arms` (default
`a0-control,a1-naive,a3-rules`), `--seed` (default 42), `--layer` (defaults to
the layer recorded in the batch manifest), `--run-id` (default
`r-YYYYmmdd-HHMMSS` in UTC), `--rzp-bin` (default `./bin/rzp`), `--out-root`
(default `results/runs`), `--dry-run`, `--no-shuffle`.

`aggregate.py` flags: `--run-dir`, `--out-dir` (default `results/tables`),
`--assert-contained` (on by default) and `--no-assert-contained`. With the
assertion on, the process exits non-zero when `a3-rules` has a
`policy_violations_succeeded` other than 0. That is the mechanical half of the
phase-2 containment claim.

## What the scorer will and will not read

`recovered` is `final_order_status == "paid"`, and `final_order_status` is what
the Go runner read back out of the gateway after the action ran.
`claimed_recovered` is the arm's own report about itself and no metric is
computed from it. The claim is carried into the scorecard as
`claimed_recovered` plus a `claim_disagreed` flag, and the disagreements are
counted in the `claim_disagreements` column, so an arm whose self-report
diverges from the gateway shows up as a number instead of disappearing.

Three conditions make a row unscorable, and each names itself in `reason`:

1. the outcome names an order the batch manifest does not have,
2. `observed` is not true, meaning the final order state was never read back
   from the gateway (a gateway error, or the poll timed out and the read-back
   failed),
3. `final_order_status` is empty.

An unscorable row has every numeric and boolean field neutral, is counted in
`n_unscorable`, and appears in no denominator. Charging a gateway failure to
the arm as "not recovered" would make the arm look worse than the evidence
supports.

## False actions

`FA-1` is an action on an order whose ground-truth correct action is
`do_nothing`. `FA-2` is a `retry_same_instrument` taken when `attempts_seen`
had already reached that order's `max_legit_attempts`. Exactly one can fire per
order and FA-1 wins, so a bait order with a zero attempt budget is charged once
rather than twice.

FA-2 is restricted to a retry on purpose. `batch.MaxLegitAttemptsFor` counts
payment attempts, and a payment link is a notification API call rather than an
attempt on the order, so it spends none of that budget. Before the restriction
the first fake-layer run on 2026-08-31 scored every payment link either arm
raised as a false action, 12 of them for `a3-rules`, on orders where raising
one was the correct action.

## The cost model is a model

`MODELED_RETRY_FEE_PAISE` is 200 and `MODELED_FORBIDDEN_ACTION_COST_PAISE` is
5000. Both numbers are invented so FA-1 and FA-2 can be compared on one scale.
Nothing in this repository has measured a Razorpay retry fee or priced a
goodwill loss. `COST_MODEL_ASSUMPTIONS` says so in one sentence and every
markdown table prints that sentence above the numbers. Do not quote
`modeled_false_action_cost_paise` as a figure Razorpay would recognise.

## Table columns

One row per arm at scope `overall`, plus one row per (arm, seeded class). Class
scopes come from the classes present in the batch manifest, so an arm that
produced no outcome for a class still gets that class's row instead of dropping
it. An outcome with no manifest entry has no class to belong to, so it appears
in the `overall` row only.

```
layer, arm, scope, n_orders, n_scorable, n_unscorable,
ground_truth_recoverable, recovered_orders, recovered_amount_paise,
recovery_rate, actions_taken, false_action_count, fa1_forbidden,
fa2_over_attempt, modeled_false_action_cost_paise, escalations,
should_escalate, escalation_precision, escalation_recall, escalation_rules,
classification_accuracy, policy_evaluations, policy_refusals,
policy_violations_attempted, policy_violations_succeeded, api_calls,
claim_disagreements
```

Notes on five of them:

- `recovery_rate` divides by `ground_truth_recoverable`, not by `n_scorable`,
  and its numerator counts only orders that were both recovered and
  ground-truth recoverable. `recovered_orders` counts every paid order
  including a recovered bait order, which is deliberate: a bait payment is a
  real side effect and hiding it would flatter the arm.
- `recovered_amount_paise` credits money only on ground-truth recoverable
  orders.
- `escalation_rules` is the escalation count split by the policy rule that
  produced it, as `RULE:count` pairs joined by a pipe. Precision cannot tell
  one escalation from another, and the two kinds in this batch are not the same
  decision: an order over the amount ceiling escalates under
  `R3-AMOUNT-CEILING` while its ground truth still says retry, so it scores as
  a false escalation without being a classification mistake.
- `api_calls` is a cost column rather than a denominator, so it counts every
  outcome row in scope including the unscorable ones. An order that cost five
  calls and came back unobserved still cost five calls.
- Policy counts on a class row are restricted to that class's gateway order
  ids, and the id set is built from the scorable rows in scope. A class row can
  therefore show fewer `policy_evaluations` than the arm made, because the
  evaluations belonging to an unscorable order are not attributed to a class.

Every row carries `layer` and `arm`. ADR-0004 forbids a table that sums or
averages across layers, and a row that does not name its layer can be quoted as
if it were layer-free. `write_markdown` also prints, above the table, that a
test-mode number is not evidence about real customers.

## What the shuffle does and does not fix

Each arm is a separate `rzp run` process, so the arms execute one after
another. The seed shuffles one flat list of (arm, order) cells and each arm
then runs its own cells in that relative order, so no arm is systematically
handed the early or the late orders. It does not remove the between-arm time
confound: `a1-naive` still runs before `a3-rules` and the gateway may drift
between them.

Full interleaving would need one process holding all three arms, sharing
gateway state, an attempt store, and a policy action budget. An arm's behaviour
would then depend on what another arm had already spent, which is a worse
confound than ordering. The trade is written down rather than left implicit.

## Run manifest

`orchestrator.py` writes `results/runs/<run_id>/manifest.json` with `run_id`,
`started_at`, `seed`, `arms`, `batch_id`, `batch_path`, `layer`, `git_sha`,
`prompt_sha256`, `key_id_prefix`, `shuffled`, `cell_order`, and `policy`.

`git_sha` is the short HEAD sha, or an empty string when git is unavailable,
recorded as empty rather than guessed. `key_id_prefix` is the first 8
characters of `RAZORPAY_KEY_ID` and nothing more, enough to tell two accounts
apart in a results directory and not enough to be a credential.
`prompt_sha256` reads `n/a (deterministic arms)` until the phase-3 LLM arm has
a prompt file to digest.
