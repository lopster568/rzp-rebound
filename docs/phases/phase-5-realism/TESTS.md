# Phase 5 tests

Written before the code, per the TDD rule in `CLAUDE.md`. The red run is pasted
below the list, and every test in the list was seen failing before its
implementation existed.

The tables below are not edited after the fact. They are the record of what was
named before any code existed, on the phase 3 convention, and everything that
was added or renamed afterwards is at the bottom under "What the list did not
predict". Four names in these tables no longer exist, all four because the
2026-09-01 citation re-verification pass found the claim behind them wrong.

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

Pasted from the run of each new test against the tree before its implementation
existed. The technique is the phase 0 and phase 3 one: the declarations go in
with the tests and the bodies do not, because a red run that will not compile
cannot be committed under a pre-commit hook that runs `go vet`.

Seven red commits, in order.

**1. `internal/networkcodes`, at feecf33.** Two lists named before either
existed.

```
--- FAIL: TestVisaCategory1IsTheTwelvePublishedCodes (0.00s)
    networkcodes_test.go:16: VisaCategory1() = [], want [04 07 12 14 15 41 43 46 57 R0 R1 R3]
    networkcodes_test.go:20: IsVisaCategory1("04") is false, and the bulletin lists it
    networkcodes_test.go:20: IsVisaCategory1("07") is false, and the bulletin lists it
    networkcodes_test.go:20: IsVisaCategory1("12") is false, and the bulletin lists it
    networkcodes_test.go:20: IsVisaCategory1("14") is false, and the bulletin lists it
    networkcodes_test.go:20: IsVisaCategory1("15") is false, and the bulletin lists it
    networkcodes_test.go:20: IsVisaCategory1("41") is false, and the bulletin lists it
    networkcodes_test.go:20: IsVisaCategory1("43") is false, and the bulletin lists it
    networkcodes_test.go:20: IsVisaCategory1("46") is false, and the bulletin lists it
    networkcodes_test.go:20: IsVisaCategory1("57") is false, and the bulletin lists it
    networkcodes_test.go:20: IsVisaCategory1("R0") is false, and the bulletin lists it
    networkcodes_test.go:20: IsVisaCategory1("R1") is false, and the bulletin lists it
    networkcodes_test.go:20: IsVisaCategory1("R3") is false, and the bulletin lists it
```

**2. `internal/classify`, at 65ac009.** The documented live-mode taxonomy.

```
--- FAIL: TestClassifierMapsRiskBlockToNeverRetry (0.00s)
    classify_test.go:106: risk block classified as unclassified, want never_retry
--- FAIL: TestClassifierIsTotalOverKnownRazorpayErrorCodes (0.00s)
    classify_test.go:175: payment_risk_check_failed classifies as unclassified
--- FAIL: TestClassifierMapsEveryDocumentedCardReason (0.00s)
    --- FAIL: TestClassifierMapsEveryDocumentedCardReason/card_not_enrolled (0.00s)
        taxonomy_test.go:61: card card_not_enrolled classified as unclassified, want new_instrument_required
    --- FAIL: TestClassifierMapsEveryDocumentedCardReason/card_expired (0.00s)
        taxonomy_test.go:61: card card_expired classified as unclassified, want new_instrument_required
    --- FAIL: TestClassifierMapsEveryDocumentedCardReason/incorrect_cvv (0.00s)
        taxonomy_test.go:61: card incorrect_cvv classified as unclassified, want reauth_required
    --- FAIL: TestClassifierMapsEveryDocumentedCardReason/debit_instrument_blocked (0.00s)
```

**3. The testdata labels, at 57e3460.** Every row in both tables having to say
which vocabulary it belongs to.

```
--- FAIL: TestErrorCodeFileLabelsEveryEntry (0.00s)
    taxonomy_test.go:362: ../../testdata/error_codes.json has no _meta.labels explaining what each label means
    taxonomy_test.go:369: payment_timed_out has label "", want one of [documented-live documented-test-mode observed-test-mode]
    taxonomy_test.go:372: payment_timed_out has no source
    taxonomy_test.go:369: insufficient_fund has label "", want one of [documented-live documented-test-mode observed-test-mode]
    taxonomy_test.go:372: insufficient_fund has no source
    taxonomy_test.go:369: payment_cancelled has label "", want one of [documented-live documented-test-mode observed-test-mode]
    taxonomy_test.go:372: payment_cancelled has no source
```

**4. `internal/policy`, at fbc392f.** Every rule declaring where its number came
from.

```
--- FAIL: TestAmountCeilingDefaultIsTheRBIEMandateThreshold (0.00s)
    citations_test.go:28: DefaultAmountCeilingPaise = 450000, want 1500000, the RBI e-mandate additional-factor threshold
    citations_test.go:40: at the threshold: verdict = escalate under R3-AMOUNT-CEILING, want allow
--- FAIL: TestTheTwoIntervalDefaultsAreDeclaredConfiguredChoices (0.00s)
    citations_test.go:86: R2-COOLDOWN is not declared a configured choice, and no industry source publishes an interval at this scale
    citations_test.go:86: R6-NOTIFY-RATE is not declared a configured choice, and no industry source publishes an interval at this scale
--- FAIL: TestEveryRuleDeclaresItsCitationStatus (0.00s)
    citations_test.go:112: R8-KILL-SWITCH declares no citation status
    citations_test.go:112: R9-IDEMPOTENCY declares no citation status
    citations_test.go:112: R7-UNKNOWN-FAIL-CLOSED declares no citation status
    citations_test.go:112: R4-NEVER-RETRY-CLASS declares no citation status
    citations_test.go:112: R3-AMOUNT-CEILING declares no citation status
```

**5. `internal/batch`, at f88b9b0.** Three named failure mixes.

