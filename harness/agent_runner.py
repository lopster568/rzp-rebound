"""Drive the a2-agent arm over one batch: one headless invocation per order.

This is the a2 half of what `rzp run` is for the other three arms. The
orchestrator calls it instead of `rzp run` when the arm is a2-agent, so the
four arms share one run directory, one run manifest, and one shuffled cell
order.

Two things it does that `rzp run` does not have to.

**It writes an outcome row for an order the server never saw.** The server
writes its own row when the session ends, which is the row the recovery number
comes from. An invocation that never reached the server, because the CLI failed
to start or the model never called a tool, leaves no row, and an order with no
row silently leaves the denominator. So after every invocation this checks, and
appends an unscorable row when one is missing. Per docs/EVAL-DESIGN.md an
unscorable outcome is counted and explained, never dropped.

**It stops on a budget.** The invocations cost a subscription. `--max-invocations`
is a hard stop, and every order it did not reach gets an unscorable row saying
so, rather than the run quietly being shorter than the batch.
"""

from __future__ import annotations

import argparse
import json
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import claude_runner  # noqa: E402


ARM = "a2-agent"

# How long to wait for the server's outcome row after the CLI has exited.
#
# The server writes that row on its way out, after the client disconnects, so
# for a moment the CLI has returned and the row is not on disk yet. Declaring
# it missing in that window would file a completed order as unscorable.
#
# It does not cover the case where the CLI killed the server before it finished
# the read-back. That happened once in eight on the live layer, and it produces
# a genuinely unscorable row: the outcome was never observed, and saying so is
# the honest reading.
OUTCOME_WAIT_SEC = 10.0
OUTCOME_POLL_SEC = 0.1

# The task line appended to the charter. It names the item and nothing else:
# every judgment the arm makes has to come from what the tools return, and a
# task line carrying a hint would be the harness deciding for the model.
TASK_TEMPLATE = (
    "You have one risk item to handle. Start with list_risk_items. "
    "Work it through to a decision and an action, and then stop."
)


def build_prompt(charter: str) -> str:
    """The charter plus the task line. The digest recorded in the run manifest
    is the charter file's, because that is the versioned artefact."""
    return charter.rstrip() + "\n\n---\n\n" + TASK_TEMPLATE


def read_order_sequence(path: str | Path) -> list[str]:
    lines = Path(path).read_text(encoding="utf-8").splitlines()
    return [line.strip() for line in lines if line.strip()]


def wait_for_outcome(
    path: Path,
    order_id: str,
    timeout: float = OUTCOME_WAIT_SEC,
    poll: float = OUTCOME_POLL_SEC,
    now=None,
    sleep=None,
) -> bool:
    """Wait for the server's outcome row for one order, or give up.

    Polls rather than sleeping a fixed interval, so a slow machine does not
    turn into an unscorable row and a fast one does not pay for it.
    """
    now = now or time.monotonic
    sleep = sleep or time.sleep

    deadline = now() + timeout
    while True:
        if order_id in outcome_ids(path):
            return True
        if now() >= deadline:
            return False
        sleep(poll)


def outcome_ids(path: Path) -> set[str]:
    """The manifest order ids the server has already written rows for."""
    if not path.exists():
        return set()
    ids = set()
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line:
            continue
        try:
            row = json.loads(line)
        except json.JSONDecodeError:
            continue
        ids.add(str(row.get("manifest_order_id") or ""))
    return ids


def unscorable_row(
    *, run_id: str, layer: str, batch_id: str, order_id: str, reason: str
) -> dict:
    """The row an order gets when no session produced one.

    `observed` is false, which is what makes harness/scorer.py call it
    unscorable rather than scoring it as a failure to recover. Charging a CLI
    that would not start to the arm's recovery rate would be charging the
    harness to the thing being measured.
    """
    return {
        "run_id": run_id,
        "arm": ARM,
        "layer": layer,
        "batch_id": batch_id,
        "manifest_order_id": order_id,
        "gateway_order_id": "",
        "class": "",
        "action_kind": "none",
        "final_order_status": "",
        "recovered": False,
        "claimed_recovered": False,
        "amount_paid_paise": 0,
        "attempts_seen": 0,
        "attempts_after": 0,
        "policy_verdict": "",
        "policy_rule": "",
        "escalated": False,
        "side_effect": False,
        "timed_out": False,
        "error": reason,
        "observed": False,
        "api_calls": 0,
    }


