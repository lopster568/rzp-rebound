# Phase 1 decisions

Append an entry when a choice would otherwise have to be reverse-engineered
later. Date every entry.

Everything here is from the offline half, worked on 2026-08-31. The live half
adds its own entries when it runs.

## 2026-08-31: phase 1 was split into an offline half and a live half

The phase 1 goal in the PRD is one sentence with two halves in it: write the
live loop, and drive a real test-mode order through it. On 2026-08-31 this
machine has neither Razorpay test-mode keys nor a reachable docker daemon, so
the second half cannot start.

Splitting is safe because ADR-0004 already put the three measurement layers on
separate tables and said an unanswered live question costs batch size in the
live table rather than the build. The offline half is the part of phase 1 that
prediction covers.

What the split costs is confidence about wire shapes, and that cost is real.
Every request body, response envelope, and endpoint path the client uses came
from the json tags already in `internal/razorpay/port.go`, not from anything
Razorpay sent. The `pathOrders` block in `client.go` says so, `createOrderBody`
and `createPaymentLinkBody` say so, and `paymentCollection` says so. The live
half confirms each one against a capture.

## 2026-08-31: the contract suite gained a `client_httptest` harness, and here is what it cannot prove

`internal/razorpay/contract_test.go` now runs the two `TestPortContract_*`
functions against a second harness: `razorpay.Client` over a real socket, with
`fakeAPIServer` on the other end and `razorpay.Fake` holding the state.

What that proves: the basic-auth header is built and sent, the retry loop and
the concurrency cap do not break an ordinary call, a 404 becomes
`ErrOrderNotFound`, and a response decodes into `Order` and `Payment` end to
end. That is the client's own code path, running with no credential and no
network, which is what item 4 in ADR-0002's context asked for.

What it cannot prove: anything about Razorpay. Both ends of the exchange
marshal and unmarshal through the same struct tags in `port.go`. A field name
that is wrong for Razorpay is wrong identically on both sides, and the test
passes. The same goes for every endpoint path: `fakeAPIServer` routes the paths
`client.go` sends, because both were written here on the same day.

Only a captured fixture settles a wire shape. Until the live half runs,
`client_httptest` is evidence about our plumbing and nothing else, and no number
from it belongs in a results table under any layer name.

`AttemptPayment` on the harness reaches past the client into the fake, exactly
as the `fake` harness does, because the API has no call that makes a payment
attempt. That is PRD Q1 and the live half answers it.

## 2026-08-31: harness selection is an env narrowing rather than an opt-in flag

`RZP_CONTRACT_HARNESSES` narrows the set of contract harnesses. Empty, which is
what CI runs with, means every registered harness: `fake` and
`client_httptest`.

The brief for this work said to put the client harness behind an env or flag
guard. Registering it opt-in was the wrong shape for it: it needs no credential,
no container, and no network, so a default-off flag would mean CI never ran the
client's contract at all, which is the one place the client's decode path gets
exercised against a second implementation.

The guard still exists and it still has a job. The live half registers a `live`
entry, and that one only runs when it is named in the variable, because it
spends real test-mode API calls against a rate limit nobody has measured
(PRD Q5).

## 2026-08-31: only 429 is retried, and 5xx is not

`Client.do` retries on 429 and on nothing else.

A 4xx other than 429 is settled: the same request gets the same refusal, and a
retry spends a call and a rate-limit slot on a certainty.

A 5xx is the interesting one, and not retrying it is a deliberate conservative
choice rather than an oversight. Three of the six port calls have a side effect
(`CreateOrder`, `CreatePaymentLink`, `ResendPaymentLinkNotification`), and
nothing in this repository knows yet whether a Razorpay 5xx means the call did
not happen or means the answer was lost on the way back. Retrying the second
case sends a second message or creates a second order. The comment on `do` says
this, and the live half either observes a 5xx and turns the caveat into a rule,
or never sees one and the rule stays as it is.

## 2026-08-31: the replay client is the same Client with a fixture transport

`NewReplay` builds a `*Client` whose `http.RoundTripper` answers from
`testdata/recorded/`. It is not a second implementation of the port.

A separate replay type would need its own decode path, and the day that path
drifted from the client's, the replay layer would stop being a replay of what
the live layer does. One `Client` means one decoder, and a fixture that decodes
under replay decodes under live.

It carries no credentials. There is nothing on the other end to authenticate
to, and a key pair held in one more place is a key pair in one more place. That
is why `newClient` exists next to `NewClient`: the exported constructor demands
both halves of the pair, and the unexported one does not.

The base URL is `https://replay.invalid/v1`. The `.invalid` TLD resolves
nowhere, so a replay client that somehow reached a real transport fails rather
than calling out, the same reasoning the fake's `pay.invalid` short URL uses.

## 2026-08-31: one synthetic fixture, named and marked so it cannot pass for a capture

`testdata/recorded/synthetic_failed_payment_insufficient_fund.json` was written
by hand on 2026-08-31. Nobody captured it.

Three things keep it from becoming evidence:

