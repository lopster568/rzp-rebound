# Phase 5 tests

Written before the code, per the TDD rule in `CLAUDE.md`. The red run is pasted
below the list, and every test in the list was seen failing before its
implementation existed.

## Go

### `internal/networkcodes`

| Test | What it pins |
|---|---|
| `TestVisaCategory1IsTheTwelvePublishedCodes` | The Category 1 list is exactly `04 07 12 14 15 41 43 46 57 R0 R1 R3`, sorted, with no additions. |
| `TestCodesMovedOutOfCategory1In2020AreNotCategory1` | `03`, `62`, `78`, and `93` are not Category 1, and the package says so explicitly rather than by omission. Stale blog posts still list all four. |
| `TestMastercardMerchantAdviceCode03IsNeverRetry` | MAC `03` means do not try again. |
| `TestNeverRetryIsFalseForAnUnknownNetworkAndCode` | Fail-open is not a thing here: an unknown network with a Category 1 code string is not silently treated as Visa. |
| `TestVisaReattemptCapIsFifteenInThirtyDays` | The published cap is carried as a named constant, not as a sentence in a comment. |

### `internal/classify`

| Test | What it pins |
|---|---|
| `TestClassifierMapsEveryDocumentedCardReason` | All 15 documented live-mode card reasons classify, table-driven, each to its named class. |
| `TestClassifierMapsEveryDocumentedUPIReason` | All 8 documented live-mode UPI reasons classify, table-driven. |
| `TestInsufficientFundsIsSpelledTheWayTheLiveDocsSpellIt` | `insufficient_funds` classifies retry eligible. The singular spelling is the test-card table's and is handled separately. |
| `TestTestModeCardTableSpellingsStillClassify` | `insufficient_fund` and `card_number_invalid`, the spellings the test-card page carries and every committed batch uses, still classify to the same classes. |
| `TestPaymentRiskCheckFailedIsNeverRetry` | The real reason replaces the stand-in. |
| `TestCardExpiredAndBlockedInstrumentAreNewInstrumentRequiredNotNeverRetry` | The distinction: both forbid another attempt on the same instrument, and only a risk block also forbids asking the customer for a different one. |
| `TestNoReasonClassifiesDifferentlyAcrossMethods` | The merged method-agnostic lookup is total and unambiguous, which is what lets a caller that does not know the method still get an answer. |
| `TestAmbiguousReasonAcrossMethodsIsUnclassified` | The fail-closed rule for the case the test above rules out today, driven through a constructed table so the rule is proven rather than vacuous. |
| `TestDocumentedErrorSourcesAreTheNinePublishedValues` | The `error.source` enum is exactly the nine documented values. |
| `TestUndocumentedErrorSourceDoesNotParse` | Anything else fails to parse rather than being carried through as if it were documented. |
| `TestErrorStepIsNotAnEnum` | `error.step` has no published enumeration, so the type stays a string, asserted so a later phase does not quietly invent one. |
| `TestClassifierHandlesTheProductionFailureShape` | A payload shaped exactly like the one failed payment in the author's live merchant account on 2026-09-01: `BAD_REQUEST_ERROR` plus `payment_timed_out` plus source `customer` plus step `payment_authentication`. It classifies transient retry eligible off the reason, and the coarse code does not override it. |
| `TestClassifierIsTotalOverKnownRazorpayErrorCodes` | Existing test, now walking a much longer `testdata/error_codes.json`. |

### `internal/policy`

| Test | What it pins |
|---|---|
| `TestAmountCeilingDefaultIsTheRBIEMandateThreshold` | The default is 1500000 paise, the Rs 15,000 additional-factor threshold. |
| `TestAmountCeilingEscalatesStrictlyAbove` | Existing behaviour at the new value: 1500000 is inside, 1500100 is not. |
| `TestMaxAttemptsSitsUnderTheVisaReattemptCap` | The configured cap is at or below `networkcodes.VisaReattemptCapPer30Days`, so the relabelling cannot be broken by someone raising the constant. |
| Golden matrix | `internal/policy/testdata/policy_matrix.golden` regenerates against the new ceiling. |

### `internal/batch`

| Test | What it pins |
|---|---|
| `TestUniformInventedProfileReproducesTheCommittedBatch` | `b-1234-40` is byte-identical after the profile refactor. This is the test that makes the refactor safe. |
| `TestEthocaProfileSharesMatchThePublishedFigures` | 44, 26, and 9 percent, and the residual is what is left rather than a fourth invented share. |
| `TestEthocaProfileBaitIsTheCitedNeverRetryShare` | 35 percent of a 40 order batch is 14 orders no arm should act on. |
| `TestObservedLiveMixWithoutItsFileIsAnErrorAndSaysWhy` | The stub profile fails loudly with the env var name in the message. |
| `TestObservedLiveMixReadsAFileOutsideTheRepository` | The loader reads the path it is given and nothing else. |
| `TestEveryProfileNamesItsSourceOrSaysInvented` | No profile can ship without a source string. |

### `internal/testcards`

| Test | What it pins |
|---|---|
| `TestCardTableEntriesCarryALabelAndASource` | Every entry in `magic_cards.json` is labelled `documented-test-mode` or `observed-test-mode` and carries a source URL and a date. |

## Python

| Test | What it pins |
|---|---|
| `test_failed_attempt_costs_no_gateway_fee` | The modelled per-attempt fee is 0, because India bills successful transactions only. |
| `test_forbidden_action_cost_is_the_chargeback_floor` | 50000 paise. |
| `test_notification_cost_is_counted_separately_from_false_actions` | A correct notification is a cost and is not a false action, so it cannot be added into the false-action column. |
| `test_notifications_are_counted_on_both_notify_actions` | `request_reauth` and `request_new_instrument` both count. |
| `test_visa_reattempt_fee_is_carried_and_not_applied` | The constant exists with its source and multiplies by zero under a cap of 3. |
| `test_cost_model_assumptions_names_every_input` | The assumption sentence printed above every table mentions each of the four numbers, so a table cannot ship with an assumption line that has fallen behind the model. |

## The red run

Pasted from the run of each new test against the tree before its
implementation existed.
