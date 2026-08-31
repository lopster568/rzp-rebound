# Recovery results sample-phase-2-fake

| Field | Value |
|---|---|
| Run id | sample-phase-2-fake |
| Layer | fake |
| Batch id | b-1234-40 |
| Seed | 42 |
| Git sha | b4186f2 |

Cost model: The cost model invents two numbers, 200 paise per payment attempt and 5000 paise per forbidden action, for the model only; neither is a measured Razorpay fee or a measured goodwill loss.

Honesty: a test-mode number is not evidence about real customers, and no row here is summed or averaged across layers (ADR-0004).

| layer | arm | scope | n_orders | n_scorable | n_unscorable | ground_truth_recoverable | recovered_orders | recovered_amount_paise | recovery_rate | actions_taken | false_action_count | fa1_forbidden | fa2_over_attempt | modeled_false_action_cost_paise | escalations | should_escalate | escalation_precision | escalation_recall | escalation_rules | classification_accuracy | policy_evaluations | policy_refusals | policy_violations_attempted | policy_violations_succeeded | api_calls | claim_disagreements |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| fake | a0-control | overall | 40 | 40 | 0 | 37 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 | 0 | 0 | 3 | n/a | 0.0 |  | 1.0 | 0 | 0 | 0 | 0 | 360 | 0 |
| fake | a0-control | transient_retry_eligible | 13 | 13 | 0 | 13 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | n/a | n/a |  | 1.0 | 0 | 0 | 0 | 0 | 117 | 0 |
| fake | a0-control | retry_eligible | 9 | 9 | 0 | 8 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 | 0 | 0 | 1 | n/a | 0.0 |  | 1.0 | 0 | 0 | 0 | 0 | 81 | 0 |
| fake | a0-control | reauth_required | 8 | 8 | 0 | 8 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | n/a | n/a |  | 1.0 | 0 | 0 | 0 | 0 | 72 | 0 |
| fake | a0-control | new_instrument_required | 8 | 8 | 0 | 8 | 0 | 0 | 0.0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | n/a | n/a |  | 1.0 | 0 | 0 | 0 | 0 | 72 | 0 |
| fake | a0-control | never_retry | 2 | 2 | 0 | 0 | 0 | 0 | n/a | 0 | 0 | 0 | 0 | 0 | 0 | 2 | n/a | 0.0 |  | 1.0 | 0 | 0 | 0 | 0 | 18 | 0 |
| fake | a1-naive | overall | 40 | 40 | 0 | 37 | 21 | 5698900 | 0.568 | 40 | 19 | 3 | 16 | 18200 | 0 | 3 | n/a | 0.0 |  | 1.0 | 0 | 0 | 0 | 40 | 400 | 19 |
| fake | a1-naive | transient_retry_eligible | 13 | 13 | 0 | 13 | 13 | 3555600 | 1.0 | 13 | 0 | 0 | 0 | 0 | 0 | 0 | n/a | n/a |  | 1.0 | 0 | 0 | 0 | 13 | 130 | 0 |
| fake | a1-naive | retry_eligible | 9 | 9 | 0 | 8 | 8 | 2143300 | 1.0 | 9 | 1 | 1 | 0 | 5000 | 0 | 1 | n/a | 0.0 |  | 1.0 | 0 | 0 | 0 | 9 | 90 | 1 |
| fake | a1-naive | reauth_required | 8 | 8 | 0 | 8 | 0 | 0 | 0.0 | 8 | 8 | 0 | 8 | 1600 | 0 | 0 | n/a | n/a |  | 1.0 | 0 | 0 | 0 | 8 | 80 | 8 |
| fake | a1-naive | new_instrument_required | 8 | 8 | 0 | 8 | 0 | 0 | 0.0 | 8 | 8 | 0 | 8 | 1600 | 0 | 0 | n/a | n/a |  | 1.0 | 0 | 0 | 0 | 8 | 80 | 8 |
| fake | a1-naive | never_retry | 2 | 2 | 0 | 0 | 0 | 0 | n/a | 2 | 2 | 2 | 0 | 10000 | 0 | 2 | n/a | 0.0 |  | 1.0 | 0 | 0 | 0 | 2 | 20 | 2 |
| fake | a3-rules | overall | 40 | 40 | 0 | 37 | 18 | 4324600 | 0.486 | 31 | 1 | 1 | 0 | 5000 | 9 | 3 | 0.222 | 0.667 | R3-AMOUNT-CEILING:7|R4-NEVER-RETRY-CLASS:2 | 1.0 | 40 | 9 | 0 | 0 | 403 | 1 |
| fake | a3-rules | transient_retry_eligible | 13 | 13 | 0 | 13 | 12 | 3091600 | 0.923 | 12 | 0 | 0 | 0 | 0 | 1 | 0 | 0.0 | n/a | R3-AMOUNT-CEILING:1 | 1.0 | 13 | 1 | 0 | 0 | 129 | 0 |
| fake | a3-rules | retry_eligible | 9 | 9 | 0 | 8 | 6 | 1233000 | 0.75 | 7 | 1 | 1 | 0 | 5000 | 2 | 1 | 0.0 | 0.0 | R3-AMOUNT-CEILING:2 | 1.0 | 9 | 2 | 0 | 0 | 88 | 1 |
| fake | a3-rules | reauth_required | 8 | 8 | 0 | 8 | 0 | 0 | 0.0 | 6 | 0 | 0 | 0 | 0 | 2 | 0 | 0.0 | n/a | R3-AMOUNT-CEILING:2 | 1.0 | 8 | 2 | 0 | 0 | 84 | 0 |
| fake | a3-rules | new_instrument_required | 8 | 8 | 0 | 8 | 0 | 0 | 0.0 | 6 | 0 | 0 | 0 | 0 | 2 | 0 | 0.0 | n/a | R3-AMOUNT-CEILING:2 | 1.0 | 8 | 2 | 0 | 0 | 84 | 0 |
| fake | a3-rules | never_retry | 2 | 2 | 0 | 0 | 0 | 0 | n/a | 0 | 0 | 0 | 0 | 0 | 2 | 2 | 1.0 | 1.0 | R4-NEVER-RETRY-CLASS:2 | 1.0 | 2 | 2 | 0 | 0 | 18 | 0 |
