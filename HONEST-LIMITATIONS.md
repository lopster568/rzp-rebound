# Honest limitations

Every limit the phase documents record, in one place, so a reader does not have
to walk four phase directories to find them. Nothing here is new. What is new
is that it is collected.

Written 2026-09-01, at the end of phase 4, and rewritten 2026-09-01 at the end
of phase 5, when the batch mix and the cost model stopped being invented and
four of the items below changed with them. The tables it qualifies are in
`/RESULTS.md`.

## What the pivot to the risk engine could not make real

Added 2026-09-05, and moved to the top of this file on 2026-09-05 so a reader
meets the current system's limits first. Everything under the headings below
this section is about the retry engine and stays exactly as it was written;
nothing in it has been softened because the system changed underneath it.
`docs/INDIA-CONSTRAINTS-AUDIT.md` is why the system changed, and `/RESULTS.md`
says which of the tables below still describe anything that exists.

The items below are the new engine's limits, and there are more of them per
feature than the old build had, because the new build closes a loop the old one
could not and every closed loop has a joint that is held together by hand.

**39. Aging is simulated from the manifest, and no Razorpay call can backdate an
invoice.** The demo is a book of aged receivables, and nothing in the invoice or
order creation API lets a caller set `issued_at` or `created_at` to a past
instant. Every item `cmd/seedbook` creates is, to Razorpay, minutes old. A gate
reading Razorpay's own `issued_at` therefore denies the whole seeded book under
`R11-NOT-YET-DUE`, which is a correct answer to the wrong question.

So the seeder writes the age it meant the item to have into the manifest, as
`age_bucket` and `simulated_at_risk_since`, and `risk-run` measures the grace
period against that instant instead. It is labelled in three places, deliberately,
so that no reading of a run can mistake a stated age for a real one: `age
manifest_simulated` in the run header on stdout, `"age_source":
"manifest_simulated"` on the run summary, and `age_source` on every single result
row and every `policy_evaluated` ledger row. An item the manifest does not know
keeps the gateway's clock and says so per row, which is why the field is per item
and not only per run.

What this costs: `R11`, `R1`, and `R2` are exercised against a timeline this
project stated. They are real rules doing real arithmetic on a number nobody
observed. The one thing that would fix it is an account with a genuine aged
ledger in it, and a test-mode account cannot have one.

**40. Any paid transition is n=1, paid by the person running the demo, and
selected rather than sampled.** The first confirmed payment against Razorpay test
mode in this project's history was completed in a browser by the author on
2026-09-05, on a probe payment link, with the documented success test card, and
the link's status was observed moving to paid. That is a real observed state
transition and it is the reason the receivables direction was chosen over the
three the audit rejected.

It is also one payment, on one link, chosen because it was the link on screen. It
is not a sample of anything. Nobody decided independently to pay; the demoer
decided to demonstrate paying. A recovered-paise figure from a `risk-poll` delta
is arithmetic on that, and `riskrun.Diff` says so in its own doc comment: a
customer who paid for their own reasons moves the same number. The control arm
exists so the question can be asked of two groups rather than of one, and at demo
scale it is a group of a handful against a group of a handful, which bounds
nothing. Do not quote a recovery rate off this system.

**41. A notification is an accepted API call and it is never a delivery.** The
strongest thing this system can observe about a message is `email_status:sent`,
read back off the invoice after the notify call, and that is Razorpay reporting
that it sent something rather than a person having read anything. Where the read
is not available the observable falls back to `notify_api:accepted`, which is
weaker still: it says the call returned success.

`{"success":true}` on a notify call means the API accepted the request. It is
worth restating what that is worth, because the evidence is unusually blunt: a
payment link created with `notify.sms` false and no contact on it at all still
answered `200` with `{"success":true}` in test mode. `notify.Receipt.DeliveryConfirmed`
is a false constant on every path, the audit phrase is that the notification API
call succeeded, and `scripts/check-docs.sh` fails the build on any wording that
claims a person was reached. Nothing anywhere counts a message as a recovery.

**42. A dispute exists only as a manifest flag.** `R13-DISPUTED-NEVER-CHASE` is a
real rule with a real refusal, and nothing in this system detects a real dispute.
Razorpay has no field for a contested debt on an invoice, an order, or a payment,
so there is nothing for a detector to read. The flag is set by
`cmd/seedbook` when it seeds the item, travels in the manifest as
`flags.disputed`, and is read back by `riskrun`'s facts provider and handed to
the gate.

