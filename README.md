# rzp-recovery-agent

Rebound (rzp-rebound): a unified revenue-at-risk engine for Razorpay, and the
regulatory self-audit that deleted its predecessor's headline action.

Three detectors sweep an account for money that is owed and not collected:
payments Razorpay reports as failed, orders created and never paid, and invoices
issued and gone overdue. All three return one item type. The queue collapses the
sightings that are the same debt, a fifteen-rule policy gate decides one action
per item, and an intervention engine executes only what the gate allowed. Every
decision is an append-only JSONL row carrying the rule id that produced it,
including the refusals, and in the MCP path it is also an OpenTelemetry span:
`cmd/rzp-mcp` is the one binary here that starts a tracer, and
`cmd/rzp risk-run` does not.

Two arms run over the same queue in one process. `a0-control` detects,
classifies, and evaluates, and then stops. `a1-engine` executes. Assignment is a
seeded shuffle stratified by source, so a recovered-paise figure is a difference
between two groups rather than a number with no counterfactual under it.

Built for the Razorpay Buildathon, Track 3, against Razorpay test mode.

## Why there is no retry button

This repository used to retry failed payments. `docs/INDIA-CONSTRAINTS-AUDIT.md`
is the audit that killed that action, and it is in the tree because the finding
is more useful than the feature was.

The finding, stated as the audit states it: every regulatory line below is
labelled **REPORTED**, sourced from consistent secondary and legal-analysis
material because the RBI and NPCI primary PDFs resisted direct retrieval. A
REPORTED label is not upgraded by later use. Each one still needs a
primary-source check before it is defended to a third party.

- **REPORTED.** RBI's Authentication Directions, issued 2025-09-25 with
  compliance from 2026-04-01, require an additional factor on effectively all
  one-off domestic digital payments. No merchant-initiated transaction or
  resubmission concept exists in India for a one-off payment, on any rail.
- **REPORTED.** One-off UPI needs the UPI PIN on every customer-initiated
  debit. There is no server-side reattempt of a failed one-off UPI payment.
- **REPORTED.** Tokenization since 2022-10-01 means a merchant cannot store a
  PAN, and a network token carries forward an existing authorization rather
  than independently authorizing a new one.

So `retry_payment` was fiction twice over: the mechanism does not exist in
Razorpay live mode, and the thing it simulated, an unattended re-charge of a
one-off instrument, is not lawful on any Indian rail. Deleting it took two rule
citations with it. The Visa 15-in-30 reattempt cap and the Mastercard advice-code
list both bound merchant-initiated re-presentment of a card authorization, and
there is nothing left here for them to bound.

The action was not gated, renamed, or left unregistered. It is gone from
`internal/riskitem`'s lawful set, `Intervention` refuses anything outside that
set, and a model that asks for a retry by name gets `M1-TOOL-ALLOWLIST`. A model
that invents one as a policy action gets `R7-UNKNOWN-FAIL-CLOSED`.

What is left is the action set a merchant may actually take: tell the customer,
give them something to pay against, record what they said, hand it to a person,
or write it off. Write-off has no tool either: `cancel_write_off` is a
decision `record_decision` can state and nothing executes, so recording one is
the whole of it, and any amount above the Rs 100 write-off floor goes to a
person under `R3-HUMAN-APPROVAL-CEILING` before it is even recorded that way.

## Quickstart

The whole loop, against Razorpay test mode. It needs test-mode keys in `.env`
(`.env.example` has the shape); the Makefile loads that file itself, so nothing
below depends on the caller having exported anything.

**1. Seed a book of receivables and record what was created.**

```
make seedbook
```

Writes `seedbook.json`: customers, invoices at four age buckets, abandoned
orders, and per-item flags for disputed, no-contact, and partial-payment plans.
It refuses to make more than its call budget of Razorpay calls in one run.
`make seedbook FLAGS='--dry-run'` prints the plan and calls nothing.

**2. Fail some payments by hand, in a browser.**

The seeder prints this step as an instruction block, because it is the one item
class that cannot be created through the API. Test-mode checkout is browser
only, and the headless attempt path this repository used until 2026-08-31
answers `403` as of 2026-09-05. Open the short URLs the seeder names, pay with one
of the documented failure cards it prints, and let the bank step decline. The
failed-payment detector reads the account's history on its next sweep.

