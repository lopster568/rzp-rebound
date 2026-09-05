# Demo script

A five minute pitch video, as a shot list. Each block gives the narration beat,
what is on screen, and the exact command to run. Written 2026-09-01 for the
retry engine, rewritten 2026-09-05 for the revenue-at-risk engine that replaced
it.

The spine of this video is no longer one command, because the loop it walks has a
human in the middle of it. Four terminal beats with two browser cutaways between
them: seed the book, fail payments in a browser, run the engine, pay a link in a
browser, poll the delta. The browser steps are not a weakness to hurry past. They
are the honest shape of the only Indian-lawful loop this project found, and the
video says so out loud.

Two things to do before the first take are at the bottom, and the recording
checklist is not optional: this repository is public and one frame of the wrong
terminal undoes the credential work in `docs/phases/phase-1-live-loop/`.

## Setup, before recording

```
make preflight
make seedbook FLAGS='--dry-run'
```

`preflight` reports the toolchain, docker, and credentials in one screen. The
seedbook dry run prints the plan it would seed and calls nothing, which is the
rehearsal for beat one and the way to check the account is reachable without
spending a live seed on it.

Then seed the real book, before recording, because the browser step in beat two
takes longer than a shot:

```
make seedbook
```

Keep the printed instruction block. It names the links to open and the documented
failure cards to use, and beat two is you reading it back.

Open a browser with exactly two tabs: one of the invoice short URLs the seeder
printed, and a second one you will use in beat five. Nothing else. The recording
checklist has why.

## 0:00 to 0:40, the finding, not the feature

**On screen:** a clean terminal, and then `docs/INDIA-CONSTRAINTS-AUDIT.md` on
GitHub, scrolled to the component verdict table.

**Say:** This repository used to retry failed payments. It does not any more, and
the reason is the most useful thing in it.

Every one of those regulatory lines is labelled REPORTED, sourced from secondary
and legal-analysis material, because the RBI and NPCI primary documents resisted
retrieval. On that sourcing: RBI's Authentication Directions require an
additional factor on effectively every one-off domestic digital payment, and
there is no merchant-initiated transaction concept in India for a one-off
payment on any rail. One-off UPI needs the PIN every time. A stored token carries
an existing authorization forward and does not create a new one.

So an unattended re-charge of a failed one-off payment is not a feature that was
hard to build. It is a feature that would be unlawful to ship, and the audit
found it was also impossible to test: the endpoint does not exist in live mode.
It was fiction twice over.

**Scroll to the probe addendum.** Four independent probes killed the mandate
direction on the same day. Subscriptions is not entitled on the test account, no
API exists to trigger a subscription charge, halted subscriptions get an invoice
instead of a charge attempt, and test card tokens expire before a second billing
cycle can run. What survived is receivables: invoices, orders, payment links.

**Say:** What I built instead is the thing that is left when you take the
unlawful action away. Three detectors, one queue, one gate, and an action set a
merchant may actually take.

## 0:40 to 1:20, the seedbook, beat one

**On screen:** the terminal. Scroll back to the `make seedbook` output from
setup, or run the dry run live if the scrollback is gone.

```
make seedbook FLAGS='--dry-run'
```

**Say while it prints:** This seeds a book of receivables in Razorpay test mode.
Invoices at four ages, abandoned orders, and per-item flags: one disputed, one
with no contact channel at all, one on a partial-payment plan. It writes a
manifest recording every id it created and it refuses to exceed its call budget.

**Point at the age column.** That column is a confession. Nothing in Razorpay's
API can backdate an invoice, so every item here is minutes old as far as the
gateway is concerned. The age is the manifest's claim, not Razorpay's, and the
run labels it in three separate places so nobody can mistake a stated age for a
real one. You will see the label in a moment.

## 1:20 to 2:00, failing payments by hand, the first browser cutaway

**On screen:** the terminal showing the seeder's printed instruction block, then
the browser tab holding one of the invoice short URLs.

