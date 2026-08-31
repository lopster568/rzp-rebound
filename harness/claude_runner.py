"""Drive one order through the Claude Code CLI, headless.

Transplanted from ~/jaeger-mcp-bench/harness/claude_runner.py, which learned
four things the hard way and all four are kept here:

1. Parse the result envelope's `subtype` before branching on the return code.
   A controlled-failure exit can be non-zero and still print a valid envelope,
   so branching on the code first files budget exhaustion under a generic
   "rc=N" and loses the reason.
2. A transport failure arrives as a clean rc=0 envelope whose `result` text
   starts with "API Error:". Without that check it scores as a real answer. In
   the source project one network blip did that to 18 of 36 trials.
3. The clean-settings file has to be written on every run. It lives in the
   system temp directory, and a reboot that removed it took down 56 trials
   before a single API call was made.
4. `tool_calls` derived from `num_turns` is an approximation. Only
   `--output-format stream-json` gives an exact count, and this arm does not
   need one: the tool calls that matter are counted server side, in the audit
   ledger, by the process that served them.

The equivalent invocation:

    claude -p <prompt> --output-format json --mcp-config <cfg>
           --strict-mcp-config --settings <clean> --disable-slash-commands
           --exclude-dynamic-system-prompt-sections --model sonnet
           --allow-dangerously-skip-permissions --max-budget-usd 0.50
           --no-session-persistence

`--strict-mcp-config` is the load-bearing flag. Without it the invocation
inherits whatever MCP servers the operator has configured, and the containment
claim would be about a tool set nobody wrote down.
"""

from __future__ import annotations

import hashlib
import json
import os
import subprocess
import tempfile
import time
from dataclasses import asdict, dataclass
from pathlib import Path


# The MCP server alias. It reaches the model in every tool name as
# `mcp__rzp__<tool>`, and the permission glob in CLEAN_SETTINGS has to match
# it. Renaming it in one place and not the other fails silently, with every
# tool call denied by the CLI before it reaches this project's own gate.
SERVER_ALIAS = "rzp"

# The clean settings the CLI runs under: no plugins, no marketplaces, and
# permission to call this project's MCP tools and nothing else. The baseline
# has to be the same on any machine, or the arm is measuring the operator's
# configuration.
CLEAN_SETTINGS = {
    "enabledPlugins": {},
    "permissions": {"allow": ["mcp__" + SERVER_ALIAS + "__*"], "deny": []},
    "extraKnownMarketplaces": {},
}

DEFAULT_MODEL = "sonnet"
DEFAULT_MAX_BUDGET_USD = 0.50
DEFAULT_TIMEOUT_SEC = 240

# A transport failure dressed as an answer. See point 2 in the module doc.
API_ERROR_MARKER = "API Error:"
INFRA_ERROR = "api_connection_error"

# One retry, not two. The phase 3 budget is 60 invocations for the night and a
# retry spends one of them, so the retry count is a budget decision as much as
# a reliability one. PLAN.md has the arithmetic.
INFRA_RETRIES = 1
INFRA_RETRY_WAIT_SEC = 20

# Envelope subtypes that are not a completed answer.
SUBTYPE_SUCCESS = "success"
SUBTYPE_BUDGET_EXHAUSTED = "error_max_budget_usd"

# Variables the mcp config has to restate for the server process.
#
# The CLI starts the server with the parent environment, which is how the
# Razorpay credentials reach it without ever being written to a file. It makes
# one exception: it strips OTEL_* from the child, because those configure its
# own telemetry. So the server got no OTLP endpoint, built no exporter, and
# every audit row came out with an empty trace id, which is FR-AUD-3 silently
# not met.
#
# Measured on 2026-09-01 with a probe server that dumped its environment: 58
# variables arrived, RAZORPAY_KEY_ID among them, OTEL_EXPORTER_OTLP_ENDPOINT
# not. PROBLEMS.md has it.
#
# These carry a host and port and a service name. None of them is a
# credential, so restating them by value in the config file leaks nothing.
PASSTHROUGH_ENV = (
    "OTEL_EXPORTER_OTLP_ENDPOINT",
    "OTEL_SERVICE_NAME",
    "RZP_JAEGER_UI_URL",
)