**3. Detect, gate, and intervene.**

```
make risk-run FLAGS='--manifest seedbook.json --out results/risk-runs/r1 \
    --detect-grace 1s --notify-window 1ns'
```

Prints the run header, one line per item with its source, arm, proposed action,
verdict, and rule, then the verdicts-by-rule roll-up. Writes `ledger.jsonl`,
`results.jsonl`, `escalations.jsonl`, and `summary.json` into `--out`.

Both flags are there because a book seeded minutes ago is not a book the
defaults are written for. `--detect-grace` is measured against Razorpay's own
`issued_at`, before the manifest's simulated age is applied, so on the 24 hour
default the overdue-invoice detector reports nothing at all; a zero falls back to
that same default, so a small positive value is the floor.
`--notify-window` opens `R6-NOTIFY-RATE`, a run-wide send rate whose one second
default means exactly one notification goes out per run. Add
`--contact-always-open` after 21:00 IST, which is when `R12-QUIET-HOURS` closes.
`--since` needs nothing: it defaults to the manifest's own `created_at`, which
keeps every order the account carried before this book out of the queue.

Offline, with no credentials and no API call of any kind:

```
make risk-run FLAGS='--dry-run --manifest internal/riskrun/testdata/manifest.json --out /tmp/dry'
```

That replays a manifest through the real detectors, the real dedupe, and the
real gate, and stops before the intervention engine.

**4. Take the before reading, pay one link in a browser, take the after.**

```
make risk-poll FLAGS='--manifest seedbook.json --run results/risk-runs/r1 \
    --out snapshots/before.json'
```

Then open one of the payment links on screen and pay it with the documented
success test card `4100 2800 0000 1007`, any future expiry, any CVV. Then:

```
make risk-poll FLAGS='--manifest seedbook.json --run results/risk-runs/r1 \
    --against snapshots/before.json --out snapshots/after.json'
```

`--run` folds in the payment links the risk run created, which the seedbook
manifest does not know about, and it goes on both polls whenever the run
directory exists. `riskrun.Diff` matches entities across the two snapshots and
counts anything present only in the later one as unmatched, so a link missing
from the before reading is a payment the delta will not count. `--against`
prints the delta: recovered paise,
change in amount due, and the entities whose status moved. Every call a poll
makes is a fetch. Nothing in `risk-poll` writes to Razorpay.

A paid transition observed this way is `n=1` and the payer is the person running
the demo. `/HONEST-LIMITATIONS.md` says so at length and no number here is
generalised past it.

## The tool surface

The language-model arm reaches the engine through eight MCP tools and nothing
else: no shell, no HTTP client, no filesystem, and no credential. The keys reach
the server process through the environment and are never written into the MCP
config file.

| Tool | What it does |
|---|---|
| `list_risk_items` | Read the queue this invocation was given |
| `get_risk_item` | Read one item in full |
| `record_decision` | State an intent and the reasoning behind it |
| `notify_item` | Email or SMS about an existing handle |
| `create_payment_link_for_item` | Mint a link for an item that has no handle |
| `resend_link_for_item` | Resend the notification for a handle that exists |
| `log_promise` | Record that a customer said they will pay |
| `escalate_item` | Hand the item to a person |

`TestServerServesExactlyTheEightNamedTools` keeps the list closed. There is no
retry tool, for the reason in the section above.

**The model never sees a classification, and it never sees a customer's address.**
It is handed the raw Razorpay failure fields, the amounts, the handle, and a flag
saying a contact channel exists and which media it supports. `internal/classify`
runs on the server side of the boundary, where the model can neither read it nor
argue with it, and the email address and phone number are handled only by the
intervention engine. Nothing on this wire can leak what nothing on this wire
holds.

## The rules

Fifteen rule ids, first match wins, three verdicts. `deny` means not this action
now; `escalate` means no automated action on this item at all and a person has to
look. Every decision carries a rule id, including an allow, so no audit row has
to be read as "no rule fired, presumably that was fine".