That is enough to prove the rule fires and to show the refusal in a demo, and it
is not evidence that the rule would ever fire in production. Wiring it to
something real means a dispute source outside Razorpay, which this project does
not have. The same applies to the source status the rule sits next to: a
cancelled or expired resource is read from the manifest in a risk run, not
detected.

**43. Failed payments cannot be seeded through the API, and the class needs a
human to exist.** Two of the three detectors have data because `cmd/seedbook`
created it. The third does not. Test-mode checkout is browser only, and the
undocumented headless attempt path this repository used through 2026-08-31
(`POST /v1/payments/create/ajax`) returns `403` as of 2026-09-05, cause
unresolved, deliberately not probed further to avoid evading a block.

So the seeder prints an operator to-do list instead: which links to open, which
documented failure cards to use, any future expiry, any CVV. A person does it by
hand and the failed-payment detector reads the account's history on its next
sweep. The consequence for a demo is that the failed-payment class is as large as
whoever ran the seeder had patience for, and the consequence for the committed
fixture is that `internal/riskrun/testdata/manifest.json` contains no
failed-payment items at all: the dry run over it exercises two detectors of the
three, and the third is covered by unit tests and by a live account somebody
clicked in.

**44. The idempotency guard is process-local and nothing evicts it.**
`internal/store` holds the committed keys and `internal/intervene` holds a second
guard, both in memory, both for the life of one process. A second run over the
same manifest starts with an empty ledger and will contact an item it already
contacted, so `R1-MAX-TOUCHES` and `R2-COOLDOWN` bound one run and not a
campaign. `R9-IDEMPOTENCY` catches a replay inside a run and nothing across runs.

Slots are never evicted either. A sweep holds one entry per item and action,
which is bounded by the batch, and a process running for a long time over many
batches would want an eviction policy and there is none. Both facts are in the
guard's own doc comment rather than only here. FR-STORE-2 is still the open item
it was before the pivot: there is no durable store.

**45. `R13` cannot fire in the MCP process at all, so the model arm is blind to
it.** This is the item above in the one place it bites hardest. `cmd/rzp-mcp`'s facts
provider fills two of the three facts a risk item does not carry: a promise the
agent logged this run is read back out of the same ledger the intervention engine
wrote it to, and the source status is the one that invocation read at startup.
The third is not filled. Nothing in that process records a dispute, so `Disputed`
stays false and `R13` cannot fire through it.

A `risk-run` driven off a seedbook manifest can fire it, because that path reads
the flag off the manifest. The model arm cannot. The gap is stated in the
provider's own doc comment rather than left to be discovered from an eval that
never trips the rule, which is exactly the failure mode phase 5 generalised: the
answer was on a page nobody had read.

**46. The TDD red-run ritual was not followed for the pivot packages.**
`internal/riskitem`, `internal/detect`, `internal/intervene`, `internal/riskrun`,
`internal/seed`, and the risk half of `internal/mcpserver` were built and tested
on the same day, and the tests were not watched failing first against absent
implementations. They are real tests over real behaviour and the suite is green,
and that is a different claim from the one this repository's process documents
make for the pre-pivot packages.

What the ritual buys and this code did not get: a test that has never been red is
a test that has not been shown to be capable of failing, and a green suite is
consistent both with code that works and with an assertion that cannot fire. It
is flagged rather than backfilled. Re-running the tests against a stubbed
implementation after the fact would produce a red run and prove nothing about the
order the code was written in, which is a ceremony rather than a fix.

**47. The cadence numbers items 21, 33, and 34 quote are the old engine's.**
Those three items are dated 2026-09-01 and they describe the retry engine. They
are kept because their findings survived the pivot, and they are wrong about
every number and two of the three rule ids they name. The current values, read
from `internal/policy/sources.go` and `internal/policy/policy.go` on 2026-09-05:

- `R2-COOLDOWN`, the minimum interval between two contacts about one debt, is 24
  hours for a failed payment, 24 hours for an unpaid order, and 48 hours for an
  overdue invoice. The 30 second constant item 34 quotes is gone. It was a retry
  rate, it bounded how fast a card was re-presented, and half a minute between
  two messages to one customer about one debt is harassment.