**Say:** Two of the three detectors have data because the seeder created it. The
third does not, and this is the one place a human is load bearing.

Test-mode checkout is browser only. The undocumented headless path this
repository drove payments through until 2026-08-31 returns `403` now, cause
unresolved, and I deliberately did not probe it further rather than work around
a block. So the seeder prints a to-do list instead of pretending.

**Pay the link with one of the printed failure cards and let the bank step
decline.** Show the decline card on screen.

**Say:** That failure is now in the account's payment history, and the
failed-payment detector reads it on the next sweep. Nothing wrote it there
directly.

## 2:00 to 3:10, the risk run, beat two

**On screen:** back to a clean terminal.

```
make risk-run FLAGS='--manifest seedbook.json --out results/risk-runs/demo'
```

**Say while the header prints:** Three detectors sweep the account. Failed
payments, orders created and never paid, invoices issued and overdue. All three
return one item type.

**Point at the items line.** Read the two numbers on it. Razorpay mints an order
when an invoice is issued, so one debt is visible to the invoice detector and to
the order detector at the same time, under two different ids. Every merged
sighting on that line is a customer who would otherwise have been contacted twice
about one debt, by two detectors that were each individually right. The queue
collapses on the root order, not on the sighting.

**Point at the age line.** There it is: `manifest_simulated`. That is label one of
three. It is also on the summary file and on every single result row.

**Then the per-item lines, as they scroll.** Source, item, arm, proposed action,
verdict, rule. Two arms, assigned by a seeded shuffle stratified by source, so
they see the same mix. The control arm detects, classifies, and evaluates, and
then stops. The engine arm executes.

**Then the verdicts-by-rule roll-up. Slow down here. This is the containment
beat.**

**Say:** Every decision carries the rule that made it, including the allows.
Now the two refusals.

`R10-NO-CONTACT-CHANNEL`. Those are the items the seeder created with no email
and no phone number. The engine will not guess an address, and it will not pick
whichever channel it happens to have, so it hands them to a person. That is an
escalation, which is a different verdict from a denial, and it is a different row
in the report.

`R13-DISPUTED-NEVER-CHASE`. Somebody contests that debt, so it never goes into an
automated cadence. And I will say the limit out loud: Razorpay has no field for a
contested debt. That flag is set by the seeder and read off the manifest.
Nothing in this system detects a real dispute, and the rule firing here is
evidence that the rule works and not that the input is real.

**Point at the escalation verdict block.** Every escalation was itself put
through the gate and allowed. Handing an item to a person is an action like any
other, and the kill switch stops it like any other.

## 3:10 to 3:50, the payment, the second browser cutaway

**On screen:** the terminal, then the browser.

```
make risk-poll FLAGS='--manifest seedbook.json --out snapshots/before.json'
```

**Say:** That is the before reading. Every call it makes is a fetch. Nothing in
the poller writes to Razorpay.

**Switch to the browser, open one of the payment links the run created or one of
the invoice short URLs, and pay it.** Card `4100 2800 0000 1007`, any future
expiry, any CVV.

**Say while it processes:** This is the first thing in the project's history that
is a genuine observed transition rather than a simulated one. Until 2026-09-05
nothing here had ever completed a payment against Razorpay test mode.

## 3:50 to 4:25, the delta, beat three, and the caveat that goes with it

**On screen:** back to the terminal.

```
make risk-poll FLAGS='--manifest seedbook.json --run results/risk-runs/demo \
    --against snapshots/before.json --out snapshots/after.json'
```

**Say:** Re-read every entity, diff it against the earlier snapshot, and print
what moved: collected paise, change in amount due, and the entities whose status
changed. There is the status flip, created to paid, read out of Razorpay rather
than claimed by the agent.

**Then, immediately, before anyone can quote the number:** That is one payment.
I paid it, in a browser, thirty seconds ago, on a link I chose because it was the
one on screen. It is `n=1`, it is the demoer paying, and it is selected rather
than sampled. It is not a recovery rate and this repository publishes no recovery
rate for this engine.