def append_json_line(path: Path, row: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("a", encoding="utf-8") as handle:
        handle.write(json.dumps(row) + "\n")


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Run the a2-agent arm over one batch, one invocation per order"
    )
    parser.add_argument("--batch", required=True)
    parser.add_argument("--batch-id", default="")
    parser.add_argument("--layer", required=True)
    parser.add_argument("--run-dir", required=True)
    parser.add_argument("--order-sequence", required=True)
    parser.add_argument("--server-binary", default="./bin/rzp-mcp")
    parser.add_argument("--prompt", default="prompts/agent_system.md")
    parser.add_argument("--model", default=claude_runner.DEFAULT_MODEL)
    parser.add_argument(
        "--max-budget-usd", type=float, default=claude_runner.DEFAULT_MAX_BUDGET_USD
    )
    parser.add_argument("--timeout-sec", type=int, default=claude_runner.DEFAULT_TIMEOUT_SEC)
    parser.add_argument(
        "--max-invocations",
        type=int,
        default=0,
        help="hard stop on how many invocations this run may make, zero means no stop",
    )
    parser.add_argument("--action-budget", type=int, default=0)
    parser.add_argument("--kill-switch-file", default="")
    parser.add_argument("--card", default="")
    args = parser.parse_args(argv)

    run_dir = Path(args.run_dir)
    arm_dir = run_dir / ARM
    arm_dir.mkdir(parents=True, exist_ok=True)
    outcomes_path = arm_dir / "outcomes.jsonl"
    invocations_path = arm_dir / "invocations.jsonl"
    config_dir = arm_dir / "mcp"
    config_dir.mkdir(parents=True, exist_ok=True)

    run_id = run_dir.name
    batch_id = args.batch_id or str(
        json.loads(Path(args.batch).read_text(encoding="utf-8")).get("batch_id") or ""
    )

    charter = Path(args.prompt).read_text(encoding="utf-8")
    prompt = build_prompt(charter)
    settings_path = claude_runner.ensure_clean_settings()

    order_ids = read_order_sequence(args.order_sequence)
    print(
        "agent_runner: %d order(s), model %s, budget %s usd per invocation"
        % (len(order_ids), args.model, args.max_budget_usd),
        file=sys.stderr,
    )

    spent = 0
    for index, order_id in enumerate(order_ids, start=1):
        if args.max_invocations and spent >= args.max_invocations:
            append_json_line(
                outcomes_path,
                unscorable_row(
                    run_id=run_id,
                    layer=args.layer,
                    batch_id=batch_id,
                    order_id=order_id,
                    reason=(
                        "not run: the invocation cap of "
                        + str(args.max_invocations)
                        + " was reached"
                    ),
                ),
            )
            continue

        config = claude_runner.mcp_config(
            server_binary=args.server_binary,
            batch_path=args.batch,
            order_id=order_id,
            layer=args.layer,
            run_dir=str(run_dir),
            arm=ARM,
            card=args.card,
            kill_switch_file=args.kill_switch_file,
            action_budget=args.action_budget,
        )
        config_path = claude_runner.write_mcp_config(
            config, config_dir / (order_id + ".json")
        )

        before = outcome_ids(outcomes_path)
        result = claude_runner.run(
            prompt=prompt,
            order_id=order_id,
            arm=ARM,
            mcp_config_path=config_path,
            settings_path=settings_path,
            model=args.model,
            max_budget_usd=args.max_budget_usd,
            timeout_sec=args.timeout_sec,
        )
        # attempts, not one. An infra retry is a second invocation and it cost
        # what a first one costs.
        spent += result.attempts
        claude_runner.append_invocation(invocations_path, result)

        if order_id not in before and not wait_for_outcome(outcomes_path, order_id):
            append_json_line(
                outcomes_path,
                unscorable_row(
                    run_id=run_id,
                    layer=args.layer,
                    batch_id=batch_id,
                    order_id=order_id,
                    reason=(
                        "the session wrote no outcome row within "
                        + str(OUTCOME_WAIT_SEC)
                        + "s of the cli exiting: "
                        + (result.reason or result.error)
                    ),
                ),
            )

        print(
            "  %3d/%d %-22s %-11s cost=%s turns=%s %s"
            % (
                index,
                len(order_ids),
                order_id,
                "unscorable" if result.unscorable else "scored",
                result.cost_usd,
                result.num_turns,
                result.error or "",
            ),
            file=sys.stderr,
        )

    print(
        "agent_runner: %d invocation(s) spent, outcomes %s"
        % (spent, outcomes_path),
        file=sys.stderr,
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
