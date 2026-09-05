# India-first constraints audit

Written 2026-09-04, updated 2026-09-05. Audits this repository's architecture
against Indian payment regulation and rails: which components model something
real, which model a mechanism with no lawful or technical counterpart in
India, and which direction the project pivoted to as a result. Two inputs: a
component-level repo audit (file:line citations checked at HEAD 2026-09-04)
and a regulatory research sweep (2026-09-04). Regulatory facts below are
labeled REPORTED, not VERIFIED: RBI/NPCI primary PDFs resisted direct
retrieval, so sourcing is consistent secondary/legal-analysis material. A
REPORTED label is not upgraded by later use of the fact; each REPORTED line
still needs a primary-source check before it is defended to a third party.

---

## 1. Regulatory ground truth (all REPORTED, sources in section 6)

- RBI's Authentication Directions (issued 2025-09-25, compliance 2026-04-01)
  require two-factor authentication on effectively all one-off domestic
  digital payments. The only exemptions found: contactless card up to Rs 5,000
  at POS, offline payments up to Rs 500, and registered e-mandates (AFA once at
  setup). No merchant-initiated transaction or resubmission concept exists in
  India for one-off payments, on any rail.
- E-mandate framework (consolidated 2026-04-21): AFA-free debit limit Rs
  15,000 per transaction generally; Rs 1,00,000 only for insurance, mutual
  funds, and credit card bills. 24-hour pre-debit notification required
  (FASTag/RuPay NCMC carve-out).
- UPI Autopay: NPCI caps executions at 4 total attempts per cycle (1 original
  + 3 retries), restricted to non-peak windows (before 10:00, 13:00-17:00,
  after 21:30). Reported from approximately 2025-08.
- One-off UPI: every customer-initiated debit requires the UPI PIN. No
  server-side reattempt of a failed one-off UPI payment exists. P2P collect
  requests were discontinued entirely effective 2025-10-01; P2M collect
  remains with raised limits.
- Tokenization (since 2022-10-01): merchants cannot store PANs; a network
  token carries forward existing authorization only. No source describes a
  token as independently authorizing re-presentment of a failed one-off
  charge (inferred from absence, flagged as such).
- RBI TAT harmonisation circular (2019-09-20): failed transactions where
  money was debited must auto-reverse within prescribed TATs (UPI merchant
  payment T+5, card-not-present T+5, UPI fund transfer T+1), with Rs 100/day
  compensation for delay, credited without a customer complaint.
- Razorpay surface: Subscriptions API supports recurring/e-mandate debits and
  is documented as testable in test mode, including failed/retry/halted
  states (test card tokens valid only 3 days). Refund API test-mode
  completeness: plausible but unresolved on the page fetched.

## 2. Component verdicts

Verdicts: REAL-IN-INDIA / CONSENTED-RAIL-ONLY / FICTION.

