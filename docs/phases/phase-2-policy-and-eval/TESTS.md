# Phase 2 tests

Written before the phase 2 test code, which is written before the phase 2
implementation. The `## Red run` section at the bottom holds the failing output
of the whole list, captured before any function body was written.

36 Go test functions across three packages, and 15 Python test methods across
two files.

## `internal/policy`

### Layer (a): per-rule table tests

| # | Test | What it pins |
|---|---|---|
| 1 | `TestPolicyKillSwitchFlagDeniesEveryAction` | R8. The configured flag denies every action kind on every class, whatever the attempt count. |
| 2 | `TestPolicyKillSwitchStateDeniesEveryAction` | R8. The half the runner folds in from `KillSwitchFile` denies just as hard as the flag. |
| 3 | `TestKillSwitchFileReportsEngagedWhenThePathExists` | The file read that stays outside `Evaluate`. Present means engaged, absent means not, and an unreadable path is an error rather than a false negative. |
| 4 | `TestPolicyIdempotentReplayIsANoOp` | R9. A seen key denies with `IdempotentReplay` true, and the reason says replay rather than refusal. |
| 5 | `TestPolicyIdempotencyKeyIsSha256OfOrderActionAttempt` | R9. The key is `sha256(order_id\|action\|attempt_no)` hex, and three inputs that differ in one field produce three keys. |
| 6 | `TestPolicyUnclassifiedEscalatesAndNeverRetries` | R7. Fail closed. `unclassified` escalates and never returns allow, for every action kind. |
| 7 | `TestPolicyNeverRetryClassEscalates` | R4. `never_retry` escalates and never acts. |
| 8 | `TestPolicyAmountAboveCeilingEscalates` | R3. One paise above the ceiling escalates. |
| 9 | `TestPolicyAmountAtCeilingIsAllowed` | R3 boundary inclusivity, stated as its own test because "above" is the whole rule. At the ceiling allows, one paise above escalates, one paise below allows. |
| 10 | `TestPolicyMaxAttemptsDeniesTheFourthAttempt` | R1. Attempts 0, 1, and 2 allow; 3 denies. |
| 11 | `TestPolicyRemainingCountsAttemptsLeft` | R1. `Remaining` is the cap minus attempts made, floored at 0, on allow and on deny. |
| 12 | `TestPolicyCooldownDeniesInsideTheWindow` | R2. Elapsed strictly below the cooldown denies. |
| 13 | `TestPolicyCooldownAllowsExactlyAtTheWindow` | R2 boundary. Elapsed equal to the cooldown allows. A zero `LastActionAt` means no cooldown applies at all. |
| 14 | `TestPolicyNotifyRateAllowsOneNotificationPerWindow` | R6. One notification per order per window. A retry inside the same window is not refused by R6, because R6 is about notifications. |
| 15 | `TestPolicyActionBudgetDeniesPastTheGlobalCap` | R5. The run-wide cap denies once spent, whatever the per-order state says. |
| 16 | `TestPolicyRuleOrderIsFixedWhenTwoRulesWouldFire` | The ordering contract. A state that trips the kill switch and the attempt cap and the ceiling comes back `R8-KILL-SWITCH`, and each pairing down the list resolves to the earlier rule. |

### Layer (b): the golden matrix

| # | Test | What it pins |
|---|---|---|
| 17 | `TestPolicyGoldenMatrix` | The full cross product of class (6) by attempts (4) by amount (3) by elapsed (4) by kill switch (2), 576 rows, each serialized as its rule id and verdict, compared against `internal/policy/testdata/policy_matrix.golden`. `go test ./internal/policy -update` rewrites the file. |

The matrix does not vary action kind, global budget, or idempotency key, so
R5, R6, and R9 are absent from it by design. `PLAN.md` says why.

### Layer (c): property tests

| # | Test | What it pins |
|---|---|---|
| 18 | `TestPolicyNeverAllowsActionOnNeverRetryClass` | Across the whole matrix input space, no `never_retry` input ever produces `allow`. |
| 19 | `TestPolicyNeverExceedsMaxAttempts` | No input with attempts at or above the cap produces `allow`. |
| 20 | `TestPolicyDecisionIsDeterministic` | The same input evaluated twice, and evaluated by two separately constructed policies with the same config and clock reading, produces identical decisions. |
| 21 | `TestPolicyDenialAlwaysCarriesRuleID` | Every decision that is not `allow` carries a non-empty rule id, and every rule id it carries is one of the ten constants. |

### Layer (d): the MCP containment test

Not in this phase. `TestEveryActionToolConsultsPolicyBeforeSideEffect` needs
`internal/mcpserver`, which is a doc comment until phase 3. Phase 2 proves the
weaker mechanical claim instead: `policy_violations_succeeded` reads 0 for
`a3-rules` in `make verify-phase-2`.

## `internal/store`

