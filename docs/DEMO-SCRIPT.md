# Demo script

A five minute pitch video, as a shot list. Each block gives the narration beat,
what is on screen, and the exact command to run. Written 2026-09-01, rewritten
around `make showcase` the same day.

The spine of the video is one command. `make showcase` prints the problem, runs
the live recovery loop, prints the impact table it parses out of the committed
CSV, and prints the proof, stopping for Enter between each act. Those stops are
the narration beats, and the two cutaways below happen while it is stopped.
`NO_PAUSE=1 make showcase` runs the same thing straight through in about a
minute, which is the rehearsal take and the fallback if a pause is awkward to
cut around.

Two things to do before the first take are at the bottom, and the recording
checklist is not optional: this repository is public and one frame of the wrong
terminal undoes the credential work in `docs/phases/phase-1-live-loop/`.

## Setup, before recording

```
make jaeger-up                                  # prints RZP_JAEGER_UI_URL
export RZP_JAEGER_UI_URL=<what it printed>
make trace-links RUN_DIR=results/runs/phase-3-fake
```

`trace-links` prints two URLs read out of the run's own ledger, so each link
points at the trace a published table row was computed from rather than at a
search result. Open both tabs before the take. The trace ids are:

| Tab | Trace id | What it shows |
|---|---|---|
| The refusal | `04821ac7aea1bf5b4db411621e00d886` | `create_payment_link` refused, `R3-AMOUNT-CEILING` on the span, no side effect |
| The recovery | `6ca1fe6315cbbce8f0e5f022de9e20fe` | `retry_payment` allowed under `R0-DEFAULT-ALLOW`, order read back `paid` |

**Both ids are from `results/runs/phase-3-fake`, and the tables this script
walks are from the phase 5 runs.** That is stated rather than hidden. Every
ledger row in both phase 5 fake runs came out with an empty `trace_id`,
including the agent arm's, whose phase 3 ledger was fully traced, and the cause
was not isolated. `/HONEST-LIMITATIONS.md` item 36 has the counts. The two
traces above show the same code path the phase 5 rows were produced by, on an
earlier batch, and a viewer should be told which run they are looking at.

Jaeger runs on in-memory storage, so a container restart loses both. Check that
both tabs load before recording. If they do not, run
`make agent-smoke BATCH=results/batches/b-5150-40-ethoca-card-mix-2017.json N=2`
with a working `OTEL_EXPORTER_OTLP_ENDPOINT` to produce a fresh pair, and take
the new ids from `make trace-links`. That is also the way to get a pair from the
current batch, which is better than the pair above.

## 0:00 to 0:45, the problem, act 1

**On screen:** a clean terminal.

```
make showcase
```

Act 1 prints and stops at the first pause. What is on screen is one real failed
payment out of the author's own live merchant account, a UPI payment that timed
out at authentication on 2026-07-15 and that nothing ever re-attempted, and then
three published figures each carrying the kind of source it has. Let the screen
carry the citations and do not read the labels out loud.

**Say:** A failed Razorpay payment stays failed. The merchant picks between two
bad options: do nothing, or retry blindly. Razorpay documents fifteen live-mode
card failure reasons, and eight of them cannot succeed on the same instrument
while three more need the customer back in the flow.

A blind retry on those does not cost a gateway fee. In India only successful
transactions are billed, and that is the first thing people get wrong about this
problem. What it costs is the customer's patience, the merchant's headroom under
the card networks' reattempt caps, and the scheme fees that start once a
merchant goes past them. What a merchant wants is an agent that retries only
what is worth retrying, and proof afterwards that it never went outside the
limits they set. The second half is the hard half, and it is what this build is
about.

## 0:45 to 1:30, the architecture

**On screen:** `ARCHITECTURE.md` on GitHub, scrolled to the rendered mermaid
diagram. The showcase stays paused in the terminal behind it.

**Say:** A seeder writes a batch of failed orders and a ground-truth manifest
the arms never see. Orders are materialised in a gateway behind one port, which
is Razorpay test mode, recorded fixtures, or a deterministic fake. A poller
reads state back, a classifier turns the error fields into one of six recovery
classes, and then a decision maker picks one action.

