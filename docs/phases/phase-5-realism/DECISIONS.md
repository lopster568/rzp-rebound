# Phase 5 decisions

Choices made while the work happened, with the alternative that was rejected.
Anything that outlives this phase goes to `docs/decisions/` instead, which in
this phase is ADR-0008.

## 1. The test-card spellings are kept, in a table of their own

`insufficient_fund` is what the Razorpay test-card page carries and
`insufficient_funds` is what the live-mode card error page carries. The same
page has `card_number_invalid`, which is not on the live-mode card list at all.

Both stay, in `testModeCardTableReasons`, separate from the two documented
live-mode tables and never returned by `DocumentedReasons`.

Deleting them was the tidier option and it was wrong twice over. Every batch
manifest committed under `results/batches/` carries `insufficient_fund`, so a
classifier that did not know it would make a published run unreplayable. And the
difference between the two vocabularies is the finding rather than a
typographical accident: phase 1 spent a day discovering that test mode does not
speak the language the documentation does, and collapsing the two tables would
delete the evidence of that.

Rejected: keeping one table with both spellings in it. That is the state phase 5
started in, and it is what made the singular spelling look documented.

## 2. `card_expired` and `debit_instrument_blocked` are new-instrument-required, not never-retry

The phase 5 brief listed both under never-retry, alongside
`payment_risk_check_failed`, and then said to decide the mapping thoughtfully
and document the distinction. This is the decision.

Both classes forbid another attempt on the same instrument: `IsRetryEligible` is
false for each. What separates them is whether asking the customer for a
different instrument is allowed.

- An expired card is the textbook new-instrument case. The customer wanted to
  pay, the card is out of date, and a payment link asking for another one is
  recoverable revenue behind one message. Blocking that would forgo the recovery
  this project exists to make.
- A blocked debit instrument is the same shape. The instrument is dead and the
  customer is not.
- A risk block is not. Contacting a customer a risk engine has flagged is itself
  an action, and it is the action a merchant is least entitled to take on that
  order. `never_retry` therefore means no action of any kind, and R4 escalates
  to a person.

That gives `never_retry` exactly one meaning instead of two, which is what makes
the R4 escalation readable in a table. `TestCardExpiredAndBlockedInstrumentAreNewInstrumentRequiredNotNeverRetry`
holds it and the doc comment on `cardReasons` explains it.

## 3. The amount ceiling moved and R1 did not

R3 was 450000 paise, picked after a fake-layer run showed 400000 escalating a
quarter of the batch. It is now 1500000, the RBI e-mandate additional-factor
threshold.

R1 was 3 and is still 3. The Visa bulletin caps Category 2 reattempts at 15 in
30 days, and 3 is inside that. That sentence said "Categories 2 and 3" until the
2026-09-01 re-verification pass read the bulletin as a PDF, which is entry 12. A
merchant is free to be stricter than the network, so the number does not have to
move for the citation to be honest. What changed is that the constant names the
bound it sits under, and a test fails if anyone raises it past 15.

The rolling 30 day window is not implemented. The store holds no history
spanning runs, and building one to enforce a bound a cap of 3 never approaches
would be a feature written to decorate a citation. `networkcodes` carries the
cap so the gap is visible rather than absent.

Rejected: moving R1 to 15 to "match the citation". That would be raising a
safety bound to look sourced, which is the failure mode this phase is about,
pointed the other way.

## 4. Largest-remainder apportionment, and what it cost

Batch counts came from "give the leftover to the first declared class". That is
fine when every share is the author's own. It is wrong the moment one is cited:
the leftover lands on the first share and moves it, and the ethoca profile's 44
percent would have become 50 percent of a 40 order batch.

Largest remainder holds every share within one order of its quota. The visible
cost is that `uniform-invented` at n=37 now splits 10/9/9/9 where it used to
split 13/8/8/8. That is a real change to a mix that was supposed to be
unchanged, and it is disclosed here rather than hidden by keeping two
algorithms.

## 5. A profile that sets its own bait share refuses `--bait`

The ethoca profile computes 14 bait orders from a cited 35 percent share.
Accepting `--bait 3` alongside it and ignoring the flag would be a flag that
silently does nothing, so `rzp seed` errors instead and names the share.

