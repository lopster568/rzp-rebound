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

from dataclasses import dataclass
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
    raise NotImplementedError("harness/claude_runner.clean_settings_path")


def ensure_clean_settings(tmp_dir: str | None = None) -> Path:
    """Write the clean settings file and return its path. Written every call,
    not only when it is missing: see point 3 in the module doc."""
    raise NotImplementedError("harness/claude_runner.ensure_clean_settings")


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
    """The --mcp-config document for one invocation: exactly one server, the
    compiled rzp-mcp binary, pointed at one order."""
    raise NotImplementedError("harness/claude_runner.mcp_config")


def write_mcp_config(config: dict, out_path: str | Path) -> Path:
    """Write an mcp config and return its path."""
    raise NotImplementedError("harness/claude_runner.write_mcp_config")


def build_command(
    *,
    prompt: str,
    mcp_config_path: str | Path,
    settings_path: str | Path,
    model: str = DEFAULT_MODEL,
    max_budget_usd: float = DEFAULT_MAX_BUDGET_USD,
) -> list[str]:
    """The argv of one headless invocation."""
    raise NotImplementedError("harness/claude_runner.build_command")


def parse_envelope(stdout: str, returncode: int, elapsed_ms: int) -> dict:
    """Read the --output-format json result envelope.

    Returns a dict with the fields Invocation carries. `subtype` is read before
    `returncode` is considered, per point 1 in the module doc.
    """
    raise NotImplementedError("harness/claude_runner.parse_envelope")


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
    """One invocation, no retry. `runner` is the subprocess call, injected so a
    test does not need the CLI on PATH."""
    raise NotImplementedError("harness/claude_runner.run_once")


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
    """One invocation with the infra retry. An invocation that fails twice for
    an infrastructure reason comes back unscorable."""
    raise NotImplementedError("harness/claude_runner.run")


def prompt_sha256(path: str | Path) -> str:
    """The digest of the prompt file that ran, for the run manifest's
    prompt_sha256 field."""
    raise NotImplementedError("harness/claude_runner.prompt_sha256")


def append_invocation(path: str | Path, invocation: Invocation) -> None:
    """Append one row to invocations.jsonl. Every invocation gets a row,
    including the unscorable ones, so the cost total is the cost spent."""
    raise NotImplementedError("harness/claude_runner.append_invocation")