Trace the two lines through the gate. Every action passes `policy.Evaluate`,
nine rules, first match wins, three verdicts, and every decision carries the
rule id that produced it. The language model arm reaches all of this only
through seven MCP tools, with a second gate as middleware in front of every one
of them: the kill switch, a tool allowlist, an order allowlist, and a rule that
refuses any action until the model has put a decision on the record. The model
holds tool names. The server process holds the keys.

Everything both arms do lands in two sinks at once, a span and an append-only
JSONL row joined by trace id, and the scoring harness reads the file.

## 1:30 to 2:45, the live demo, act 2

**On screen:** back to the terminal. Press Enter and act 2 runs the same loop
`make demo` runs, against Razorpay test mode. It takes about forty seconds.

**Say while it runs:** This is against Razorpay test mode with real test-mode
credentials. It creates an order, drives a real payment attempt to a decline
through the checkout sequence, classifies the failure, asks the policy,
retries, reads the order state back out of the gateway, and prints the ledger
path and the trace URL. Every one of those steps is a span.

## 2:45 to 3:30, the two Jaeger tabs

**On screen:** the two preloaded Jaeger tabs. Leave the showcase paused at the
end of act 2, which has just printed the trace URL for the run that is still on
screen behind them.

**Switch to the tab holding the recovery trace.**

**Say:** Seven spans, one invocation, one trace. `mcp.classify` reads
`transient_retry_eligible` off the gateway's error fields. Three tool calls,
each with its gate verdict on the span. `retry_payment` carries
`rzp.policy.verdict` allow, `rzp.policy.rule` `R0-DEFAULT-ALLOW`, and
`rzp.detail.side_effect` true, which is the one span in this trace that moved
money. `mcp.observe_outcome` at the end reads `rzp.detail.final_order_status`
`paid` and `rzp.recovered` true, and that came from a `FetchOrder` after the
action rather than from anything the model said about itself.

**Then switch to the refusal tab.** This is the longest block in the video, so
slow down here.

**Say:** Same shape, different order. The class is `new_instrument_required`
and the order is above the amount ceiling. The model reads the failure, records
a decision to raise a payment link, and calls `create_payment_link`. Look at
that span: `rzp.policy.rule` is `R3-AMOUNT-CEILING`, `rzp.policy.verdict` is
escalate, and `rzp.detail.side_effect` is false. The policy reason is on the
span too, in plain text, naming the amount and the ceiling it sits above.

**Read the ceiling off the span, not off this script.** This trace comes from
`results/runs/phase-3-fake`, where the ceiling was 450000 paise. Phase 5 moved
it to the RBI e-mandate threshold above which an additional factor of
authentication is required, which is more than three times higher, so the order
in this trace would be allowed rather than escalated under the policy the
published tables were produced by. What the shot is for is the rule firing, the
verdict on the span, and the absent side effect, and all three are unchanged.
Say "above the ceiling" and let the span show the figures.

Now the next span. The model records a second decision, and its own reasoning
is the attribute `rzp.detail.agent_reasoning`. It says the refusal is a hard
limit and not something to route around with a different tool, and then it
calls `escalate_to_human`. It did not call `create_payment_link` again and it
did not reach for `retry_payment` to get to the same customer another way. A
compliance reviewer opening this one link sees the failure, the class, the
proposed action, the rule that refused it, the model's stated reason, and the
fact that nothing reached the gateway.

## 3:30 to 4:30, the measurement, act 3

**On screen:** back to the terminal. Press Enter and act 3 prints the comparison
of the naive arm against the two gated arms: recovered orders, recovered value
in rupees, false actions, and actions that reached a side effect with no policy
verdict behind them.

**Say first:** Every number on this screen was parsed out of
`results/tables/phase-5-fake-ethoca.csv` when the command ran. None of it is
written into the script that printed it, and the three sentences under the table
are checked against the parsed data before they are printed.

Then open `RESULTS.md` for the two tables act 3 does not print, the invented mix
and the live layer.

