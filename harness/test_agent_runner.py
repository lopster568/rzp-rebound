"""Tests for harness/agent_runner.py.

Both of these are about the same thing: an order that produced a decision must
not be filed as unscorable because of when the harness happened to look.
"""

from __future__ import annotations

import json
import sys
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory

sys.path.insert(0, str(Path(__file__).resolve().parent))
import agent_runner  # noqa: E402


def write_row(path: Path, order_id: str) -> None:
    with path.open("a", encoding="utf-8") as handle:
        handle.write(json.dumps({"manifest_order_id": order_id}) + "\n")


class WaitForOutcomeTest(unittest.TestCase):
    def test_a_row_that_arrives_late_is_still_found(self):
        # The server writes its outcome row on the way out, after the client
        # has disconnected, so for a moment the CLI has returned and the row is
        # not on disk. Declaring it missing in that window files a completed
        # order as unscorable.
        with TemporaryDirectory() as tmp:
            path = Path(tmp) / "outcomes.jsonl"
            clock = {"t": 0.0}
            written = {"done": False}

            def now():
                return clock["t"]

            def sleep(d):
                clock["t"] += d
                if clock["t"] >= 0.5 and not written["done"]:
                    write_row(path, "order_late")
                    written["done"] = True

            self.assertTrue(
                agent_runner.wait_for_outcome(
                    path, "order_late", timeout=5.0, poll=0.1, now=now, sleep=sleep
                )
            )

    def test_a_row_that_never_arrives_gives_up_at_the_deadline(self):
        with TemporaryDirectory() as tmp:
            path = Path(tmp) / "outcomes.jsonl"
            clock = {"t": 0.0}

            def now():
                return clock["t"]

            def sleep(d):
                clock["t"] += d

            self.assertFalse(
                agent_runner.wait_for_outcome(
                    path, "order_never", timeout=1.0, poll=0.1, now=now, sleep=sleep
                )
            )
            self.assertGreaterEqual(clock["t"], 1.0)

    def test_an_unscorable_row_is_not_observed_and_takes_no_action(self):
        # harness/scorer.py reads `observed` to decide scorability. A fallback
        # row that claimed to be observed would be scored as a failure to
        # recover, which charges the harness to the arm.
        row = agent_runner.unscorable_row(
            run_id="r-1",
            layer="live",
            batch_id="b-8080-8",
            order_id="order_x",
            reason="the session wrote no outcome row",
        )
        self.assertFalse(row["observed"])
        self.assertFalse(row["recovered"])
        self.assertFalse(row["escalated"])
        self.assertFalse(row["side_effect"])
        self.assertEqual(row["action_kind"], "none")
        self.assertEqual(row["arm"], agent_runner.ARM)
        self.assertIn("no outcome row", row["error"])


if __name__ == "__main__":
    unittest.main()
