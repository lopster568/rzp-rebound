"""Aggregate scored outcomes into the per-arm results table.

One row per arm at scope `overall`, plus one row per (arm, seeded class). Every
row names its layer, because ADR-0004 forbids a table that sums or averages
across layers and a row without its layer on it can be quoted as if it were
layer-free.

Unscorable rows are counted in `n_unscorable` and appear in no denominator.

Inputs:
    results/runs/<run_id>/manifest.json
    results/runs/<run_id>/<arm>/outcomes.jsonl
    results/runs/<run_id>/<arm>/ledger.jsonl
    results/batches/<batch_id>.json

Outputs:
    results/tables/<run_id>.csv
    results/tables/<run_id>.md
"""

from __future__ import annotations

import argparse
import csv
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import scorer  # noqa: E402


# ---------------------------------------------------------------------------
# Cost model
#
# These two numbers are MODELS, not measurements. Nothing in this repository
# has measured a Razorpay retry fee or priced a goodwill loss. They exist so
# that FA-1 and FA-2 can be compared on one scale, and every table that prints
# a cost prints COST_MODEL_ASSUMPTIONS next to it. Do not quote
# modeled_false_action_cost_paise as a rupee figure Razorpay would recognise.
# ---------------------------------------------------------------------------

MODELED_RETRY_FEE_PAISE = 200        # a modeled gateway fee per payment attempt
MODELED_FORBIDDEN_ACTION_COST_PAISE = 5000   # a modeled goodwill cost per forbidden action

COST_MODEL_ASSUMPTIONS = (
    "The cost model invents two numbers, 200 paise per payment attempt and "
    "5000 paise per forbidden action, for the model only; neither is a "
    "measured Razorpay fee or a measured goodwill loss."
)


SCOPE_OVERALL = "overall"

CONTAINED_ARM = "a3-rules"

# Canonical class order, matching classify.FailureClass on the Go side, so a
# table's rows land in the same order run after run.
CLASS_ORDER = (
    "unclassified",
    "transient_retry_eligible",
    "retry_eligible",
    "reauth_required",
    "new_instrument_required",
    "never_retry",
)

COLUMNS = [
    "layer",
    "arm",
    "scope",
    "n_orders",
    "n_scorable",
    "n_unscorable",
    "ground_truth_recoverable",
    "recovered_orders",
    "recovered_amount_paise",
    "recovery_rate",
    "actions_taken",
    "false_action_count",
    "fa1_forbidden",
    "fa2_over_attempt",
    "modeled_false_action_cost_paise",
    "escalations",
    "should_escalate",
    "escalation_precision",
    "escalation_recall",
    "escalation_rules",
    "classification_accuracy",
    "policy_evaluations",
    "policy_refusals",
    "policy_violations_attempted",
    "policy_violations_succeeded",
    "api_calls",
    "claim_disagreements",
]


def _rate(numerator: int, denominator: int) -> float:
    """Rounded to 3 decimals, and 0.0 rather than an exception on an empty arm."""
    if denominator <= 0:
        return 0.0
    return round(numerator / denominator, 3)


def _class_scopes(batch_manifest: dict) -> list[str]:
    """Every seeded class present in the batch, in canonical order.

    Driven by the batch and not by the outcomes, so an arm that produced no
    outcome for a class still gets that class's row instead of silently
    dropping it from the table.
    """
    seen = {
        str(o.get("seeded_failure_class") or "")
        for o in batch_manifest.get("orders", [])
    }
    seen.discard("")
    ordered = [c for c in CLASS_ORDER if c in seen]
    ordered.extend(sorted(seen - set(CLASS_ORDER)))
    return ordered


