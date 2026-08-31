"""Tests for harness/claude_runner.py.

None of these starts the CLI. The subprocess call is injected, so the tests
pin the four things that went wrong in the source project this module was
transplanted from: the envelope is parsed before the return code, a transport
failure dressed as an answer is caught, an infra failure is retried once and
then declared unscorable, and every invocation writes a cost row whatever
happened to it.
"""

from __future__ import annotations

import json
import sys
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory

sys.path.insert(0, str(Path(__file__).resolve().parent))
import claude_runner  # noqa: E402


class FakeProc:
    """What subprocess.run returns, as much of it as the parser reads."""

    def __init__(self, stdout: str = "", stderr: str = "", returncode: int = 0):
        self.stdout = stdout
        self.stderr = stderr
        self.returncode = returncode


def envelope(**overrides) -> str:
    body = {
        "type": "result",
        "subtype": "success",
        "result": "I retried order_x and it settled as paid.",
        "num_turns": 7,
        "duration_ms": 41230,
        "total_cost_usd": 0.0412,
        "usage": {
            "input_tokens": 5120,
            "output_tokens": 640,
            "cache_read_input_tokens": 20480,
            "cache_creation_input_tokens": 1024,
        },
    }
    body.update(overrides)
    return json.dumps(body)


def scripted(*procs):
    """A runner that returns each proc in turn and records the argv it saw."""
    calls = []
    remaining = list(procs)

    def run(cmd, **kwargs):
        calls.append(cmd)
        if not remaining:
            raise AssertionError("the runner was called more times than the test scripted")
        return remaining.pop(0)

    run.calls = calls
    return run


def invoke(runner, **overrides):
    kwargs = {
        "prompt": "recover order_x",
        "order_id": "order_x",
        "arm": "a2-agent",
        "mcp_config_path": "/tmp/cfg.json",
        "settings_path": "/tmp/settings.json",
        "sleep": lambda _: None,
        "runner": runner,
    }
    kwargs.update(overrides)
    return claude_runner.run(**kwargs)


class CommandTest(unittest.TestCase):
    def test_command_carries_the_strict_flag_the_model_and_the_budget(self):
        cmd = claude_runner.build_command(
            prompt="recover order_x",
            mcp_config_path="/tmp/cfg.json",
            settings_path="/tmp/settings.json",
        )

        self.assertEqual(cmd[0], "claude")
        self.assertIn(
            "--strict-mcp-config",
            cmd,
            "without --strict-mcp-config the invocation inherits the operator's "
            "own MCP servers, and the containment claim is about a tool set "
            "nobody wrote down",
        )
        self.assertIn("--no-session-persistence", cmd)
        self.assertIn("--allow-dangerously-skip-permissions", cmd)

        for flag, value in (
            ("-p", "recover order_x"),
            ("--output-format", "json"),
            ("--mcp-config", "/tmp/cfg.json"),
            ("--settings", "/tmp/settings.json"),
            ("--model", claude_runner.DEFAULT_MODEL),
            ("--max-budget-usd", str(claude_runner.DEFAULT_MAX_BUDGET_USD)),
        ):
            self.assertIn(flag, cmd, flag)
            self.assertEqual(cmd[cmd.index(flag) + 1], value, flag)

    def test_mcp_config_names_only_the_rzp_server(self):
        cfg = claude_runner.mcp_config(
            server_binary="/repo/bin/rzp-mcp",
            batch_path="results/batches/b-1234-40.json",
            order_id="order_ab12",
            layer="fake",
            run_dir="results/runs/r-1",
            arm="a2-agent",
        )

        self.assertEqual(list(cfg.keys()), ["mcpServers"])
        servers = cfg["mcpServers"]
        self.assertEqual(
            list(servers.keys()),
            [claude_runner.SERVER_ALIAS],
            "one server, because the agent's whole reach is this tool set",
        )
        entry = servers[claude_runner.SERVER_ALIAS]
        self.assertEqual(entry["command"], "/repo/bin/rzp-mcp")
        self.assertIn("-order", entry["args"])
        self.assertEqual(entry["args"][entry["args"].index("-order") + 1], "order_ab12")
        self.assertIn("-batch", entry["args"])
        self.assertIn("-run-dir", entry["args"])

        # The permission glob in the clean settings has to name the same alias,
        # or the CLI denies every tool call before this project's gate sees it.
        allow = claude_runner.CLEAN_SETTINGS["permissions"]["allow"]
        self.assertIn("mcp__" + claude_runner.SERVER_ALIAS + "__*", allow)


