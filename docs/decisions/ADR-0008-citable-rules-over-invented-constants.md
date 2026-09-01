# ADR-0008: every constant is cited or is declared a configured choice

| | |
|---|---|
| Status | Accepted |
| Date | 2026-09-01 |
| Applies from | Phase 5 |

## Context

By the end of phase 4 this repository was rigorous about one thing and silent
about another.

It was rigorous about provenance downstream of a run. Every published number is
checked against the CSV that produced it, no table crosses a measurement layer,
an unscorable row leaves every denominator, and `scripts/claims-check.sh` fails
a build over a digit that drifted.

It was silent about provenance upstream of a run. The retry cap was 3, the
cooldown 30 seconds, the amount ceiling 450000 paise, the modelled costs 200 and
5000 paise, the failure taxonomy eight strings off a test-card page, and the
batch mix 28/24/24/24. Every one of those was chosen by the author. Two of them
said so. The rest read as settings, which is what a number in a `const` block
reads as when nothing says otherwise.

That asymmetry is the thing a payments reviewer notices first, and it is worse
than an unsourced number in a blog post, because the surrounding rigour makes
the unsourced numbers look checked. A run whose inputs came from nowhere,
measured to three decimal places, is a precise answer to a question nobody
asked.

Two of the constants were also simply wrong once anyone looked. The classifier
had spelled `insufficient_funds` singular since phase 0, which is not the
live-mode spelling. The cost model charged 200 paise for a failed payment
attempt, and a failed transaction carries no gateway fee in India at all.

## Decision

**Every constant in this repository is either cited, with a source, or declared
a configured choice, with a reason. There is no third category.**

Four consequences, all mechanical rather than remembered:

1. **`internal/policy` declares the status of every rule.** `CitedValues` and
   `ConfiguredChoices` are maps keyed by rule id, and
   `TestEveryRuleDeclaresItsCitationStatus` walks `RuleIDs` and fails a rule
   that is in neither map or in both. A tenth rule cannot arrive without
   someone deciding which of the two things its number is.

2. **A cited list lives in its own package with its source in the header.**
   `internal/networkcodes` holds the Visa Category 1 set and the Mastercard
   do-not-try-again advice code, with the bulletin URL in the file comment. A
   network's rules pasted inline into a policy comment are indistinguishable
   from a policy author's opinion six months later.

3. **A cited value that this project does not implement is carried and said to
   be unimplemented.** The Visa 15-in-30 reattempt cap is a constant in
   `networkcodes` and the rolling window is not built, because the store holds
   no history spanning runs and building one to enforce a bound a cap of 3 never
   approaches would be a feature written to decorate a citation. The Visa
   excessive-reattempt fee is in the cost model with its source and a second
   constant holding the zero it contributes.

4. **A published external number goes through `scripts/claims-allow.txt` with
   its source in the reason field.** It never enters the fact set. The fact set
   is built from committed runs, a cited industry figure is not a run artifact,
   and letting one in would break the one guarantee `claims_check.py` makes.

**Refusing to invent a citation is part of the decision, not an exception to
it.** No industry source publishes a retry interval at seconds scale. The
shortest scheme-native one is the Mastercard automated-clearing schedule
starting at one hour, and the RBI e-mandate 24 hour pre-debit notice is a notice
floor rather than a rate. `R2-COOLDOWN` and `R6-NOTIFY-RATE` therefore keep
their 30 seconds and are declared configured choices. Attaching either source to
a 30 second constant would be worse than attaching none, because it would look
checked.

## Consequences

**Every published table moved.** Not because the code got better at recovering
payments, but because the amount ceiling went from a number tuned after a run to
the RBI e-mandate threshold, the batch amounts moved to straddle it, and the
cost model stopped charging for something free. The phase 3 tables stay in the
tree as the record of what the phase 3 code did on the phase 3 batch, and the
phase 5 tables are the current ones. `results/batches/b-1234-40.json` is
deliberately not regenerated: rebuilding that path under the new taxonomy would
overwrite the input to a published table with a different batch of the same
name.

**A cited mix is somebody else's mix.** The `ethoca-card-mix-2017` profile makes
35 percent of the batch orders no arm should act on, because lost, stolen, and
fraud declines are a third of published card declines. That is a harder batch
than the invented one, the numbers on it are worse, and the share is the
source's. A profile that had been tuned would not have done that.

**`docs/EVIDENCE.md` is now a document that can be wrong.** It carries analyst
estimates and vendor claims with a label on each. A vendor claim is evidence
about what is being claimed and not evidence that the claim is true, and section
8 of that file lists what still cannot be made real without production data.

**The alternative was to keep the numbers and add a disclaimer.** Rejected. A
disclaimer that says "these constants are illustrative" is true and it is not
useful, because it does not tell a reader which ones would change under real
data and which are load-bearing. Naming each one cited or chosen does.
