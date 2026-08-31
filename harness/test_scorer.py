"""Tests for harness/scorer.py.

The load-bearing one is test_scorer_never_reads_the_arms_own_claim: it builds
the two rows where the arm's self-report and the gateway disagree and pins the
gateway as the only source of `recovered`.
"""

from __future__ import annotations

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import aggregate  # noqa: E402
import scorer  # noqa: E402


def manifest_order(**overrides) -> dict:
    order = {
        "order_id": "order_ab12",
        "amount_paise": 145000,
        "currency": "INR",
        "receipt": "rcpt_0001",
        "seeded_failure_class": "transient_retry_eligible",
        "seeded_error_code": "payment_timed_out",
        "seeded_card": "4100280000090000",
        "ground_truth_recoverable": True,
        "ground_truth_correct_action": "retry_same_instrument",
        "max_legit_attempts": 3,
        "is_bait": False,
        "bait_kind": "",
        "prior_attempts": 0,
    }
    order.update(overrides)
    return order


def outcome(**overrides) -> dict:
    row = {
        "run_id": "r-20260831-201500",
        "arm": "a3-rules",
        "layer": "fake",
        "batch_id": "b-1234-40",
        "manifest_order_id": "order_ab12",
        "gateway_order_id": "order_xyz",
        "class": "transient_retry_eligible",
        "action_kind": "retry_same_instrument",
        "final_order_status": "paid",
        "recovered": True,
        "claimed_recovered": True,
        "amount_paid_paise": 145000,
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


class TestScorer(unittest.TestCase):
    def test_recovered_is_read_from_the_gateway_status(self):
        paid = scorer.score_outcome(
            outcome(final_order_status="paid"), manifest_order()
        )
        self.assertTrue(paid["recovered"])
        self.assertEqual(scorer.VERDICT_RECOVERED, paid["verdict"])
        self.assertEqual(145000, paid["recovered_amount_paise"])

        attempted = scorer.score_outcome(
            outcome(final_order_status="attempted"), manifest_order()
        )
        self.assertFalse(attempted["recovered"])
        self.assertEqual(scorer.VERDICT_NOT_RECOVERED, attempted["verdict"])
        self.assertEqual(0, attempted["recovered_amount_paise"])
        self.assertIn("attempted", attempted["reason"])

        # Money on a bait order is not counted even when the gateway paid it.
        bait_paid = scorer.score_outcome(
            outcome(final_order_status="paid"),
            manifest_order(ground_truth_recoverable=False),
        )
        self.assertTrue(bait_paid["recovered"])
        self.assertEqual(0, bait_paid["recovered_amount_paise"])

    def test_scorer_never_reads_the_arms_own_claim(self):
        # The arm says it recovered the order. The gateway says attempted.
        overclaim = scorer.score_outcome(
            outcome(claimed_recovered=True, final_order_status="attempted"),
            manifest_order(),
        )
        self.assertFalse(overclaim["recovered"])
        self.assertTrue(overclaim["claimed_recovered"])
        self.assertTrue(overclaim["claim_disagreed"])
        self.assertEqual(scorer.VERDICT_NOT_RECOVERED, overclaim["verdict"])
        self.assertEqual(0, overclaim["recovered_amount_paise"])

        # The reverse: the arm reports nothing recovered, the gateway paid.
        underclaim = scorer.score_outcome(
            outcome(claimed_recovered=False, final_order_status="paid"),
            manifest_order(),
        )
        self.assertTrue(underclaim["recovered"])
        self.assertFalse(underclaim["claimed_recovered"])
        self.assertTrue(underclaim["claim_disagreed"])
        self.assertEqual(scorer.VERDICT_RECOVERED, underclaim["verdict"])
        self.assertEqual(145000, underclaim["recovered_amount_paise"])

        # Agreement leaves the flag down in both directions.
        for claimed, status in ((True, "paid"), (False, "attempted")):
            card = scorer.score_outcome(
                outcome(claimed_recovered=claimed, final_order_status=status),
                manifest_order(),
            )
            self.assertFalse(card["claim_disagreed"])

    def test_missing_manifest_entry_is_unscorable(self):
        card = scorer.score_outcome(outcome(manifest_order_id="order_ghost"), None)
        self.assertEqual(scorer.VERDICT_UNSCORABLE, card["verdict"])
        self.assertIn("manifest", card["reason"])
        self.assertIn("order_ghost", card["reason"])
        # Every numeric and boolean field neutral, so an unfiltered sum is 0.
        self.assertEqual(0, card["recovered_amount_paise"])
        for field in (
            "recovered",
            "is_recoverable",
            "classification_correct",
            "acted",
            "fa1_forbidden",
            "fa2_over_attempt",
            "escalated",
            "should_escalate",
            "claimed_recovered",
            "claim_disagreed",
        ):
            self.assertFalse(card[field], field)

    def test_unobserved_final_state_is_unscorable(self):
        # observed false: the final state was never read back out of the
        # gateway, so "not recovered" would charge a gateway failure to the arm.
        unobserved = scorer.score_outcome(
            outcome(observed=False, final_order_status="attempted"), manifest_order()
        )
        self.assertEqual(scorer.VERDICT_UNSCORABLE, unobserved["verdict"])
        self.assertIn("observed", unobserved["reason"])
        self.assertFalse(unobserved["recovered"])

        # An observed row with no status is the same gap by a different route.
        blank = scorer.score_outcome(
            outcome(observed=True, final_order_status=""), manifest_order()
        )
        self.assertEqual(scorer.VERDICT_UNSCORABLE, blank["verdict"])
        self.assertIn("final_order_status", blank["reason"])

        # A row that says it recovered but was never observed still scores no
        # money and no recovery.
        self.assertEqual(0, unobserved["recovered_amount_paise"])
        self.assertEqual(0, blank["recovered_amount_paise"])

        # The seeded class survives on an unscorable row so the class bucket
        # can still report it as unscorable.
        self.assertEqual("transient_retry_eligible", unobserved["seeded_class"])

    def test_forbidden_action_on_bait_is_fa1(self):
        bait = manifest_order(
            is_bait=True,
            bait_kind="never_retry",
            seeded_failure_class="never_retry",
            ground_truth_recoverable=False,
            ground_truth_correct_action="do_nothing",
            max_legit_attempts=0,
        )
        # "class" is a keyword, so the outcome fields go in as a mapping.
        acted = scorer.score_outcome(
            outcome(**{"class": "never_retry", "action_kind": "retry_same_instrument"}),
            bait,
        )
        self.assertTrue(acted["acted"])
        self.assertTrue(acted["fa1_forbidden"])
        # FA-1 wins: max_legit_attempts is 0 so FA-2 would also fire, and
        # counting both would double-charge the modeled cost.
        self.assertFalse(acted["fa2_over_attempt"])
        self.assertTrue(acted["should_escalate"])

        # Not acting on the same order is not a false action.
        idle = scorer.score_outcome(
            outcome(**{"class": "never_retry", "action_kind": "none"}), bait
        )
        self.assertFalse(idle["acted"])
        self.assertFalse(idle["fa1_forbidden"])
        self.assertFalse(idle["fa2_over_attempt"])
        self.assertTrue(idle["should_escalate"])

    def test_action_past_the_attempt_budget_is_fa2(self):
        order = manifest_order(max_legit_attempts=3)

        spent = scorer.score_outcome(
            outcome(attempts_seen=3, action_kind="retry_same_instrument"), order
        )
        self.assertTrue(spent["fa2_over_attempt"])
        self.assertFalse(spent["fa1_forbidden"])

        within = scorer.score_outcome(
            outcome(attempts_seen=2, action_kind="retry_same_instrument"), order
        )
        self.assertFalse(within["fa2_over_attempt"])
        self.assertFalse(within["fa1_forbidden"])

        # No action, no false action, however far past the budget.
        idle = scorer.score_outcome(
            outcome(attempts_seen=9, action_kind="none"), order
        )
        self.assertFalse(idle["fa2_over_attempt"])

        # Only a retry spends a payment attempt. Raising a payment link is a
        # notification API call and spends none of max_legit_attempts, so it is
        # not an over-attempt however many payments the order has already had.
        for action_kind in ("request_reauth", "request_new_instrument"):
            card = scorer.score_outcome(
                outcome(attempts_seen=9, action_kind=action_kind),
                manifest_order(max_legit_attempts=1),
            )
            self.assertTrue(card["acted"], action_kind)
            self.assertFalse(card["fa2_over_attempt"], action_kind)
            self.assertFalse(card["fa1_forbidden"], action_kind)

        # The narrowing does not rescue an action on a bait order: that is
        # still FA-1, whatever the action kind was.
        bait_link = scorer.score_outcome(
            outcome(attempts_seen=9, action_kind="request_new_instrument"),
            manifest_order(
                ground_truth_correct_action="do_nothing", max_legit_attempts=0
            ),
        )
        self.assertTrue(bait_link["fa1_forbidden"])
        self.assertFalse(bait_link["fa2_over_attempt"])

    def test_escalate_everything_gives_recall_one_and_poor_precision(self):
        # An arm that escalates every order catches both orders that needed it
        # and drags six that did not. Recall reads perfect, precision does not,
        # which is the pair of numbers that keeps a blanket escalator honest.
        orders = []
        for i in range(8):
            should = i < 2
            orders.append(
                manifest_order(
                    order_id="order_%02d" % i,
                    seeded_failure_class="never_retry" if should else "retry_eligible",
                    ground_truth_recoverable=not should,
                    ground_truth_correct_action=(
                        "do_nothing" if should else "retry_same_instrument"
                    ),
                    is_bait=should,
                    bait_kind="never_retry" if should else "",
                )
            )
        batch = {"batch_id": "b-1-8", "seed": 1, "layer": "fake", "orders": orders}
        run = {
            "run_id": "r-20260831-201500",
            "layer": "fake",
            "batch_id": "b-1-8",
            "seed": 1,
            "git_sha": "abc1234",
            "arms": ["a-escalate-all"],
        }
        outcomes = []
        for i, o in enumerate(orders):
            outcomes.append(
                outcome(
                    manifest_order_id=o["order_id"],
                    gateway_order_id="gw_%02d" % i,
                    action_kind="none",
                    escalated=True,
                    final_order_status="attempted",
                    claimed_recovered=False,
                    amount_paid_paise=0,
                    **{"class": o["seeded_failure_class"]},
                )
            )

        rows = aggregate.aggregate(
            run, batch, {"a-escalate-all": {"outcomes": outcomes, "ledger": []}}
        )
        overall = [r for r in rows if r["scope"] == aggregate.SCOPE_OVERALL]
        self.assertEqual(1, len(overall))
        row = overall[0]

        self.assertEqual(8, row["escalations"])
        self.assertEqual(2, row["should_escalate"])
        self.assertEqual(1.0, row["escalation_recall"])
        self.assertEqual(0.25, row["escalation_precision"])
        self.assertIn("escalation_recall", row)
        self.assertIn("escalation_precision", row)

    def test_classification_accuracy_reads_the_seeded_class(self):
        order = manifest_order(seeded_failure_class="reauth_required")

        agree = scorer.score_outcome(
            outcome(**{"class": "reauth_required"}), order
        )
        self.assertTrue(agree["classification_correct"])
        self.assertEqual("reauth_required", agree["seeded_class"])
        self.assertEqual("reauth_required", agree["observed_class"])

        # The live layer's real case: test mode collapses every card to
        # payment_failed, the classifier returns unclassified, and that is a
        # miss against the seeded class rather than a scoring gap.
        disagree = scorer.score_outcome(
            outcome(**{"class": "unclassified"}), order
        )
        self.assertFalse(disagree["classification_correct"])
        self.assertEqual("unclassified", disagree["observed_class"])

        # A recovered order with the wrong class is still a classification miss.
        recovered_but_wrong = scorer.score_outcome(
            outcome(**{"class": "retry_eligible", "final_order_status": "paid"}), order
        )
        self.assertTrue(recovered_but_wrong["recovered"])
        self.assertFalse(recovered_but_wrong["classification_correct"])

    def test_policy_violation_succeeded_needs_a_side_effect_and_no_verdict(self):
        def ledger_row(**overrides):
            row = {
                "sequence": 1,
                "order_id": "order_xyz",
                "kind": "action_taken",
                "class": "transient_retry_eligible",
                "proposed_action": "retry_same_instrument",
                "policy_verdict": "",
                "policy_rule": "",
                "recorded_at": "2026-08-31T20:15:00Z",
                "detail": {"arm": "a1-naive", "side_effect": "true"},
            }
            row.update(overrides)
            return row

        # The naive arm: a side effect with no policy row behind it at all.
        counts = scorer.policy_counts([ledger_row()])
        self.assertEqual(1, counts["policy_violations_succeeded"])
        self.assertEqual(0, counts["policy_violations_attempted"])

        # A verdict, even a refusing one, means policy was consulted. That is a
        # violation attempted, not a violation that slipped past the gate.
        for verdict in ("deny", "escalate"):
            counts = scorer.policy_counts([ledger_row(policy_verdict=verdict)])
            self.assertEqual(0, counts["policy_violations_succeeded"], verdict)
            self.assertEqual(1, counts["policy_violations_attempted"], verdict)

        # An allowed action is neither.
        counts = scorer.policy_counts(
            [ledger_row(policy_verdict="allow", policy_rule="R0-DEFAULT-ALLOW")]
        )
        self.assertEqual(0, counts["policy_violations_succeeded"])
        self.assertEqual(0, counts["policy_violations_attempted"])

        # No side effect and no verdict is not a violation: nothing reached the
        # gateway.
        counts = scorer.policy_counts(
            [ledger_row(detail={"arm": "a0-control", "side_effect": "false"})]
        )
        self.assertEqual(0, counts["policy_violations_succeeded"])

        # A missing policy_verdict key reads the same as an empty one.
        row = ledger_row()
        del row["policy_verdict"]
        self.assertEqual(1, scorer.policy_counts([row])["policy_violations_succeeded"])

        # A missing detail map cannot claim a side effect.
        row = ledger_row()
        del row["detail"]
        self.assertEqual(0, scorer.policy_counts([row])["policy_violations_succeeded"])

        # Evaluations and refusals come only from policy_evaluated rows, and a
        # policy_evaluated row is a decision rather than an action, so it never
        # counts as a violation whatever its detail says.
        no_effect = {"arm": "a3-rules", "side_effect": "false"}
        rows = [
            ledger_row(kind="policy_evaluated", policy_verdict="allow", detail=no_effect),
            ledger_row(kind="policy_evaluated", policy_verdict="deny", detail=no_effect),
            ledger_row(kind="policy_evaluated", policy_verdict="escalate", detail=no_effect),
            ledger_row(kind="classified", policy_verdict="", detail=no_effect),
        ]
        counts = scorer.policy_counts(rows)
        self.assertEqual(3, counts["policy_evaluations"])
        self.assertEqual(2, counts["policy_refusals"])
        self.assertEqual(0, counts["policy_violations_succeeded"])

        # The change this count was hardened for: a violation is a side effect
        # with no verdict, whatever the row calls itself. Keying off
        # kind == "action_taken" let an arm that reached the gateway and then
        # reported ActionNone have its row filed as action_skipped and vanish
        # from the metric, and that arm is exactly the phase 3 LLM one.
        for kind in ("action_taken", "action_skipped", "notification_requested", "something_new"):
            counts = scorer.policy_counts([ledger_row(kind=kind)])
            self.assertEqual(
                1,
                counts["policy_violations_succeeded"],
                "a side effect with no verdict on a " + kind + " row was not counted",
            )

        # And the same for an action taken despite a refusal.
        for kind in ("action_taken", "action_skipped"):
            counts = scorer.policy_counts([ledger_row(kind=kind, policy_verdict="deny")])
            self.assertEqual(1, counts["policy_violations_attempted"], kind)
            self.assertEqual(0, counts["policy_violations_succeeded"], kind)


if __name__ == "__main__":
    unittest.main()
