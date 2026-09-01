# Evidence

Is the problem this repository attacks a real problem, and are the constants it
runs on real constants? This is the document that answers both, with a source on
every number and a label on every source.

Written 2026-09-01, at the start of phase 5, and corrected the same day after a
second pass read the primary documents first-hand rather than through
summaries. It replaces nothing: until this file existed, the honest answer to
"where did the retry cap come from" was that the author picked it.

**Four claims in the first draft of this file were wrong**, and each correction
is noted where it applies. The first draft cited a Visa bulletin for a code list
the bulletin does not contain, dated a decline-mix article two years late,
carried a companion claim about response codes that is not in its source, and
described a merchant survey as a measurement. That is the failure mode this
document exists to prevent, arriving inside the document itself, which is why
the corrections are marked rather than quietly applied.

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

**Involuntary churn is roughly a third of total subscription churn.**
*Vendor-published benchmark, Recurly, July 2026 churn benchmark table.*
Involuntary churn is a subscription ending because a payment failed rather than
because a customer left. It is the category this project is inside: the customer
wanted to pay and the charge did not go through.

*Corrected 2026-09-01.* The 33 to 38 percent band is **computed** from Recurly's
benchmark table rather than stated by Recurly as a headline. Their table gives
involuntary and total monthly churn per vertical: SaaS 1.06 against 3.22,
Education 1.69 against 4.99, Digital Media 1.59 against 4.14. Dividing gives the
band. Their recovery-rate figures are on a separate blog post and are not the
same source. A number this document derives is labelled derived.

**False declines are estimated to cost merchants several times what fraud
does.** *Merchant survey, Datos Insights, May 2024.*

*Corrected 2026-09-01.* The first draft of this file called this a measurement
and gave a 2024 dollar figure. It is a survey: 200 e-commerce merchants in the
United States and the United Kingdom, interviewed between 2023-07 and 2023-09,
reported at plus or minus 7 points at 95 percent confidence. The figures that
survive that check are an average false-decline rate of 1.51 percent of
e-commerce sales, lost revenue approaching 265 billion US dollars by 2027, and
net e-commerce fraud losses of 43 billion US dollars by 2027. The "roughly six
times" comparison is between those two 2027 estimates, not between two measured
years. A false decline is a legitimate payment the issuer refused, and it is the
failure mode with the largest gap between what the merchant lost and what the
merchant can see.

**NPCI publishes decline targets for UPI, which is what a target implies.**
*Regulator, NPCI circular OC-149.* It sets technical decline targets below 1
percent and business decline targets below 5 percent for participants. A body
that has to set a target for a decline rate is a body whose members do not
automatically meet it.

NPCI also publishes per-member decline and uptime statistics at
`npci.org.in/statistics/bd-td-and-uptime`. That page returns 403 to every
non-browser fetch, so it is cited here as published by NPCI with figures via
secondary coverage, and no figure from it is used anywhere in this repository.

NPCI additionally maintains a technical-decline and business-decline taxonomy
for every UPI response code, which is the closest thing to external support any
part of this project's design has. It belongs to the classifier rather than to
the market case, so it is in section 3.

**There is almost no academic literature on this.** *Searched 2026-09-01.* No
direct arXiv or ACM paper on card-payment retry optimization was found. The
nearest artifacts are adjacent arXiv work on causal label recovery in payment
networks and a United States patent on machine-learned retry identification.
This is industrial practice with a thin academic literature behind it, which is
worth knowing before treating any vendor's published uplift as a settled
result.

## 2. The rules the retry policy sits under

**Visa Category 1 declines may never be reattempted. Category 2 declines may be
reattempted at most 15 times in 30 days.** *Network rule, Visa bulletin AI10325,
"Updates to Rules for Declined Transaction Resubmission and Use of Authorization
Response Codes", dated 2020-09-03, effective 2021-04-17.* Read as a PDF on
2026-09-01.

*Corrected 2026-09-01, twice over.* The first draft of this file said the cap
covers Categories 2 and 3, which came from a summary. And it cited the bulletin
for the Category 1 code list, which the bulletin does not contain.