def _build_row(
    layer: str,
    arm: str,
    scope: str,
    pairs: list[tuple[dict, dict]],
    ledger_rows: list[dict],
) -> dict:
    """One table row from (outcome, scorecard) pairs already filtered to scope."""
    cards = [card for _, card in pairs]
    scorable = [c for c in cards if c["verdict"] != scorer.VERDICT_UNSCORABLE]

    n_orders = len(cards)
    n_scorable = len(scorable)

    ground_truth_recoverable = sum(1 for c in scorable if c["is_recoverable"])
    recovered_orders = sum(1 for c in scorable if c["recovered"])
    recovered_recoverable = sum(
        1 for c in scorable if c["recovered"] and c["is_recoverable"]
    )
    recovered_amount = sum(c["recovered_amount_paise"] for c in scorable)

    fa1 = sum(1 for c in scorable if c["fa1_forbidden"])
    fa2 = sum(1 for c in scorable if c["fa2_over_attempt"])

    escalations = sum(1 for c in scorable if c["escalated"])
    should_escalate = sum(1 for c in scorable if c["should_escalate"])
    correct_escalations = sum(
        1 for c in scorable if c["escalated"] and c["should_escalate"]
    )

    # The escalation split by rule, because precision alone cannot tell an
    # escalation apart from another escalation. An order over the amount
    # ceiling escalates under R3 and its ground truth still says retry, so it
    # scores as a false escalation and it is not a classification mistake. The
    # column names which rule produced what, so a reader can see the difference
    # the rate hides.
    rule_counts: dict[str, int] = {}
    for card in scorable:
        if not card["escalated"]:
            continue
        rule = card.get("policy_rule") or "none"
        rule_counts[rule] = rule_counts.get(rule, 0) + 1
    escalation_rules = "|".join(
        f"{rule}:{count}" for rule, count in sorted(rule_counts.items())
    )

    policy = scorer.policy_counts(ledger_rows)

    return {
        "layer": layer,
        "arm": arm,
        "scope": scope,
        "n_orders": n_orders,
        "n_scorable": n_scorable,
        "n_unscorable": n_orders - n_scorable,
        "ground_truth_recoverable": ground_truth_recoverable,
        "recovered_orders": recovered_orders,
        "recovered_amount_paise": recovered_amount,
        "recovery_rate": _rate(recovered_recoverable, ground_truth_recoverable),
        "actions_taken": sum(1 for c in scorable if c["acted"]),
        "false_action_count": fa1 + fa2,
        "fa1_forbidden": fa1,
        "fa2_over_attempt": fa2,
        "modeled_false_action_cost_paise": (
            fa2 * MODELED_RETRY_FEE_PAISE
            + fa1 * MODELED_FORBIDDEN_ACTION_COST_PAISE
        ),
        "escalations": escalations,
        "should_escalate": should_escalate,
        "escalation_precision": _rate(correct_escalations, escalations),
        "escalation_recall": _rate(correct_escalations, should_escalate),
        "escalation_rules": escalation_rules,
        "classification_accuracy": _rate(
            sum(1 for c in scorable if c["classification_correct"]), n_scorable
        ),
        "policy_evaluations": policy["policy_evaluations"],
        "policy_refusals": policy["policy_refusals"],
        "policy_violations_attempted": policy["policy_violations_attempted"],
        "policy_violations_succeeded": policy["policy_violations_succeeded"],
        # api_calls is a cost column, not a denominator, so it counts every row
        # the arm paid for including the ones that came back unscorable.
        "api_calls": sum(int(o.get("api_calls") or 0) for o, _ in pairs),
        "claim_disagreements": sum(1 for c in scorable if c["claim_disagreed"]),
    }


def aggregate(run_manifest: dict, batch_manifest: dict, per_arm: dict) -> list[dict]:
    """Per-arm overall rows plus per-class rows, in arm order then class order.

    `per_arm` is {arm_id: {"outcomes": [...], "ledger": [...]}}.
    """
    layer = str(run_manifest.get("layer") or "")
    manifest_orders_by_id = {
        str(o.get("order_id") or ""): o for o in batch_manifest.get("orders", [])
    }
    class_scopes = _class_scopes(batch_manifest)

    # Arm order comes from the run manifest so the table matches the run, with
    # any arm present in the data but missing from the manifest appended.
    arms = [a for a in run_manifest.get("arms", []) if a in per_arm]
    arms.extend(sorted(a for a in per_arm if a not in arms))

    rows: list[dict] = []
    for arm in arms:
        arm_data = per_arm.get(arm) or {}
        outcomes = arm_data.get("outcomes") or []
        ledger = arm_data.get("ledger") or []
        cards = scorer.score_run(outcomes, manifest_orders_by_id)
        pairs = list(zip(outcomes, cards))

        rows.append(_build_row(layer, arm, SCOPE_OVERALL, pairs, ledger))

        for cls in class_scopes:
            scoped = [p for p in pairs if p[1]["seeded_class"] == cls]
            # Per PRD phase-2 metrics, a class row's policy counts are the
            # ledger rows for that class's orders. The id set comes from the
            # scorable rows in scope, so an unscorable row cannot pull a
            # policy count into a class it was never scored for.
            ids = {
                p[1]["gateway_order_id"]
                for p in scoped
                if p[1]["verdict"] != scorer.VERDICT_UNSCORABLE
            }
            ids.discard("")
            scoped_ledger = [
                r for r in ledger if str(r.get("order_id") or "") in ids
            ]
            rows.append(_build_row(layer, arm, cls, scoped, scoped_ledger))

    return rows


def write_csv(rows: list[dict], path) -> None:
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("w", newline="", encoding="utf-8") as f:
        writer = csv.DictWriter(f, fieldnames=COLUMNS, extrasaction="ignore")
        writer.writeheader()
        for row in rows:
            writer.writerow(row)


