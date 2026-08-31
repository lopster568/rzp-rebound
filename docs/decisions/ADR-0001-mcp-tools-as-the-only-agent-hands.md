# ADR-0001: MCP tools are the agent's only hands

| | |
|---|---|
| Status | Accepted |
| Date | 2026-08-31 |
| Applies from | Phase 3 |

## Context

Phase 3 puts a language model in a loop that moves money. The model is the one
component in this system whose next output cannot be predicted from its
inputs, and PRD goal 2 says action must be provably bounded, ranked above
recovery rate.

The credentials are real test-mode Razorpay keys, loaded by `internal/config`
from `RAZORPAY_KEY_ID` and `RAZORPAY_KEY_SECRET`. The actions available are the
six calls on `razorpay.Port`, three of which have a side effect: `CreateOrder`,
`CreatePaymentLink`, and `ResendPaymentLinkNotification`.

A containment claim has to be measurable. `policy_violations_succeeded` must
read 0, and a number like that can only be computed if there is exactly one
path from the model to a side effect.

## Decision

The model's entire reach is the MCP tool set served by `cmd/rzp-mcp` through
`internal/mcpserver`. It gets no shell, no HTTP client, no filesystem, and no
credentials. Every hand it has is a Go function in this repository, and every
action tool consults the policy before its first side effect (ADR-0003).

The keys stay in the server process. The model holds tool names and arguments.

## Consequences

- What the agent can do is a list of Go functions, so the capability set is
  enumerable rather than argued about (FR-MCP-1).
- Text arriving from an order note or a receipt string cannot become an API
  call. The worst it can do is talk the model into invoking a tool that already
  exists, which the policy then evaluates.
- Every tool call is a span (FR-MCP-3), so the audit trail is built from what
  the process observed, not from the model's account of what it did.
- Adding a capability means writing a tool, a policy rule, and a test. That is
  slower on purpose.
- The ceiling on the demo is whatever we anticipated. The agent cannot surprise
  us in a good way either, and that cost is accepted.

## Alternatives considered

**Give the model the keys and a generic HTTP tool.** Fastest to build, and
containment becomes unprovable. There is no single place to put the check, so
`policy_violations_succeeded` is a number nobody can compute.

**Give the model a shell or a code-execution tool.** Same problem, plus
arbitrary execution inside a process holding payment credentials.

**Let the model act freely and review the audit trail afterwards.** A detector
finds the violation after the money moved. The metric this project reports is
that no violation succeeded, and post-hoc review cannot produce it.

**Put a human approval step in front of every action.** That is a different
product. It also makes autonomy unmeasurable, because every recorded action
carries a person's judgment as well as the agent's.