- `R6-NOTIFY-RATE` is one second, and it is not a per-customer rule at all. It is
  a run-wide send rate: the minimum interval between any two notifications this
  run sends, to anyone, so a sweep that just found two hundred overdue invoices
  does not emit two hundred sends in a burst. `cmd/rzp risk-run` takes
  `-notify-window` to move it.
- `R1` is `R1-MAX-TOUCHES`, not `R1-MAX-ATTEMPTS`. It is a lifetime cap on
  outbound contacts about one debt: 3 for a failed payment, 3 for an unpaid
  order, 4 for an overdue invoice, because an issued invoice is a debt the
  customer has already acknowledged.
- `R4` is `R4-NEVER-CONTACT`, not `R4-NEVER-RETRY-CLASS`. It refuses to chase a
  customer whose payment the gateway's risk check blocked, rather than refusing
  to re-present a card.

Every one of these is a configured choice and none of them is cited.
`policy.ConfiguredChoices` says so per rule and the block in `sources.go` says so
per number, which is the same status item 34 recorded for the values it
replaced. That is the part of item 34 that did not change.

## What Razorpay test mode does not give you

**1. Test mode collapses every failure to one reason.** All eight documented
magic cards in `testdata/magic_cards.json` were driven through the checkout
sequence on 2026-08-31, one order each. Every one came back with `error_reason`
`payment_failed`, `error_code` `BAD_REQUEST_ERROR`, `error_source` `gateway`,
and `error_step` `payment_authorization`, with no variation. Not one documented
reason string came back, so zero cards are marked `"verified": true` and each
row records what came back instead.

`payment_failed` names no cause a policy can act on, so `classify.Classify`
returns `unclassified`, so `R7-UNKNOWN-FAIL-CLOSED` fires, so the rules arm
escalates every live order and takes no action. That is the correct output of an
honest measurement of a gateway that does not distinguish its failures, and it
is not tuned away. The documented reason tables in `internal/classify` are
exercised by the fake gateway and by nothing live.

**Phase 5 corrected one thing about this and softened another.**

The correction: `payment_failed` is not an undocumented string. Razorpay
documents it on the live-mode card error page as the bank declining without
giving a specific reason, with a suggested action of contacting the bank or
trying a different card. Phase 1 recorded it as a mystery and it is a documented
generic decline. It still classifies as `unclassified`, and the documented
suggested action is the reason rather than an argument against it: "try a
different card" rules out a same-instrument retry, which is what the fail-closed
default already delivers, and promoting it to `new_instrument_required` would
mean sending a payment link to every customer whose payment a bank declined
without saying why. Phase 5 `DECISIONS.md` entry 11.

The softening: a read-only probe of the author's own live Razorpay merchant
account on 2026-09-01, covering 2026-07-15 to 2026-08-31, returned two payments.
One had failed, carrying `error_reason` `payment_timed_out`, `error_source`
`customer`, `error_step` `payment_authentication`, and `error_code`
`BAD_REQUEST_ERROR`. So a documented live-mode reason string does appear in
production, the documented `error.source` enumeration does appear in production,
and the coarse-code-plus-specific-reason structure holds outside test mode. This
item moves from "never observed" to "observed once, in an account holding two
payments". Two payments are a specimen and not a distribution, and no share or
rate anywhere in this repository comes from that account.
`docs/EVIDENCE.md` section 7 has the full statement of what it does and does not
establish.

None of this establishes that the documented codes come back from test mode.
They may be produced by the hosted Checkout widget, which simulates the decline
in its own front end. This project has not driven that widget and says nothing
about it.

**2. A live recovery rate is a rate for outcomes this project selected.** The
outcome of a test-mode payment attempt is chosen at the last checkout call by
one form field carrying `S` or `F`, and the card never reaches it. The
materialiser sends `S` for the orders the manifest says a retry can recover and
`F` for the rest, which is the gateway standing in for the world.

So the live layer is evidence that the loop runs end to end against the real
API, that the wire shapes are right, and that the state read back is what it
says. It is not evidence that a recovery decision caused a recovery, and no
phase can make it one, because test mode has no mechanism that would settle
differently based on the decision. The naive arm's live recovery rate of 0.667
is that number and nothing more.