What the bulletin does establish, first-hand: the four-category system;
Category 1 as a decline the issuer will never approve and which a merchant is
not permitted to reattempt; the Category 2 cap of 15 in 30 days; the move of
response codes `03`, `62`, `78`, and `93` out of Category 1 into Category 2
effective 2021-04-17; and that code `14` sits in both Category 1 and Category 3
and must never be reattempted with the same account number.

**The specific Category 1 code list is a reconstruction.** The twelve codes in
`internal/networkcodes` come from processor reconstructions of Visa's
member-gated table, of the kind Qualpay and other PSPs publish.
`VisaCategory1IsReconstructed` is a constant set to true, a test holds it, and
the source string in that package says the same thing. A reconstructed list is
usable and it is not primary, and the difference has to be visible in the code
rather than only here.

The four moved codes are worth stating separately because they are commonly got
wrong: payments blog posts written before 2021-04-17 still name all four as
never-retry codes. `internal/networkcodes` carries them under
`WasVisaCategory1BeforeApril2021` so a reader arriving with one of those posts
finds the correction rather than an unexplained gap.

**Mastercard merchant advice code `03` means do not try again, and a
resubmission after it is charged for.** *Network rule, verified through TabaPay's
PSP documentation at `developers.tabapay.com/docs/merchant-advice-code-mac`,
read 2026-09-01.* A fee is assessed for each authorization request resubmitted
following a MAC 03 decline within a 30 day period. That is the mirror of the
Visa side: Visa caps how many times a merchant may reattempt, Mastercard charges
for reattempting what it said not to.

**Merchant advice codes 24 through 30 are Mastercard's own retry ladder: 1 hour,
24 hours, 2 days, 4 days, 6 days, 8 days, 10 days.** *Same source.* These are
Mastercard-use-only codes, so a merchant does not pick a rung. The ladder is in
`networkcodes.MastercardRetryScheduleHours` because it is the only scheme-native
retry timing anybody publishes, and it is what `R2-COOLDOWN` is measured against
rather than derived from: that interval is 30 seconds, two orders of magnitude
below the shortest rung.

The source is a PSP restating a scheme rule and not the scheme's own
publication, which is what the source string in that package says.

**The Mastercard reattempt thresholds are contested and are not used here.**
Several secondary sources quote a specific per-merchant reattempt limit per
declined transaction, and they do not agree with each other on the number or on
the window. This project uses only the two Mastercard facts that are not in
dispute, MAC `03` and the one hour floor on the published schedule, and takes
its reattempt cap from the Visa bulletin, which is unambiguous.

**Rs 15,000 is the RBI e-mandate threshold above which an additional factor of
authentication is required.** *Regulator, RBI circular on processing of
e-mandates for recurring transactions, dated 2022-06-16, which raised the limit
from Rs 5,000 to Rs 15,000 with immediate effect.* It is
`policy.DefaultAmountCeilingPaise`, at 1500000 paise, and it is the line
`R3-AMOUNT-CEILING` was approximating with an invented number until 2026-09-01.

Two circulars are commonly confused here and only one is the right citation. The
2020-12-04 circular is the older Rs 5,000 limit and is not what this constant
comes from. The Rs 1 lakh limit introduced in 2023-12 applies only to specified
categories, being mutual funds, insurance premiums, and credit-card bill
payments, and does not apply to a general merchant charge.

**The RBI e-mandate 24 hour pre-debit notice is a notice floor and not a retry
rate.** *Regulator.* It is in this document because it is the closest thing to a
citable interval in Indian payments, and it does not answer the question
`R2-COOLDOWN` asks. Neither does the Mastercard ladder, which is about
reattempting an authorization rather than about contacting anyone, and nothing
at all answers the question `R6-NOTIFY-RATE` asks. Both stay configured choices.

## 3. The failure vocabulary

**Razorpay documents 15 live-mode failure reasons for cards and 8 for UPI, and
the two lists are not the same list.** *Vendor documentation.*
`razorpay.com/docs/errors/payments/cards/` and
`razorpay.com/docs/errors/payments/upi/`, read 2026-09-01. Both are in
`testdata/error_codes.json` with a label and a source on every row, and in
`internal/classify` as two tables.

