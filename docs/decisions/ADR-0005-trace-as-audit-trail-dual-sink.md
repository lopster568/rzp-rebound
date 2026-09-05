# ADR-0005: the trace is the audit trail, one event to two sinks

| | |
|---|---|
| Status | Accepted |
| Date | 2026-09-01 |
| Applies from | Phase 1 |

## Context

This decision was made on 2026-08-31, during phase 1's live loop work, as part
of building `internal/audit`. It has no ADR of its own until now: phase 5's
numbering sweep on 2026-09-01 found the gap between ADR-0004 and ADR-0007, and
this file closes it.

Two readers need one event for different reasons. A compliance reviewer picks
one action and reconstructs why it was taken (FR-AUD-1), which wants a
timeline: the classification, the policy verdict, and the side effect nested
under the order that produced them, in order, with durations. A scoring pass
needs the same event to compute a rate across a whole batch with nothing
watching, which wants a flat, greppable line it can join to the ground-truth
manifest.

A trace serves the first well and the second badly. `docker` has been down for
both phases run so far (phase 1 `DECISIONS.md`), and a scoring pass cannot
depend on a live Jaeger backend being reachable at report time. A flat JSONL
ledger serves the scorer well and the reviewer badly: a bare row has no
nesting under the tool calls and checkout steps that produced it, so a
reviewer reading one has nothing to open.

Keeping two independently written trails, one call site that opens a span and
a separate call site that appends a ledger line, is the same drift risk this
project already named for the test-card table in `internal/testcards`
(phase 0 `DECISIONS.md`) and for the redaction patterns in `internal/redact`
(phase 1 `DECISIONS.md`): two copies of one fact, written by two call sites,
disagree the day someone edits one and not the other, and an audit trail that
can disagree with itself does not tell a reader which half is wrong.

## Decision

One `audit.Recorder.Record` call per event. Internally it writes the event to
two places from one `Event` value: a JSON line to the configured `io.Writer`,
and attributes on the span the caller's `context.Context` carries, pulled with
`trace.SpanFromContext(ctx)`. `trace_id` on the ledger row is the join back to
the span.

The ledger write happens first. If it fails, `Record` returns the error and
never touches the span, so a run that could not write its ledger does not also
produce a trace attribute implying that it did.

A row recorded with no span active in the context still writes, with an empty
`trace_id`. A run whose collector was down still has to produce a scoreable
file (phase 1 `DECISIONS.md`), which is the concrete case this project has
already hit twice.

Both sinks go through `internal/redact` on the same pass, before either one
sees a value (`ARCHITECTURE.md`, "The trace is the audit trail"). There is one
redaction call, not one per sink.

The ledger row kinds are a closed set: `classified`, `policy_evaluated`,
`action_taken`, `action_skipped`, `notification_requested`,
`outcome_observed`, plus `tool_call` and `decision_recorded` written directly
by the MCP server, and `intervention_applied`, `escalation_raised`, and
`promise_logged` written by `internal/intervene`. The last three split on
whether anything was called and on what it was: an escalation and a logged
promise reach no Razorpay resource, so filing either as an intervention would
put a refusal to act automatically in the same bucket as an action.
A denied action still writes a row, which is what FR-AUD-1
asks for: a refusal has to be reconstructable from the trail the same way an
allow is.

## Consequences

- A span attribute takes the last write for a key. A run of several orders
  through one span would overwrite one order's detail with the next's; the
  ledger keeps every row separately. That is the second reason both sinks
  exist, not just the offline-scoring one: within a single span, the trace
  alone would already lose history the ledger does not.
- `recovered` and `claimed_recovered` sit next to each other in the same row
  on purpose (`docs/AUDIT-TRACE-SCHEMA.md`). `recovered` comes from a
  `FetchOrder` after the action; `claimed_recovered` is what the action said
  about itself. When they disagree, the row shows the disagreement instead of
  resolving it toward either sink.
- Treating the trace as one of the two audit sinks, rather than throwaway
  debug output, raised the bar on what a span attribute is allowed to carry.
  `otelhttp`'s default `url.full` attribute put the Razorpay key id into six
  span attributes of one demo run, because two of the four checkout calls
  carry `key_id` as a query parameter and the callback carries it as a path
  segment. It was found by grepping a trace on 2026-08-31 and fixed by taking
  `otelhttp` off `razorpay.Attempter` entirely, because there was no clean
  seam to scrub the attribute after `otelhttp` had already set it from the
  request URL at span start. A trace that was only a debugging aid could have
  tolerated a URL in an attribute; an audit sink cannot.
- The same redaction pass covering both sinks can also corrupt both sinks
  identically from one false positive. `internal/audit` shortens the
  idempotency key to twelve characters in the row because the card-shaped
  pattern matches any run of thirteen or more digits, and a sha256 digest
  contains one about five percent of the time. That had already scrubbed the
  middle out of four committed rows before it was caught.
- Two sinks, one shape, kept in sync by convention rather than by the
  compiler: the span attribute keys (`AttrOrderID`, `AttrKind`, `AttrClass`,
  and the rest) and the ledger's JSON fields are two views of one `Record`
  value, and nothing enforces that a new field is threaded into both except
  that they are written from the same call.
- A reviewer opens a trace and sees one order end to end: a root span with
  the classification, every `tools/call`, and the gateway read-back nested
  under it, because `internal/audit` writes onto whatever span is active
  rather than opening its own.

## Alternatives considered

**Ledger only, no spans.** Cheaper, and no `otelhttp` key-leak risk at all,
because there would be nothing to leak into. Rejected: it drops the nested,
timed view of one order that lets a reviewer open one link and see the whole
cycle (FR-AUD-3), and it drops the requirement that a decision be traceable
through a live investigation tool rather than only a flat file a reviewer has
to reconstruct order by hand.

**Spans only, ledger derived by exporting from Jaeger afterward.** Rejected:
it makes the scoring pass depend on a reachable trace backend at report time,
and the docker daemon has been down for both phases run so far. A number this
project publishes cannot depend on infrastructure that has already failed to
come up twice.

**Two independently maintained writers, one for spans and one for the
ledger, each called at the site that has the information.** Rejected for the
same reason `internal/testcards` has one card table and `internal/redact` has
one pattern set instead of a copy per caller: the first time someone updates
the span attributes for a new event and forgets the ledger line, or the
reverse, the two sinks disagree about what happened, and neither reader can
tell which one is wrong.
