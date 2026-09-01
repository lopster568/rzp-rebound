# Phase 5 plan: realism hardening

Written 2026-09-01, before any phase 5 code. The phase after submission, and
the one that answers the only question a payments reviewer will actually ask:
where did these constants come from.

## Goal

Replace every invented piece of content with the real, citable industry
equivalent, and relabel honestly what cannot be cited.

Phases 0 through 4 built a system that is honest about what it measured. It was
not honest about where its inputs came from, because it never said. Eight error
reasons out of a test-card page stood in for a taxonomy. A retry cap of 3, a
cooldown of 30 seconds, an amount ceiling of 450000 paise, a modelled cost of
200 and 5000 paise, and a batch mix of 28/24/24/24 were all chosen by the
author, and only two of them said so.

The rule this phase writes: **every constant is either cited or labelled a
configured choice.** There is no third category, and "it seemed about right" is
the second one.

## The two failure modes this phase has to avoid

**Inventing a citation.** Not every constant has an industry equivalent. There
is no published interval at seconds scale for a retry cooldown or a
notification rate: the shortest scheme-native retry interval anyone publishes
is the Mastercard automated-clearing schedule, which starts at one hour, and
the closest Indian regulatory number is the RBI e-mandate 24 hour pre-debit
notice, which is a notice floor and not a rate. Attaching either to a 30 second
constant would be worse than leaving it unlabelled, because it would look
checked. Those two get the words "configured choice, no citable industry value
exists at this scale" in the code and in the docs.

**Laundering a published number into a run artifact.** `scripts/claims_check.py`
builds its fact set from committed runs only. A cited external number is not a
run artifact and must not be made to look like one. Every external number in
this phase goes through `scripts/claims-allow.txt` with its source in the
reason field, which is the mechanism that already exists for settings and
protocol constants.

## What changes, and what each change is backed by

### 1. The classifier adopts the documented live-mode taxonomy

Today `internal/classify` maps eight reason strings taken from the test-card
page. Razorpay documents the live-mode reasons per payment method, and the two
lists are not the same list.

| Change | Backing |
|---|---|
| Cards: 15 documented reasons, table-driven | `razorpay.com/docs/errors/payments/cards/` |
| UPI: 8 documented reasons, its own table | `razorpay.com/docs/errors/payments/upi/` |
| `insufficient_fund` becomes `insufficient_funds` | The live docs spell it plural. The repository has spelled it singular since phase 0 and that is a real bug against the live vocabulary. |
| `payment_risk_check_failed` replaces `testcards.PendingRiskBlockCode` | It is the documented reason the stand-in was standing in for. PRD Q2 closes. |
| `error.source` becomes an enum type | `razorpay.com/docs/payments/payment-gateway/rainy-day/errors/payment-error-parameters/` documents nine values. |
| `error.step` stays a free string | The same page publishes no enumeration for it. |

The test-card spellings are not deleted. They are moved into their own table,
labelled as what the test-card page carries rather than what live mode
documents, because the fake gateway and every committed batch use them and
because the distinction is the finding.

Fail-closed is unchanged. A reason no table holds is `unclassified` and is
never retry eligible. A reason two method tables disagree about is
`unclassified` too, which is a new rule and needs a test.

### 2. `internal/networkcodes`, a new package

The card networks publish which decline codes may never be reattempted. That
list is a fact about the payments industry and it belongs in a package of its
own with the source URLs in its header, not inline in a policy comment.

- Visa Category 1, "never reattempt": `04 07 12 14 15 41 43 46 57 R0 R1 R3`.
- The four codes stale blogs still list as Category 1 and which the 2020 update
  moved out: `03 62 78 93`. Carried deliberately, with a predicate that says
  they are not Category 1, so a future reader who found them in a blog post
  finds the correction here.
- Mastercard merchant advice code `03`, "do not try again".
- The Visa reattempt cap: 15 per declined transaction per 30 rolling days.

### 3. The policy rules get their citation status written down

| Rule | Before | After | Status |
|---|---|---|---|
| R1-MAX-ATTEMPTS | 3, "by requirement" | 3, unchanged, relabelled a conservative merchant policy under the Visa 15-in-30 cap | cited bound, configured value |
| R2-COOLDOWN | 30s, unlabelled | 30s, unchanged | configured choice, no citable value |
| R3-AMOUNT-CEILING | 450000 paise, tuned after a run | 1500000 paise, the RBI e-mandate additional-factor-of-authentication threshold of Rs 15,000 | cited |
| R4-NEVER-RETRY-CLASS | one stand-in code | the documented risk reason, plus the network lists behind the class | cited |
| R6-NOTIFY-RATE | 30s, unlabelled | 30s, unchanged | configured choice, no citable value |

R1 keeps its value. The Visa bulletin caps reattempts at 15 in 30 rolling days
for Categories 2 and 3; 3 is inside that and a merchant is free to be stricter.
What changes is that the number now names the cap it sits under. A rolling
30 day window is not implemented, because the store has no history that spans
runs and building one to enforce a bound this policy is nowhere near would be a
feature written to decorate a citation.