```
--- FAIL: TestUniformInventedProfileKeepsTheSharesItAlwaysHad (0.00s)
    profiles_test.go:42: no profile named uniform-invented
--- FAIL: TestEthocaProfileSharesMatchThePublishedFigures (0.00s)
    profiles_test.go:82: no profile named ethoca-card-mix-2019
--- FAIL: TestEthocaProfileBaitIsTheCitedNeverRetryShare (0.00s)
    profiles_test.go:136: no profile named ethoca-card-mix-2019
--- FAIL: TestObservedLiveMixWithoutItsFileIsAnErrorAndSaysWhy (0.00s)
    profiles_test.go:176: no profile named observed-live-mix
--- FAIL: TestObservedLiveMixReadsAFileOutsideTheRepository (0.00s)
    profiles_test.go:216: no profile named observed-live-mix
```

**6. The cost model, at b9467b9.** Python, so the shape is different.

```
======================================================================
ERROR: test_an_unscorable_row_notifies_nothing (harness.test_cost_model.CostColumns.test_an_unscorable_row_notifies_nothing)
----------------------------------------------------------------------
Traceback (most recent call last):
  File "/home/oni/rzp-recovery-agent/harness/test_cost_model.py", line 155, in test_an_unscorable_row_notifies_nothing
    self.assertFalse(got["notified"])
```

**7. The citation corrections, at e7dba60.** A red run inside the phase, after
a second verification pass found four of its own claims wrong. This is the one
that matters most, because the code it corrected was already green.

```
--- FAIL: TestDocumentedErrorSourcesArePerMethod (0.00s)
    --- FAIL: TestDocumentedErrorSourcesArePerMethod/card_method (0.00s)
        taxonomy_test.go:248: DocumentedSources("card") = [beneficiary_bank business customer customer_psp gateway internal issuer issuer_bank network], want [business customer gateway internal issuer_bank]
    --- FAIL: TestDocumentedErrorSourcesArePerMethod/upi_method (0.00s)
        taxonomy_test.go:248: DocumentedSources("upi") = [beneficiary_bank business customer customer_psp gateway internal issuer issuer_bank network], want [beneficiary_bank business customer customer_psp gateway internal issuer_bank network]
    --- FAIL: TestDocumentedErrorSourcesArePerMethod/_method (0.00s)
        taxonomy_test.go:248: DocumentedSources("") = [beneficiary_bank business customer customer_psp gateway internal issuer issuer_bank network], want [beneficiary_bank business customer customer_psp gateway internal issuer_bank network]
    taxonomy_test.go:271: "beneficiary_bank" is not documented for UPI, and it is on that list
    taxonomy_test.go:271: "customer_psp" is not documented for UPI, and it is on that list
    taxonomy_test.go:271: "network" is not documented for UPI, and it is on that list
--- FAIL: TestUndocumentedErrorSourceDoesNotParse (0.00s)
    taxonomy_test.go:281: ParseSource("issuer") returned issuer, and it is on neither documented list
```

## What the list did not predict

Three test groups were added after the list above was written, all of them from
the 2026-09-01 citation re-verification pass and all of them recorded here
rather than by editing the tables above.

| Test | Why it exists |
|---|---|
| `TestTheCategory1ListSaysItIsReconstructed` | The Visa bulletin does not enumerate Category 1. The first draft cited it for the list anyway, so `VisaCategory1IsReconstructed` is a constant a test holds rather than a caveat in prose. |
| `TestCode14MayNeverBeReattemptedOnTheSameAccountNumber` | The one code-level rule the bulletin does state outright, and the never-retry predicate alone could not express it. |
| `TestDocumentedErrorSourcesArePerMethod` | `error.source` is documented per method, five values for cards and eight for UPI. The original test asserted a flat nine including `issuer`, which is on neither list. |
| `TestPaymentFailedIsDocumentedAndStillUnclassified` | `payment_failed` is documented live-mode vocabulary, which phase 1 had recorded as a mystery. The classification does not move and the test carries the argument for why. |
| `TestErrorCodeFileLabelsEveryEntry`, extended | A row can be `documented-live` and still carry no class. That is what `_meta.pending` means, and there is exactly one such row. |

And four names in the tables above were renamed rather than added to, because
the claim each one asserted turned out to be wrong.

| Named before the code | What it is now | Why |
|---|---|---|
| `TestVisaCategory1IsTheTwelvePublishedCodes` | `TestVisaCategory1IsTheTwelveReconstructedCodes` | The codes are not published in the bulletin. |
| `TestCodesMovedOutOfCategory1In2020AreNotCategory1` | `TestCodesMovedOutOfCategory1AreNotCategory1` | The bulletin is dated 2020-09-03 and takes effect 2021-04-17, so neither year alone is right in the name. |
| `TestVisaReattemptCapIsFifteenInThirtyDays` | `TestVisaReattemptCapIsFifteenInThirtyDaysForCategory2` | The cap is stated for Category 2, not Categories 2 and 3. |
| `TestDocumentedErrorSourcesAreTheNinePublishedValues` | `TestDocumentedErrorSourcesArePerMethod` | There are not nine and they are not one list. |

Two more differ from the list for reasons that are not corrections.
`TestUniformInventedProfileReproducesTheCommittedBatch` became
`TestUniformInventedProfileKeepsTheSharesItAlwaysHad`, because the taxonomy and
the amount range moved underneath `b-1234-40` and byte-identity was no longer
the right invariant; the shares are. And
`TestAmountCeilingEscalatesStrictlyAbove` is folded into
`TestAmountCeilingDefaultIsTheRBIEMandateThreshold` rather than standing alone.

`TestClassifierHandlesTheProductionFailureShape` was in the list and is worth
naming again here: it is the only test in this repository built from a payment
that was neither seeded nor read off a documentation page.