## 6. The observed-live-mix profile ships with no data

A read-only probe of the author's own live Razorpay merchant account on
2026-09-01, covering 2026-07-15 to 2026-08-31, returned two payments. One
captured UPI payment of 69900 paise, one failed UPI payment of 178800 paise.

**Two payments cannot seed a mix.** A distribution built from n=2 is not a
distribution, and shipping one under a name like `observed-live-mix` would be
exactly the invention this phase exists to remove, wearing the word "observed".
The loader stays, reading `RZP_OBSERVED_MIX_FILE` from outside the repository,
and the profile errors with that variable named until there is real data behind
it.

**What the specimen is used for instead.** It is production validation of a
structural claim, one data point, in `docs/EVIDENCE.md` section 7 and in
`HONEST-LIMITATIONS.md`. That one failed payment confirms three things nothing
else in this project could: a documented live-mode reason string does appear in
production, the documented `error.source` enumeration does appear in production,
and the coarse-code-plus-specific-reason structure holds outside test mode.
`TestClassifierHandlesTheProductionFailureShape` is built from its shape.

Aggregate fields only. No payload from that account is published, and the
amounts and the date range are the only things from it that appear anywhere.

## 7. `b-1234-40` is not regenerated, and the verify seed moved

Phase 5 changed the reason vocabulary, the amount range, and the apportionment.
Re-running `rzp seed --seed 1234 --n 40 --bait 3` would therefore write a
different batch to the path that is the committed input to the phase 3 tables.

So the phase 5 batches use seed 5150, `make verify-phase-2` and
`make verify-phase-3` were pointed at the same seed so a gate run cannot
overwrite the phase 3 input, and `scripts/verify-batches.sh` deliberately does
not check `b-1234-40`, with a comment saying why the row that would fail on
purpose is missing.

The consequence is that `b-1234-40` can no longer be rebuilt by this code. It is
a record of what the phase 3 run consumed, not a batch anyone can reproduce
today, and `HONEST-LIMITATIONS.md` says so.

## 8. `a2-agent` does not run on the live layer from phase 5

Test mode returns `payment_failed` for every card, so every live order
classifies as unclassified and both gated arms escalate everything. That was the
phase 3 live result and it is not a defect. It also means there is no
classification signal on that layer for a model to differ from a rule set on:
the phase 3 live run spent 8 invocations to demonstrate that the agent did
exactly what the rule set did, on a gateway that gave neither of them anything
to work with.

Phase 5 spends that budget on the fake layer instead, where the two arms can be
told apart. The live table is three arms and the row that would have been
`a2-agent` is absent rather than empty.

## 9. Notification cost is a column of its own, not part of the false-action cost

A payment link on a reauth-required order is the correct action. Its cost, 20
paise of transactional SMS, is real and belongs in the model. Adding it to
`modeled_false_action_cost_paise` would charge an arm for doing the right thing
in a column whose whole purpose is counting mistakes, so it gets
`notifications_sent` and `modeled_notification_cost_paise` instead.

The effect is that `a3-rules` and `a2-agent`, which send the notifications, now
carry a cost the naive arm mostly does not, and the naive arm carries a
forbidden-action cost ten times what it used to. Both directions are visible in
one table.

## 10. `docs/EVIDENCE.md` is inside the claims gate

`scripts/claims_check.py` builds its fact set from committed runs only, so an
external cited figure has no way to pass check 2 except through
`scripts/claims-allow.txt` with its source on the line.

Adding EVIDENCE.md to the gated file list means every industry number in it has
to be entered there by hand with a reason. That is deliberate friction: the
document with the most external numbers in the repository is the one where an
unsourced number would do the most damage, and the allow-list is the only place
those numbers can live.

Rejected: leaving EVIDENCE.md ungated on the reasoning that it is prose about
sources rather than results. Prose about sources is exactly where a wrong number
survives longest.

## 11. `payment_failed` is documented and still has no class

The 2026-09-01 re-verification pass found that `payment_failed`, the only reason
Razorpay test mode ever returned, is not an undocumented string. Razorpay
documents it on the live-mode card error page: the bank declined the payment
without providing a specific reason, suggested action contact the bank or try a
different card.

