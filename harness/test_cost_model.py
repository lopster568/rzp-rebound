"""The cost model's inputs, each one against the source it now names.

Phase 5 rebuilt this model. The old one charged 200 paise for every payment
attempt and 5000 paise for every forbidden action, both invented, and both wrong
in a way that matters rather than merely unsourced:

  - A failed transaction carries no gateway fee in India. Razorpay and PayU both
    bill successful transactions only, so the per-attempt fee is zero and the
    old model was charging for something free.
  - The floor a merchant pays on a chargeback is Rs 500. The old forbidden
    action cost was a tenth of that.
  - A notification is not free, and the old model did not have one at all.

Every number below is in scripts/claims-allow.txt with its source on the line.
"""

from __future__ import annotations

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

import aggregate  # noqa: E402
import scorer  # noqa: E402


def _scored(**overrides) -> dict:
    row = {
        "verdict": scorer.VERDICT_NOT_RECOVERED,
        "recovered": False,
        "recovered_amount_paise": 0,
        "is_recoverable": True,
        "seeded_class": "retry_eligible",
        "observed_class": "retry_eligible",
        "classification_correct": True,
        "action_kind": "none",
        "acted": False,
        "notified": False,
        "fa1_forbidden": False,
        "fa2_over_attempt": False,
        "escalated": False,
        "should_escalate": False,
        "policy_rule": "",
        "claimed_recovered": False,
        "claim_disagreed": False,
    }
    row.update(overrides)
    return row


class CostModelInputs(unittest.TestCase):
    def test_failed_attempt_costs_no_gateway_fee(self):
        """India bills successful transactions only, so a failed retry is free."""
        self.assertEqual(aggregate.MODELED_FAILED_ATTEMPT_FEE_PAISE, 0)

    def test_forbidden_action_cost_is_the_chargeback_floor(self):
        """Rs 500, which is 50000 paise, and ten times the old invented figure."""
        self.assertEqual(aggregate.MODELED_FORBIDDEN_ACTION_COST_PAISE, 50000)

    def test_notification_cost_is_the_top_of_the_transactional_sms_band(self):
        """Indian transactional SMS runs 15 to 20 paise. The model takes 20."""
        self.assertEqual(aggregate.MODELED_NOTIFICATION_COST_PAISE, 20)

    def test_visa_reattempt_fee_is_carried_and_not_applied(self):
        """The one citable per-reattempt charge, and it does not apply here.

        The Visa reattempt-abuse fee is about ten US cents, roughly 875 paise,
        and it applies beyond the 15-in-30 network cap. A policy capped at 3
        never reaches it. Carrying the number with its source and multiplying it
        by zero is more honest than omitting it, because a reader who has heard
        of the fee needs to find out here why it is not in the total.
        """
        self.assertEqual(aggregate.VISA_EXCESSIVE_REATTEMPT_FEE_PAISE, 875)
        self.assertEqual(aggregate.VISA_EXCESSIVE_REATTEMPT_FEE_APPLIED_PAISE, 0)

    def test_cost_model_assumptions_names_every_input(self):
        """A table cannot ship with an assumption line that fell behind the model."""
        text = aggregate.COST_MODEL_ASSUMPTIONS
        for number in ("0", "20", "50000", "875"):
            self.assertIn(number, text, "the assumption sentence does not name " + number)
        for word in ("model", "cited"):
            self.assertIn(word, text.lower())


class CostColumns(unittest.TestCase):
    def _row(self, scored):
        return aggregate._build_row(
            layer="fake",
            arm="a3-rules",
            scope=aggregate.SCOPE_OVERALL,
            pairs=[({}, c) for c in scored],
            ledger_rows=[],
        )

    def test_a_false_retry_no_longer_costs_a_gateway_fee(self):
        row = self._row([_scored(acted=True, action_kind="retry_same_instrument",
                                 fa2_over_attempt=True)])
        self.assertEqual(row["fa2_over_attempt"], 1)
        self.assertEqual(row["modeled_false_action_cost_paise"], 0)

    def test_a_forbidden_action_costs_the_chargeback_floor(self):
        row = self._row([_scored(acted=True, action_kind="retry_same_instrument",
                                 fa1_forbidden=True, should_escalate=True)])
        self.assertEqual(row["modeled_false_action_cost_paise"], 50000)

    def test_notification_cost_is_counted_separately_from_false_actions(self):
        """A correct notification is a cost and is not a false action.

        Folding it into the false-action column would charge an arm for doing
        the right thing, and the whole point of that column is that it counts
        mistakes.
        """
        row = self._row([_scored(acted=True, action_kind="request_reauth", notified=True)])
        self.assertEqual(row["false_action_count"], 0)
        self.assertEqual(row["modeled_false_action_cost_paise"], 0)
        self.assertEqual(row["notifications_sent"], 1)
        self.assertEqual(row["modeled_notification_cost_paise"], 20)

    def test_notifications_are_counted_on_both_notify_actions(self):
        row = self._row([
            _scored(acted=True, action_kind="request_reauth", notified=True),
            _scored(acted=True, action_kind="request_new_instrument", notified=True),
            _scored(acted=True, action_kind="retry_same_instrument"),
        ])
        self.assertEqual(row["notifications_sent"], 2)
        self.assertEqual(row["modeled_notification_cost_paise"], 40)

    def test_scorer_marks_the_two_notify_actions_and_nothing_else(self):
        manifest = {
            "seeded_failure_class": "reauth_required",
            "ground_truth_correct_action": "request_reauth",
            "max_legit_attempts": 1,
        }
        for action, want in (
            ("request_reauth", True),
            ("request_new_instrument", True),
            ("retry_same_instrument", False),
            ("none", False),
            ("", False),
        ):
            outcome = {
                "manifest_order_id": "order_x",
                "observed": True,
                "final_order_status": "attempted",
                "attempts_seen": 0,
                "action_kind": action,
            }
            got = scorer.score_outcome(outcome, manifest)
            self.assertEqual(got["notified"], want, action)

    def test_an_unscorable_row_notifies_nothing(self):
        got = scorer.score_outcome({"manifest_order_id": "order_x", "observed": False}, {})
        self.assertFalse(got["notified"])


if __name__ == "__main__":
    unittest.main()
