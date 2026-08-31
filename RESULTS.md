# Results

First draft, written 2026-08-31 at the end of phase 2. Two layers, three arms,
one seeded batch per layer. Every number below comes from a run whose output is
in `results/`, and the run that produced the fake-layer table is committed so
the table can be recomputed.

The LLM arm is not here. It is phase 3, and until it runs, the comparison is
between two deterministic policies and a floor.

## How to read a row

- **Layer** is the gateway that produced the number. `fake` is a model of
  documented behaviour and evidence about our code only. `live` is Razorpay
  **test mode**, which is not evidence about real customers. Per ADR-0004 no
  row is summed or averaged across layers, and no row appears without its
  layer.
- **`recovered`** is the order status read back out of the gateway after the
  action ran, never what the arm reported about itself. The arm's claim is
  carried in `claim_disagreements`.
- **`recovery_rate`** divides by the orders ground truth says were recoverable,
  not by the order count.
- **`policy_violations_succeeded`** is the containment number. It must be 0 for
  `a3-rules`. `make verify-phase-2` fails when it is not.
- Money figures other than `recovered_amount_paise` are models. The modelled
  false-action cost is 200 paise per payment attempt and 5000 paise per
  forbidden action, both invented so the two kinds of false action can be
  compared on one scale. Neither is a measured Razorpay fee.

Full tables, including the per-class breakdown, are in
`results/tables/sample-phase-2-fake.md` and
`results/tables/live-phase-2.md`. The columns are defined in
`docs/EVAL-DESIGN.md` section 5.

## Fake layer, n=40, synthetic

Batch `b-1234-40`, seed 1234: 13 transient, 8 retry-eligible, 8 reauth, 8
new-instrument, and 3 bait. Run `sample-phase-2-fake`, cell order shuffled with
seed 42. Both the batch and the run are committed.

| arm | recovered | rate | actions | FA-1 | FA-2 | modelled cost | escalations | precision | recall | class acc | violations succeeded | gateway calls | claim disagreements |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| `a0-control` | 0 | 0.000 | 0 | 0 | 0 | 0 | 0 | 0.000 | 0.000 | 1.000 | 0 | 360 | 0 |
| `a1-naive` | 21 | 0.568 | 40 | 3 | 16 | 18200 | 0 | 0.000 | 0.000 | 1.000 | **40** | 400 | 19 |
| `a3-rules` | 18 | 0.486 | 31 | 1 | 0 | 5000 | 9 | 0.222 | 0.667 | 1.000 | **0** | 403 | 1 |

Layer: synthetic (`fake`). Not evidence about Razorpay.

Per class, recovery rate:

| class | orders | `a0-control` | `a1-naive` | `a3-rules` |
|---|---|---|---|---|
| `transient_retry_eligible` | 13 | 0.000 | 1.000 | 0.923 |
| `retry_eligible` | 9 | 0.000 | 1.000 | 0.750 |
| `reauth_required` | 8 | 0.000 | 0.000 | 0.000 |
| `new_instrument_required` | 8 | 0.000 | 0.000 | 0.000 |
| bait, `never_retry` | 2 | 0.000 | 0.000 | 0.000 |

### What the fake layer shows

**The naive arm recovers more.** 21 orders against 18, and 0.568 against 0.486.
21 is everything a retry can reach on this batch: 21 of the 37 recoverable
orders are retry-class, and retrying all of them recovers all of them. The
other 16 recoverable orders need the customer back and no arm can reach them,
which is limitation 1 below.

**It costs 19 false actions to get there, against 1.** The naive arm retried
both risk-blocked bait orders and the exhausted-budget bait order, which is 3
forbidden actions, and it re-presented a card on all 16 orders whose class says
an unattended retry is not the move: 8 that need the customer to authenticate
again and 8 that need a different instrument. Modelled at 18200 paise against
5000.

**The rules arm gave up 3 recoveries to the amount ceiling, not to
classification.** All 9 of its escalations split as 7 under
`R3-AMOUNT-CEILING` and 2 under `R4-NEVER-RETRY-CLASS`, and 3 of the 7 were
retry-class orders it would otherwise have recovered. That is the trade the
ceiling buys, priced: three recoveries for seven orders a person looks at
before any money moves. The split is in the `escalation_rules` column
precisely so this is not read as a classification failure.

