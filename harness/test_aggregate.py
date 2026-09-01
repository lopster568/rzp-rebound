"""Tests for harness/aggregate.py.

The table is the phase-2 deliverable, so these pin the three things a reader
would otherwise have to trust: unscorable rows are out of every denominator,
every row names its layer, and the csv and the markdown are the same numbers.
"""

from __future__ import annotations

import csv
import sys
import tempfile
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import aggregate  # noqa: E402
import scorer  # noqa: E402


LAYER = "fake"

RUN_MANIFEST = {
    "run_id": "r-20260831-201500",
    "started_at": "2026-08-31T20:15:00Z",
    "seed": 1234,
    "arms": ["a0-control", "a1-naive", "a3-rules"],
    "batch_id": "b-1234-40",
    "batch_path": "results/batches/b-1234-40.json",
    "layer": LAYER,
    "git_sha": "abc1234",
    "prompt_sha256": "n/a (deterministic arms)",
    "key_id_prefix": "",
    "shuffled": True,
    "cell_order": [],
    "policy": {
        "max_attempts_per_order": 3,
        "cooldown": "30s",
        "amount_ceiling_paise": 400000,
        "action_budget": 500,
    },
}


def order(order_id, cls, recoverable, correct_action, max_legit=3, amount=145000):
    return {
        "order_id": order_id,
        "amount_paise": amount,
        "currency": "INR",
        "receipt": "rcpt_" + order_id,
        "seeded_failure_class": cls,
        "seeded_error_code": "payment_timed_out",
        "seeded_card": "4100280000090000",
        "ground_truth_recoverable": recoverable,
        "ground_truth_correct_action": correct_action,
        "max_legit_attempts": max_legit,
        "is_bait": correct_action == "do_nothing",
        "bait_kind": "never_retry" if correct_action == "do_nothing" else "",
        "prior_attempts": 0,
    }


def batch(orders, batch_id="b-1234-40", seed=1234):
    return {"batch_id": batch_id, "seed": seed, "layer": LAYER, "orders": orders}


def outcome(order_id, **overrides):
    row = {
        "run_id": RUN_MANIFEST["run_id"],
        "arm": "a3-rules",
        "layer": LAYER,
        "batch_id": "b-1234-40",
        "manifest_order_id": order_id,
        "gateway_order_id": "gw_" + order_id,
        "class": "transient_retry_eligible",
        "action_kind": "retry_same_instrument",
        "final_order_status": "attempted",
        "recovered": False,
        "claimed_recovered": False,
        "amount_paid_paise": 0,
        "attempts_seen": 1,
        "attempts_after": 2,
        "policy_verdict": "allow",
        "policy_rule": "R0-DEFAULT-ALLOW",
        "escalated": False,
        "side_effect": True,
        "timed_out": False,
        "error": "",
        "observed": True,
        "api_calls": 5,
    }
    row.update(overrides)
    return row


def row_for(rows, arm, scope):
    for r in rows:
        if r["arm"] == arm and r["scope"] == scope:
            return r
    raise AssertionError("no row for arm=" + arm + " scope=" + scope)


