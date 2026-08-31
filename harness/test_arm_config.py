"""Tests for harness/arm_config.py.

The load-bearing one is
test_a2_config_matches_the_other_arms_except_the_decision_maker. The four-arm
table in RESULTS.md is a comparison of decision makers, and it only is one if
everything else about the four runs was the same. That property is asserted
here rather than described in a document.
"""

from __future__ import annotations

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import arm_config  # noqa: E402


SHARED = {
    "batch_path": "results/batches/b-1234-40.json",
    "batch_id": "b-1234-40",
    "layer": "fake",
    "run_dir": "results/runs/r-20260901-000000",
}


class ArmConfigTest(unittest.TestCase):
    def test_a2_config_matches_the_other_arms_except_the_decision_maker(self):
        agent = arm_config.build(arm_config.ARM_AGENT, **SHARED)
        rules = arm_config.build(arm_config.ARM_RULES, **SHARED)

        self.assertEqual(
            arm_config.differing_keys(agent, rules),
            set(arm_config.IDENTITY_EXEMPT),
            "a2-agent and a3-rules differ in something other than the arm label "
            "and the decision maker, so the four-arm table is not a comparison "
            "of decisions",
        )

        # And against every other arm, not only the rules arm.
        for arm in arm_config.ARMS:
            if arm == arm_config.ARM_AGENT:
                continue
            other = arm_config.build(arm, **SHARED)
            self.assertEqual(
                arm_config.differing_keys(agent, other),
                set(arm_config.IDENTITY_EXEMPT),
                "a2-agent and " + arm + " differ in more than the decision maker",
            )

    def test_every_arm_gets_the_same_layer_batch_and_policy(self):
        configs = [arm_config.build(arm, **SHARED) for arm in arm_config.ARMS]
        for key in ("layer", "batch_path", "batch_id", "policy", "card", "currency"):
            values = {repr(c[key]) for c in configs}
            self.assertEqual(
                len(values), 1, "the four arms disagree about " + key + ": " + repr(values)
            )

    def test_a_different_agent_budget_fails_the_identity_assertion(self):
        # The test that proves the test above can fail. Without this, a
        # differing_keys that always returned the exempt set would pass
        # everything.
        agent = arm_config.build(arm_config.ARM_AGENT, action_budget=99, **SHARED)
        rules = arm_config.build(arm_config.ARM_RULES, **SHARED)
        self.assertIn(
            "action_budget",
            arm_config.differing_keys(agent, rules),
            "a config built with a different action budget did not show up as a "
            "difference, so the identity assertion proves nothing",
        )

    def test_an_unknown_arm_is_an_error(self):
        with self.assertRaises(Exception):
            arm_config.decision_maker("a9-typo")

    def test_the_agent_is_the_only_llm_decision_maker(self):
        for arm in arm_config.ARMS:
            maker = arm_config.decision_maker(arm)
            if arm == arm_config.ARM_AGENT:
                self.assertTrue(maker.startswith("llm:"), maker)
            else:
                self.assertTrue(maker.startswith("deterministic:"), maker)


if __name__ == "__main__":
    unittest.main()
