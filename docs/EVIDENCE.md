# Evidence

Is the problem this repository attacks a real problem, and are the constants it
runs on real constants? This is the document that answers both, with a source on
every number and a label on every source.

Written 2026-09-01, at the start of phase 5. It replaces nothing: until this
file existed, the honest answer to "where did the retry cap come from" was that
the author picked it.

## How to read a row

Every claim below carries the kind of source it has, and the kinds are not
equal.

| Label | What it means |
|---|---|
| **network rule** | A card network's own published rule. Binding on merchants. |
| **regulator** | A regulator or a national payments body. Binding in its jurisdiction. |
| **vendor documentation** | A payment processor documenting its own API. Authoritative about that API and about nothing else. |
| **analyst estimate** | A research firm's published figure. A model, with a methodology that is usually not published. |
| **vendor claim** | A payments company's own marketing about its own product. Carried because it is what the market says, and labelled because nobody audited it. |
| **observed here** | Something this project measured, with the run in `results/`. |

A vendor claim is in this document for the same reason a competitor's press
release is in a market analysis. It is evidence about what is being claimed, not
evidence that the claim is true.

## 1. The problem: failed payments that never come back

**Involuntary churn runs 33 to 38 percent of total subscription churn.**
*Analyst and vendor-published, Recurly, 2026 research.* Involuntary churn is a
subscription ending because a payment failed rather than because a customer left.
It is the category this project is inside: the customer wanted to pay and the
charge did not go through.

**False declines cost more than fraud, by roughly six times.**
*Analyst estimate, Datos Insights, 2024.* Their published estimate puts false
decline volume at about six times card fraud losses, and the 2024 estimate of
false-decline value in the United States at 174 billion US dollars. A false
decline is a legitimate payment the issuer refused. It is the failure mode with
the largest gap between what the merchant lost and what the merchant can see.

**NPCI publishes decline targets for UPI, which is what a target implies.**
*Regulator, NPCI circular OC-149.* It sets technical decline targets below 1
percent and business decline targets below 5 percent for participants. A body
that has to set a target for a decline rate is a body whose members do not
automatically meet it.

## 2. The rules the retry policy sits under

**Visa Category 1 declines may never be reattempted. Categories 2 and 3 may be
reattempted at most 15 times per declined transaction in 30 rolling days.**
*Network rule, Visa bulletin AI10325, "Updates to Rules for Declined Transaction
Resubmission and Use of Authorization Response Codes".* The Category 1 list is
in `internal/networkcodes` with the bulletin URL in the file header.

Two things about that list are worth stating because they are commonly got
wrong. Response codes `03`, `62`, `78`, and `93` were moved out of Category 1 by
the 2020 update, and payments blog posts written before it still name all four
as never-retry codes. `internal/networkcodes` carries them under
`WasVisaCategory1Before2020` so a reader arriving with one of those posts finds
the correction rather than an unexplained gap.

**Mastercard merchant advice code `03` means do not try again.** *Network rule.*
Mastercard also publishes an automated-clearing retry schedule, whose shortest
interval is one hour.

**The Mastercard reattempt thresholds are contested and are not used here.**
Several secondary sources quote a specific per-merchant reattempt limit per
declined transaction, and they do not agree with each other on the number or on
the window. This project uses only the two Mastercard facts that are not in
dispute, MAC `03` and the one hour floor on the published schedule, and takes
its reattempt cap from the Visa bulletin, which is unambiguous.

**Rs 15,000 is the RBI e-mandate threshold above which an additional factor of
authentication is required.** *Regulator, RBI E-mandate Framework.* It is
`policy.DefaultAmountCeilingPaise`, at 1500000 paise, and it is the line
`R3-AMOUNT-CEILING` was approximating with an invented number until 2026-09-01.

**The RBI e-mandate 24 hour pre-debit notice is a notice floor and not a retry
rate.** *Regulator.* It is in this document because it is the closest thing to a
citable interval in Indian payments, and it does not answer the question
`R2-COOLDOWN` asks. See section 5.

## 3. The failure vocabulary

**Razorpay documents 15 live-mode failure reasons for cards and 8 for UPI, and
the two lists are not the same list.** *Vendor documentation.*
`razorpay.com/docs/errors/payments/cards/` and
`razorpay.com/docs/errors/payments/upi/`, read 2026-09-01. Both are in
`testdata/error_codes.json` with a label and a source on every row, and in
`internal/classify` as two tables.

