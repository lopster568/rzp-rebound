# Honest limitations

Every limit the phase documents record, in one place, so a reader does not have
to walk four phase directories to find them. Nothing here is new. What is new
is that it is collected.

Written 2026-09-01. The tables it qualifies are in `/RESULTS.md`.

## What Razorpay test mode does not give you

**1. Test mode collapses every failure to one reason.** All eight documented
magic cards in `testdata/magic_cards.json` were driven through the checkout
sequence on 2026-08-31, one order each. Every one came back with `error_reason`
`payment_failed`, `error_code` `BAD_REQUEST_ERROR`, `error_source` `gateway`,
and `error_step` `payment_authorization`, with no variation. Not one documented
reason string came back, so zero cards are marked `"verified": true` and each
row records what came back instead.

`payment_failed` names no cause, so `classify.Classify` returns `unclassified`,
so `R7-UNKNOWN-FAIL-CLOSED` fires, so both gated arms escalate every live order
and take no action. That is the correct output of an honest measurement of a
gateway that does not distinguish its failures, and it is not tuned away. The
eight-reason mapping in `internal/classify` is exercised by the fake gateway
and by nothing live.

This does not establish that the documented codes are wrong. They may be
produced by the hosted Checkout widget, which simulates the decline in its own
front end. This project has not driven that widget and says nothing about it.

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

**5. The risk-block error code is unknown.** Nothing in the 2026-08-31 run
produced one. `testcards.PendingRiskBlockCode` stands in and is deliberately
not shaped like a Razorpay value. PRD Q2 is open.

**6. UPI could not be driven server side.** `POST /v1/payments/create/upi`
answered `400` under Basic auth and `401` with the key id in the body or the
query string. The two `upi_vpas` rows in the card table stay unverified.

## What the numbers are, as measured

**7. The highest reachable recovery rate on the fake batch is 0.568.** All 37
non-bait orders carry `ground_truth_recoverable: true`, which is the recovery
rate's denominator, and only the 21 retry-class orders can actually reach
`paid` in a run. The correct action for the other 16 is to raise a payment
link, this project observes an API call and never a person, and nothing here
models a customer coming back. The denominator was not narrowed to flatter the
rate; the ceiling is stated instead, and the naive arm reaches exactly it.

**8. Classification accuracy carries no information on the fake layer.** The
fake seeds the reason and the classifier reads it, so the number is 1.000 for
every arm. The one that carries information is the live 0.000, and its cause is
limitation 1.

**9. Three of the nine policy rules fired in a run, and none of the three
middleware rules did.** Counted from the committed ledgers:

| Run and arm | `policy_evaluated` rows by rule |
|---|---|
| fake, `a3-rules` | `R0-DEFAULT-ALLOW` 31, `R3-AMOUNT-CEILING` 7, `R4-NEVER-RETRY-CLASS` 2 |
| fake, `a2-agent` | `R0-DEFAULT-ALLOW` 43, `R3-AMOUNT-CEILING` 14, `R4-NEVER-RETRY-CLASS` 2 |
| live, both gated arms | `R7-UNKNOWN-FAIL-CLOSED` 8 |

So R3, R4, and R7 fired, R0 is the id on an allow rather than a rule, and R1,
R2, R5, R6, R8, and R9 never fired in any run. One cycle per order rules them
out: the timestamps R2 and R6 read are zero on a first action, no idempotency
key repeats, the budget is far above the order count, the kill switch is unset,
and nothing arrives with enough attempts to reach R1's cap of 3. Every
`tool_call` row in both runs carries an allow, so the agent tripped none of
`M1-TOOL-ALLOWLIST`, `M2-ORDER-ALLOWLIST`, or `M3-DECISION-REQUIRED`.

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

**11. The modelled false-action cost is invented.** 200 paise per payment
attempt and 5000 paise per forbidden action, chosen so the two kinds of false
action sit on one scale. Nothing here has measured a Razorpay retry fee or
priced a goodwill loss. Do not quote it as a figure Razorpay would recognise.

**12. The amount ceiling moved once, after a run.** It was 400000 paise and is
450000, because at 400000 it escalated a quarter of the batch on amount alone
and swamped every escalation number with orders whose ground truth said retry.
The change is in the phase 2 `DECISIONS.md` and in the constant's own doc
comment with the number it was before, because a threshold moved after seeing a
result has to be disclosed rather than quietly changed.

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

**16. One live outcome row is unscorable.** On `order_dkhfak807uotlk` the
invocation completed, the agent read the order, recorded a decision, and
escalated, and the ledger has all of it. The `outcome_observed` row is missing:
the CLI killed the server process before its read-back of the live API
finished. An outcome nobody read cannot be graded either way, so it is counted
and left out of every denominator. It is why `a2-agent`'s live escalation
precision is 0.286 against `a3-rules`'s 0.250: the agent is scored over 7 rows
and the rules arm over 8, and one bait order is in the row that dropped out.
The two arms behaved identically on all 8.

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

**19. The fake-layer run was produced by a binary that predates two fixes.**
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

**20. The live agent arm was re-run and the other three arms were not.** The
first live `a2-agent` attempt came back with all 8 rows unscorable, because the
CLI's exit cancelled the context the gateway read-back was on. The read-back
now runs on a context the session's cancellation cannot reach, and the arm was
re-run on the fixed binary over the same batch and the same order sequence.
The other three arms' live data is from the original run.

**21. The action budget can overshoot under concurrency.** The middleware
checks the invocation budget before the handler spends it, so two calls
admitted at once can both spend. The lock in `Server.act` stops the attempt cap
being raced; it does not make the invocation budget exact. The bound that
matters, `R1-MAX-ATTEMPTS`, is exact.

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

## The rule behind this file

A number in prose is a claim, and it gets checked against the run that produced
it even when the same person wrote both an hour apart. Three sentences in the
first draft of `/RESULTS.md` did not survive that check, none of them in a
table cell and all of them in sentences a reader would quote.
`scripts/claims-check.sh` now does that mechanically, and
`make verify-phase-4` runs it.