**3. Notification delivery is unobservable.** The only thing this system sees
is Razorpay's HTTP response to the resend call. A payment link created with
`notify.sms` false and no contact on it at all still answered `200` with
`{"success":true}`. `notify.Receipt.DeliveryConfirmed` is a false constant on
every path, the audit phrase is that the notification API call succeeded, and
`scripts/check-docs.sh` fails the build on any wording that claims a person was
reached. Nothing here counts a message as a recovery.

**4. No Razorpay rate limit has been measured.** A deliberate probe on
2026-08-31 made 40 calls in 30.009 seconds, a rate of 1.3 calls per second, and
a counting transport underneath the retry loop saw zero 429 responses. Roughly
100 further calls that day produced none either. That rules out a limit low
enough to matter at this pace and is not a measurement of the limit. PRD Q5
stays open, and the four backoff and concurrency constants in
`internal/razorpay/client.go` are a starting point rather than a setting
derived from data.

**5. The risk-block error code is documented and has still never been
observed.** It is `payment_risk_check_failed`, on Razorpay's live-mode card
error page, read 2026-09-01. `testcards.PendingRiskBlockCode` is retired and
`internal/classify` carries the documented string. PRD Q2 is closed.

The limit that remains is that nothing this project has ever run produced a risk
block, so the reason is documented and not observed, and the never-retry class
it maps to is exercised by seeded fake-gateway orders and by no real response.
It is also worth noting how the question closed: the trigger written for it in
phase 0 looked for the answer in a fixture capture, and the answer was on a
documentation page nobody had read. That is the failure phase 5 generalises.

**6. UPI could not be driven server side.** `POST /v1/payments/create/upi`
answered `400` under Basic auth and `401` with the key id in the body or the
query string. The two `upi_vpas` rows in the card table stay unverified.

## What the numbers are, as measured

**7. No arm can reach a recovery rate of 1.000 on any of these batches.** Every
non-bait order carries `ground_truth_recoverable: true`, which is the recovery
rate's denominator, and only the retry-class orders can actually reach `paid` in
a run. The correct action for the rest is to raise a payment link, this project
observes an API call and never a person, and nothing here models a customer
coming back.

On `phase-5-fake-ethoca` the ceiling is 0.769 and the naive arm reaches exactly
it. On `phase-5-fake-uniform` it is 0.514 and the naive arm reaches exactly that
one. The denominator was not narrowed to flatter either rate; the ceiling is
stated instead.

**8. Classification accuracy carries no information on the fake layer.** The
fake seeds the reason and the classifier reads it, so the number is 1.000 for
every arm. The one that carries information is the live 0.000, and its cause is
limitation 1.

**9. Three of the nine policy rules fired in a run, and none of the three
middleware rules did.** Counted from the committed phase 5 ledgers:

| Run and arm | `policy_evaluated` rows by rule |
|---|---|
| `phase-5-fake-ethoca`, `a3-rules` | `R0-DEFAULT-ALLOW` 22, `R3-AMOUNT-CEILING` 4, `R4-NEVER-RETRY-CLASS` 14 |
| `phase-5-fake-ethoca`, `a2-agent` | `R0-DEFAULT-ALLOW` 28, `R3-AMOUNT-CEILING` 8, `R4-NEVER-RETRY-CLASS` 14 |
| `phase-5-fake-uniform`, `a3-rules` | `R0-DEFAULT-ALLOW` 31, `R3-AMOUNT-CEILING` 7, `R4-NEVER-RETRY-CLASS` 2 |
| `phase-5-live`, `a3-rules` | `R7-UNKNOWN-FAIL-CLOSED` 8 |

So R3, R4, and R7 fired, R0 is the id on an allow rather than a rule, and R1,
R2, R5, R6, R8, and R9 never fired in any run. One cycle per order rules them
out: the timestamps R2 and R6 read are zero on a first action, no idempotency
key repeats, the budget is far above the order count, the kill switch is unset,
and nothing arrives with enough attempts to reach R1's cap of 3. Every
`tool_call` row carries an allow, so the agent tripped none of
`M1-TOOL-ALLOWLIST`, `M2-ORDER-ALLOWLIST`, or `M3-DECISION-REQUIRED`.