**`error.source` has a documented enumeration of nine values. `error.step` does
not.** *Vendor documentation,* the payment error parameters page. The first is
a type in `internal/classify`; the second stays a string, and there is a test on
the absence so a later phase does not invent one.

**The class each reason maps to is this project's judgment and is cited
nowhere.** Razorpay publishes the reason strings. It does not publish a mapping
from a reason to "retry this" or "ask for a different card", and adopting a
documented vocabulary does not make the mapping documented. Every class in
`internal/classify` is an argument in a comment, not a citation.

## 4. What a mistake costs

The cost model in `harness/aggregate.py` is a model. What changed in phase 5 is
that each input names a source.

| Input | Value | Source | Kind |
|---|---|---|---|
| Gateway fee on a failed attempt | 0 paise | `razorpay.com/pricing/`, `payu.in/pricing/`: successful transactions only are billed | vendor documentation |
| Cost of a forbidden action | 50000 paise | The Rs 500 chargeback fee floor, Razorpay's own pricing explainer | vendor documentation |
| Cost of one notification | 20 paise | Indian transactional SMS runs 15 to 20 paise a message; the model takes the top of the band | vendor documentation |
| Visa excessive-reattempt fee | 875 paise | About 10 US cents under the Visa Reattempt Abuse Framework, reported by processors rather than published as a rate card | vendor claim |

The last row applies beyond the 15-in-30 cap and this policy caps attempts at 3,
so it is carried with its source and multiplied by zero. A reader who has heard
of the fee has to be able to find out here why it is not in the total.

The first row is the one that changed the shape of the model. A failed retry in
India costs the merchant no gateway fee, so an over-attempt is not expensive in
money. It is expensive in the two things this project cannot price: the
customer's patience, and the issuer's opinion of the merchant.

## 5. The batch mix

**Card declines: insufficient funds 44 percent, lost or stolen 26 percent, fraud
9 percent, with response codes `05` and `51` together about 80 percent of all
declines.** *Vendor-published research, Mastercard and Ethoca, `ethoca.com`,
figures describing 2019.* This is the `ethoca-card-mix-2019` profile in
`internal/batch`, and the vintage is in the profile name because a 2019 decline
mix is a 2019 decline mix.

Two honest notes on it. The three cited categories sum to 79 percent and the
source does not break out the remaining 21, which the profile carries as a
residual across the other classes and marks uncited. And 35 percent of that mix,
the lost, stolen, and fraud share, is orders no merchant should reattempt and no
merchant should message the cardholder about. In this repository's vocabulary
those are bait orders, so a citable card-decline mix makes over a third of the
batch unactionable. That share is the source's, not the author's, and it moves
every number in the table.

## 6. What vendors claim, labelled as vendor claims

**Razorpay Optimizer press material claims merchants lose up to 30 percent of
revenue to failed payments.** *Vendor claim.* It is carried here because it is
the number the Indian market quotes. It is also stated inconsistently across
Razorpay's own posts, which is worth knowing before anyone builds a business
case on it: the same figure appears attached to different denominators in
different pieces of their material. Nobody outside the vendor has audited any
version of it.

**Stripe and Adyen both publish recovery uplift from machine-learned retry
timing.** *Vendor claim.* Both companies publish results from their own
production systems on their own traffic, with no methodology, no baseline
definition, and no independent replication. They are evidence that large
processors think the problem is worth solving with a model. They are not a
number this project can compare itself against, and section 7 says why nothing
in this repository tries.

## 7. The production specimen

On 2026-09-01 a read-only probe of the author's own live Razorpay merchant
account returned aggregate statistics for 2026-07-15 to 2026-08-31. *Observed
here.*

Two payments. One captured UPI payment of 69900 paise. One failed UPI payment of
178800 paise, carrying `error_code` `BAD_REQUEST_ERROR`, `error_reason`
`payment_timed_out`, `error_source` `customer`, and `error_step`
`payment_authentication`.

Aggregate statistics from a live merchant account owned by the author. Raw
payloads are not published, because they carry customer information. The amounts
and the date range above may be stated and nothing else from that account
appears anywhere public.

**What one failed payment establishes.** Three things, and they are worth more
than their sample size suggests because each is a structural claim rather than a
rate:

1. A documented live-mode reason string does appear in production.
   `payment_timed_out` is on both the card and the UPI list this project
   adopted, and until this probe no reason from those lists had ever been seen
   outside the documentation.