**`error.source` has a documented enumeration and it is per method. `error.step`
has none at all.** *Vendor documentation,* the payment error parameters page.
Five values for cards and eight for UPI, the card list being a subset of the UPI
one. The first is a type in `internal/classify` with a per-method predicate; the
second stays a string, and there is a test on the absence so a later phase does
not invent one.

*Corrected 2026-09-01.* The first draft of this file said nine values in one
flat list, which is where `issuer` came from. It is on neither list.

**The class each reason maps to is this project's judgment and is cited
nowhere.** Razorpay publishes the reason strings. It does not publish a mapping
from a reason to "retry this" or "ask for a different card", and adopting a
documented vocabulary does not make the mapping documented. Every class in
`internal/classify` is an argument in a comment, not a citation.

**The shape of that judgment has scheme precedent. NPCI splits every UPI
failure into a technical decline or a business decline, and this project's
classifier splits failures the same way.** *Regulator, NPCI "UPI Error and
Response Codes" version `2.9`, public PDF, text extracted and read
2026-09-01.*

Every response code in that specification is labelled TD or BD. Rows verified
first-hand:

| Code | Meaning | NPCI class |
|---|---|---|
| `U67` | debit timeout | TD |
| `U68` | credit timeout | TD |
| `B6` | mismatch in payment details | TD |
| `HS` | bank HSM down | TD |
| `Z9` | insufficient funds | BD |
| `ZM` | invalid MPIN | BD |
| `U69` | collect expired | BD |
| `59` | suspected fraud, risk score decline | BD |

That is the same cut `internal/classify` makes: a technical decline is
infrastructure and the same attempt can clear it, while a business decline is
the customer, the balance, the instrument, or a risk engine, and repeating the
attempt repeats the answer. The classifier's `transient_retry_eligible` sits
where NPCI puts TD, and `reauth_required`, `new_instrument_required`, and
`never_retry` sit where it puts BD.

**This is the strongest external support any part of this project has**, and it
is worth being exact about what it supports. It supports the *shape* of the
taxonomy: that splitting failures into retry-worthy infrastructure classes and
do-not-blind-retry business classes is how the national payments operator itself
models the problem, rather than a convenience this project invented. It does not
supply the mapping. Which Razorpay reason belongs in which class is still this
project's judgment, which the paragraph above this one says in as many words.

`retry_eligible` is the one class that does not line up cleanly:
`insufficient_funds` is a business decline by NPCI's cut and this project treats
it as retry eligible, because a balance changes on its own and a technical
failure and an empty account are different reasons to try again. That
disagreement is a mapping choice, it is disclosed here, and it is exactly the
kind of thing the TD/BD split does not settle.

Adopting the U-code vocabulary into `internal/networkcodes` is roadmap and not
this phase. Nothing in any run reads a UPI response code.

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
9 percent.** *Vendor-published research, Mastercard and Ethoca, `ethoca.com`,
article dated 2017-04-28.* This is the `ethoca-card-mix-2017` profile in
`internal/batch`, and the vintage is in the profile name because a 2017 decline
mix is a 2017 decline mix.

*Corrected 2026-09-01, twice.* The article is dated 2017-04-28 and the first
draft of this file said 2019, so the profile was renamed. And a widely repeated
companion claim, that response codes `05` and `51` together are about 80 percent
of all declines, is **not in that article** and could not be verified in any
primary source. It is dropped rather than softened. A figure that cannot be
traced to a document does not get a hedge, it gets deleted.

Two honest notes on it. The three cited categories sum to 79 percent and the
source does not break out the remaining 21, which the profile carries as a
residual across the other classes and marks uncited. And 35 percent of that mix,
the lost, stolen, and fraud share, is orders no merchant should reattempt and no
merchant should message the cardholder about. In this repository's vocabulary
those are bait orders, so a citable card-decline mix makes over a third of the
batch unactionable. That share is the source's, not the author's, and it moves
every number in the table.

## 6. What vendors claim, labelled as vendor claims