| Rule | Verdict | What it decides | Value |
|---|---|---|---|
| `R8-KILL-SWITCH` | deny | A halt beats every reason an action might be fine | configured |
| `R9-IDEMPOTENCY` | deny | Already committed, so this is a replay and does nothing | configured |
| `R7-UNKNOWN-FAIL-CLOSED` | escalate | An unknown source, an unlawful action, or a failure nothing recognises | **cited** |
| `R11-NOT-YET-DUE` | deny | Inside the per-source grace, so it is not a debt yet | configured |
| `R10-NO-CONTACT-CHANNEL` | escalate | No email and no phone number, and nothing here may guess one | configured |
| `R4-NEVER-CONTACT` | escalate | The gateway's risk check refused it, the resource is terminal, or nothing is owed | **cited** |
| `R15-PROMISE-HOLD` | deny | The merchant said it would wait and the hold has not expired | configured |
| `R12-QUIET-HOURS` | deny | Outside the contact band | configured |
| `R13-DISPUTED-NEVER-CHASE` | escalate | Somebody contests the debt | configured |
| `R3-HUMAN-APPROVAL-CEILING` | escalate | The amount, or any write-off above the floor, wants a person | configured |
| `R1-MAX-TOUCHES` | escalate | The item has had its lifetime contacts | configured |
| `R2-COOLDOWN` | deny | The last contact about it was too recent | configured |
| `R6-NOTIFY-RATE` | deny | The run is sending too fast | configured |
| `R5-ACTION-BUDGET` | deny | The run has spent its blast radius | configured |
| `R0-DEFAULT-ALLOW` | allow | Nothing refused | configured |

**Two rules carry a citation and the rest do not, and that is the honest count.**
`policy.CitedValues` and `policy.ConfiguredChoices` are maps rather than a
paragraph, because a paragraph cannot be tested;
`TestEveryRuleDeclaresItsCitationStatus` fails a rule that is in neither or in
both. The pivot emptied most of that file: the Visa reattempt cap, the
Mastercard advice-code list, and the RBI e-mandate threshold were all cited
there on 2026-09-01 and all three are gone. The e-mandate threshold is the one
worth naming, because the number stayed and only the citation went. Rs 15,000 is
a real Indian line between an amount that may be debited unattended under a
registered mandate and one that may not, and this engine debits nothing: it
sends messages and mints links, and the customer authenticates every payment
themselves. Applied to a link, the threshold discriminates nothing. The operator's
own question survives, and it is nobody's published number.

**`R12-QUIET-HOURS` is the one configured choice that looks like it has a
regulator behind it, and it does not.** India's commercial-communication regime
for SMS runs through TRAI's DLT registration and does restrict delivery hours for
some traffic (**REPORTED**), but no primary TRAI document was read by this
project, and whether a payment reminder counts as promotional or transactional
under it is unresolved. The contact band in `internal/quiet` is therefore a
merchant's own politeness rule. Do not quote it as a regulated window.
`policy.ConfiguredChoices` carries that sentence in the code, marked
NEEDS-VERIFICATION.

There is no R14. Write-off approval was drafted as one and folded into R3,
because "an amount above which a person decides" and "a write-off, which a
person always decides" are the same control asking about two thresholds.

## Results

Live risk-engine numbers are filled in from committed runs only, and
`/RESULTS.md` says which are and are not there yet. The tables below are the
retired retry system's, kept because their inputs are committed and their cells
still check against the CSVs behind them.

### Pre-pivot, fake layer, n=40, published card-decline mix

**These describe the retired action set.** Four arms, `a0-control`, `a1-naive`,
`a2-agent`, `a3-rules`, over a seeded batch of failed card payments, where the
action under test was a retry. The engine those numbers were produced by no
longer exists. They are kept, unedited, because deleting a published table is a
worse habit than labelling one, and because the containment column is the one
claim that survived the pivot intact.

| run | layer | arm | recovered | rate | FA-1 | FA-2 | modelled cost | escalations | refusals | violations succeeded |
|---|---|---|---|---|---|---|---|---|---|---|
| phase-5-fake-ethoca | fake | `a0-control` | 0 | 0.000 | 0 | 0 | 0 | 0 | 0 | 0 |
| phase-5-fake-ethoca | fake | `a1-naive` | 20 | 0.769 | **14** | 6 | **700000** | 0 | 0 | **40** |
| phase-5-fake-ethoca | fake | `a2-agent` | 16 | 0.615 | 0 | 0 | 0 | 18 | **22** | **0** |
| phase-5-fake-ethoca | fake | `a3-rules` | 16 | 0.615 | 0 | 0 | 0 | 18 | 18 | **0** |