2. The documented `error.source` enumeration does appear in production.
   `customer` is one of the nine values.
3. The coarse-code-plus-specific-reason structure holds outside test mode. The
   `error_code` is still `BAD_REQUEST_ERROR`, exactly as in test mode, and all
   the signal is in `error_reason`. That is the shape `internal/classify` is
   built around, and it is now observed rather than assumed.

`TestClassifierHandlesTheProductionFailureShape` is built from exactly that
shape.

**What it does not establish.** Anything about a distribution. Two payments are
a specimen. No share, rate, or mix anywhere in this repository is derived from
that account, the `observed-live-mix` batch profile ships with no data in it,
and `DECISIONS.md` for this phase records the decision not to seed one from a
two-payment account.

It is also the one line in this project that is a real recovery story rather
than a modelled one. The author's own live account lost a 178800 paise UPI
payment to an authentication timeout, and nothing ever re-attempted it or asked
the customer to try again. That is the exact failure class the payment-link
nudge in this system targets.

## 8. What cannot be made real without production data

The honest half. Every item here is something phase 5 could not fix by reading a
document, and each one is a limit on what any number in `/RESULTS.md` means.

**1. Whether the documented reasons are ever returned at volume.** Razorpay test
mode collapses every one of the eight documented magic cards to `payment_failed`
with `BAD_REQUEST_ERROR`, observed 2026-08-31 across all eight. The production
probe of 2026-09-01 softens this from "never observed" to "observed once, in an
account holding two payments", which is a real change and is not a distribution.
Nobody can say from this repository how often a merchant sees
`insufficient_funds` rather than something that names no cause.

**2. Whose failure mix this is.** The `ethoca-card-mix-2019` profile is card
declines, published by a fraud-prevention vendor, describing 2019, across
whatever merchant population their data covers. It is not Indian, it is not
UPI-inclusive, it is not this decade, and it is not any particular merchant's.
It is the best citable mix available and it is somebody else's.

**3. Recovery causality in test mode.** A test-mode payment attempt is settled
at the last checkout call by one form field carrying `S` or `F`, and the card
never reaches it. A live-layer recovery rate is therefore a rate for outcomes
this project selected. No phase can make it evidence that a recovery decision
caused a recovery, because test mode has no mechanism that settles differently
based on the decision.

**4. Retry-timing uplift, which is excluded on purpose.** Every published figure
for "retrying at hour N recovers X percent more" is vendor marketing with no
methodology behind it. This project implements no timing strategy, publishes no
timing result, and does not cite anyone else's. The `R2-COOLDOWN` interval is a
configured choice and says so.

**5. Mastercard's reattempt thresholds.** Contested across secondary sources, as
section 2 records. Nothing here depends on them.

**6. The UPI reason table is classified and never exercised.** The fake gateway
stamps every payment it seeds as a card payment, so no run in `results/` has
driven a UPI reason through the classifier. The eight UPI mappings are covered
by unit tests and by nothing else.

**7. The network decline code lists are the same.** No Razorpay payload this
project has observed carries a raw network response code, so
`classify.ClassifyNetworkDeclineCode` is reached by its unit test and by no run.

## Sources

| Source | Kind | Read |
|---|---|---|
| Visa bulletin AI10325, `usa.visa.com` | network rule | 2026-09-01 |
| Mastercard merchant advice codes | network rule | 2026-09-01 |
| RBI E-mandate Framework | regulator | 2026-09-01 |
| NPCI circular OC-149 | regulator | 2026-09-01 |
| `razorpay.com/docs/errors/payments/cards/` | vendor documentation | 2026-09-01 |
| `razorpay.com/docs/errors/payments/upi/` | vendor documentation | 2026-09-01 |
| Razorpay payment error parameters page | vendor documentation | 2026-09-01 |
| `razorpay.com/pricing/`, `payu.in/pricing/` | vendor documentation | 2026-09-01 |
| Razorpay pricing explainer, chargeback fee floor | vendor documentation | 2026-09-01 |
| Recurly involuntary churn research | analyst estimate | 2026-09-01 |
| Datos Insights false decline research | analyst estimate | 2026-09-01 |
| Mastercard and Ethoca card-decline shares, `ethoca.com` | vendor claim | 2026-09-01 |
| Razorpay Optimizer press material | vendor claim | 2026-09-01 |
| Stripe and Adyen retry-model results | vendor claim | 2026-09-01 |
| The author's own live merchant account, aggregates only | observed here | 2026-09-01 |