The R4 count is the visible effect of the batch mix change: it goes from 2 on
the invented mix to 14 on the published one, because a real card-decline mix is
a third lost, stolen, and fraud.

Those six rules and those three are covered by per-rule unit tables and by a
576-cell golden matrix, and `R8-KILL-SWITCH` was additionally driven end to end
by pointing `--kill-switch-file` at an existing path, which took the rules arm
to zero actions on all 40 orders. An agent that never names an order it was not
given is a good result and it is not evidence that the allowlist works, which
is what the test is for.

**10. `policy_violations_attempted` is 0 for all four arms, by construction.**
It counts an action that reached a side effect while carrying a refusal, and in
a system where the refusal comes first that is zero however hard the agent
pushes. Phase 3 did not redefine it to make it move. The column that moved is
`policy_refusals`, which for `a2-agent` is refusals of what a model asked for.

**11. The modelled cost is a model, and since phase 5 its inputs are cited
rather than invented.** It was 200 paise per payment attempt and 5000 paise per
forbidden action, both chosen by the author so the two kinds of false action sat
on one scale. Both were wrong rather than merely unsourced.

It is now 0 paise for a failed payment attempt, because India bills successful
transactions only; 50000 paise per forbidden action, the Rs 500 chargeback fee
floor, which is ten times the figure it replaces; and 20 paise per notification,
the top of the transactional SMS band, which the old model did not charge at
all. `docs/EVIDENCE.md` section 4 has every source and
`harness/aggregate.py` carries them in its own comments.

What is still a limit: it remains arithmetic on published rates applied to
counts this project measured, not a figure any processor billed anyone. The one
thing an over-attempt actually costs, the customer's patience and the issuer's
opinion of the merchant, has no published price and is not in the model at all,
so FA-2 now contributes zero to a cost column and that understates it. Do not
quote the total as a figure Razorpay would recognise.

**12. The amount ceiling moved twice, and only the second move had an argument
behind it.** It was 400000 paise, then 450000 after a fake-layer run showed
400000 escalating a quarter of the batch on amount alone and swamping every
escalation number with orders whose ground truth said retry. That is a threshold
tuned to a result, however honestly the move was disclosed at the time.

It is now 1500000 paise, the Rs 15,000 threshold above which the RBI e-mandate
framework requires an additional factor of authentication. That is a real Indian
payments line between an amount that may be taken unattended and one that needs
a person, which is the question R3 asks. The batch amount range moved from
50000..500000 to 50000..1700000 paise so the rule can still fire, and that range
is a configured choice: it is set so the threshold sits in roughly the top
eighth of the distribution rather than the middle, which is the phase 2 finding
applied deliberately instead of discovered.

Both moves are in the constant's own doc comment with the numbers it was
before.

**13. One run per layer, and the agent arm is not deterministic.** The other
three arms reproduce from a seed. `a2-agent` does not, and it was sampled once
per order with no repeats, so there is no spread and a second run could land
somewhere else. Repeat runs are the honest fix and they cost another forty
headless invocations, so this is not fixed.

**14. The arms ran sequentially, not interleaved.** The seed shuffles order
position within an arm; it does not remove the between-arm time confound, and
`a1-naive` still runs before `a3-rules`. Full interleaving would need one
process sharing gateway state, an attempt store, and a policy budget across
arms, so one arm's behaviour would depend on what another had already spent.
That is a worse confound, so this trade is the one taken.

**15. The arm's class-to-action table agrees with the manifest's by
construction.** `recovery.ActionForClass` and `batch.CorrectActionFor` return
the same action for the same class. They are separate functions so a later
phase can change one without silently moving the score, but today an arm that
classifies correctly also picks the correct action, so the interesting error
mode is misclassification rather than action selection.

**16. Nothing is unscorable in the phase 5 runs, and the fix that got there is
worth naming.** The phase 3 live run had one unscorable `a2-agent` row: the CLI
killed the server process before its read-back of the live API finished, so the
final order state was never observed, and an outcome nobody read cannot be
graded either way. The read-back now runs on a context the session's
cancellation cannot reach. All three phase 5 runs report `n_unscorable` 0 on
every arm.

That is a fix landing, not a limitation retired. The rule still stands: a row
the harness cannot score is named rather than folded into "not recovered",
because folding a gateway failure into that column would charge it to the arm.