| Component | Verdict | Basis |
|---|---|---|
| Batch generator + Ethoca 2017 mix | FICTION for India | Card-only by construction (`internal/batch/batch.go:260-265`); zero UPI failures modeled; NPCI TD/BD taxonomy exists only as prose in `docs/EVIDENCE.md:196-239`. Wrong rail for a UPI-majority market, and `HONEST-LIMITATIONS.md:330-340` already concedes it. |
| Classifier taxonomy | Vocabulary REAL, semantics FICTION | Reason strings are Razorpay's documented live-mode lists and a UPI table exists (`internal/classify/classify.go:114-132`) but is dead code in every run. The class names `TransientRetryEligible`/`RetryEligible` assert same-instrument re-presentment, which no Indian one-off rail permits. `IsRetryEligible` is the fictional bit. |
| R1 MAX-ATTEMPTS (Visa 15-in-30) | FICTION | The Visa cap bounds merchant-initiated card resubmission. No MIT exists for one-off Indian payments, so there is nothing for it to bound. |
| R2 COOLDOWN | REAL | Uncited rate control, rail-agnostic. |
| R3 AMOUNT-CEILING (Rs 15,000) | CONSENTED-RAIL-ONLY | India-binding, but only on mandates. Applied to one-off failures it discriminates nothing: every one-off amount needs AFA. Category error on the current rail, literally the right rule on the mandate rail. |
| R4 NEVER-RETRY | Split | Razorpay `payment_risk_check_failed` half is REAL; Visa Cat 1 / MC MAC 03 half is foreign-network, reconstructed, and reached by no run. |
| R5 ACTION-BUDGET | REAL | Rail-agnostic. |
| R6 NOTIFY-RATE | REAL, miscited | The actually binding Indian source (TRAI commercial-communication / DLT regime for SMS) is not cited. NEEDS-VERIFICATION. |
| R7 FAIL-CLOSED, R8 KILL-SWITCH, R9 IDEMPOTENCY | REAL | Rail-agnostic. |
| `retry_payment` action | FICTION, twice over | Mechanism does not exist in live mode (`internal/razorpay/attempt.go:137-157`) AND the action it simulates (unattended re-charge of a one-off instrument) is unlawful on every Indian rail. `razorpay.Port` has no token/mandate/subscription call to map it to. |
| `create_payment_link` + resend | REAL-IN-INDIA | Documented API, non-synthetic 200 fixtures. Delivery and payment remain unobservable in the current build. |
| Escalation | REAL concept, no sink | No queue or ticket; an in-process tally feeding precision/recall metrics. |
| Audit trail (OTel + ledger) | REAL, rail-agnostic | Nothing card-specific in event kinds or span attributes. Known trace-id gap stands. |
| Cost model (Rs 500 chargeback floor) | FICTION on both rails | A chargeback requires a successful debit to dispute; every order in these batches is a failed payment. UPI disputes run through NPCI URCS/ODR on a different fee basis (uncited). |

## 3. Metric survival under the India frame

- Dies: `recovery_rate`, `recovered_orders`, `recovered_amount_paise`,
  `fa2_over_attempt`, and the entire naive arm. Every fake-layer recovery
  number is a rate for outcomes the harness chose.
- Survives: escalation precision/recall, `policy_evaluations`,
  `policy_refusals`, `notifications_sent`, `api_calls`, containment
  (`policy_violations_succeeded`).
- The live-layer rules-arm row (recovery 0.0, escalate all 8 under R7) was
  the honest India answer all along: classify nothing you cannot source, act
  on nothing you may not act on, escalate the rest.

## 4. E2E-honest direction candidates

**(a) Consented-rail dunning on Razorpay Subscriptions / e-mandates (test
mode).** The only direction where a retry-shaped action has a lawful
production counterpart. R3 becomes the right rule on the right rail; NPCI's
1+3 attempt cap and execution windows and the 24-hour pre-debit notification
become genuinely citable stopping rules, replacing the Visa cap. Carries
over: policy engine, audit trail, MCP gate, four-arm scaffold. Rebuild:
`attempt.go` in full (documented subscription charge calls), batch generator
seeds mandate-backed subscriptions. Blocker: NEEDS-VERIFICATION that test
mode permits mandate registration and merchant-initiated debit without a real
bank AFA step; test card tokens lasting 3 days may constrain multi-cycle
tests.

**(b) Refund / duplicate-payment ops agent.** Cleanest fully-closed E2E loop
in test mode. Refunds are merchant-unilateral: no customer, no AFA, no rail
asymmetry. Loop: seed captured payments including deliberate duplicates,
agent detects, policy gates, `POST /v1/payments/{id}/refund`, read refund
state back, score against manifest. The agent's decision causes the observed
state change, which nothing in the pre-pivot build achieves. R9 idempotency
becomes load-bearing (a double refund is real money), R3 becomes a real
above-this-a-human-approves control, and the RBI TAT circular (T+5
auto-reversal, Rs 100/day penalty) supplies a genuine regulatory frame and a
priceable cost of inaction. Rebuild: classifier taxonomy
(duplicate/overcharge/failed-capture), batch seeds captured payments.
Blocker: confirm Refund API completeness in test mode (unresolved, likely
fine).