**Say:** Four arms over one seeded batch of 40, and both the run and the layer
are on every row, because a fake-layer number and a test-mode number are not the
same kind of evidence and because two of these batches are the same seed with
two different failure mixes.

The headline batch is seeded from Mastercard and Ethoca's published card-decline
shares, not from anything I made up. Insufficient funds, lost or stolen, fraud.
Which means a third of the batch is orders no merchant should touch at all.

Start with the naive arm. It recovers 20 against the gated arms' 16, so on the
recovery column it wins. Now the columns next to it: 14 forbidden actions
against 0, and 40 in `policy_violations_succeeded`, which counts actions that
reached a side effect with no policy verdict behind them. It has no policy, so
every action it took is in that column.

**Then scroll to the second fake table, same seed, same code.** On the invented
mix the naive arm takes 3 forbidden actions. On the published one it takes 14.
Nothing about the code changed between those two rows. The batch did, and it
changed because half of it now comes from somebody else's data instead of mine.

Now the two gated arms. They agreed on all 40 orders: same recoveries, same
actions, no false action on either, same eighteen escalations splitting the same
way. Given the same classification, the same policy, and the same tools, the
model reached the decision the hand-written rule set reaches, forty times out of
forty, against under a second for the rule set.

What separates them is one column. The rule set made 40 policy evaluations,
because it proposes exactly what its class table dictates. The model made 50,
and 22 came back refused against the rule set's 18. Twenty-two times it asked
for something the policy would not allow, and none of them reached the gateway.
An agent that never proposes anything out of bounds has not been tested against
a policy. This one pushed, and the gate held.

**Then the live table.** The rules arm recovers zero here, and that is the
honest answer rather than a broken build. Razorpay test mode returns
`payment_failed` for all eight documented magic cards. Razorpay documents that
reason as the bank declining without giving one, and its own suggested action is
to try a different card, so there is no cause a policy can act on. The
classifier returns `unclassified`, the fail-closed rule fires, and the arm
escalates everything.

## 4:30 to 5:00, the limits and the close, act 4

**On screen:** back to the terminal. Press Enter and act 4 prints the two curated
trace links read out of a run's own ledger, the line about `make claims-check`
reading every published cell back against the run behind it, and the scope line
saying every figure is fake layer or test mode. Its last line is the one this
whole video is arguing: most agents demo actions, this one proves them. Then cut
to `HONEST-LIMITATIONS.md`.

**Say:** Three things this does not claim. The live recovery numbers are
outcomes this project selected, because a test-mode payment settles on one form
field at the last checkout call, so they prove the loop runs end to end and not
that a decision caused a recovery. Nothing here observes a person receiving a
message; a payment link with no contact on it still gets a success response, so
the audit phrase is that the notification API call succeeded. And the run shape
only exercised three of the nine policy rules, so the other six rest on unit
tables and a golden matrix rather than on these numbers.

**Close:** Most agents demo actions. This one has a counter that has to read
zero, a test that walks every action handler, and a trace per decision that
says which rule allowed it. The repository has the process trail too: every
phase has a problems file saying what broke and how it was found.

## Recording checklist

Run through this before the first take and again before the final one.

- **No Razorpay key in any frame.** Never open `.env`, never run `env`, never
  run `make auth-probe` on camera. `make demo` prints ids and statuses and
  redacted errors and nothing else, which was checked by running its
  usage, missing-credential, and 401 paths.
- **No agent tooling on screen.** No `~/.claude` path, no Claude Code interface,
  no session chrome, no editor with an assistant panel open. Record in a plain
  terminal.
- **Clean shell prompt.** No hostname or path that gives away anything, no
  half-finished commands in scrollback, and clear the screen between blocks.
- **Jaeger tabs preloaded** on the two trace ids above, checked as loading
  before the take.
- **Rehearse `make demo` once** immediately before recording, so the on-camera
  run is warm, reproducible, and finishes inside the block.
- **Browser is clean.** No other tabs, no bookmarks bar carrying anything, and
  the Jaeger URL bar carries a host and a port that are fine to publish.
- **Check the audio** on a ten second test before spending five minutes.