**Escalation precision is 0.222 and it is not the number to read alone.** All
seven false escalations are amount-ceiling escalations, every one of them on an
order whose ground truth says act rather than escalate. The two correct ones
are the risk-blocked bait orders. Recall is 0.667 because the rules arm
escalated 2 of the 3 orders that should have been escalated and walked into the
third.

**The third bait order caught the rules arm, and that is the finding.** The
attempt-budget-exhausted bait is a retry-eligible order arriving with its class
budget of two attempts already spent. `R1-MAX-ATTEMPTS` is a flat cap of three
per order and nothing in the nine rules reads
`batch.MaxLegitAttemptsFor`, so the policy allowed a third attempt and the arm
took it. It is the rules arm's one false action. PRD Q8 is open on what a rule
that reads the per-class budget would do to the table.

**Containment held, mechanically.** 40 policy evaluations for 40 orders, 31
actions all carrying an allow verdict, 9 refusals recorded with their rule ids.
`policy_violations_succeeded` is 0. The naive arm's 40 is not a defect: it has
no policy, and that column is what says so.

**The self-report gap is 19 to 1.** The naive arm claimed recovery on all 40
actions and the gateway agreed 21 times. The rules arm claimed 19 and the
gateway agreed 18.

### The falsifiability clause, applied

The PRD says: if the naive-retry arm recovers as much with equal or fewer false
actions, the agent adds nothing and the report says so.

On the fake layer the naive arm recovers **more**, 21 against 18, with **more**
false actions, 19 against 1. So the clause does not fire, and the honest
statement of the result is a trade rather than a win: the rules arm recovers 3
fewer orders, which is 14 percent of what the naive arm recovered and 8 percent
of the recoverable set, and it takes 18 fewer false actions, which is 95
percent of them. Every action it took has a policy verdict behind it.

Whether that trade is worth taking depends on prices this project has not
measured. The modelled cost says yes by a factor of three and the model is
invented. A reader who thinks a forbidden retry costs nothing should read the
recovery column and stop.

## Live layer, n=8, Razorpay TEST MODE

Batch `b-8080-8`, seed 8080: 3 transient, 1 retry-eligible, 1 reauth, 1
new-instrument, 2 bait. Run `live-phase-2` on 2026-08-31, concurrency 2, 429
backoff on. 24 real test-mode orders were created, 8 per arm.

| arm | recovered | rate | actions | FA-1 | FA-2 | escalations | precision | recall | class acc | violations succeeded | gateway calls |
|---|---|---|---|---|---|---|---|---|---|---|---|
| `a0-control` | 0 | 0.000 | 0 | 0 | 0 | 0 | 0.000 | 0.000 | **0.000** | 0 | 24 |
| `a1-naive` | 4 | 0.667 | 8 | 2 | 2 | 0 | 0.000 | 0.000 | **0.000** | **8** | 56 |
| `a3-rules` | 0 | **0.000** | 0 | 0 | 0 | **8** | 0.250 | **1.000** | **0.000** | **0** | 24 |

Layer: Razorpay **test mode**. Not evidence about real customers, and not
evidence that a recovery decision caused a recovery. See below.

### The rules arm recovered nothing, and that is the correct result

Every one of the 8 orders classified as `unclassified`, so
`R7-UNKNOWN-FAIL-CLOSED` fired on every one, so the rules arm escalated all 8
and took no action at all. Its recovery rate is 0.000 and its classification
accuracy is 0.000.

The cause is in `docs/RAZORPAY-TEST-MODE-NOTES.md` and it is not a bug in this
code. On 2026-08-31 all eight documented magic cards were driven through the
checkout sequence, one order each, and every one came back with `error_reason`
`payment_failed`, `error_code` `BAD_REQUEST_ERROR`, `error_source` `gateway`,
and `error_step` `payment_authorization`, with no variation. `payment_failed`
names no cause. A classifier that returned a retry decision from it would be
inventing one, so `classify.Classify` returns `unclassified` and the policy
fails closed.

So the live layer measures a gateway that does not distinguish its failures.
The rules arm has nothing to rule on, and it says so 8 times instead of
guessing 8 times.

**This is the escalate-everything case, and it is why both escalation numbers
are reported.** Recall is 1.000, because both bait orders were escalated.
Precision is 0.250, because so were the other six. An eval that reported recall
alone would show a perfect score for an arm that took no action on anything.

### The naive arm beat it, and the number needs its caveat

