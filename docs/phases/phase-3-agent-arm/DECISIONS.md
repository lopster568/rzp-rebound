# Phase 3 decisions

Choices made while the work happened, with the alternative that was rejected.
Anything that outlives this phase goes to `docs/decisions/` instead.

## 1. One server process per order, not one per batch

`cmd/rzp-mcp` takes one manifest order id and serves one order.

Three reasons. The fake gateway is in memory, so a separate `rzp run` process
could not share it and the agent arm would have needed its own materialiser,
which is the one thing that has to be identical across arms. One invocation per
order is what the night's budget counts, and a budget that counts something
other than what it caps is not a budget. And an agent that cannot see the next
order cannot spend one order's budget on another.

The cost is written into `PLAN.md` rather than discovered later: `R5-ACTION-BUDGET`
reads `store.ActionsThisRun`, which in a one-order process is that order's
actions. For `a2-agent` the run-wide budget is a per-order budget. It is set
low enough that a model looping on one order hits it, which is the containment
property a per-order process can actually offer.

## 2. `record_decision` gates the action tools, and the gate is layer 1

An action tool for an order is refused until a decision for that order is on
the record, with rule id `M3-DECISION-REQUIRED`.

The reason is FR-AUD-1. A compliance reviewer picks one action and
reconstructs why it was taken. For a deterministic arm the "why" is the rule
set, which is in the repository. For a model it is the reasoning the model
stated, and reasoning stated after the fact is a reconstruction rather than a
record.

The gate is in the middleware rather than in each handler, so a tool written
after this document is gated without anybody remembering to gate it. That is
the same argument ADR-0003 makes for the kill switch.

Rejected: asking for the reasoning as an argument on every action tool. It
gets the text but not the ordering, and the ordering is the property worth
having.

## 3. The two allowlist rules get M-prefixed ids, the two budget rules keep theirs

`M1-TOOL-ALLOWLIST` and `M2-ORDER-ALLOWLIST` are new rules and get new ids.
`R8-KILL-SWITCH` and `R5-ACTION-BUDGET` are existing rules that the middleware
can also enforce, and they keep their ids with the layer recorded in the audit
row's detail.

ADR-0003 already says a budget-shaped rule can fire in both layers and that the
row names which layer refused. Giving the middleware's kill switch a different
id would make one control read as two in the escalation-by-rule split.

## 4. `create_payment_link` commits nothing, `resend` commits the notification

The two tools are one action in the rules arm and two here, because the agent
needs the link id back before it can send. Committing the link creation moves
either `LastNotifyAt`, which makes R6 refuse the resend the link exists for, or
the order's attempt count, which is a category error: `batch.MaxLegitAttemptsFor`
counts payment attempts and a payment link is not one. `PROBLEMS.md` entry 3
has the arithmetic.

So the link raises without committing and the resend commits with the notify
action. What bounds link raising is the invocation's action budget in layer 1.

The honest cost: an agent with budget left can raise several links for one
order and only the last resend is rate limited. The budget is the bound, and it
is a cruder bound than R6 is.

## 5. `escalate_to_human` proceeds on an escalate verdict, and is stopped by a deny

Every action tool runs `policy.Evaluate` first. For escalation the verdict that
matters is different: `R7-UNKNOWN-FAIL-CLOSED` and `R4-NEVER-RETRY-CLASS`
return escalate, and those are exactly the orders where handing the work to a
person is right. An escalation refused because the policy said escalate would
be the system refusing to do the thing it just asked for.

So escalate passes on allow or escalate, and stops on deny. The kill switch, a
replay, and an exhausted budget all still stop it. The evaluation is recorded
either way, so the verdict behind an escalation is in the ledger.

## 6. The agent sees the classifier's reading, because the rules arm does

`get_payment_detail` returns `failure_class`, which is
`classify.Classify` over the error fields the gateway returned.

That is parity, not a leak. `internal/classify` is a component of this system
doing its job on observable input, and `a3-rules` gets the same reading from
the same function. An arm that had to infer the class from raw error strings
while another was handed it would be a comparison of inputs rather than of
decisions, which is what FR-REC-4 exists to prevent.

The charter in `prompts/agent_system.md` states what each class means for the
same reason. The rules arm's class-to-action table is in the repository; giving
the model the same table is parity, and withholding it would measure whether
the model happens to know Razorpay's semantics.

`TestToolResponseNeverContainsGroundTruthFields` draws the line and says where:
the class and the failure reason are observations, everything else in the
manifest is the answer.

## 7. A new column for the agent's cost, and `n/a` for arms that have none

`agent_invocations`, `agent_input_tokens`, `agent_output_tokens`,
`agent_cost_usd`, and `agent_wall_clock_ms`, summed from `invocations.jsonl`,
at scope `overall` only.

Every invocation counts, including the unscorable ones. An invocation that
failed for an infrastructure reason spent the same subscription as one that
produced a decision.

The three deterministic arms read `n/a` rather than 0, for the reason the
escalation rates do: a zero there is a claim about a model call that did not
happen. Per-class rows read `n/a` too, because a per-class split would be read
as the cost of handling that class and what it would be is the cost of however
many orders of that class this batch happened to have.

## 8. `policy_violations_attempted` was not redefined to make it non-zero

Phase 2 left an open question shaped like a trap. ADR-0003 says
`policy_violations_attempted` should not be zero, because an agent that never
proposes anything out of bounds has not been tested against a policy. As
implemented in `harness/scorer.py` it counts action rows that reached a side
effect while carrying a refusal verdict, which in a correct system is zero by
construction and stays zero however hard the agent pushes.

The two readings are not the same number and the published one was not moved to
fit the sentence. What the phase 3 brief actually asks, whether the agent
attempts policy violations and whether any succeed, is answered by two columns
that already exist plus one that did not:

- `policy_violations_succeeded` stays exactly as defined and must be 0.
- `policy_refusals` counts refusals of actions the arm proposed. For
  `a3-rules` those are refusals of what its own table dictated; for `a2-agent`
  they are refusals of what a model asked for, which is the number ADR-0003 is
  reaching for.
- Every tool call, allowed or refused, is a `tool_call` ledger row carrying the
  gate verdict and the rule, so a refused action tool call is countable
  directly.

`RESULTS.md` states the distinction rather than letting one number carry two
meanings.

## 9. The gate's rule order mirrors `policy.Evaluate`'s

Kill switch, then the two allowlists, then the decision requirement, then the
budget. A halt beats every reason a call might otherwise be fine, which is why
R8 is first in both layers. Then the rules that say this call names something
that does not exist. Then the one about the order. Then the one about the
invocation.

Two layers whose rules fired in different orders would make a double refusal
hard to read, and the audit row names the layer precisely so a reader does not
have to guess.

## 10. `verify-phase-3` caps the agent at two invocations

The gate runs all four arms over the 40-order batch and stops `a2-agent` after
two orders. The orders past the cap get an outcome row saying they were not
run, so the table says what it did rather than looking like a short batch.

The gate exists to prove the pipeline runs end to end and that nothing got past
the gate. Driving forty headless invocations to learn that would spend a
subscription on a build step, and the number that matters,
`policy_violations_succeeded`, is 0 or not on two orders as much as on forty.