### Pre-pivot, live layer, n=8, Razorpay test mode

| run | layer | arm | scorable | unscorable | recovered | rate | FA-1 | FA-2 | escalations | refusals | violations succeeded |
|---|---|---|---|---|---|---|---|---|---|---|---|
| phase-5-live | live | `a0-control` | 8 | 0 | 0 | 0.000 | 0 | 0 | 0 | 0 | 0 |
| phase-5-live | live | `a1-naive` | 8 | 0 | 4 | 0.667 | 2 | 2 | 0 | 0 | **8** |
| phase-5-live | live | `a3-rules` | 8 | 0 | 0 | 0.000 | 0 | 0 | 8 | 8 | **0** |

A test-mode number is not evidence about real customers, and no row here is
summed or averaged across layers.

What survives the pivot from those tables is the containment column and the
refusal column. The gated arms reached the gateway zero times without a policy
verdict behind them, and the model arm proposed things the policy refused rather
than proposing only what it knew would pass. What does not survive is every
recovery figure: the audit's section 3 lists `recovery_rate`,
`recovered_orders`, `recovered_amount_paise`, `fa2_over_attempt`, and the entire
naive arm as metrics that die under the India frame, because they are rates for
outcomes the harness chose about an action that may not be taken.

## Where to look

| Document | What is in it |
|---|---|
| [`docs/INDIA-CONSTRAINTS-AUDIT.md`](docs/INDIA-CONSTRAINTS-AUDIT.md) | The audit: what was fiction, what was real, and the direction that survived probing |
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | The diagram, the detectors, the dedupe, the two-layer gate, the trace-as-audit-trail design |
| [`RESULTS.md`](RESULTS.md) | The risk-run output schema, and the pre-pivot tables under their banner |
| [`HONEST-LIMITATIONS.md`](HONEST-LIMITATIONS.md) | Every limit the build found, collected, pre-pivot and post-pivot |
| [`docs/DEMO-SCRIPT.md`](docs/DEMO-SCRIPT.md) | The five minute walkthrough, as a timed shot list |
| [`docs/DEMO-DEPLOY.md`](docs/DEMO-DEPLOY.md) | The public demo page: what is behind the link, and how to put it on one |
| [`docs/PRD.md`](docs/PRD.md) | Scope, requirements with their covering tests, open questions |
| [`docs/EVAL-DESIGN.md`](docs/EVAL-DESIGN.md) | How a run is measured and why each metric exists |
| [`docs/EVIDENCE.md`](docs/EVIDENCE.md) | Where every constant came from and what kind of source it has |
| [`docs/RAZORPAY-TEST-MODE-NOTES.md`](docs/RAZORPAY-TEST-MODE-NOTES.md) | What test mode actually returns, observed rather than read |
| [`docs/AUDIT-TRACE-SCHEMA.md`](docs/AUDIT-TRACE-SCHEMA.md) | Span names, attributes, and ledger fields, written from a run |
| [`docs/phases/`](docs/phases/) | The process trail: plan, test list, problems, decisions, and report per phase |
| [`docs/decisions/`](docs/decisions/) | The ADRs |

The phase directories are the honest record. `PROBLEMS.md` in each one says what
broke, how it was found, and what it cost, including the four credential leaks
and the parallel-tool-call race the containment metric could not see.

## The gates

```
make ci
```

`lint`, the Go and Python tests, `docs-check`, and `claims-check`.
`docs-check` fails the build on an em dash, on wording that claims a person was
reached rather than that a notification API call succeeded, on anything shaped
like a Razorpay key, and on a relative date. `claims-check` reads every cell of
every published table back against the CSV of the run behind it, and every number
in prose against the set of numbers the committed runs actually contain.

## Stack

Go 1.25 (`internal/` packages, three binaries), the Model Context Protocol Go
SDK, OpenTelemetry with Jaeger, Razorpay test mode over plain `net/http` with no
SDK, and a Python standard-library harness for scoring. No third-party Python
package and no install step.