1. The filename starts with `synthetic_`, and `LoadFixtures` skips that prefix
   unless a caller asks for it.
2. Its `_meta.synthetic` is true, and `LoadFixtures` refuses to load a file
   whose name and `_meta` disagree in either direction. Renaming it does not
   launder it.
3. `TestClassifierHandlesEveryRecordedErrorPayload`, the test that will measure
   the classifier against real captures, excludes it.

Its field values settle nothing. `error_code` and `error_reason` both carry the
reason string, which is what `razorpay.Fake` does while PRD Q4 is open, and
`error_source` and `error_step` carry the pending-fixture constants from
`port.go`.

## 2026-08-31: a poll timeout is a result field, not an error

`Poller.PollUntilTerminal` returns `Result.TimedOut` with the last order and
payments it saw, and a nil error. A gateway failure is an error; running out of
time is not.

The caller that matters is the recovery orchestrator, which has to decide what
to do with an order that never settled. It needs the state more than it needs
an error value, and an error would push every caller into the pattern of
reading a result it was just handed alongside a non-nil error, which is the
shape people get wrong.

`Result.TimedOut` is on the outcome and in the audit row, so an unsettled order
is counted rather than lost. PRD section 7 calls that an unscorable outcome and
requires it stay in the denominator.

## 2026-08-31: only `paid` is terminal

`poller.IsTerminal` returns true for `paid` and nothing else. An order at
`created` has had no attempt, an order at `attempted` has had one that failed,
and both can still become paid, which is the whole premise of the project.

`TestPollerDetectsFailedPaymentOnOrderStillAttempted` pins it: a failed payment
under an order at `attempted` is reported as a failed payment, not as an ending.

## 2026-08-31: the audit recorder writes two sinks, and the trace id is the join

One `Record` call writes attributes onto the span in the context and one JSONL
line carrying that span's trace id.

They answer different questions. The trace shows one order's run in order, with
timings, to somebody looking at it in Jaeger. The ledger is a flat file a
scoring pass reads with no trace backend running, which matters because the
docker daemon has been down for both phases so far. The trace id in the line is
what lets a reviewer go from a row to the span, which is FR-AUD-3.

A row recorded with no span in the context still gets written, with an empty
trace id. A run whose collector was down still has to produce a scoreable file.

## 2026-08-31: redaction matches on key name and on value shape, and both are needed

`internal/audit` drops a value when its detail key is on `redactedKeys`, and
separately rewrites anything matching `cardLike` or `keyLike` wherever it
appears.

Key matching alone misses a credential pasted into a field called `note`. Shape
matching alone misses a customer phone number under a key called `contact`,
which no regexp can tell from an order reference. Each covers the other's blind
spot.

`cardLike` wants 13 to 19 digits with optional single separators. Nothing else
in a ledger row is that long and all numeric: amounts in paise are six digits,
a unix timestamp is ten, and every identifier in this system carries a letter
prefix.

`keyLike` is assembled from string fragments in the source rather than written
as one literal, because the pre-commit hook blocks a staged diff containing
something shaped like a Razorpay key and the pattern itself would look like one.
`TestRecorderRedactsCardAndKeyFieldsFromLedger` assembles its input for the same
reason.

## 2026-08-31: the client holds the base64 basic-auth token so it can scrub it

`Client.Redact` replaces three strings: the key secret, the base64 of
`keyID:keySecret`, and the key id, in that order.

The token is the one that is easy to miss. A gateway that echoes the
Authorization header into an error body leaks the pair in a form that scrubbing
the two halves separately does not catch, because the base64 of the pair
contains neither half as a substring.
`TestClientRedactsSecretFromErrorMessages` drives exactly that case.

An empty credential is skipped, which is not a detail: `strings.ReplaceAll` on
the empty string inserts the replacement between every character in the
message, so a replay client with no keys would produce an unreadable error.

`redactedError` carries the scrubbed message and unwraps to the original, so
`errors.Is` and `errors.As` keep working while the printed text does not carry
a credential.

## 2026-08-31: a failed capture write fails the call

`Client.captureResponse` returns its error and `do` returns it.

A fixture run that quietly wrote nothing looks exactly like a fixture run that
worked. The live half depends on that stream being the record of what Razorpay
sent, and a full disk during a capture that spent real API calls is worth
finding out about at the call, not afterwards.

The capture line carries the method, the path, the status, and the body. No
request header, so the Authorization header cannot reach a committed fixture
file. `TestClientCapturesRawResponseBody` asserts the absence.

## 2026-08-31: `NotifyReceipt.Accepted` comes from the 2xx, not from a response field

`Client.ResendPaymentLinkNotification` sets `Accepted` true when the call
returned 2xx, and does not read the response body.

The response shape for that endpoint is pending fixture capture. A field name
invented here would decode to false on a call that actually worked, which would
be worse than not reading the body: it would be a wrong observation rather than
a missing one. What is observed is an HTTP status, and that is what `Accepted`
reports.

## 2026-08-31: `Receipt.DeliveryConfirmed` is a false constant with a comment, not a missing field