@dataclass
class Invocation:
    """What one headless invocation produced.

    `unscorable` is the field the report reads. An invocation that failed for
    an infrastructure reason produced no decision, so scoring it either way
    would charge the harness to the arm. Per docs/EVAL-DESIGN.md section 5 it
    is counted, explained, and left out of every denominator.
    """

    order_id: str
    arm: str
    ok: bool
    unscorable: bool
    reason: str
    answer: str
    error: str
    input_tokens: int | None
    output_tokens: int | None
    cache_read_tokens: int | None
    cache_creation_tokens: int | None
    cost_usd: float | None
    duration_ms: int
    num_turns: int
    tool_calls: int
    budget_exhausted: bool
    attempts: int


def clean_settings_path(tmp_dir: str | None = None) -> Path:
    """Where the clean settings file lives."""
    root = Path(tmp_dir) if tmp_dir else Path(tempfile.gettempdir())
    return root / "rzp-bench-clean-settings.json"


def ensure_clean_settings(tmp_dir: str | None = None) -> Path:
    """Write the clean settings file and return its path.

    Written every call, not only when it is missing. See point 3 in the module
    doc: the file lives in a directory the operating system is entitled to
    empty, and a run that assumed it was still there lost every invocation
    before a single API call was made.
    """
    path = clean_settings_path(tmp_dir)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(CLEAN_SETTINGS, indent=2) + "\n", encoding="utf-8")
    return path


def mcp_config(
    *,
    server_binary: str,
    batch_path: str,
    order_id: str,
    layer: str,
    run_dir: str,
    arm: str,
    card: str = "",
    kill_switch_file: str = "",
    action_budget: int = 0,
) -> dict:
    """The --mcp-config document for one invocation.

    Exactly one server. ADR-0001 says the model's whole reach is this tool set,
    and a config with a second server in it would make that false without
    anything failing.
    """
    args = [
        "-batch", batch_path,
        "-order", order_id,
        "-layer", layer,
        "-run-dir", run_dir,
        "-arm", arm,
    ]
    if card:
        args += ["-card", card]
    if kill_switch_file:
        args += ["-kill-switch-file", kill_switch_file]
    if action_budget:
        args += ["-action-budget", str(action_budget)]

    # The server inherits the parent environment, which is where
    # RAZORPAY_KEY_ID and RAZORPAY_KEY_SECRET are. No credential is written
    # here: a key in a config file is a key on disk in a directory this harness
    # does not own.
    #
    # What is written here is the telemetry configuration, because the CLI
    # strips OTEL_* on its way to the child. See PASSTHROUGH_ENV. A variable
    # that is not set in this process is left out rather than written empty, so
    # the file says what was configured and not what was not.
    env = {}
    for name in PASSTHROUGH_ENV:
        value = os.environ.get(name, "").strip()
        if value:
            env[name] = value

    return {
        "mcpServers": {
            SERVER_ALIAS: {
                "command": server_binary,
                "args": args,
                "env": env,
            }
        }
    }


def write_mcp_config(config: dict, out_path: str | Path) -> Path:
    """Write an mcp config and return its path."""
    path = Path(out_path)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(config, indent=2) + "\n", encoding="utf-8")
    return path


def build_command(
    *,
    prompt: str,
    mcp_config_path: str | Path,
    settings_path: str | Path,
    model: str = DEFAULT_MODEL,
    max_budget_usd: float = DEFAULT_MAX_BUDGET_USD,
) -> list[str]:
    """The argv of one headless invocation.

    Every flag here is load bearing and PLAN.md says why. The two that are
    easiest to drop by accident are --strict-mcp-config, without which the run
    inherits the operator's own MCP servers, and --settings, without which it
    inherits their plugins.
    """
    return [
        "claude",
        "-p", prompt,
        "--output-format", "json",
        "--mcp-config", str(mcp_config_path),
        "--strict-mcp-config",
        "--settings", str(settings_path),
        "--disable-slash-commands",
        "--exclude-dynamic-system-prompt-sections",
        "--model", model,
        "--allow-dangerously-skip-permissions",
        "--max-budget-usd", str(max_budget_usd),
        "--no-session-persistence",
    ]


def _int_or_none(value):
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        return None
    return int(value)


