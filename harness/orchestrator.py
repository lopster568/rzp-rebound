"""Build a run: shuffle the arms-by-orders cell list, write the run manifest,
then invoke `rzp run` once per arm.

An honest limit on the shuffle. Each arm is a separate `rzp run` process, so
the arms execute one after another rather than interleaved. What the seed buys
is decorrelation of order position inside an arm: the three arms draw from one
shuffled cell list, so they see the batch in three different orders and no arm
is systematically handed the early or the late orders. It does not remove a
between-arm time confound, because arm 1 runs before arm 3 and the gateway may
have drifted in between.

Full interleaving would need one process holding all three arms at once, and
that process would share gateway state, an attempt store, and a policy action
budget between arms. An arm's behaviour would then depend on what another arm
had already spent, which is a worse confound than ordering. The trade is stated
here and in docs/EVAL-DESIGN.md rather than left for a reader to infer from the
code.
"""

from __future__ import annotations

import argparse
import datetime
import hashlib
import json
import os
import random
import subprocess
import sys
from pathlib import Path


DEFAULT_ARMS = "a0-control,a1-naive,a3-rules"

# The LLM arm. It is not driven by `rzp run`: its decision maker is a headless
# `claude` invocation per order, and harness/agent_runner.py is what runs those.
# Everything else about it is the same, which is the property
# harness/test_arm_config.py asserts key by key.
AGENT_ARM = "a2-agent"

# The policy is read out of the binary that is about to run, never copied here.
#
# The first version of this file had the four numbers written out as a mirror
# of the Go defaults, and they went stale the same hour the amount ceiling
# moved on 2026-08-31: the manifest said 400000 while every arm in that run had
# used 450000. A manifest that disagrees with the run it describes is worse
# than one that omits the field, so it is fetched and a failure is recorded as
# a failure.
POLICY_UNAVAILABLE = {"error": "rzp policy-config did not answer"}


def policy_config(rzp_bin: str) -> dict:
    """Ask the runner for the policy it will use."""
    try:
        out = subprocess.run(
            [rzp_bin, "policy-config"],
            capture_output=True,
            text=True,
            check=True,
        )
        return json.loads(out.stdout)
    except (OSError, subprocess.CalledProcessError, json.JSONDecodeError):
        return dict(POLICY_UNAVAILABLE)

# The deterministic arms have no prompt. A run that includes the LLM arm
# records the digest of the charter file that actually ran, computed from the
# bytes on disk rather than copied from anywhere.
PROMPT_SHA256_DETERMINISTIC = "n/a (deterministic arms)"

DEFAULT_PROMPT_PATH = "prompts/agent_system.md"


def prompt_sha256(arms: list[str], prompt_path: str) -> str:
    """The prompt digest for the run manifest.

    A run with no LLM arm has no prompt and says so. A run with one and no
    readable prompt file is an error recorded as an error: a manifest that
    claims a digest it could not compute is worse than one that admits it.
    """
    if AGENT_ARM not in arms:
        return PROMPT_SHA256_DETERMINISTIC
    try:
        return hashlib.sha256(Path(prompt_path).read_bytes()).hexdigest()
    except OSError as err:
        return "error: could not read " + prompt_path + ": " + str(err)

# A key id is a credential, so only a prefix is recorded.
#
# Eight characters is "rzp_test" and nothing after it, which distinguishes a
# test-mode run from a live-mode one and does not distinguish two test accounts.
# That is deliberate and the comment used to overclaim it: the field exists so a
# results directory says which mode produced it, and a length that separated
# accounts would be a length that leaked part of a key.
KEY_ID_PREFIX_LEN = 8


def git_sha() -> str:
    """Short HEAD sha, or empty string when git is unavailable or this is not a
    checkout. A missing sha is recorded as empty rather than guessed."""
    try:
        out = subprocess.run(
            ["git", "rev-parse", "--short", "HEAD"],
            capture_output=True,
            text=True,
            check=False,
        )
    except OSError:
        return ""
    if out.returncode != 0:
        return ""
    return out.stdout.strip()


def key_id_prefix() -> str:
    return (os.environ.get("RAZORPAY_KEY_ID") or "")[:KEY_ID_PREFIX_LEN]


def default_run_id(now: datetime.datetime | None = None) -> str:
    now = now or datetime.datetime.now(datetime.UTC)
    return "r-" + now.strftime("%Y%m%d-%H%M%S")


def build_cells(arms: list[str], order_ids: list[str]) -> list[dict]:
    return [{"arm": arm, "order_id": oid} for arm in arms for oid in order_ids]