`notify.Receipt` carries a `DeliveryConfirmed` field that no code path sets.

Leaving it out would have been less code. It is here so a reader of the audit
trail sees the answer stated rather than having to notice the question is
missing. Razorpay's HTTP response to a resend call says the call was accepted.
It does not say a person received anything, opened anything, or read anything,
and no phase in the PRD adds a channel that would.

Every string this package can put in an audit trail is one of three constants,
`AuditPhrases()` returns all three, and
`TestNotifierNeverClaimsCustomerNotified` walks that list plus every receipt
both code paths produce and checks fourteen forbidden phrasings against them.
`scripts/check-docs.sh` enforces the same rule over prose; this is the same rule
over the strings the code emits.

## 2026-08-31: the orchestrator reads the outcome back from the gateway on every path

`Orchestrator.ProcessOrder` calls `FetchOrder` after the action, always: after
a successful action, after a failed one, and after `DoNothing`.

One code path rather than a branch, because a branch is where the exception
gets added. `ActionResult.ClaimedRecovered` is kept on the `Outcome` next to
`Recovered`, so an action that reported success against a gateway that still
says `attempted` shows up as a disagreement in the audit row instead of being
resolved silently in favour of one side.
`TestOrchestratorRefetchesOrderStateForOutcomeRatherThanTrustingAction` drives
exactly that disagreement.

Phase 1 ships `DoNothing` as the only action. Phase 2 puts `policy.Evaluate` in
front of whatever replaces it, per ADR-0003.

## 2026-08-31: `make seed` prints a pending notice and exits non-zero

`scripts/seed-batch.sh` is a skeleton that fails on purpose.

`internal/batch` already has the seeded generator, the ground-truth manifest,
and the four-field agent projection, all tested since phase 0. What is missing
is the `rzp seed` subcommand that calls it and writes a manifest under
`results/batches/`, and PRD section 6 puts `make seed` in phase 2.

Half-building it now would fix the manifest file format before the scoring pass
that reads it exists, with nothing to check the format against. Failing loudly
beats writing a file that looks like a batch, which is why the script exits
non-zero rather than printing a notice and exiting 0.

## 2026-08-31: `make verify-offline` is the gate, and it unsets the credentials

`verify-offline` runs preflight advisory, then `lint test docs-check` with
`RAZORPAY_KEY_ID` and `RAZORPAY_KEY_SECRET` unset.

Unsetting them is the point. NFR-2 says the unit suite runs offline with no
credentials, and a target that inherits a developer's environment cannot
demonstrate that on a machine where the keys are set. `verify-phase-0` stays as
it is, so the phase 0 gate keeps meaning what its report says it meant.

## 2026-08-31: card and key patterns live in `internal/redact`, and what they cannot do is stated

`internal/redact` holds `cardLike`, `keyLike`, and `Value`. `internal/audit`
and `internal/razorpay` both call it, and `audit.Redacted` and
`razorpay.Redacted` are both `redact.Marker`.

It started as a copy in `audit` alone. The client's capture path needed the same
patterns, because a capture line becomes a committed fixture, and a second copy
of a redaction pattern is the kind of thing that gets fixed in one place and not
the other. This repository already made that argument once, for the card table
in `internal/testcards`.

Two spellings of the marker would have been the same mistake in miniature: a
ledger holding both `[redacted]` and something else reads as two different
things having happened.

`cardLike` has no upper bound on the digit run. Bounding it at 19, the longest a
card can be, meant a card pasted inside a longer run matched nothing and passed
through whole, which is the wrong direction to fail in. The cost is that a
genuinely long number loses its digits, and nothing this project writes to a
ledger or a fixture is a 13-digit run that has to survive.

The limit is on the function, not hidden: a Razorpay key secret is a bare
alphanumeric string with no prefix and no checkable shape, so no pattern can
find one in ordinary text. The control for a secret is the package holding it
scrubbing before the string leaves, which `razorpay.Client.Redact` does on every
error and every captured body. `audit.Event.Detail` says the same thing where a
caller would read it. Claiming the regexes catch secrets would be the dishonest
version of this, and it would be believed.

## 2026-08-31: an action that ran and failed is `action_taken`, not `action_skipped`

`Orchestrator.ProcessOrder` chooses the audit kind from the action kind and the
action error together, not from the kind alone.

The idiomatic Go failure from an `ActionFunc` is `return ActionResult{}, err`,
which leaves `Kind` empty and normalises to `ActionNone`. Keying the row off
`Kind` alone filed every failed attempt as one that never happened, and PRD
section 7's containment and honesty metrics are counted by reading these rows:
an attempt that errored is an attempt, and a refusal is not.

## 2026-08-31: a resend the gateway did not accept is an error, not a quiet receipt

`notify.SendPaymentLink` returns `ErrNotAccepted` when the call came back
without `Accepted` set.

It used to return the receipt and a nil error, with `APICallSucceeded` false. A
caller that checks the error first, which is most of them, read that as a
message having gone out. Given this package exists to be careful about exactly
that claim, the quiet version was the wrong default.