**17. The naive arm's attempt cap never engages.** One action per order, and
nothing arrives with enough prior attempts. It is a safety bound, not a shaper
of these numbers.

**18. `do_nothing` as a recorded decision would score as neither.** An arm that
decides `do_nothing` and calls no action tool takes no action and makes no
escalation, so it earns no escalation credit for a bait order it handled
correctly. The charter asks for `escalate_to_human` instead and every
non-action in this run was an escalation, so the case did not arise. It is an
asymmetry in the scoring, not in the arm.

## What the published run itself carries

**19. The phase 3 fake-layer run was produced by a binary that predates two
fixes.** This item is about `results/runs/phase-3-fake`, which is still in the
tree as the input to the phase 3 tables. The phase 5 runs are unaffected.
Both landed after that run had started, and restarting would have cost forty
more headless invocations against a budget that was already close.

The first is cosmetic. `finishAction` wrote `attempt_no` as a literal zero on
the agent arm's `action_taken` and `action_skipped` rows, so in
`results/runs/phase-3-fake/a2-agent/ledger.jsonl` that field reads 0 while the
live run carries the real value. Nothing reads it: FA-2 comes from
`attempts_seen` on the outcome row, and the containment counts read the kind,
the verdict, and the side-effect flag.

The second is not cosmetic and was checked rather than assumed. The mutex that
serialises the action path landed after the fake run started, so that run drove
the unlocked binary. Its outcome rows were checked afterwards for the signature
the race would leave, an order carrying more attempts than `R1-MAX-ATTEMPTS`
permits. The highest `attempts_after` in the run is 3, which is the cap, so
nothing in the published fake-layer table came from a raced rule.

**20. The phase 3 live agent arm was re-run and the other three arms were
not.** The first live `a2-agent` attempt came back with all 8 rows unscorable,
because the CLI's exit cancelled the context the gateway read-back was on. The
read-back now runs on a context the session's cancellation cannot reach, and the
arm was re-run on the fixed binary over the same batch and the same order
sequence. The other three arms' live data is from the original run. This is
about the phase 3 tables; `phase-5-live` was driven in one pass on the fixed
binary.

**21. The action budget can overshoot under concurrency.** Written 2026-09-01
about the retry engine, and the rule id in it is a pre-pivot one. The middleware
checks the invocation budget before the handler spends it, so two calls
admitted at once can both spend. The lock in `Server.act` stops the attempt cap
being raced; it does not make the invocation budget exact. The bound that
matters, `R1-MAX-ATTEMPTS`, is exact.

The race is unchanged and so is the finding. What changed on 2026-09-05 is the
rule: `R1` is `R1-MAX-TOUCHES` now and it bounds outbound contacts about one
debt, not re-presentments of a card. The pre-pivot name is left in the paragraph
above rather than rewritten, for the reason the heading of this file's pivot
section gives.

**22. `ActionOutput.Action` means two things.** On a middleware refusal it is
the tool name and on a handler refusal it is the policy action. Cosmetic, in a
field the model reads and no metric does, and it was left alone rather than
changed between the two layers' runs.

## What the security controls do not reach

**23. An encoded credential defeats every redaction pattern here.**
`Client.Redact` matches three literal strings and `internal/redact` matches two
shapes. A reviewer put a base64 and a percent-encoded key id through `apiError`
and `captureResponse` and both survived into the capture line, which is what
becomes a committed fixture. There is no evidence Razorpay returns an encoded
key id, and encoding-aware redaction is an unbounded problem. The control is
the package that holds the credential scrubbing before the string leaves; the
patterns are a backstop, and this is a case where the backstop does not reach.

**24. A key secret has no shape to match.** It is a bare alphanumeric string,
so no pattern finds one in ordinary text. The same argument applies: the
control is `razorpay.Client.Redact` and `razorpay.Attempter`'s own scrubbing on
every error and every captured body.

**25. Four credential leaks have been found in this project, all four in code
whose tests were green at the time.** Two in the phase 1 offline review round,
one in a span attribute of a real Jaeger trace, and one in an unpinned redirect
that would have handed the full request URI to whatever host a `Location`
header named. None of them was carelessness in the redaction code. Each lived
on a surface the redaction tests were not asking about, and each was found by
someone told to construct a leak rather than to read for one. That is a
statement about how the next one will be found, not a claim that there are no
more.

