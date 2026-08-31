"""Build the run configuration for one arm.

The four-arm table only means anything if the four arms ran under the same
settings. This module is the one place those settings are decided, so that
"identical except the decision maker" is a property a test can check rather
than a claim a reader has to take on trust.

`differing_keys(build(a), build(b))` is that check. For any two arms it must
return exactly two keys: `arm`, which is the label, and `decision_maker`, which
is the substance. A change that gave the agent a larger action budget, a
different card, or a different layer would add a third key and fail
`harness/test_arm_config.py`.
"""

from __future__ import annotations


ARM_CONTROL = "a0-control"
ARM_NAIVE = "a1-naive"
ARM_AGENT = "a2-agent"
ARM_RULES = "a3-rules"

# Report order. a2 sits between the naive arm and the rules arm because that is
# where the interesting comparison is: the phase 2 table is a trade between
# recovery and false actions, and the question phase 3 asks is where an agent
# lands inside it.
ARMS = (ARM_CONTROL, ARM_NAIVE, ARM_AGENT, ARM_RULES)

# What decides an action, per arm. This is the only substantive difference
# between two arm configs, and the test that says so reads this dict.
DECISION_MAKERS = {
    ARM_CONTROL: "deterministic: take no action",
    ARM_NAIVE: "deterministic: retry every failure",
    ARM_AGENT: "llm: claude sonnet, headless, through the mcp tool set",
    ARM_RULES: "deterministic: classify, then policy.Evaluate, then act or escalate",
}

# The two keys allowed to differ between two arm configs.
IDENTITY_EXEMPT = ("arm", "decision_maker")


def build(
    arm: str,
    *,
    batch_path: str,
    batch_id: str,
    layer: str,
    run_dir: str,
    card: str = "4100280000080001",
    currency: str = "INR",
    kill_switch_file: str = "",
    action_budget: int = 0,
    policy: dict | None = None,
) -> dict:
    """Return the run configuration for one arm.

    Every value below is the same for every arm. The only two that read `arm`
    are the label and the decision maker, which is the property
    `differing_keys` exists to check.
    """
    return {
        "arm": arm,
        "decision_maker": decision_maker(arm),
        "layer": layer,
        "batch_path": batch_path,
        "batch_id": batch_id,
        "run_dir": run_dir,
        "card": card,
        "currency": currency,
        "kill_switch_file": kill_switch_file,
        "action_budget": action_budget,
        "policy": policy if policy is not None else {},
    }


def differing_keys(left: dict, right: dict) -> set[str]:
    """Keys whose values differ between two configs, including keys present in
    only one of them.

    A missing key counts as a difference rather than as a match against a
    default. Two configs where one omits `card` are not two configs that agree
    about the card: they are one config that names it and one that leaves it to
    whatever the binary happens to default to.
    """
    missing = object()
    keys = set(left) | set(right)
    return {k for k in keys if left.get(k, missing) != right.get(k, missing)}


def decision_maker(arm: str) -> str:
    """The decision maker for an arm.

    An unknown arm raises rather than returning a default. A typo in an arm id
    that produced a config would produce a run, and a run that scored, and a
    row in the table under a name nobody meant.
    """
    try:
        return DECISION_MAKERS[arm]
    except KeyError:
        raise KeyError(
            "unknown arm " + repr(arm) + ", want one of " + ", ".join(ARMS)
        ) from None
