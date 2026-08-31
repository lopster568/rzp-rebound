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

# Live half, 2026-08-31

## 2026-08-31: docker runs on another machine, over ssh, behind one variable

The machine the live half ran on has no docker CLI at all. The build machine
next to it has Docker 29.6.1 and Compose v5.3.0 and answers `ssh` without a
password.

`DOCKER_SSH_HOST` is the seam. Empty means the local docker CLI, which is what
every earlier phase assumed and what a fresh clone still gets. Set to an ssh
destination, `scripts/jaeger-up.sh` and `scripts/jaeger-down.sh` pipe the
compose file over stdin to `docker compose -f -` on that host, and the health
wait curls that host's published ports instead of localhost.
`scripts/preflight.sh` checks ssh reachability and a remote daemon instead of a
local one, so a machine with no docker stops reporting a hard failure about a
daemon nothing was going to use.

The compose file is piped rather than copied. The build machine has no checkout
of this repository, and putting one there would mean two compose files that can
drift, with the one under version control not necessarily the one describing
what came up.

`COMPOSE_PROJECT` is fixed to `rzp` in `scripts/lib.sh` rather than derived
from the working directory. With the file arriving on stdin, docker would name
the project after whatever directory the ssh session landed in, and a teardown
that guessed a different name would find nothing to tear down.

The cost is that the Jaeger UI is not on localhost, which is why
`RZP_JAEGER_UI_URL` exists in the configuration and why `Config.TraceURL` takes
a root rather than building one.

## 2026-08-31: PRD Q1 is answered, and the answer is undocumented surface

`razorpay.Attempter` drives a test-mode payment attempt through four checkout
calls. `docs/RAZORPAY-TEST-MODE-NOTES.md` has the sequence and
`PROBLEMS.md` has the spike.

It is deliberately not a method on `Client` and deliberately not on `Port`:

- `Client` is the documented server API and authenticates with the key pair
  over HTTP Basic. The attempter's four calls take the key id alone, as a form
  field or a query parameter, and two of them answer with HTML pages that have
  to be parsed for a form. Putting both behind one type would tell a caller the
  whole surface carries the same support promise.
- `Port` is what the fake, the replay client, and the live client all satisfy.
  Adding an attempt to it would mean the replay client had to pretend to make
  one, and the phase 2 policy gate sits in front of actions on `Port`.

The HTML parsing is regexp rather than a parser. These are two pages of a
handful of hidden inputs each, the project has no other reason to carry an HTML
dependency, and a page whose shape changed enough to break a regexp has changed
enough that the sequence needs re-checking anyway.
`ErrAttemptSequenceBroke` is the error that says so, kept distinct from a
decode failure further down.

Form actions on those pages are absolute URLs pointing at `api.razorpay.com`.
Only their path and query are followed, resolved against the configured API
root, so a page cannot send a run to a host it was not pointed at and a test
server sees its own address.

## 2026-08-31: the attempter has no otelhttp, and its spans are better for it

`razorpay.Client` keeps its `otelhttp` transport. `razorpay.Attempter` has a
plain one and opens its own span per step.

The reason is a leak that was found in a real trace and is written up in
`PROBLEMS.md`: `otelhttp` records `url.full`, two of the four checkout calls
carry `key_id` as a query parameter, and the callback the last one redirects to
carries it as a path segment. Six span attributes of one demo run held half a
credential pair.

The alternative was to keep the instrumentation and scrub afterwards. That was
rejected because there is no clean seam for it: `otelhttp` sets the attribute
from the request URL at span start, and every place to intervene is either a
wrapper that has to un-rewrite what it rewrote or a span processor that would
need the credentials passed into the telemetry package. Not giving the span the
URL is simpler and cannot be got wrong later.

What replaced it is better telemetry, not a compromise.
`razorpay.checkout.create_payment`, `.authenticate`, `.gateway`, and `.settle`
say which of four undocumented calls a run stopped on. `HTTP POST` did not.

## 2026-08-31: PRD Q4 is settled, and the fake was corrected to match

A real failed payment carries the coarse class in `error.code`
(`BAD_REQUEST_ERROR`) and the specific reason in `error.reason`
(`payment_failed`), with `error.source` `gateway` and `error.step`
`payment_authorization`. Both fields are populated, which is what the
classifier's "reason wins over code" rule assumed and can now rely on.

`razorpay.Fake` used to put the reason string in both fields, because which one
carried it was the open question. It now splits them the same way round. A fake
that answers a settled question the wrong way teaches the wrong shape to every
offline test built on it, and `internal/batch` seeds ground truth through that
fake.