class EnvelopeTest(unittest.TestCase):
    def test_envelope_parsing_reads_cost_tokens_and_duration(self):
        result = invoke(scripted(FakeProc(stdout=envelope())))

        self.assertTrue(result.ok)
        self.assertFalse(result.unscorable)
        self.assertEqual(result.input_tokens, 5120)
        self.assertEqual(result.output_tokens, 640)
        self.assertEqual(result.cache_read_tokens, 20480)
        self.assertEqual(result.cache_creation_tokens, 1024)
        self.assertAlmostEqual(result.cost_usd, 0.0412)
        self.assertEqual(result.duration_ms, 41230)
        self.assertEqual(result.num_turns, 7)

    def test_a_nonzero_exit_with_an_envelope_is_read_from_the_envelope(self):
        # The reason the parser reads subtype before returncode. A controlled
        # failure can exit non-zero and still say what happened.
        result = invoke(
            scripted(
                FakeProc(
                    stdout=envelope(subtype=claude_runner.SUBTYPE_BUDGET_EXHAUSTED),
                    returncode=1,
                )
            )
        )
        self.assertTrue(result.budget_exhausted)
        self.assertNotIn("rc=", result.error)


class FailureTest(unittest.TestCase):
    def test_an_infra_error_is_retried_once_and_then_unscorable(self):
        api_error = envelope(result=claude_runner.API_ERROR_MARKER + " connect ECONNREFUSED")
        runner = scripted(FakeProc(stdout=api_error), FakeProc(stdout=api_error))

        result = invoke(runner)

        self.assertEqual(len(runner.calls), 2, "the infra failure was not retried exactly once")
        self.assertEqual(result.attempts, 2)
        self.assertTrue(result.unscorable)
        self.assertEqual(result.error, claude_runner.INFRA_ERROR)

    def test_a_retry_that_succeeds_is_scorable(self):
        api_error = envelope(result=claude_runner.API_ERROR_MARKER + " connect ECONNREFUSED")
        runner = scripted(FakeProc(stdout=api_error), FakeProc(stdout=envelope()))

        result = invoke(runner)

        self.assertEqual(len(runner.calls), 2)
        self.assertFalse(result.unscorable)
        self.assertTrue(result.ok)

    def test_a_budget_exhausted_invocation_is_unscorable(self):
        result = invoke(
            scripted(FakeProc(stdout=envelope(subtype=claude_runner.SUBTYPE_BUDGET_EXHAUSTED)))
        )
        self.assertTrue(result.budget_exhausted)
        self.assertTrue(
            result.unscorable,
            "hitting the spend cap is an infrastructure limit, not a decision "
            "the arm made, so it does not belong in a denominator",
        )

    def test_unparseable_output_is_unscorable_rather_than_an_answer(self):
        result = invoke(scripted(FakeProc(stdout="not json at all", returncode=0)))
        self.assertTrue(result.unscorable)
        self.assertEqual(result.answer, "")


class CostRowTest(unittest.TestCase):
    def test_every_invocation_writes_a_row_including_the_unscorable_ones(self):
        api_error = envelope(result=claude_runner.API_ERROR_MARKER + " connect ECONNREFUSED")
        with TemporaryDirectory() as tmp:
            path = Path(tmp) / "invocations.jsonl"

            good = invoke(scripted(FakeProc(stdout=envelope())))
            bad = invoke(scripted(FakeProc(stdout=api_error), FakeProc(stdout=api_error)))
            claude_runner.append_invocation(path, good)
            claude_runner.append_invocation(path, bad)

            rows = [json.loads(line) for line in path.read_text().splitlines() if line.strip()]

        self.assertEqual(len(rows), 2, "an unscorable invocation still cost money and still needs a row")
        self.assertTrue(rows[1]["unscorable"])
        for row in rows:
            self.assertIn("order_id", row)
            self.assertIn("cost_usd", row)
            self.assertIn("duration_ms", row)

    def test_prompt_sha256_is_the_digest_of_the_prompt_file(self):
        import hashlib

        with TemporaryDirectory() as tmp:
            path = Path(tmp) / "agent_system.md"
            body = "# charter\n\nRecover revenue, one order at a time.\n"
            path.write_text(body, encoding="utf-8")

            want = hashlib.sha256(body.encode("utf-8")).hexdigest()
            self.assertEqual(claude_runner.prompt_sha256(path), want)


class CleanSettingsTest(unittest.TestCase):
    def test_the_clean_settings_file_is_written_every_call(self):
        with TemporaryDirectory() as tmp:
            path = claude_runner.ensure_clean_settings(tmp)
            self.assertTrue(path.exists())
            path.unlink()
            again = claude_runner.ensure_clean_settings(tmp)
            self.assertTrue(
                again.exists(),
                "the settings file was not rewritten after being removed, which "
                "is the failure that took down a whole run in the source project",
            )
            self.assertEqual(json.loads(again.read_text()), claude_runner.CLEAN_SETTINGS)


if __name__ == "__main__":
    unittest.main()