def shuffle_cells(cells: list[dict], seed: int) -> None:
    """Shuffle in place with a locally scoped RNG.

    A locally scoped Random keeps the global RNG untouched, so nothing else in
    this process can consume draws and change the run a seed reproduces.
    """
    random.Random(seed).shuffle(cells)


def arm_sequence(cells: list[dict], arm: str) -> list[str]:
    return [c["order_id"] for c in cells if c["arm"] == arm]


def build_manifest(
    *,
    run_id: str,
    seed: int,
    arms: list[str],
    batch_manifest: dict,
    batch_path: str,
    layer: str,
    cells: list[dict],
    shuffled: bool,
    started_at: str,
    policy: dict,
    prompt_digest: str,
    agent: dict | None = None,
) -> dict:
    return {
        "run_id": run_id,
        "started_at": started_at,
        "seed": seed,
        "arms": arms,
        "batch_id": str(batch_manifest.get("batch_id") or ""),
        "batch_path": batch_path,
        "layer": layer,
        "git_sha": git_sha(),
        "prompt_sha256": prompt_digest,
        "key_id_prefix": key_id_prefix(),
        "shuffled": shuffled,
        "cell_order": cells,
        "policy": policy,
        "agent": agent or {},
    }


def arm_command(
    rzp_bin: str,
    arm: str,
    layer: str,
    batch: str,
    run_dir: Path,
    seq_path: Path,
    agent: dict | None = None,
) -> list[str]:
    if arm == AGENT_ARM:
        return agent_command(arm, layer, batch, run_dir, seq_path, agent or {})
    return [
        rzp_bin,
        "run",
        "-arm",
        arm,
        "-layer",
        layer,
        "-batch",
        batch,
        "-run-dir",
        str(run_dir),
        "-order-sequence",
        str(seq_path),
    ]