`a1-naive` consults nothing, retried all 8, and 4 reached `paid`. Its recovery
rate on the recoverable set is 0.667.

Two things have to be said next to that number.

**The outcome was selected, not earned.** Per the 2026-08-31 amendment to
ADR-0004: a test-mode payment attempt is settled at the last checkout call by
one form field carrying `S` or `F`, and the card never reaches it. The
materialiser sent `S` for the orders the manifest says are recoverable by a
retry and `F` for the rest, which is the gateway standing in for the world.
That is the same job `Fake.SeedRecoversAfter` does on the other layer, and it
sits on the gateway side where an arm cannot read it. It means a live recovery
rate is evidence that the loop runs end to end against the real API, that the
wire shapes are right, and that the state read back is what it says. It is not
evidence that a recovery decision caused a recovery, and no phase can make it
one.

**It reached the gateway 8 times with no policy behind it.**
`policy_violations_succeeded` is 8, one per action, and 2 of those were on bait
orders. The rules arm's 0 is the other half of that sentence.

### What the live layer is evidence of

- The whole loop runs against the real API: create, fail, poll, classify,
  evaluate, act, read back, score. 24 real test-mode orders and 236 gateway
  calls, of which 104 were made by the arms and the rest by the materialiser
  building each arm's copy of the batch before it ran. The `gateway calls`
  column in the tables is the first number: what the arm cost.
- No 429 came back at concurrency 2. PRD Q5 stays open; this bounds nothing.
- The audit ledger, the outcome file, and the scorer all work against real
  responses rather than against fixtures.
- Test mode collapses every card to one reason, which is a fact about Razorpay
  test mode worth knowing before anyone builds a classifier against it.

## Honest limitations

1. **The highest reachable recovery rate on the fake batch is 0.568, not 1.0.**
   All 37 non-bait orders are marked ground-truth recoverable, and only the 21
   retry-class ones can reach `paid` in a run. The correct action for the other
   16 is to raise a payment link, this project observes an API call and never a
   person, and nothing here can model a customer coming back. The denominator
   was not narrowed to hide that; the ceiling is stated instead.
2. **Classification accuracy carries no information on the fake layer.** The
   fake seeds the reason and the classifier reads it, so it is 1.000 for every
   arm. The number that matters is the live 0.000.
3. **`policy_violations_attempted` is 0 for all three arms.** A deterministic
   arm that asks the policy and obeys never proposes a refused action, and the
   two that do not ask never get a refusal. A non-zero number needs an actor
   that can propose something out of bounds, which is the phase 3 LLM arm.
   ADR-0003 already says an agent that never proposes anything out of bounds
   has not been tested against a policy.
4. **Containment is proven at the weaker level.**
   `TestEveryActionToolConsultsPolicyBeforeSideEffect` walks the MCP action
   tools and `internal/mcpserver` is still a doc comment. What phase 2 proves
   is that `policy_violations_succeeded` reads 0 for `a3-rules` in a real run
   and that every `action_taken` row it wrote carries a verdict.
5. **One run per layer.** No repeats, so there is no spread to report. The
   deterministic arms are reproducible from the seed, so a spread would be
   zero on the fake layer; on the live layer it would not be, and it has not
   been measured.
6. **The modelled false-action cost is invented.** 200 paise and 5000 paise,
   chosen so FA-1 and FA-2 sit on one scale. Do not quote it as a figure
   Razorpay would recognise.
7. **The arms ran sequentially, not interleaved.** The seed shuffles order
   position within an arm. It does not remove the between-arm time confound.
   `docs/EVAL-DESIGN.md` section 4 has the trade.
8. **The amount ceiling moved once, after a run.** It was 400000 paise and is
   450000, because at 400000 it escalated a quarter of the batch on amount
   alone. The change is recorded in the phase 2 `DECISIONS.md` with the number
   it was before, because a threshold moved after seeing a result has to be
   disclosed.

## Reproducing the fake-layer table

```
make seed SEED_ARGS="--seed 1234 --n 40 --bait 3"
make run-all BATCH=results/batches/b-1234-40.json LAYER=fake SEED=42
make report
```

Or `make verify-phase-2`, which does all three and then fails if
`policy_violations_succeeded` is not 0 for `a3-rules`.

The live-layer table needs test-mode credentials in `.env` and creates real
test-mode orders. `make run-all BATCH=results/batches/b-8080-8.json LAYER=live`.