Phase 1 had recorded it as a mystery, and half of this repository's live-layer
narrative rested on that. So the correction had to be made and the question it
opens had to be answered: does a documented suggested action make it
classifiable?

**It stays `unclassified`, and the suggested action is the reason rather than an
argument against it.** "Try a different card" rules out a same-instrument retry,
which is exactly what the fail-closed default delivers through
`R7-UNKNOWN-FAIL-CLOSED`. The classification was already doing what the
documentation advises.

**It is not promoted to `new_instrument_required`,** which is the tempting move
because it would let the live layer show a recovery attempt instead of eight
escalations. Three reasons, in order of weight:

1. A support instruction written for a human looking at one order is not a
   class. "Contact your bank" is advice to a person who can judge one case;
   `new_instrument_required` is a rule that fires unattended across a batch.
2. Acting on it means sending a payment link to every customer whose payment a
   bank declined without saying why, which on the live layer is every order.
   That is the over-messaging R4 and R7 exist to prevent.
3. It would change a published live-layer result from the outside, by
   reinterpretation rather than by measurement, in the phase whose whole subject
   is not doing that.

`TestPaymentFailedIsDocumentedAndStillUnclassified` holds both halves, and
`testdata/error_codes.json` now labels the row `documented-live` while keeping
it in `_meta.pending`, which is a combination the label test explicitly allows.

## 12. The citation corrections were marked, not quietly applied

A second verification pass on 2026-09-01 read the primary documents first-hand
and found four claims in the first draft of `docs/EVIDENCE.md` wrong: the Visa
bulletin was cited for a Category 1 code list it does not contain, the Ethoca
article was dated two years late, a companion claim about response codes 05 and
51 was carried that is not in its source, and a merchant survey was described as
a measurement.

Every one of them is corrected in place with a `Corrected 2026-09-01` line
saying what it used to say. That is more words than a silent fix and it is the
right trade: this is the document that exists to stop unsourced numbers, the
failure mode arrived inside it on its first day, and a reader deciding how much
to trust the rest of the file needs to know that.

The dropped claim is dropped rather than hedged. A figure that cannot be traced
to a document does not get a qualifier, it gets deleted.

## 13. The batch file the run consumed keeps its old name

Renaming the profile to `ethoca-card-mix-2017` changed the batch id, and by then
the four-arm run had been driven at 40 headless invocations against a cap of 45.

Three options. Re-run, which the budget does not allow. Edit the run manifest's
`batch_id` and `batch_path` to point at the new file, which is editing a run
artifact to fit a story and is the thing this repository does not do. Or keep
both files and prove they are the same batch.

The third. `scripts/verify-batches.sh` rebuilds the correctly named file from
the profile and then asserts that the old-named file differs from it in nothing
but `batch_id`. The orders are byte-identical, because the shares never changed
and only the label did. `HONEST-LIMITATIONS.md` item 30 records it.

## 14. Addendum, 2026-09-01: the showcase is a script, not a phase

`scripts/showcase.sh` and `make showcase` were built after phase 5 closed. They
open no new phase directory, because nothing about the system changed: the
showcase drives the commands that already existed and reads the tables that were
already committed. A phase directory for a presentation layer would be process
theatre.

Three decisions inside it are worth recording.

**Every number in act 3 is parsed at run time.** The impact table, the three
takeaways under it, and the compliance line are all computed from
`results/tables/phase-5-fake-ethoca.csv` when the command runs. Nothing is typed
into the script. A showcase carrying its own copy of the results would be the
exact failure `make claims-check` exists to catch, published in the one artifact
a reviewer is most likely to watch.

**The takeaways are asserted before they are printed.** A sentence like "the
gated arms took zero false actions" is a claim about the parsed data, so each
one is a condition on the parsed row and prints a line naming the failed check
instead when the condition does not hold. Prose that stays true only until the
next run is prose that will be wrong on camera.

**Act 2 degrades rather than dies.** Without credentials or a reachable gateway
it names what is missing, prints `make preflight`, describes what the loop would
have done, and carries on to the remaining acts. It never prints a transcript of
a run that did not happen. The alternative, a canned transcript for the
credential-less path, was rejected without much thought: a project whose pitch is
that its numbers are real does not ship a fake terminal.