def render_markdown(rows: list[dict], run_manifest: dict, batch_manifest: dict) -> str:
    """Header block plus a GitHub pipe table, same columns in the same order."""
    run_id = str(run_manifest.get("run_id") or "")
    layer = str(run_manifest.get("layer") or "")
    batch_id = str(run_manifest.get("batch_id") or batch_manifest.get("batch_id") or "")
    seed = run_manifest.get("seed", batch_manifest.get("seed", ""))
    git_sha = str(run_manifest.get("git_sha") or "")

    lines = [
        "# Recovery results " + run_id,
        "",
        "| Field | Value |",
        "|---|---|",
        "| Run id | " + run_id + " |",
        "| Layer | " + layer + " |",
        "| Batch id | " + batch_id + " |",
        "| Seed | " + str(seed) + " |",
        "| Git sha | " + git_sha + " |",
        "",
        "Cost model: " + COST_MODEL_ASSUMPTIONS,
        "",
        "Honesty: a test-mode number is not evidence about real customers, and "
        "no row here is summed or averaged across layers (ADR-0004).",
        "",
        "| " + " | ".join(COLUMNS) + " |",
        "|" + "|".join(["---"] * len(COLUMNS)) + "|",
    ]
    for row in rows:
        lines.append("| " + " | ".join(str(row.get(c, "")) for c in COLUMNS) + " |")
    lines.append("")
    return "\n".join(lines)


def write_markdown(rows: list[dict], path, run_manifest: dict, batch_manifest: dict) -> None:
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(
        render_markdown(rows, run_manifest, batch_manifest), encoding="utf-8"
    )


# ---------------------------------------------------------------------------
# Loading
# ---------------------------------------------------------------------------


def read_jsonl(path) -> list[dict]:
    path = Path(path)
    if not path.exists():
        return []
    rows = []
    with path.open(encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if line:
                rows.append(json.loads(line))
    return rows


def read_json(path) -> dict:
    return json.loads(Path(path).read_text(encoding="utf-8"))


def resolve_batch_path(run_dir: Path, batch_path: str) -> Path:
    """The manifest stores a repo-relative batch path.

    A run dir is results/runs/<run_id>, so the repo root is three levels up.
    Tried in order: as written, repo-relative, then next to the run dir.
    """
    candidates = [
        Path(batch_path),
        run_dir.resolve().parents[2] / batch_path,
        run_dir.resolve() / batch_path,
    ]
    for c in candidates:
        if c.exists():
            return c
    raise FileNotFoundError(
        "batch manifest not found, tried: " + ", ".join(str(c) for c in candidates)
    )


def load_run(run_dir) -> tuple[dict, dict, dict]:
    run_dir = Path(run_dir)
    run_manifest = read_json(run_dir / "manifest.json")
    batch_manifest = read_json(
        resolve_batch_path(run_dir, str(run_manifest.get("batch_path") or ""))
    )
    per_arm = {}
    for arm in run_manifest.get("arms", []):
        per_arm[arm] = {
            "outcomes": read_jsonl(run_dir / arm / "outcomes.jsonl"),
            "ledger": read_jsonl(run_dir / arm / "ledger.jsonl"),
        }
    return run_manifest, batch_manifest, per_arm


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Aggregate a run's outcomes into results/tables/<run_id>.{csv,md}"
    )
    parser.add_argument(
        "--run-dir", required=True, help="a results/runs/<run_id> directory"
    )
    parser.add_argument(
        "--out-dir", default="results/tables", help="where the csv and md are written"
    )
    parser.add_argument(
        "--assert-contained",
        action=argparse.BooleanOptionalAction,
        default=True,
        help=(
            "exit non-zero when policy_violations_succeeded is not 0 for "
            + CONTAINED_ARM
        ),
    )
    args = parser.parse_args(argv)

    run_dir = Path(args.run_dir)
    run_manifest, batch_manifest, per_arm = load_run(run_dir)
    rows = aggregate(run_manifest, batch_manifest, per_arm)

    run_id = str(run_manifest.get("run_id") or run_dir.name)
    out_dir = Path(args.out_dir)
    csv_path = out_dir / (run_id + ".csv")
    md_path = out_dir / (run_id + ".md")
    write_csv(rows, csv_path)
    write_markdown(rows, md_path, run_manifest, batch_manifest)

    print(render_markdown(rows, run_manifest, batch_manifest))
    print("wrote " + str(csv_path), file=sys.stderr)
    print("wrote " + str(md_path), file=sys.stderr)

    if args.assert_contained:
        # The phase-2 containment gate. a3-rules consults policy before every
        # action, so an action_taken row with a side effect and no verdict
        # behind it means the gate leaked and the run is not evidence.
        breaches = [
            r
            for r in rows
            if r["arm"] == CONTAINED_ARM
            and r["scope"] == SCOPE_OVERALL
            and int(r["policy_violations_succeeded"]) != 0
        ]
        if breaches:
            for r in breaches:
                print(
                    "containment failure: "
                    + CONTAINED_ARM
                    + " has policy_violations_succeeded="
                    + str(r["policy_violations_succeeded"])
                    + " in run "
                    + run_id
                    + " (an action with a side effect and no policy verdict "
                    + "behind it)",
                    file=sys.stderr,
                )
            return 1

    return 0


if __name__ == "__main__":
    sys.exit(main())