The fake keeps the eight **documented** reason strings rather than the one
observed string. That looks inconsistent and is not: the fake exists to give
the classifier's six classes something to be exercised against, and a fake that
only ever produced `payment_failed` would make every offline test assert the
same fail-closed answer. The card table now records that the documented
reasons are unverified, which is where a reader is told.

`ErrorSourcePendingFixture` and `ErrorStepPendingFixture` are gone, replaced by
`ErrorSourceGateway` and `ErrorStepPaymentAuthorization`. They were placeholders
for exactly this fact.

## 2026-08-31: `payment_failed` classifies as unclassified, on purpose

The only failure reason Razorpay test mode was observed producing names no
cause. Nothing in `payment_failed` says whether a balance, a disabled card, or
a gateway hiccup stopped the charge.

Three options were on the table:

1. Map it to a retry class so the demo shows a retry decision with a reason
   behind it. Rejected: that is inventing a fact, and it is the exact
   dishonesty this project exists to avoid. A retry recommendation traceable to
   a made-up mapping is worse than no recommendation.
2. Map it to `NeverRetry`. Rejected: also a claim. Nothing observed says this
   failure is permanent, and the payments that recovered on a second attempt
   during this phase all started from this reason.
3. Leave it out of the reasons table so it falls to the fail-closed
   `Unclassified` default. Taken.

The cost is visible and was accepted: every live demo run classifies as
unclassified, which reads worse than a green retry decision would. It is the
true answer for a reason string that carries no cause.

What was added rather than left implicit: `payment_failed` is now listed in
`testdata/error_codes.json` with `_meta.pending` and a `pending_reason`, and
`TestClassifierLeavesTheObservedLiveReasonUnclassified` asserts the behaviour.
A reader of that file finds the live reason and the reasoning, instead of a
table of eight documented reasons and no hint that live mode produces none of
them.

The eight-entry reason table stays. It is exercised by the fake and by the
batch seeder, and phase 2's policy work needs a spread of classes to gate.

## 2026-08-31: `NotifyReceipt.Accepted` reads the response body now, and means no more than it did

The resend endpoint answers `{"success":true}`. The offline half inferred
acceptance from the 2xx because the body shape was unknown, and documented that
as a deliberate refusal to invent a field name. The field turned out to exist,
so it is read, with a fallback to the status code when a body carries no
`success` at all.

This makes the observation narrower, not the claim wider. A 200 whose body
reports a refusal is now visible instead of being counted as an acceptance.

What did not change, and will not: a payment link created with no contact on it
and notification off still answered `notify_by/sms` with 200 and
`{"success":true}`. Razorpay's response reports that an API call succeeded. It
does not report that a message was sent, and it certainly does not report that
a person read one. `notify.Receipt.DeliveryConfirmed` stays a false constant
and the audit phrase stays "notification API call succeeded".

## 2026-08-31: `Order.Notes` is a named type with its own decoder

Razorpay sends `notes` as an object when there are notes and as an empty array
when there are none. `PROBLEMS.md` has how that was found.

A named `Notes` type with an `UnmarshalJSON` was chosen over the alternatives:

- `json.RawMessage` plus a helper: pushes the problem to every call site, and
  the call sites are the ones that would forget.
- `map[string]any`: loses the string typing the audit trail relies on, for a
  field whose values are strings.

A non-empty array stays an error. It is not an empty map with a different
spelling, and quietly returning an empty map would drop data that was on the
order without anything saying so.

## 2026-08-31: the demo prints what it is not evidence of, every run

`make demo` ends with a paragraph saying the outcome of each payment attempt
was chosen by the command and sent to the mock bank in one form field, and that
no recovery rate from this layer is evidence that the agent's decision caused a
recovery.

It is printed on every run rather than kept in a document, because the run
output is what gets pasted into a chat, a slide, or a submission, and a caveat
that lives only in `docs/` is a caveat that travels separately from the number
it qualifies.

What the run genuinely establishes is on the line above it: the final order
state was read back from Razorpay after the action rather than reported by the
action. That distinction is the whole point of `Orchestrator.ProcessOrder`
re-fetching, and it survives the fact that the attempt outcome was selected.

## 2026-08-31: the `live` contract harness lives behind the build tag, with the harness registration

`RZP_CONTRACT_HARNESSES` narrows the harness set, and `live` is registered from
`internal/razorpay/live_test.go`, which carries `//go:build integration`.

Registering it in `contract_test.go` and having it skip on missing credentials
was the other option. Putting the registration behind the tag is better: a name
that only exists behind the tag cannot be selected by accident from an untagged
run, and `make ci` cannot reach for a credential even by mistyping a variable.