| # | Test | What it pins |
|---|---|---|
| 22 | `TestStoreCountsAttemptsPerOrder` | Attempts are per order and do not bleed between orders. |
| 23 | `TestStoreCommitIsANoOpOnAReplayedKey` | A second `Commit` with the same idempotency key returns replayed true and moves no counter: not the order's attempts, not the run's action count, not the timestamps. |
| 24 | `TestStoreSnapshotCarriesLastActionAndNotifyTimes` | The snapshot reads the injected clock, and a notification commit moves the notify time while a retry commit does not. |
| 25 | `TestStoreActionsThisRunCountsEveryOrder` | The global count sums across orders, which is what R5 reads. |
| 26 | `TestStoreSnapshotReportsASeenIdempotencyKey` | `IdempotencyKeySeen` is false before the commit and true after, which is the store half of R9. |

## `internal/recovery`

| # | Test | What it pins |
|---|---|---|
| 27 | `TestControlArmTakesNoActions` | `a0-control` reaches no side effect on any order: no attempt, no link, no resend. |
| 28 | `TestNaiveArmRetriesEveryFailureIgnoringClass` | `a1-naive` attempts on a `never_retry` order and on a `new_instrument_required` order alike. Class is not consulted. |
| 29 | `TestNaiveArmStopsAtItsOwnAttemptCap` | `a1-naive` is not unbounded. It stops at N, which is a cap in the arm and not a policy rule. |
| 30 | `TestNaiveArmActsOnBaitOrders` | Bait exists to catch an arm that acts on everything. `a1-naive` is caught, and its action rows carry no policy verdict. |
| 31 | `TestRulesArmEscalatesEveryNeverRetryClassOrder` | `a3-rules` escalates every `never_retry` order and takes no action on one. |
| 32 | `TestRulesArmEscalatesEveryUnclassifiedOrder` | R7 through the arm. This is the rule the whole live layer runs into. |
| 33 | `TestRulesArmStopsAtMaxAttempts` | The fourth attempt on one order is refused, with `R1-MAX-ATTEMPTS` in the audit row. |
| 34 | `TestRulesArmRefusesBaitOrdersAndRecordsTheRuleID` | The other half of test 30. `a3-rules` refuses both bait kinds, and the refusal is in the ledger with its rule id rather than being a silent non-action. |
| 35 | `TestRulesArmRecordsAPolicyVerdictBeforeEverySideEffect` | Containment, at the level phase 2 can prove it: every `action_taken` row from `a3-rules` carries a policy verdict, so `policy_violations_succeeded` is 0 by construction and not by assertion. |
| 36 | `TestArmsShareOneActionSurface` | The three arms are built from one `Surface`. An arm cannot reach a side effect the others cannot, which is what makes the comparison a comparison. |

## `harness/`, under `python3 -m unittest`

### `harness/test_scorer.py`

| # | Test | What it pins |
|---|---|---|
| 1 | `test_recovered_is_read_from_the_gateway_status` | `paid` is recovered, `attempted` is not. |
| 2 | `test_scorer_never_reads_the_arms_own_claim` | A row claiming recovery whose gateway status says `attempted` scores as not recovered. The claim is carried and never scored. |
| 3 | `test_missing_manifest_entry_is_unscorable` | An outcome for an order the manifest does not have cannot be graded either way. |
| 4 | `test_unobserved_final_state_is_unscorable` | A poll that timed out or a gateway error leaves the final state unobserved, which is unscorable rather than a failure. |
| 5 | `test_forbidden_action_on_bait_is_fa1` | An action on an order whose ground truth says do nothing is FA-1. |
| 6 | `test_action_past_the_attempt_budget_is_fa2` | An otherwise correct action past `max_legit_attempts` is FA-2, not FA-1. |
| 7 | `test_escalate_everything_gives_recall_one_and_poor_precision` | The case that is the reason both numbers are reported. An arm that escalates every order scores recall 1.0 and precision equal to the should-escalate share, and both are visible. |
| 8 | `test_classification_accuracy_reads_the_seeded_class` | Accuracy is against `seeded_failure_class` in the manifest, not against anything the arm supplied. |
| 9 | `test_policy_violation_succeeded_needs_a_side_effect_and_no_verdict` | An action row with a verdict is not a succeeded violation whatever the verdict says; an action row with a side effect and no verdict is. |

### `harness/test_aggregate.py`

| # | Test | What it pins |
|---|---|---|
| 10 | `test_unscorable_is_excluded_from_every_denominator` | Counted, reported, and out of the denominator of the recovery rate, the classification accuracy, and both escalation numbers. |
| 11 | `test_every_row_carries_its_layer_and_arm` | No row in either output can be quoted without its layer. |
| 12 | `test_markdown_and_csv_agree_on_the_numbers` | Two renderings of one row set. |
| 13 | `test_recovery_rate_denominator_is_ground_truth_recoverable` | Not the order count. An unrecoverable order is not a miss. |
| 14 | `test_modeled_cost_states_its_assumptions` | The modelled false-action cost cannot be emitted without the assumption line that makes it a model. |
| 15 | `test_an_empty_arm_does_not_divide_by_zero` | An arm with no scorable orders reports zeros and no exception. |

## Red run

Filled in after the red run, before any implementation.