R3 moves and the move is a correction. 450000 was picked after a run showed
400000 escalating a quarter of the batch, which is a threshold tuned to a
result. Rs 15,000 is the threshold above which the RBI e-mandate framework
requires an additional factor of authentication, which is a real Indian
payments line between "unattended is fine" and "a human has to be in the loop"
and is exactly what this rule is for. The batch amount range moves to straddle
it, so the rule can still fire.

### 4. The cost model is rebuilt on cited numbers

The current model charges 200 paise per payment attempt and 5000 paise per
forbidden action, both invented. Both are wrong in a way that matters.

- **A failed attempt in India costs no gateway fee.** Razorpay and PayU both
  bill successful transactions only. The modelled per-attempt fee goes to zero,
  and the finding is that the old model was charging for something free.
- **The forbidden-action cost was ten times too low.** The floor a merchant
  actually pays on a chargeback is Rs 500, so 50000 paise replaces 5000.
- **A notification is not free.** Transactional SMS in India runs 15 to 20
  paise a message. The model gains a notification cost column at the top of
  that band, which is a cost the old model did not have at all.
- **The one citable per-reattempt charge does not apply here.** The Visa
  reattempt-abuse fee is about ten US cents and it applies beyond the 15-in-30
  cap. A policy capped at 3 never reaches it. The number is carried with its
  source and multiplied by zero, which is more honest than omitting it.

The model stays a model, the assumption sentence stays printed above every
table, and PRD 9.2 is unchanged. What changes is that each input now names a
source instead of naming nobody.

### 5. Batch profiles, named for what they are

`cmd/rzp/seed.go` holds one hard-coded mix, 28/24/24/24, described in its own
comment as "the shape of a real failure mix" with nothing behind that. Three
named profiles replace it.

| Profile | What it is |
|---|---|
| `uniform-invented` | The existing shares, unchanged so `b-1234-40` still reproduces byte for byte, renamed to say what they are. |
| `ethoca-card-mix-2019` | Mastercard/Ethoca published card-decline shares: insufficient funds 44 percent, lost or stolen 26 percent, fraud 9 percent. The vintage is in the name because a 2019 mix is a 2019 mix. |
| `observed-live-mix` | A loader for a real merchant's aggregates, reading a JSON file at `RZP_OBSERVED_MIX_FILE`, a path outside the repository. Unset means the profile errors and says so. Merchant data never enters git. |

The cited shares sum to 79 percent. The residual 21 percent is split across the
remaining classes and labelled in the profile as not cited, rather than being
folded into one of the cited shares to make the arithmetic tidy.

The ethoca profile makes 35 percent of the batch orders no arm should act on,
because lost, stolen, and fraud declines are exactly that. In this repository's
vocabulary those are bait orders. The share is the source's, not the author's.

### 6. `docs/EVIDENCE.md`

The document that answers "is this problem real". Involuntary churn rates, false
decline volumes, the network reattempt rules, the NPCI UPI decline targets, and
the vendor claims, each labelled with what kind of source it is: a network
bulletin, a regulator circular, an analyst estimate, or a vendor's own
marketing. Vendor claims are carried and labelled vendor claims, including the
one whose own publisher states it two different ways.

It ends with the list of things that cannot be made real without production
data, which is the honest half.

### 7. Everything the runs produce is re-run

New rules and a new batch profile mean the published tables are stale the
moment the code lands. Three runs:

| Run | Layer | n | Arms | Invocations |
|---|---|---|---|---|
| `phase-5-fake-ethoca` | fake | 40 | all four | 40 |
| `phase-5-fake-uniform` | fake | 40 | a0, a1, a3 | 0 |
| `phase-5-live` | live | 8 | a0, a1, a3 | 0 |

The headline moves to the ethoca profile. The uniform run stays published for
comparison, because a profile change that moved every number and was reported
only in its new form would be unreadable. The live layer drops `a2-agent`: it
adds no classification signal on a gateway that returns one reason for
everything, and the budget is better spent on the fake layer where the agent
can be told apart from the rule set.

## Exit criteria

1. Every constant in `internal/policy`, `internal/classify`, `internal/batch`,
   and `harness/aggregate.py` is either cited in its own doc comment or carries
   the words "configured choice".
2. `internal/networkcodes` exists with source URLs in its header.
3. `testdata/error_codes.json` and `testdata/magic_cards.json` label every
   entry `documented-live` or `observed-test-mode`, with a source and a date.
4. `docs/EVIDENCE.md` exists, is linked from `README.md` and `docs/PRD.md`, and
   every external number in it is in `scripts/claims-allow.txt` with its source
   as the reason.
5. `ADR-0008` records the rule.
6. Three runs are committed, `RESULTS.md` reads from them, and
   `make verify-phase-4` semantics still hold: the claims gate passes and no
   published cell disagrees with its CSV.
7. `make ci` is green and Actions is green.

## What this phase does not do

- It does not implement a rolling 30 day reattempt window.
- It does not build the `observed-live-mix` profile from real data. A read-only
  probe of the author's live account on 2026-09-01 returned two payments. Two
  payments are a specimen, not a distribution, and seeding a mix from them
  would be the invention this phase exists to remove.
- It does not claim the documented live-mode reasons are what a merchant will
  see. Test mode returns one reason for every card, and this project has still
  never observed the documented vocabulary at scale.