## What is not built

**26. FR-BATCH-6, the paid-order bait, does not exist.** Two bait kinds ship
and both fire. A third, an order already `paid` in the gateway, would catch an
arm that acts without reading state. It stays unbuilt rather than counted as
built.

**27. FR-STORE-2, durable resume, is half done.** `Store.Observe` primes an
order from the gateway's own payment count, so a rerun against the same gateway
orders sees the attempts already made. A rerun through `rzp run` materialises
fresh orders and starts clean. There is no durable store.

**28. PRD Q8 is open on purpose.** No rule reads `batch.MaxLegitAttemptsFor`,
so the `attempt_budget_exhausted` bait is allowed a third attempt and both
gated arms take it. That is the one false action each of them has. Adding a
budget-aware rule in the same phase that added the agent would have confounded
two changes, and adding it in the phase that publishes the tables would change
the tables it publishes.

**29. There is no durable audit sink, no dashboard, and no UI.** The outputs
are Jaeger, a JSONL ledger, and a markdown table. That is the whole product
surface and PRD 5 says so.

## What phase 5 could not make real

**30. The headline batch's failure mix is somebody else's.** The
`ethoca-card-mix-2017` profile is card declines, published by a
fraud-prevention vendor, describing 2017, across whatever merchant population
their data covers. It is not Indian, it is not UPI-inclusive, it is not this
decade, and it is not any particular merchant's. It is the best citable mix
available and every number in the headline table sits on it. The three cited
shares sum to 79 percent and the residual is spread across the other classes and
marked uncited in the profile itself.

The vintage was recorded as 2019 until a first-hand check of the source
article's date on 2026-09-01 corrected it to 2017, and by then the four-arm run
had been driven at 40 headless invocations. The batch file the run consumed
therefore carries the old name. Both files are in the tree,
`scripts/verify-batches.sh` proves they differ in nothing but `batch_id`, and no
run artifact was edited to fit the new name. A widely quoted companion claim,
that response codes 05 and 51 are together about 80 percent of declines, is not
in that article and could not be verified anywhere, so it is not carried.

**31. The `observed-live-mix` profile ships with no data in it.** A read-only
probe of the author's own live Razorpay merchant account on 2026-09-01 returned
two payments over six weeks. Two payments are a specimen and not a distribution,
and seeding a mix from them would be the invention phase 5 exists to remove
wearing the word "observed". The loader reads a path from
`RZP_OBSERVED_MIX_FILE`, outside the repository, and the profile errors with
that variable named until there is real data behind it.

**32. The Visa Category 1 code list is reconstructed, not primary.** Bulletin
AI10325 establishes the four categories, defines Category 1 as a decline the
issuer will never approve, caps Category 2 reattempts at 15 in 30 days, and
moves four codes out of Category 1 effective 2021-04-17. It does not enumerate
Category 1. The twelve codes in `internal/networkcodes` are a processor
reconstruction of Visa's member-gated table, `VisaCategory1IsReconstructed` is
true, and the source string says so. This project first cited the bulletin for
the list itself, which the bulletin does not contain.

**33. Two cited code paths are exercised by unit tests and by no run.** Written
2026-09-01 about the retry engine. `R4-NEVER-RETRY-CLASS` below is a pre-pivot
rule id; the rule is `R4-NEVER-CONTACT` since 2026-09-05 and it refuses contact
rather than re-presentment. No
Razorpay payload this project has observed carries a raw network response code,
so `classify.ClassifyNetworkDeclineCode` and every list in
`internal/networkcodes` are reached by their own tests and by nothing else.
`R4-NEVER-RETRY-CLASS` fires in runs through the Razorpay reason, never through
the network path. Separately, the fake gateway stamps every payment it seeds as
a card payment, so `batch.Generate` draws from the documented card table and the
eight UPI reason mappings have never been driven either.

**34. The two interval rules have no citable value and say so.** Written
2026-09-01 about the retry engine, and the 30 second figure in it is that
engine's. No industry
source publishes a retry interval at seconds scale. The shortest scheme-native
one is the Mastercard automated-clearing schedule, which starts at one hour, and
the RBI e-mandate 24 hour pre-debit notice is a notice floor rather than a rate.
`R2-COOLDOWN` and `R6-NOTIFY-RATE` keep their 30 seconds and are declared
configured choices in `policy.ConfiguredChoices`, which a test walks. Attaching
either source to a 30 second constant would be worse than attaching none.