**(c) Customer re-engagement ladder, human closes the loop.** Keeps the
India-correct frame: drop `retry_same_instrument`, collapse retry classes
into re-engage, metrics move to link conversion. A tester paying a small real
UPI amount on an agent-raised link, with the poller observing `plink` flip
created to paid, would be the first genuine recovery signal in the project.
Blocker: needs live keys and real rupees, contradicting the PRD non-goal of
no real money.

**Cross-cutting for all three:** retire or demote the
`ethoca-card-mix-2017` profile and promote the NPCI TD/BD taxonomy from prose
to code.

## 5. Probe addendum (live test-mode API calls with the repo's test key)

Direction (a), mandate retry sequencer / failed-subscription recovery, is
dead, four independent ways, probed 2026-09-05:

1. Subscriptions is not entitled on the test account: every `/v1/plans` and
   `/v1/subscriptions` call returns a bare 401 Unauthorized while
   `/v1/orders`, `/v1/payment_links`, `/v1/invoices`, `/v1/customers` return
   200 on the same key in the same minute.
2. Even entitled, no API exists to trigger or retry a subscription charge;
   the documented endpoint list has no charge or retry call. The only lever
   is the Dashboard "Charge this now" button (docs).
3. For halted subscriptions, exactly the state a recovery agent targets, test
   mode replaces that button with "Issue Invoice" and no charge is attempted
   on the saved card (docs).
4. Cycle compression is impossible: plan floor is effectively weekly, while
   test-mode card tokens allow a subsequent debit only within 3 days of token
   creation, so a second billing cycle cannot be demoed in test mode at all
   (docs).

Surviving surfaces, probed 2026-09-05:

- Invoices/receivables: full control loop closed live in test mode: create
  customer, draft invoice, issue (mints `order_id` + `short_url`), notify by
  email (`{"success":true}` and `email_status` observed moving `null` to
  `sent`), cancel, poll `status`/`amount_paid`/`amount_due`. `partial_payment`
  accepted. Verdict: E2E-closable except the paid transition, which needs a
  human paying in a browser with a documented success test card.
- Payment links: pollable `status` (created/partially_paid/paid/cancelled/
  expired), notify works, webhooks documented. Same human-browser step for
  completion. Account auto-reminder scheduler returned `{"status":"failed"}`
  (probed): do not rely on Razorpay-side reminders; the agent owns cadence.
- Regression: the repo's undocumented headless checkout path (`POST
  /v1/payments/create/ajax`) now returns 403 Forbidden (nginx) as of
  2026-09-05; it worked 2026-08-31. Cause unresolved (WAF vs removal);
  deliberately not probed further to avoid evading a block. The old build's
  live-layer mechanism is no longer reproducible; demo payment completion
  must go through hosted checkout in a real browser.

**Update, 2026-09-05, verified in a browser by the project's author:** the
human-browser step named above as the last unknown is closed. Test-mode
hosted checkout on a probe payment link (`plink_TYEx2CoiwQvYow`) was
completed end to end in a real browser using the documented success test
card 4100 2800 0000 1007, and the link's status was observed moving to paid.
This is the first confirmed instance in this project of a payment actually
completing against Razorpay test mode. Consequence: a human clicks the
payment link on screen, pays with the documented test card, and the agent's
poller reads the state flip from created (or partially_paid) to paid as a
real observed transition, not a simulated one.

Consequence for direction: the E2E-honest direction is the receivables
chaser / promise-to-pay tracker on the Invoices API, with payment links as
the re-engagement arm.

## 6. Sources (regulatory sweep, 2026-09-04, all REPORTED)

- KPMG / IBM / Business Standard summaries of RBI Authentication Mechanisms
  for Digital Payment Transactions Directions, 2025.
- Taxguru / LexOrbis summaries of RBI Digital Payments E-Mandate Framework,
  2026 (RBI/DPSS/2026-27/396).
- Business Standard and Kiwi commentary on NPCI UPI Autopay attempt caps and
  execution windows.
- Medianama on P2P collect discontinuation (2025-10-01); News on Air on P2M
  limits.
- Chargebee / Inai on RBI tokenization (2022-10-01).
- Razorpay docs: Test Subscriptions page, Refunds API overview.
- Legality Simplified summary of RBI TAT harmonisation circular
  (RBI/2019-20/67).