class TestAggregate(unittest.TestCase):
    def test_unscorable_is_excluded_from_every_denominator(self):
        orders = [
            order("o1", "transient_retry_eligible", True, "retry_same_instrument"),
            order("o2", "transient_retry_eligible", True, "retry_same_instrument"),
            order("o3", "transient_retry_eligible", True, "retry_same_instrument"),
        ]
        scorable_rows = [
            outcome("o1", final_order_status="paid", amount_paid_paise=145000),
            outcome("o2", final_order_status="attempted"),
        ]
        unscorable_rows = [
            # The gateway read-back never happened.
            outcome("o3", observed=False, api_calls=0),
            # An outcome for an order the manifest does not have.
            outcome("o_ghost", gateway_order_id="gw_ghost", api_calls=0),
        ]

        with_gaps = aggregate.aggregate(
            RUN_MANIFEST,
            batch(orders),
            {"a3-rules": {"outcomes": scorable_rows + unscorable_rows, "ledger": []}},
        )
        without_gaps = aggregate.aggregate(
            RUN_MANIFEST,
            batch(orders),
            {"a3-rules": {"outcomes": scorable_rows, "ledger": []}},
        )

        gaps_row = row_for(with_gaps, "a3-rules", aggregate.SCOPE_OVERALL)
        clean_row = row_for(without_gaps, "a3-rules", aggregate.SCOPE_OVERALL)

        self.assertEqual(4, gaps_row["n_orders"])
        self.assertEqual(2, gaps_row["n_scorable"])
        self.assertEqual(2, gaps_row["n_unscorable"])

        # Adding two unscorable rows moves nothing but the two count columns.
        for column in aggregate.COLUMNS:
            if column in ("n_orders", "n_unscorable"):
                continue
            self.assertEqual(clean_row[column], gaps_row[column], column)

        # api_calls is a cost column, not a denominator, so an unscorable row
        # that cost calls would still be charged for them.
        expensive = aggregate.aggregate(
            RUN_MANIFEST,
            batch(orders),
            {
                "a3-rules": {
                    "outcomes": scorable_rows + [outcome("o3", observed=False, api_calls=7)],
                    "ledger": [],
                }
            },
        )
        self.assertEqual(
            17, row_for(expensive, "a3-rules", aggregate.SCOPE_OVERALL)["api_calls"]
        )

    def test_every_row_carries_its_layer_and_arm(self):
        orders = [
            order("o1", "transient_retry_eligible", True, "retry_same_instrument"),
            order("o2", "never_retry", False, "do_nothing", max_legit=0),
            order("o3", "reauth_required", True, "request_reauth"),
        ]
        per_arm = {
            "a0-control": {"outcomes": [outcome("o1", action_kind="none")], "ledger": []},
            "a1-naive": {"outcomes": [outcome("o2")], "ledger": []},
            "a3-rules": {"outcomes": [outcome("o3")], "ledger": []},
        }
        rows = aggregate.aggregate(RUN_MANIFEST, batch(orders), per_arm)

        classes = aggregate._class_scopes(batch(orders))
        self.assertEqual(3, len(classes))
        self.assertEqual(len(per_arm) * (1 + len(classes)), len(rows))

        for r in rows:
            self.assertEqual(LAYER, r["layer"])
            self.assertIn(r["arm"], per_arm)
            self.assertTrue(r["layer"], "a row without a layer can be quoted layer-free")
            self.assertTrue(r["arm"])
            self.assertIn(r["scope"], [aggregate.SCOPE_OVERALL] + classes)
            self.assertEqual(sorted(aggregate.COLUMNS), sorted(r.keys()))

        # Class rows are in the canonical order under each arm's overall row.
        for arm in per_arm:
            scopes = [r["scope"] for r in rows if r["arm"] == arm]
            self.assertEqual([aggregate.SCOPE_OVERALL] + classes, scopes)

    def test_markdown_and_csv_agree_on_the_numbers(self):
        orders = [
            order("o1", "transient_retry_eligible", True, "retry_same_instrument"),
            order("o2", "never_retry", False, "do_nothing", max_legit=0),
        ]
        b = batch(orders)
        rows = aggregate.aggregate(
            RUN_MANIFEST,
            b,
            {
                "a1-naive": {
                    "outcomes": [
                        outcome("o1", final_order_status="paid", amount_paid_paise=145000),
                        outcome("o2", **{"class": "never_retry"}),
                    ],
                    "ledger": [
                        {
                            "sequence": 1,
                            "order_id": "gw_o2",
                            "kind": "action_taken",
                            "policy_verdict": "",
                            "detail": {"arm": "a1-naive", "side_effect": "true"},
                        }
                    ],
                }
            },
        )

        with tempfile.TemporaryDirectory() as tmp:
            csv_path = Path(tmp) / "run.csv"
            md_path = Path(tmp) / "run.md"
            aggregate.write_csv(rows, csv_path)
            aggregate.write_markdown(rows, md_path, RUN_MANIFEST, b)

            with csv_path.open(encoding="utf-8", newline="") as f:
                csv_rows = list(csv.reader(f))
            md_lines = md_path.read_text(encoding="utf-8").splitlines()

        self.assertEqual(aggregate.COLUMNS, csv_rows[0])

        # The markdown table starts at its header row and runs to the end.
        header = "| " + " | ".join(aggregate.COLUMNS) + " |"
        start = md_lines.index(header)
        md_table = [
            [cell.strip() for cell in line.strip().strip("|").split("|")]
            for line in md_lines[start + 2 :]
            if line.strip()
        ]

        self.assertEqual(len(rows), len(md_table))
        self.assertEqual(len(csv_rows) - 1, len(md_table))
        for csv_row, md_row, source in zip(csv_rows[1:], md_table, rows):
            self.assertEqual(csv_row, md_row)
            self.assertEqual([str(source[c]) for c in aggregate.COLUMNS], md_row)

        # A spot check that the numbers under test are the interesting ones and
        # not a table of zeros.
        overall = row_for(rows, "a1-naive", aggregate.SCOPE_OVERALL)
        self.assertEqual(145000, overall["recovered_amount_paise"])
        self.assertEqual(1, overall["fa1_forbidden"])
        self.assertEqual(1, overall["policy_violations_succeeded"])

    def test_recovery_rate_denominator_is_ground_truth_recoverable(self):
        # 4 recoverable orders and 2 bait. The arm pays 2 of the recoverable
        # ones and, wrongly, 1 bait order.
        orders = [
            order("r1", "transient_retry_eligible", True, "retry_same_instrument"),
            order("r2", "transient_retry_eligible", True, "retry_same_instrument"),
            order("r3", "transient_retry_eligible", True, "retry_same_instrument"),
            order("r4", "transient_retry_eligible", True, "retry_same_instrument"),
            order("b1", "never_retry", False, "do_nothing", max_legit=0),
            order("b2", "never_retry", False, "do_nothing", max_legit=0),
        ]
        paid = {"final_order_status": "paid", "amount_paid_paise": 145000}
        outcomes = [
            outcome("r1", **paid),
            outcome("r2", **paid),
            outcome("r3"),
            outcome("r4"),
            outcome("b1", **{"class": "never_retry"}, **paid),
            outcome("b2", **{"class": "never_retry"}),
        ]
        rows = aggregate.aggregate(
            RUN_MANIFEST, batch(orders), {"a1-naive": {"outcomes": outcomes, "ledger": []}}
        )
        overall = row_for(rows, "a1-naive", aggregate.SCOPE_OVERALL)

        self.assertEqual(6, overall["n_scorable"])
        self.assertEqual(4, overall["ground_truth_recoverable"])
        # Every paid order counts here, bait included, so a recovered bait
        # order stays visible instead of being quietly dropped.
        self.assertEqual(3, overall["recovered_orders"])
        # The rate is 2/4, not 3/6 and not 2/6: the denominator is
        # ground_truth_recoverable and the bait payment is not in the numerator.
        self.assertEqual(0.5, overall["recovery_rate"])
        # Money on the bait order is not credited.
        self.assertEqual(290000, overall["recovered_amount_paise"])

        never_retry = row_for(rows, "a1-naive", "never_retry")
        self.assertEqual(2, never_retry["n_scorable"])
        self.assertEqual(0, never_retry["ground_truth_recoverable"])
        # Nothing in this class was recoverable, so there is no rate to state.
        # 0.0 would read as a failure to recover orders that were never
        # recoverable, which is the opposite of what the row shows.
        self.assertEqual(aggregate.UNDEFINED, never_retry["recovery_rate"])
        self.assertEqual(1, never_retry["recovered_orders"])

    def test_modeled_cost_states_its_assumptions(self):
        orders = [
            # Bait, acted on: FA-1.
            order("b1", "never_retry", False, "do_nothing", max_legit=0),
            # Budget spent, acted on: FA-2.
            order("o1", "retry_eligible", True, "retry_same_instrument", max_legit=3),
        ]
        b = batch(orders)
        rows = aggregate.aggregate(
            RUN_MANIFEST,
            b,
            {
                "a1-naive": {
                    "outcomes": [
                        outcome("b1", **{"class": "never_retry"}),
                        outcome("o1", attempts_seen=3, **{"class": "retry_eligible"}),
                    ],
                    "ledger": [],
                }
            },
        )
        overall = row_for(rows, "a1-naive", aggregate.SCOPE_OVERALL)

        self.assertEqual(1, overall["fa1_forbidden"])
        self.assertEqual(1, overall["fa2_over_attempt"])
        self.assertEqual(2, overall["false_action_count"])
        # Phase 5: a failed attempt carries no gateway fee in India, so FA-2
        # contributes nothing to the modelled cost and FA-1 carries all of it.
        self.assertEqual(
            aggregate.MODELED_FORBIDDEN_ACTION_COST_PAISE
            + aggregate.MODELED_FAILED_ATTEMPT_FEE_PAISE,
            overall["modeled_false_action_cost_paise"],
        )
        self.assertEqual(50000, overall["modeled_false_action_cost_paise"])

        # The cost number never travels without the sentence saying it is a
        # model, and the sentence names every constant in it.
        md = aggregate.render_markdown(rows, RUN_MANIFEST, b)
        self.assertIn(aggregate.COST_MODEL_ASSUMPTIONS, md)
        for constant in (
            aggregate.MODELED_FAILED_ATTEMPT_FEE_PAISE,
            aggregate.MODELED_FORBIDDEN_ACTION_COST_PAISE,
            aggregate.MODELED_NOTIFICATION_COST_PAISE,
            aggregate.VISA_EXCESSIVE_REATTEMPT_FEE_PAISE,
        ):
            self.assertIn(str(constant), aggregate.COST_MODEL_ASSUMPTIONS)
        for phrase in ("is a model", "cited inputs", "billed to anyone"):
            self.assertIn(phrase, aggregate.COST_MODEL_ASSUMPTIONS)
        # ADR-0004 and the test-mode caveat ride along with the table.
        self.assertIn("ADR-0004", md)
        self.assertIn("test-mode number is not evidence about real customers", md)

    def test_an_empty_arm_does_not_divide_by_zero(self):
        # a0-control takes no action, and on a batch where the gateway returned
        # nothing at all it produces no outcomes either. Every rate reads
        # UNDEFINED rather than 0.0, and nothing raises.
        #
        # 0.0 was the old answer and it is a lie in the place it matters most:
        # an arm that never escalates has 0 over 0 precision, and 0.000 reads
        # as "every escalation it made was wrong".
        orders = [order("o1", "transient_retry_eligible", True, "retry_same_instrument")]
        b = batch(orders)
        rows = aggregate.aggregate(
            RUN_MANIFEST,
            b,
            {
                "a0-control": {"outcomes": [], "ledger": []},
                # An arm whose only row is unscorable has n_scorable 0, which is
                # the same divide-by-zero by a different route.
                "a1-naive": {"outcomes": [outcome("o1", observed=False)], "ledger": []},
            },
        )

        self.assertEqual(2 * (1 + len(aggregate._class_scopes(b))), len(rows))
        for r in rows:
            self.assertEqual(aggregate.UNDEFINED, r["recovery_rate"], r["arm"] + "/" + r["scope"])
            self.assertEqual(aggregate.UNDEFINED, r["escalation_precision"])
            self.assertEqual(aggregate.UNDEFINED, r["escalation_recall"])
            self.assertEqual(aggregate.UNDEFINED, r["classification_accuracy"])
            self.assertEqual(0, r["modeled_false_action_cost_paise"])
            self.assertEqual(0, r["recovered_amount_paise"])

        # And the specific case the change was made for: an arm with real
        # scorable orders that escalated none of them reports an undefined
        # precision, not a perfect failure.
        naive = row_for(rows, "a1-naive", aggregate.SCOPE_OVERALL)
        self.assertEqual(0, naive["escalations"])
        self.assertEqual(aggregate.UNDEFINED, naive["escalation_precision"])

        control = row_for(rows, "a0-control", aggregate.SCOPE_OVERALL)
        self.assertEqual(0, control["n_orders"])
        self.assertEqual(0, control["n_scorable"])
        self.assertEqual(0, control["n_unscorable"])
        self.assertEqual(0, control["api_calls"])

        naive = row_for(rows, "a1-naive", aggregate.SCOPE_OVERALL)
        self.assertEqual(1, naive["n_orders"])
        self.assertEqual(0, naive["n_scorable"])
        self.assertEqual(1, naive["n_unscorable"])

        # The writers survive an all-zero table too.
        with tempfile.TemporaryDirectory() as tmp:
            aggregate.write_csv(rows, Path(tmp) / "empty.csv")
            aggregate.write_markdown(rows, Path(tmp) / "empty.md", RUN_MANIFEST, b)
            self.assertTrue((Path(tmp) / "empty.csv").exists())
            self.assertTrue((Path(tmp) / "empty.md").exists())

    def test_scorer_verdicts_are_the_only_scorability_signal(self):
        # Guard against a future refactor that starts reading a row's own
        # "recovered" field instead of the scorer's verdict.
        card = scorer.score_outcome(
            outcome("o1", observed=False, recovered=True, claimed_recovered=True),
            order("o1", "transient_retry_eligible", True, "retry_same_instrument"),
        )
        self.assertEqual(scorer.VERDICT_UNSCORABLE, card["verdict"])
        self.assertFalse(card["recovered"])