def parse_envelope(stdout: str, returncode: int, elapsed_ms: int) -> dict:
    """Read the --output-format json result envelope.

    `subtype` is read before `returncode` is considered. A controlled-failure
    exit can be non-zero and still print a valid envelope, and branching on the
    code first files budget exhaustion under a generic rc=N and loses the
    reason. Point 1 in the module doc.
    """
    parsed = None
    parse_error = ""
    try:
        parsed = json.loads(stdout)
    except (json.JSONDecodeError, TypeError) as err:
        parse_error = str(err)

    blank = {
        "answer": "",
        "error": "",
        "input_tokens": None,
        "output_tokens": None,
        "cache_read_tokens": None,
        "cache_creation_tokens": None,
        "cost_usd": None,
        "duration_ms": elapsed_ms,
        "num_turns": 0,
        "tool_calls": 0,
        "budget_exhausted": False,
        "ok": False,
        "unscorable": True,
        "reason": "",
    }

    if isinstance(parsed, dict) and "subtype" in parsed:
        subtype = str(parsed.get("subtype") or "")
        usage = parsed.get("usage") or {}
        num_turns = _int_or_none(parsed.get("num_turns")) or 0
        answer = str(parsed.get("result") or "")

        out = dict(blank)
        out.update(
            {
                "input_tokens": _int_or_none(usage.get("input_tokens")),
                "output_tokens": _int_or_none(usage.get("output_tokens")),
                "cache_read_tokens": _int_or_none(usage.get("cache_read_input_tokens")),
                "cache_creation_tokens": _int_or_none(
                    usage.get("cache_creation_input_tokens")
                ),
                "cost_usd": parsed.get("total_cost_usd"),
                "duration_ms": _int_or_none(parsed.get("duration_ms")) or elapsed_ms,
                "num_turns": num_turns,
                # An approximation, and only an approximation. One tool call is
                # about two extra turns. The tool calls that matter are counted
                # server side by the process that served them, in the audit
                # ledger, and that count is the one the report uses.
                "tool_calls": max(0, (num_turns - 1) // 2),
            }
        )

        # A transport failure dressed as an answer, rc=0 and all. Point 2 in
        # the module doc: without this check it scores as a real answer.
        if answer.startswith(API_ERROR_MARKER):
            out["error"] = INFRA_ERROR
            out["reason"] = "the cli returned a transport error as its answer"
            return out

        out["answer"] = answer
        out["budget_exhausted"] = subtype == SUBTYPE_BUDGET_EXHAUSTED
        out["ok"] = True
        if subtype == SUBTYPE_SUCCESS:
            out["unscorable"] = False
            out["reason"] = "the invocation completed"
        else:
            out["error"] = subtype
            out["unscorable"] = True
            out["reason"] = "the invocation ended on " + subtype
        return out

    out = dict(blank)
    if returncode != 0:
        out["error"] = "rc=" + str(returncode)
        out["reason"] = "the cli exited " + str(returncode) + " and printed no result envelope"
        return out
    if parse_error:
        out["error"] = "json decode: " + parse_error
        out["reason"] = "the cli printed something that is not a result envelope"
        return out

    out["error"] = "no subtype in the envelope"
    out["reason"] = "the envelope has no subtype, so what happened cannot be read off it"
    return out


def _default_runner(cmd, timeout_sec):
    return subprocess.run(
        cmd, capture_output=True, text=True, timeout=timeout_sec, check=False
    )


def run_once(
    *,
    prompt: str,
    order_id: str,
    arm: str,
    mcp_config_path: str | Path,
    settings_path: str | Path,
    model: str = DEFAULT_MODEL,
    max_budget_usd: float = DEFAULT_MAX_BUDGET_USD,
    timeout_sec: int = DEFAULT_TIMEOUT_SEC,
    runner=None,
) -> Invocation:
    """One invocation, no retry.

    `runner` is the subprocess call, injected so the tests do not need the CLI
    on PATH and do not spend a subscription to assert an argv.
    """
    cmd = build_command(
        prompt=prompt,
        mcp_config_path=mcp_config_path,
        settings_path=settings_path,
        model=model,
        max_budget_usd=max_budget_usd,
    )

    started = time.monotonic()
    try:
        if runner is None:
            proc = _default_runner(cmd, timeout_sec)
        else:
            proc = runner(cmd, timeout=timeout_sec)
    except subprocess.TimeoutExpired:
        return Invocation(
            order_id=order_id,
            arm=arm,
            ok=False,
            unscorable=True,
            reason="the invocation did not finish inside " + str(timeout_sec) + "s",
            answer="",
            error="timeout",
            input_tokens=None,
            output_tokens=None,
            cache_read_tokens=None,
            cache_creation_tokens=None,
            cost_usd=None,
            duration_ms=timeout_sec * 1000,
            num_turns=0,
            tool_calls=0,
            budget_exhausted=False,
            attempts=1,
        )
    except OSError as err:
        return Invocation(
            order_id=order_id,
            arm=arm,
            ok=False,
            unscorable=True,
            reason="the cli could not be started: " + str(err),
            answer="",
            error=INFRA_ERROR,
            input_tokens=None,
            output_tokens=None,
            cache_read_tokens=None,
            cache_creation_tokens=None,
            cost_usd=None,
            duration_ms=0,
            num_turns=0,
            tool_calls=0,
            budget_exhausted=False,
            attempts=1,
        )

    elapsed_ms = int((time.monotonic() - started) * 1000)
    parsed = parse_envelope(proc.stdout, proc.returncode, elapsed_ms)

    return Invocation(
        order_id=order_id,
        arm=arm,
        ok=parsed["ok"],
        unscorable=parsed["unscorable"],
        reason=parsed["reason"],
        answer=parsed["answer"],
        error=parsed["error"],
        input_tokens=parsed["input_tokens"],
        output_tokens=parsed["output_tokens"],
        cache_read_tokens=parsed["cache_read_tokens"],
        cache_creation_tokens=parsed["cache_creation_tokens"],
        cost_usd=parsed["cost_usd"],
        duration_ms=parsed["duration_ms"],
        num_turns=parsed["num_turns"],
        tool_calls=parsed["tool_calls"],
        budget_exhausted=parsed["budget_exhausted"],
        attempts=1,
    )


def run(
    *,
    prompt: str,
    order_id: str,
    arm: str,
    mcp_config_path: str | Path,
    settings_path: str | Path,
    model: str = DEFAULT_MODEL,
    max_budget_usd: float = DEFAULT_MAX_BUDGET_USD,
    timeout_sec: int = DEFAULT_TIMEOUT_SEC,
    runner=None,
    sleep=None,
) -> Invocation:
    """One invocation with the infra retry.

    Only an infrastructure failure is retried. A budget-exhausted invocation is
    not: the model spent the cap, and spending it again would cost the same
    money for the same reason.
    """
    sleep = sleep or time.sleep
    attempts = 0
    result = None

    for attempt in range(INFRA_RETRIES + 1):
        attempts = attempt + 1
        result = run_once(
            prompt=prompt,
            order_id=order_id,
            arm=arm,
            mcp_config_path=mcp_config_path,
            settings_path=settings_path,
            model=model,
            max_budget_usd=max_budget_usd,
            timeout_sec=timeout_sec,
            runner=runner,
        )
        result.attempts = attempts
        if result.error not in (INFRA_ERROR, "timeout"):
            return result
        if attempt < INFRA_RETRIES:
            sleep(INFRA_RETRY_WAIT_SEC)

    result.attempts = attempts
    result.unscorable = True
    result.reason = (
        "the invocation failed for an infrastructure reason "
        + str(attempts)
        + " time(s), so it produced no decision to score"
    )
    return result


def prompt_sha256(path: str | Path) -> str:
    """The digest of the prompt file that ran.

    Computed from the bytes on disk, not from a string a caller passed in. The
    run manifest's prompt_sha256 is meant to identify the file that ran, and a
    digest of anything else identifies nothing.
    """
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def append_invocation(path: str | Path, invocation: Invocation) -> None:
    """Append one row to invocations.jsonl.

    Every invocation gets a row, including the unscorable ones. The cost
    columns in the report are sums over this file, and an unscorable
    invocation spent the same subscription as a scorable one.
    """
    target = Path(path)
    target.parent.mkdir(parents=True, exist_ok=True)
    with target.open("a", encoding="utf-8") as handle:
        handle.write(json.dumps(asdict(invocation), sort_keys=True) + "\n")