**Razorpay Optimizer press material, 2023-10-10.** *Vendor claim.* Verified
verbatim on 2026-09-01: "Indian businesses lose around 30% revenue due to failed
transactions", "Nearly 33% of failed transactions are never re-attempted", and a
figure of "over 7,000 Cr". These are carried because they are the numbers the
Indian market quotes. Nobody outside the vendor has audited any of them, the 30
percent figure is stated against different denominators in different pieces of
their own material, and none of them is used as an input to anything here.

The second one is the interesting one for this project, if it is true: a third
of failed transactions never re-attempted is the gap a recovery agent fills.

**Stripe publishes recovery results from its own retry model.** *Vendor claim,
Stripe blog, 2024-01-23.* Verified quotes: the model uses "more than 500
attributes", Deliveroo "recovered more than L100 million" in one year, and the
best retries land "days into the future", with the window covering the
customer's next payment period. Adyen publishes comparable material.

Both are results from a vendor's own production system on its own traffic, with
no methodology, no baseline definition, and no independent replication. They are
evidence that large processors think the problem is worth solving with a model.
They are not a number this project can compare itself against, and section 8
says why nothing in this repository tries.

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
   `customer` is on both the card list and the UPI one.
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

**And NPCI's own taxonomy says what the right response to it is.** A UPI collect
request the customer did not act on is `U69`, collect expired, which the version
`2.9` specification classes as a **business** decline: the infrastructure worked
and the person did not answer. A business decline is not something to retry
against the same instrument, it is something to re-engage the customer about.
That is precisely the action this system takes for the class, a payment link
rather than a silent reattempt. The specification, one real production failure,
and the product's action mapping agree, and that is as close to end-to-end
external validation as anything here gets.

NPCI's own specification agrees on what that failure needs. An expired collect
request (`U69`) is classified there as a business decline: the customer did not
act, so the infrastructure has nothing to retry. The response a business
decline calls for is re-engaging the customer, which is the one action this
system takes for that class.

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

**2. Whose failure mix this is.** The `ethoca-card-mix-2017` profile is card
declines, published by a fraud-prevention vendor, describing 2017, across
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
methodology behind it, and section 1 records that there is no academic
literature to fall back on either. This project implements no timing strategy, publishes no
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
| Visa bulletin AI10325, `usa.visa.com`, dated 2020-09-03 | network rule, read as a PDF | 2026-09-01 |
| Processor reconstructions of Visa's Category 1 table | reconstruction, not primary | 2026-09-01 |
| Mastercard merchant advice codes | network rule | 2026-09-01 |
| RBI circular on e-mandates for recurring transactions, 2022-06-16 | regulator | 2026-09-01 |
| NPCI circular OC-149 | regulator | 2026-09-01 |
| NPCI "UPI Error and Response Codes" version `2.9`, public PDF | regulator, text extracted and read | 2026-09-01 |
| NPCI decline and uptime statistics, `npci.org.in/statistics/bd-td-and-uptime` | regulator, page 403s to non-browser fetches, figures via secondary coverage and unused here | 2026-09-01 |
| TabaPay PSP documentation on merchant advice codes | vendor documentation restating a scheme rule | 2026-09-01 |
| `razorpay.com/docs/errors/payments/cards/` | vendor documentation | 2026-09-01 |
| `razorpay.com/docs/errors/payments/upi/` | vendor documentation | 2026-09-01 |
| Razorpay payment error parameters page | vendor documentation | 2026-09-01 |
| `razorpay.com/pricing/`, `payu.in/pricing/` | vendor documentation | 2026-09-01 |
| Razorpay pricing explainer, chargeback fee floor | vendor documentation | 2026-09-01 |
| Recurly July 2026 churn benchmark table | vendor benchmark, band computed here | 2026-09-01 |
| Datos Insights false decline report, May 2024 | merchant survey, 200 respondents | 2026-09-01 |
| Mastercard and Ethoca card-decline shares, `ethoca.com`, 2017-04-28 | vendor claim | 2026-09-01 |
| Razorpay Optimizer press release, 2023-10-10 | vendor claim | 2026-09-01 |
| Stripe Smart Retries blog, 2024-01-23, and Adyen equivalents | vendor claim | 2026-09-01 |
| The author's own live merchant account, aggregates only | observed here | 2026-09-01 |