Neither constant is 30 seconds any more and neither rule is about a retry. The
current values are in the pivot section above, under item 47, and they are what
`internal/policy/sources.go` holds. The paragraph above is kept because the
finding it records, that no source publishes these numbers, survived the pivot
unchanged: the values moved and their citation status did not.

**35. The Mastercard reattempt thresholds are contested and nothing here uses
them.** Secondary sources quote a specific per-merchant reattempt limit and do
not agree with each other on the number or the window. Only the two undisputed
Mastercard facts are used: advice code 03, and the one hour floor on the
published schedule.

**36. The phase 5 fake-layer runs carry no trace ids, and the cause was not
isolated.** Counted from the ledgers rather than assumed:

| Run and arm | Rows carrying a trace id |
|---|---|
| `phase-3-fake`, `a2-agent` | 443 of 443 |
| `phase-5-fake-ethoca`, `a2-agent` | 0 of 404 |
| `phase-5-fake-ethoca`, `a3-rules` | 0 of 166 |
| `phase-5-live`, `a3-rules` | 32 of 32 |

Two halves, and only one is a regression. The three deterministic fake-layer
arms carried no trace id in phase 3 either, so that half is how the fake-layer
runner has always behaved. The agent arm is the regression: its ledger was fully
traced on the phase 3 fake run and is not traced at all on the phase 5 one.
Every arm of the phase 5 live run is traced, so the tracer is not broken
outright.

*Corrected 2026-09-01: the cause is isolated, from archived evidence.* The
harness restates `OTEL_EXPORTER_OTLP_ENDPOINT` by value into each order's MCP
config at generation time, and the configs each run archived under its own
`mcp/` directory settle it. The phase 3 fake configs carry the endpoint,
scheme-less, and that run is fully traced, which exonerates the no-scheme
candidate outright. The phase 5 fake configs carry an empty `env` block: the
shell that generated them had no endpoint exported, so the server took the
no-op tracer branch in `cmd/rzp-mcp`, and a no-op span has no id to give. That
branch was silent. It now writes one warning line to stderr naming the variable
and what is lost, and `TestFakeLayerSaysSoWhenItServesWithoutTraceIDs` pins
it.

Nothing else is affected. The runs completed, the ledgers are complete, and
every number in the tables stands, because no metric reads a trace id. What is
lost is the link from a published row to a Jaeger trace, which is the whole
trace-as-audit-trail claim in ADR-0005. `docs/DEMO-SCRIPT.md` therefore still
carries the phase 3 trace ids and says which run they come from. A tracer that
stops recording ids while every gate stays green is a real gap in an audit
design that leans on it, and nothing in the suite would have caught it.

**37. `results/batches/b-1234-40.json` can no longer be rebuilt by this code.**
Phase 5 changed the reason vocabulary, the amount range, and the apportionment
method, so `rzp seed --seed 1234 --n 40 --bait 3` now produces a different batch.
The file stays in the tree as the record of what the phase 3 run consumed, the
phase 5 gates were pointed at seed 5150 so a gate run cannot overwrite it, and
`scripts/verify-batches.sh` deliberately does not check it, with a comment
saying why the row that would fail on purpose is missing.

**38. `uniform-invented` does not split the way it used to.** The apportionment
method changed from "give the leftover to the first declared class" to largest
remainder, because the old rule would have moved the ethoca profile's cited 44
percent share to 50 percent of a 40 order batch. The shares are unchanged and
the counts are not: at n=37 non-bait the split is 10/9/9/9 where it was 13/8/8/8.
That is a real change to a mix that was supposed to be the one thing holding
still for the comparison, and it is why the comparison in `/RESULTS.md` is
between two phase 5 runs rather than between a phase 5 run and a phase 3 one.

## The rule behind this file

A number in prose is a claim, and it gets checked against the run that produced
it even when the same person wrote both an hour apart. Three sentences in the
first draft of `/RESULTS.md` did not survive that check, none of them in a
table cell and all of them in sentences a reader would quote.
`scripts/claims-check.sh` now does that mechanically, and
`make verify-phase-4` runs it.
