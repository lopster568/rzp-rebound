# ADR-0003: the policy gate runs server side, in two layers

| | |
|---|---|
| Status | Accepted |
| Date | 2026-08-31 |
| Applies from | Phase 2, enforced in phase 3 |

## Context

The agent proposes actions. Something has to be able to refuse, and the refusal
has to sit on the only path from a proposal to a side effect (ADR-0001).

Two metrics depend on where that gate goes. `policy_violations_attempted`
counts proposals the policy refused, and it should not be zero: an agent that
never proposes anything out of bounds has not been tested against a policy.
`policy_violations_succeeded` counts actions that reached a side effect with no
policy verdict behind them, and it must be 0.

A gate in one place has one failure mode. A per-handler check can be forgotten
when a new tool is written. A generic middleware cannot judge an action whose
arguments it does not understand, so it cannot enforce a spend budget, which
needs the amount.

## Decision

Two layers, both inside the server process, both ahead of any side effect.

**Layer 1, middleware around every MCP tool call.** It knows nothing about what
the tool does. It checks the kill switch, the per-batch spend budget, and the
order allowlist, and it opens a span carrying the verdict. It applies to every
registered tool, including one written after this document.

**Layer 2, `policy.Evaluate` as the first statement of every action handler.**
It gets the specifics: order id, failure class, attempts already made, amount
in paise. It returns allow or deny plus the rule that decided (FR-POL-1).

Unbypassable is proven rather than asserted.
`TestEveryActionToolConsultsPolicyBeforeSideEffect` (planned, phase 3)
enumerates the registered action tools and fails when any handler can reach a
side effect without a recorded policy verdict. At runtime,
`policy_violations_succeeded` counts what got through, and the report puts that
number on its front page.

## Consequences

- Each layer covers the other's failure mode. A forgotten `Evaluate` still
  meets the middleware; an action the middleware cannot judge still meets
  `Evaluate`.
- Every action costs one more evaluation and one more span. At the batch sizes
  in scope that is not a budget anyone will notice.
- A denied action still writes an audit row (FR-REC-1). Refusals are countable,
  which is what turns `policy_violations_attempted` into a metric instead of a
  silence.
- Budget-shaped rules can fire in both layers. The audit row names which layer
  refused, so a double refusal reads as one denial with a known origin.
- The kill switch is the single control that stops everything, and it lives in
  the layer that cannot be skipped.
- A new action tool that skips `Evaluate` fails a test instead of shipping.

## Alternatives considered

**Policy in the prompt.** The model is told the limits and asked to respect
them. Nothing enforces it and nothing measures it, which fails PRD goal 2 on
both counts.

**Policy only in the action handlers.** Precise, and one forgotten call is one
unbounded tool. Nothing sweeps for the omission at runtime.

**Policy only in middleware.** Uniform and unforgettable, and a budget check
that cannot see the amount is not a budget check. It would enforce the kill
switch and the allowlist and nothing else.

**A separate policy service.** Another process to start for a demo and another
way for a run to fail. It buys no isolation either: the gate has to sit on the
same side of the boundary as the credentials, which is this process.

**Client-side policy in the agent harness.** The harness is the component being
evaluated. A gate there measures whether the agent's own wrapper works, not
whether the agent is contained.