What the control arm is for is exactly that question, asked of two groups instead
of one, and at demo scale it is a handful against a handful, which bounds nothing.
The tool prints its own disclaimer under the figure for the same reason.

**Say:** And the one next to it. A notification here is an accepted API call. The
strongest thing this system can observe is `email_status` reading sent, which is
Razorpay saying it sent something, not a person having read anything. A payment
link created with no contact on it at all still answered `200` with success in
test mode, which is why that wording is enforced by a build gate rather than left
to discipline.

## 4:25 to 5:00, the audit row and the close

**On screen:** the run's `ledger.jsonl`, one row, pretty-printed. Then
`HONEST-LIMITATIONS.md`.

**Say:** One row per decision. The item, the proposed action, the verdict, the
rule id, the reason in plain English, the idempotency key, the touch number, the
age source. A refused action writes a row too, which is what makes refusals
countable instead of silent.

**Then cut to the limitations file, scrolled to the pivot section.**

**Say:** Eight new limits, dated, in the same file as the twenty-something the
old build found, and none of the old ones were softened when the system changed
underneath them. The aging is simulated and labelled in three places. Any paid
transition is one payment by the person running the demo. A notification is an
API acceptance and never a delivery. A dispute is a manifest flag. Failed
payments need a human to exist. The idempotency guard bounds one run and not a
campaign. The model arm cannot trip the dispute rule at all, and that is written
in the code that fails to fill the field. And the test-first ritual was not
followed for the pivot packages, which is flagged rather than backfilled.

**Close:** Most agents demo actions. This one deleted its headline action because
the regulation says it cannot exist, kept the audit that proves it, and shipped
the gate that refuses the rest. Every published number in the repository is read
back against the run behind it by a script that runs in CI, and every limit the
build found is in one file with a date on it.

## Recording checklist

Run through this before the first take and again before the final one.

- **No Razorpay key in any frame.** Never open `.env`, never run `env`, never run
  `make auth-probe` on camera. The risk-engine commands print ids, statuses, and
  redacted errors and nothing else.
- **No agent tooling on screen.** No `~/.claude` path, no Claude Code interface,
  no session chrome, no editor with an assistant panel open. Record in a plain
  terminal.
- **Clean shell prompt.** No hostname or path that gives away anything, no
  half-finished commands in scrollback, and clear the screen between blocks.
- **Browser is clean.** Two tabs, both of them a Razorpay test-mode page. No
  other tabs, no bookmarks bar carrying anything, no autofill dropdown appearing
  over the card form. Check the card form once before recording, so the browser
  has already offered and been refused whatever it wants to offer.
- **Seed the book before recording, not on camera.** The browser decline in beat
  two takes longer than its shot, and a seed that hits its call budget mid-take is
  not recoverable inside five minutes.
- **Rehearse the dry run once** immediately before recording, so the on-camera
  run is warm and finishes inside its block:
  `make risk-run FLAGS='--dry-run --manifest internal/riskrun/testdata/manifest.json --out /tmp/rehearsal'`
- **Check the audio** on a ten second test before spending five minutes.

## What is not in this video, and why

The old script's two Jaeger tabs are gone from the shot list. They showed a
`retry_payment` span allowed under `R0-DEFAULT-ALLOW` and a `create_payment_link`
refused under `R3-AMOUNT-CEILING`, from `results/runs/phase-3-fake`. The first of
those is a span for an action that no longer exists, and the second cites a rule
under an id and a ceiling the pivot changed. Showing either would be showing a
viewer a system this repository does not have.

The trace design is unchanged and `ARCHITECTURE.md` describes it. A presenter who
wants a Jaeger shot should produce a fresh pair from a risk run with an OTLP
endpoint configured and take the ids off that run's own ledger, rather than
reusing the pair in this document's history. `/HONEST-LIMITATIONS.md` item 36 is
the standing gap in that design and it is not closed.
