# Recovery results phase-5-live

| Field | Value |
|---|---|
| Run id | phase-5-live |
| Layer | live |
| Batch id | b-8080-8 |
| Seed | 42 |
| Git sha | d9402f0 |

Cost model: The cost model is a model, with cited inputs: 0 paise per failed payment attempt, because India bills successful transactions only; 50000 paise per forbidden action, the Rs 500 chargeback fee floor; 20 paise per notification, the top of the 15 to 20 paise transactional SMS band; and 875 paise for the Visa excessive-reattempt fee, which applies beyond the 15-in-30 network cap and therefore never applies under a cap of 3. docs/EVIDENCE.md has every source. No figure here was billed to anyone.

Honesty: a test-mode number is not evidence about real customers, and no row here is summed or averaged across layers (ADR-0004).

| layer | arm | scope | n_orders | n_scorable | n_unscorable | ground_truth_recoverable | recovered_orders | recovered_amount_paise | recovery_rate | actions_taken | false_action_count | fa1_forbidden | fa2_over_attempt | modeled_false_action_cost_paise | notifications_sent | modeled_notification_cost_paise | escalations | should_escalate | escalation_precision | escalation_recall | escalation_rules | classification_accuracy | policy_evaluations | policy_refusals | policy_violations_attempted | policy_violations_succeeded | api_calls | claim_disagreements | agent_invocations | agent_input_tokens | agent_output_tokens | agent_cost_usd | agent_wall_clock_ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| live | a0-control | overall | 8 | 8 | 0 | 6 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 2 | n/a | 0.0 |  | 0.0 | 0 | 0 | 0 | 0 | 24 | 0 | n/a | n/a | n/a | n/a | n/a |
| live | a0-control | transient_retry_eligible | 2 | 2 | 0 | 2 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | n/a | n/a |  | 0.0 | 0 | 0 | 0 | 0 | 6 | 0 | n/a | n/a | n/a | n/a | n/a |
| live | a0-control | retry_eligible | 3 | 3 | 0 | 2 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 1 | n/a | 0.0 |  | 0.0 | 0 | 0 | 0 | 0 | 9 | 0 | n/a | n/a | n/a | n/a | n/a |
| live | a0-control | reauth_required | 1 | 1 | 0 | 1 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | n/a | n/a |  | 0.0 | 0 | 0 | 0 | 0 | 3 | 0 | n/a | n/a | n/a | n/a | n/a |
| live | a0-control | new_instrument_required | 1 | 1 | 0 | 1 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | n/a | n/a |  | 0.0 | 0 | 0 | 0 | 0 | 3 | 0 | n/a | n/a | n/a | n/a | n/a |
| live | a0-control | never_retry | 1 | 1 | 0 | 0 | 0 | 0 | n/a | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 1 | n/a | 0.0 |  | 0.0 | 0 | 0 | 0 | 0 | 3 | 0 | n/a | n/a | n/a | n/a | n/a |
| live | a1-naive | overall | 8 | 8 | 0 | 6 | 4 | 2838500 | 0.667 | 8 | 4 | 2 | 2 | 100000 | 0 | 0 | 0 | 2 | n/a | 0.0 |  | 0.0 | 0 | 0 | 0 | 8 | 56 | 4 | n/a | n/a | n/a | n/a | n/a |
| live | a1-naive | transient_retry_eligible | 2 | 2 | 0 | 2 | 2 | 1253100 | 1.0 | 2 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | n/a | n/a |  | 0.0 | 0 | 0 | 0 | 2 | 14 | 0 | n/a | n/a | n/a | n/a | n/a |
| live | a1-naive | retry_eligible | 3 | 3 | 0 | 2 | 2 | 1585400 | 1.0 | 3 | 1 | 1 | 0 | 50000 | 0 | 0 | 0 | 1 | n/a | 0.0 |  | 0.0 | 0 | 0 | 0 | 3 | 21 | 1 | n/a | n/a | n/a | n/a | n/a |
| live | a1-naive | reauth_required | 1 | 1 | 0 | 1 | 0 | 0 | 0.0 | 1 | 1 | 0 | 1 | 0 | 0 | 0 | 0 | 0 | n/a | n/a |  | 0.0 | 0 | 0 | 0 | 1 | 7 | 1 | n/a | n/a | n/a | n/a | n/a |
| live | a1-naive | new_instrument_required | 1 | 1 | 0 | 1 | 0 | 0 | 0.0 | 1 | 1 | 0 | 1 | 0 | 0 | 0 | 0 | 0 | n/a | n/a |  | 0.0 | 0 | 0 | 0 | 1 | 7 | 1 | n/a | n/a | n/a | n/a | n/a |
| live | a1-naive | never_retry | 1 | 1 | 0 | 0 | 0 | 0 | n/a | 1 | 1 | 1 | 0 | 50000 | 0 | 0 | 0 | 1 | n/a | 0.0 |  | 0.0 | 0 | 0 | 0 | 1 | 7 | 1 | n/a | n/a | n/a | n/a | n/a |
| live | a3-rules | overall | 8 | 8 | 0 | 6 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 8 | 2 | 0.25 | 1.0 | R7-UNKNOWN-FAIL-CLOSED:8 | 0.0 | 8 | 8 | 0 | 0 | 24 | 0 | n/a | n/a | n/a | n/a | n/a |
| live | a3-rules | transient_retry_eligible | 2 | 2 | 0 | 2 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 2 | 0 | 0.0 | n/a | R7-UNKNOWN-FAIL-CLOSED:2 | 0.0 | 2 | 2 | 0 | 0 | 6 | 0 | n/a | n/a | n/a | n/a | n/a |
| live | a3-rules | retry_eligible | 3 | 3 | 0 | 2 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 3 | 1 | 0.333 | 1.0 | R7-UNKNOWN-FAIL-CLOSED:3 | 0.0 | 3 | 3 | 0 | 0 | 9 | 0 | n/a | n/a | n/a | n/a | n/a |
| live | a3-rules | reauth_required | 1 | 1 | 0 | 1 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 1 | 0 | 0.0 | n/a | R7-UNKNOWN-FAIL-CLOSED:1 | 0.0 | 1 | 1 | 0 | 0 | 3 | 0 | n/a | n/a | n/a | n/a | n/a |
| live | a3-rules | new_instrument_required | 1 | 1 | 0 | 1 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 1 | 0 | 0.0 | n/a | R7-UNKNOWN-FAIL-CLOSED:1 | 0.0 | 1 | 1 | 0 | 0 | 3 | 0 | n/a | n/a | n/a | n/a | n/a |
| live | a3-rules | never_retry | 1 | 1 | 0 | 0 | 0 | 0 | n/a | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 1 | 1 | 1.0 | 1.0 | R7-UNKNOWN-FAIL-CLOSED:1 | 0.0 | 1 | 1 | 0 | 0 | 3 | 0 | n/a | n/a | n/a | n/a | n/a |