def agent_command(
    arm: str, layer: str, batch: str, run_dir: Path, seq_path: Path, agent: dict
) -> list[str]:
    """The a2-agent driver. Same batch, same layer, same run directory, same
    order sequence: what differs is that the decision maker is a model."""
    cmd = [
        sys.executable,
        "-m",
        "harness.agent_runner",
        "--batch",
        batch,
        "--layer",
        layer,
        "--run-dir",
        str(run_dir),
        "--order-sequence",
        str(seq_path),
        "--server-binary",
        str(agent.get("server_binary") or "./bin/rzp-mcp"),
        "--prompt",
        str(agent.get("prompt") or DEFAULT_PROMPT_PATH),
        "--model",
        str(agent.get("model") or "sonnet"),
        "--max-budget-usd",
        str(agent.get("max_budget_usd") or 0.50),
    ]
    if agent.get("max_invocations"):
        cmd += ["--max-invocations", str(agent["max_invocations"])]
    if agent.get("action_budget"):
        cmd += ["--action-budget", str(agent["action_budget"])]
    if agent.get("timeout_sec"):
        cmd += ["--timeout-sec", str(agent["timeout_sec"])]
    return cmd


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="Run every arm over one seeded batch and write a run manifest"
    )
    parser.add_argument("--batch", required=True, help="path to a batch manifest json")
    parser.add_argument("--arms", default=DEFAULT_ARMS, help="comma separated arm ids")
    parser.add_argument("--seed", type=int, default=42, help="shuffle seed")
    parser.add_argument(
        "--layer", default="", help="live, replay, or fake; defaults to the batch's layer"
    )
    parser.add_argument("--run-id", default="", help="default r-YYYYmmdd-HHMMSS in UTC")
    parser.add_argument("--rzp-bin", default="./bin/rzp", help="path to the rzp binary")
    parser.add_argument("--out-root", default="results/runs", help="where run dirs go")
    parser.add_argument(
        "--dry-run", action="store_true", help="print the plan and write nothing"
    )
    parser.add_argument(
        "--no-shuffle", action="store_true", help="keep arm-major batch order"
    )
    parser.add_argument(
        "--prompt", default=DEFAULT_PROMPT_PATH, help="the agent charter, for a2-agent"
    )
    parser.add_argument("--mcp-bin", default="./bin/rzp-mcp", help="the mcp server binary")
    parser.add_argument("--model", default="sonnet", help="the model a2-agent runs")
    parser.add_argument("--max-budget-usd", type=float, default=0.50)
    parser.add_argument(
        "--max-invocations",
        type=int,
        default=0,
        help="hard stop on a2-agent's headless invocations, zero means no stop",
    )
    parser.add_argument("--agent-action-budget", type=int, default=0)
    parser.add_argument("--agent-timeout-sec", type=int, default=0)
    args = parser.parse_args(argv)

    batch_path = args.batch
    batch_manifest = json.loads(Path(batch_path).read_text(encoding="utf-8"))
    order_ids = [str(o.get("order_id") or "") for o in batch_manifest.get("orders", [])]
    if not order_ids:
        print("batch has no orders: " + batch_path, file=sys.stderr)
        return 2

    arms = [a.strip() for a in args.arms.split(",") if a.strip()]
    if not arms:
        print("no arms given", file=sys.stderr)
        return 2

    layer = args.layer or str(batch_manifest.get("layer") or "")
    if not layer:
        print(
            "no layer: pass --layer, or seed a batch that records one",
            file=sys.stderr,
        )
        return 2

    run_id = args.run_id or default_run_id()
    run_dir = Path(args.out_root) / run_id

    cells = build_cells(arms, order_ids)
    shuffled = not args.no_shuffle
    if shuffled:
        shuffle_cells(cells, args.seed)

    agent = {
        "server_binary": args.mcp_bin,
        "prompt": args.prompt,
        "model": args.model,
        "max_budget_usd": args.max_budget_usd,
        "max_invocations": args.max_invocations,
        "action_budget": args.agent_action_budget,
        "timeout_sec": args.agent_timeout_sec,
    }

    started_at = datetime.datetime.now(datetime.UTC).strftime("%Y-%m-%dT%H:%M:%SZ")
    manifest = build_manifest(
        run_id=run_id,
        seed=args.seed,
        arms=arms,
        batch_manifest=batch_manifest,
        batch_path=batch_path,
        layer=layer,
        cells=cells,
        shuffled=shuffled,
        started_at=started_at,
        policy=policy_config(args.rzp_bin),
        prompt_digest=prompt_sha256(arms, args.prompt),
        agent=agent if AGENT_ARM in arms else {},
    )

    if args.dry_run:
        print("dry run, nothing written")
        print("run_id: " + run_id)
        print("run_dir: " + str(run_dir))
        print("layer: " + layer)
        print("batch: " + batch_path + " (" + str(len(order_ids)) + " orders)")
        print("arms: " + ", ".join(arms))
        print("seed: " + str(args.seed) + ", shuffled: " + str(shuffled))
        print("git_sha: " + manifest["git_sha"])
        print("key_id_prefix: " + manifest["key_id_prefix"])
        print("cells: " + str(len(cells)))
        for arm in arms:
            seq = arm_sequence(cells, arm)
            seq_path = run_dir / arm / "order_sequence.txt"
            print("would write " + str(seq_path) + " (" + str(len(seq)) + " ids)")
            print(
                "would run "
                + " ".join(
                    arm_command(
                        args.rzp_bin, arm, layer, batch_path, run_dir, seq_path, agent
                    )
                )
            )
        return 0

    run_dir.mkdir(parents=True, exist_ok=True)
    (run_dir / "manifest.json").write_text(
        json.dumps(manifest, indent=2) + "\n", encoding="utf-8"
    )

    for arm in arms:
        arm_dir = run_dir / arm
        arm_dir.mkdir(parents=True, exist_ok=True)
        seq_path = arm_dir / "order_sequence.txt"
        seq = arm_sequence(cells, arm)
        seq_path.write_text("\n".join(seq) + "\n", encoding="utf-8")

        cmd = arm_command(
            args.rzp_bin, arm, layer, batch_path, run_dir, seq_path, agent
        )
        print("running: " + " ".join(cmd), file=sys.stderr)
        # No capture_output: the child inherits stdout and stderr so a long arm
        # streams instead of going quiet for minutes.
        completed = subprocess.run(cmd, check=False)
        if completed.returncode != 0:
            # Abort the whole run. A partial run whose later arms are missing
            # would still aggregate, and the table would compare a full arm
            # against a truncated one.
            print(
                "arm "
                + arm
                + " exited "
                + str(completed.returncode)
                + ", aborting run "
                + run_id,
                file=sys.stderr,
            )
            return completed.returncode

    # Record which run finished last, by name rather than by mtime.
    #
    # scripts/report.sh used to pick the newest directory under results/runs/,
    # so touching an older run made `make report` score the wrong one and
    # overwrite that run's committed table in place. Review finding,
    # 2026-08-31.
    Path(args.out_root).joinpath("LAST_RUN").write_text(run_id + "\n", encoding="utf-8")

    print("run complete: " + str(run_dir), file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
