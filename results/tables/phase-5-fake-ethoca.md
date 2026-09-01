# Recovery results phase-5-fake-ethoca

| Field | Value |
|---|---|
| Run id | phase-5-fake-ethoca |
| Layer | fake |
| Batch id | b-5150-40-ethoca-card-mix-2019 |
| Seed | 42 |
| Git sha | 67cc5b4 |

Cost model: The cost model is a model, with cited inputs: 0 paise per failed payment attempt, because India bills successful transactions only; 50000 paise per forbidden action, the Rs 500 chargeback fee floor; 20 paise per notification, the top of the 15 to 20 paise transactional SMS band; and 875 paise for the Visa excessive-reattempt fee, which applies beyond the 15-in-30 network cap and therefore never applies under a cap of 3. docs/EVIDENCE.md has every source. No figure here was billed to anyone.

Honesty: a test-mode number is not evidence about real customers, and no row here is summed or averaged across layers (ADR-0004).

| layer | arm | scope | n_orders | n_scorable | n_unscorable | ground_truth_recoverable | recovered_orders | recovered_amount_paise | recovery_rate | actions_taken | false_action_count | fa1_forbidden | fa2_over_attempt | modeled_false_action_cost_paise | notifications_sent | modeled_notification_cost_paise | escalations | should_escalate | escalation_precision | escalation_recall | escalation_rules | classification_accuracy | policy_evaluations | policy_refusals | policy_violations_attempted | policy_violations_succeeded | api_calls | claim_disagreements | agent_invocations | agent_input_tokens | agent_output_tokens | agent_cost_usd | agent_wall_clock_ms |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| fake | a0-control | overall | 40 | 40 | 0 | 26 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 14 | n/a | 0.0 |  | 1.0 | 0 | 0 | 0 | 0 | 360 | 0 | n/a | n/a | n/a | n/a | n/a |
| fake | a0-control | transient_retry_eligible | 3 | 3 | 0 | 3 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | n/a | n/a |  | 1.0 | 0 | 0 | 0 | 0 | 27 | 0 | n/a | n/a | n/a | n/a | n/a |
| fake | a0-control | retry_eligible | 17 | 17 | 0 | 17 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | n/a | n/a |  | 1.0 | 0 | 0 | 0 | 0 | 153 | 0 | n/a | n/a | n/a | n/a | n/a |
| fake | a0-control | reauth_required | 3 | 3 | 0 | 3 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | n/a | n/a |  | 1.0 | 0 | 0 | 0 | 0 | 27 | 0 | n/a | n/a | n/a | n/a | n/a |
| fake | a0-control | new_instrument_required | 3 | 3 | 0 | 3 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | n/a | n/a |  | 1.0 | 0 | 0 | 0 | 0 | 27 | 0 | n/a | n/a | n/a | n/a | n/a |
| fake | a0-control | never_retry | 14 | 14 | 0 | 0 | 0 | 0 | n/a | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 14 | n/a | 0.0 |  | 1.0 | 0 | 0 | 0 | 0 | 126 | 0 | n/a | n/a | n/a | n/a | n/a |
| fake | a1-naive | overall | 40 | 40 | 0 | 26 | 20 | 17391300 | 0.769 | 40 | 20 | 14 | 6 | 700000 | 0 | 0 | 0 | 14 | n/a | 0.0 |  | 1.0 | 0 | 0 | 0 | 40 | 400 | 20 | n/a | n/a | n/a | n/a | n/a |
| fake | a1-naive | transient_retry_eligible | 3 | 3 | 0 | 3 | 3 | 953400 | 1.0 | 3 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | n/a | n/a |  | 1.0 | 0 | 0 | 0 | 3 | 30 | 0 | n/a | n/a | n/a | n/a | n/a |
| fake | a1-naive | retry_eligible | 17 | 17 | 0 | 17 | 17 | 16437900 | 1.0 | 17 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | n/a | n/a |  | 1.0 | 0 | 0 | 0 | 17 | 170 | 0 | n/a | n/a | n/a | n/a | n/a |
| fake | a1-naive | reauth_required | 3 | 3 | 0 | 3 | 0 | 0 | 0.0 | 3 | 3 | 0 | 3 | 0 | 0 | 0 | 0 | 0 | n/a | n/a |  | 1.0 | 0 | 0 | 0 | 3 | 30 | 3 | n/a | n/a | n/a | n/a | n/a |
| fake | a1-naive | new_instrument_required | 3 | 3 | 0 | 3 | 0 | 0 | 0.0 | 3 | 3 | 0 | 3 | 0 | 0 | 0 | 0 | 0 | n/a | n/a |  | 1.0 | 0 | 0 | 0 | 3 | 30 | 3 | n/a | n/a | n/a | n/a | n/a |
| fake | a1-naive | never_retry | 14 | 14 | 0 | 0 | 0 | 0 | n/a | 14 | 14 | 14 | 0 | 700000 | 0 | 0 | 0 | 14 | n/a | 0.0 |  | 1.0 | 0 | 0 | 0 | 14 | 140 | 14 | n/a | n/a | n/a | n/a | n/a |
| fake | a2-agent | overall | 40 | 40 | 0 | 26 | 16 | 10747700 | 0.615 | 22 | 0 | 0 | 0 | 0 | 6 | 120 | 18 | 14 | 0.778 | 1.0 | R3-AMOUNT-CEILING:4|R4-NEVER-RETRY-CLASS:14 | 1.0 | 50 | 22 | 0 | 0 | 344 | 0 | 40 | 518 | 52204 | 3.367192 | 892472 |
| fake | a2-agent | transient_retry_eligible | 3 | 3 | 0 | 3 | 3 | 953400 | 1.0 | 3 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | n/a | n/a |  | 1.0 | 3 | 0 | 0 | 0 | 27 | 0 | n/a | n/a | n/a | n/a | n/a |
| fake | a2-agent | retry_eligible | 17 | 17 | 0 | 17 | 13 | 9794300 | 0.765 | 13 | 0 | 0 | 0 | 0 | 0 | 0 | 4 | 0 | 0.0 | n/a | R3-AMOUNT-CEILING:4 | 1.0 | 21 | 8 | 0 | 0 | 153 | 0 | n/a | n/a | n/a | n/a | n/a |
| fake | a2-agent | reauth_required | 3 | 3 | 0 | 3 | 0 | 0 | 0.0 | 3 | 0 | 0 | 0 | 0 | 3 | 60 | 0 | 0 | n/a | n/a |  | 1.0 | 6 | 0 | 0 | 0 | 33 | 0 | n/a | n/a | n/a | n/a | n/a |
| fake | a2-agent | new_instrument_required | 3 | 3 | 0 | 3 | 0 | 0 | 0.0 | 3 | 0 | 0 | 0 | 0 | 3 | 60 | 0 | 0 | n/a | n/a |  | 1.0 | 6 | 0 | 0 | 0 | 33 | 0 | n/a | n/a | n/a | n/a | n/a |
| fake | a2-agent | never_retry | 14 | 14 | 0 | 0 | 0 | 0 | n/a | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 14 | 14 | 1.0 | 1.0 | R4-NEVER-RETRY-CLASS:14 | 1.0 | 14 | 14 | 0 | 0 | 98 | 0 | n/a | n/a | n/a | n/a | n/a |
| fake | a3-rules | overall | 40 | 40 | 0 | 26 | 16 | 10747700 | 0.615 | 22 | 0 | 0 | 0 | 0 | 6 | 120 | 18 | 14 | 0.778 | 1.0 | R3-AMOUNT-CEILING:4|R4-NEVER-RETRY-CLASS:14 | 1.0 | 40 | 18 | 0 | 0 | 388 | 0 | n/a | n/a | n/a | n/a | n/a |
| fake | a3-rules | transient_retry_eligible | 3 | 3 | 0 | 3 | 3 | 953400 | 1.0 | 3 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | n/a | n/a |  | 1.0 | 3 | 0 | 0 | 0 | 30 | 0 | n/a | n/a | n/a | n/a | n/a |
| fake | a3-rules | retry_eligible | 17 | 17 | 0 | 17 | 13 | 9794300 | 0.765 | 13 | 0 | 0 | 0 | 0 | 0 | 0 | 4 | 0 | 0.0 | n/a | R3-AMOUNT-CEILING:4 | 1.0 | 17 | 4 | 0 | 0 | 166 | 0 | n/a | n/a | n/a | n/a | n/a |
| fake | a3-rules | reauth_required | 3 | 3 | 0 | 3 | 0 | 0 | 0.0 | 3 | 0 | 0 | 0 | 0 | 3 | 60 | 0 | 0 | n/a | n/a |  | 1.0 | 3 | 0 | 0 | 0 | 33 | 0 | n/a | n/a | n/a | n/a | n/a |
| fake | a3-rules | new_instrument_required | 3 | 3 | 0 | 3 | 0 | 0 | 0.0 | 3 | 0 | 0 | 0 | 0 | 3 | 60 | 0 | 0 | n/a | n/a |  | 1.0 | 3 | 0 | 0 | 0 | 33 | 0 | n/a | n/a | n/a | n/a | n/a |
| fake | a3-rules | never_retry | 14 | 14 | 0 | 0 | 0 | 0 | n/a | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 14 | 14 | 1.0 | 1.0 | R4-NEVER-RETRY-CLASS:14 | 1.0 | 14 | 14 | 0 | 0 | 126 | 0 | n/a | n/a | n/a | n/a | n/a |