`make lint` runs `go vet -tags=integration ./...` as well as the untagged vet,
so the tagged file is still type-checked in CI. A test that only compiles on
the one machine that runs it rots.

## 2026-08-31: fixtures are captured through `cmd/rzp`, and the script scans them afterwards

`scripts/capture-fixtures.sh` runs `go run ./cmd/rzp capture` and then greps
every file it wrote for the two key prefixes and for the two configured
credentials.

The grep is not redundant with the client's redaction or with the pre-commit
hook. The hook matches the two key prefixes, which would miss a base64 basic
auth token, and it only sees a staged diff. The client's redaction is the real
control and this is the check on the control, run at the moment the files are
written rather than at the moment somebody remembers to stage them. It looks
for the configured secret by value, which is the only way to look for a key
secret at all: it is a bare alphanumeric string with no shape to match.

## 2026-08-31: both clients pin redirects to their own origin

`pinnedRedirect` in `client.go` is the `CheckRedirect` on `razorpay.Client` and
on `razorpay.Attempter`. A hop to anything other than the scheme and host the
client was built for is refused, and the ceiling is 3 rather than Go's 10.

The alternative was to keep following redirects and rely on Go's own header
rule. That is not enough here, and the reason is specific rather than general:
Go strips the `Authorization` header across a domain change and never strips a
URL, and on the checkout path the credential is in the URL. `PROBLEMS.md` has
the reproduction, including a 307 that replayed the card number and the CVV to
a foreign host.

Same-origin hops are still followed, and that is not a compromise: the settle
call answers 302 and the payment only settles once the callback is followed,
and that callback is on the same origin as the API. `make test-integration`
passing against live test mode is what confirms it rather than the reasoning.

The refusal error names the two hosts and never the URL, because a refused
redirect target is exactly the kind of URL that carries a credential.

## 2026-08-31: the empty-body tolerance belongs to one call, not to `do`

`doWith` takes `tolerateEmptyBody` and only `ResendPaymentLinkNotification`
passes it true.

It was unconditional in `do` when it was written, with a comment about not
turning a successful side effect into a reported failure for a call that moves
money. The comment was right about the resend and wrong about everything else:
all six call sites pass a non-nil `out`, so the tolerance applied to every read
as well. `FetchOrder` returned an empty status and a nil error, and
`ListPaymentsForOrder` returned an empty slice and a nil error.

The list is the one that decided this. An empty slice is a positive claim that
an order has no attempts on it, the poller and the orchestrator both act on
that claim, and a recovery decision made from an invented empty list is exactly
the class of number this project exists not to produce. A read that cannot be
decoded is an error.

## 2026-08-31: a note that is not a string keeps its JSON text

`Notes.UnmarshalJSON` decodes into `map[string]json.RawMessage` and falls back
to the raw text for any value that is not a JSON string.

Returning an error was the other option and it is what the code did. It is the
same defect the type was added to fix, one shape along: the order exists in
Razorpay, the caller gets an error, and nothing knows the id of what it just
read. This project only ever writes string notes, so the case needs an order
created from the dashboard or by another integration, which is exactly the kind
of thing that turns up once and takes an afternoon.

A non-empty array is still an error, and that has not changed. An array is not
a map with a different spelling, and returning an empty map for one would drop
data. A scalar note has a faithful string form; an array of notes does not.

## 2026-08-31: a step is recorded when it is sent

`AttemptOutcome.Steps` gets a name before the call, not after a 2xx.

The trail exists to say what happened. A call that reached Razorpay and had its
effect but whose response failed is a call that was made, and the case that
matters is the last one, where the mock bank has been told to authorize and the
response comes back 500. Recording that as an attempt that never happened is
the wrong direction to be wrong in for a system that moves money.

The cost is that presence in `Steps` no longer means success. That is on the
field's doc comment, and the error returned alongside says which step stopped
the run.

## 2026-08-31: `make` and the scripts load `.env` the same way

Every shell script sources `scripts/lib.sh` and calls `load_dotenv`. The
Makefile's `RUN_WITH_ENV` goes through the same function for the Go
entrypoints, and the shell targets do not use it at all because the scripts
already do it themselves.

`set -a; . ./.env; set +a` was the first version and it was wrong in three
ways, all of them found in review. Sourcing a missing file is fatal under dash,
so a fresh checkout could not run `make jaeger-down`, a target that needs no
credentials. `set -a` gives the file precedence over the caller's exported
environment, which is the opposite of the rule `load_dotenv` documents and
implements, so the same variable resolved differently depending on which entry
point was used. And sourcing executes command substitution inside a value.

One loader, one precedence rule, one place to fix the next parsing bug.