class TestAgentCostColumns(unittest.TestCase):
    """The cost of the agent arm, which the three deterministic arms do not
    have. Phase 3."""

    def _run(self, invocations):
        orders = [
            order("o1", "transient_retry_eligible", True, "retry_same_instrument"),
            order("o2", "transient_retry_eligible", True, "retry_same_instrument"),
        ]
        manifest = dict(RUN_MANIFEST)
        manifest["arms"] = ["a2-agent", "a3-rules"]
        per_arm = {
            "a2-agent": {
                "outcomes": [
                    outcome("o1", arm="a2-agent"),
                    outcome("o2", arm="a2-agent"),
                ],
                "ledger": [],
                "invocations": invocations,
            },
            "a3-rules": {
                "outcomes": [outcome("o1"), outcome("o2")],
                "ledger": [],
            },
        }
        return aggregate.aggregate(manifest, batch(orders), per_arm)

    def test_agent_cost_columns_sum_the_invocation_rows(self):
        rows = self._run(
            [
                {
                    "order_id": "o1",
                    "unscorable": False,
                    "input_tokens": 100,
                    "output_tokens": 20,
                    "cost_usd": 0.01,
                    "duration_ms": 1000,
                },
                {
                    "order_id": "o2",
                    "unscorable": True,
                    "input_tokens": 50,
                    "output_tokens": 5,
                    "cost_usd": 0.02,
                    "duration_ms": 2000,
                },
            ]
        )
        agent = row_for(rows, "a2-agent", aggregate.SCOPE_OVERALL)

        self.assertEqual(agent["agent_input_tokens"], 150)
        self.assertEqual(agent["agent_output_tokens"], 25)
        self.assertAlmostEqual(agent["agent_cost_usd"], 0.03)
        self.assertEqual(agent["agent_wall_clock_ms"], 3000)
        self.assertEqual(
            agent["agent_invocations"],
            2,
            "an unscorable invocation still spent money and still counts in the cost",
        )

    def test_agent_cost_columns_are_not_a_number_for_the_deterministic_arms(self):
        rows = self._run([])
        rules = row_for(rows, "a3-rules", aggregate.SCOPE_OVERALL)
        for column in (
            "agent_invocations",
            "agent_input_tokens",
            "agent_output_tokens",
            "agent_cost_usd",
            "agent_wall_clock_ms",
        ):
            self.assertEqual(
                rules[column],
                aggregate.UNDEFINED,
                "an arm that made no model invocation has no "
                + column
                + ", and a zero there is a claim about something that did not happen",
            )


if __name__ == "__main__":
    unittest.main()
