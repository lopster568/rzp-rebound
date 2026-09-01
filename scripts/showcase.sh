#!/usr/bin/env bash
# The guided tour. Four acts in one terminal: why failed payments matter, the
# loop running for real, what the committed run measured, and where the proof
# is.
#
# Usage:
#   scripts/showcase.sh          # pauses for Enter between acts
#   NO_PAUSE=1 scripts/showcase.sh   # no pauses, for a screen recording
#
# Every number in act 3 is parsed out of results/tables/phase-5-fake-ethoca.csv
# at run time. None of them is written into this file. A showcase that hardcoded
# its own results would be the exact failure the claims gate exists to catch.
#
# Act 2 needs test-mode keys in .env and a reachable gateway. Without them it
# says what is missing, prints the command that fixes it, and carries on. It
# never prints a demo that did not run.
set -uo pipefail

ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
# shellcheck source=scripts/lib.sh
. "$ROOT/scripts/lib.sh" || {
	printf 'error: cannot source %s/scripts/lib.sh\n' "$ROOT" >&2
	exit 1
}
cd "$ROOT" || die "cannot cd to $ROOT"
load_dotenv "$ROOT/.env"

case "${1:-}" in
-h | --help)
	sed -n '2,17p' "$0"
	exit 0
	;;
'') ;;
*) die "unknown argument: $1" ;;
esac

TABLE="results/tables/phase-5-fake-ethoca.csv"
TRACE_RUN="results/runs/phase-3-fake"

# pause waits for the viewer between acts. NO_PAUSE=1 skips every pause, which
# is what a screen recording wants. A non-interactive stdin skips them too, so
# piping this into a file does not hang a build.
pause() {
	[ "${NO_PAUSE:-0}" = "1" ] && return 0
	[ -t 0 ] || return 0
	printf '\n    [Enter] to continue '
	read -r _ || true
	printf '\n'
}

# ---------------------------------------------------------------- act 1

heading "ACT 1 of 4   THE PROBLEM IS REAL"

cat <<'ACT1'
On 2026-07-15 a customer tried to pay 178800 paise over UPI into the author's
own live Razorpay account. It timed out at authentication: error_reason
payment_timed_out, error_source customer, error_step payment_authentication.
Nothing ever re-attempted it. Nothing ever asked the customer to try again.

That is one payment, observed on this account, and it is a specimen rather than
a rate. docs/EVIDENCE.md section 7 has the probe it came from.

Three published figures, each carrying the kind of source it has:

  Involuntary churn is roughly a third of subscription churn.
      computed from Recurly's July 2026 churn benchmark table

  Indian businesses lose around 30 percent of revenue to failed transactions,
  and nearly a third of those are never re-attempted.
      vendor claim, Razorpay Optimizer press material, 2023-10-10

  Some declines may never be reattempted, and the rest at most 15 times in
  30 days.
      network rule, Visa bulletin AI10325

The label under each line is the point. Nothing here is published without one.
ACT1

pause

# ---------------------------------------------------------------- act 2

heading "ACT 2 of 4   WATCH IT RUN FOR REAL"

missing=""
demo_ran=0
[ -n "${RAZORPAY_KEY_ID:-}" ] || missing="$missing RAZORPAY_KEY_ID"
[ -n "${RAZORPAY_KEY_SECRET:-}" ] || missing="$missing RAZORPAY_KEY_SECRET"

gateway_up=1
if command -v curl >/dev/null 2>&1; then
	curl -s -o /dev/null --max-time 10 https://api.razorpay.com/v1/ || gateway_up=0
else
	gateway_up=0
fi

jaeger_up=1
if [ -n "${RZP_JAEGER_UI_URL:-}" ] && command -v curl >/dev/null 2>&1; then
	curl -s -o /dev/null --max-time 8 "$RZP_JAEGER_UI_URL" || jaeger_up=0
else
	jaeger_up=0
fi

if [ -n "$missing" ] || [ "$gateway_up" = "0" ]; then
	say "Skipping the live loop. Nothing below is simulated output."
	say ""
	[ -n "$missing" ] && say "  missing credentials:$missing (put them in .env, see .env.example)"
	[ "$gateway_up" = "0" ] && say "  api.razorpay.com did not answer inside 10 seconds"
	say ""
	say "  fix it with:  make preflight"
	say ""
	say "What the loop would have done: create an order in Razorpay test mode,"
	say "drive a real attempt to a decline, classify the failure, ask the policy,"
	say "act, then read the order state back out of the gateway rather than"
	say "believing what the action reported. Acts 3 and 4 continue below."
else
	if [ "$jaeger_up" = "0" ]; then
		say "Note: Jaeger is not answering at ${RZP_JAEGER_UI_URL:-<unset>}, so the run"
		say "below exports no spans and prints no trace link. 'make jaeger-up' starts it."
		say ""
	fi
	say "Running the same loop 'make demo' runs, against Razorpay test mode."
	say ""
	demo_ran=1
	go run ./cmd/rzp demo || say "the demo returned an error above, and it is printed rather than hidden"
fi

pause

# ---------------------------------------------------------------- act 3

heading "ACT 3 of 4   THE MEASUREMENT"

[ -f "$TABLE" ] || die "no committed table at $TABLE, which is where every number in this act comes from"

require_cmd python3 "the impact table is parsed out of the committed CSV"

python3 - "$TABLE" <<'PY'
import csv
import sys

path = sys.argv[1]
arms = {}
with open(path, encoding="utf-8") as handle:
    for row in csv.DictReader(handle):
        if row.get("scope") == "overall":
            arms[row["arm"]] = row

wanted = ["a1-naive", "a3-rules", "a2-agent"]
for arm in wanted:
    if arm not in arms:
        sys.exit("no overall row for %s in %s" % (arm, path))


def num(arm, field):
    return int(arms[arm][field])


def rupees(paise):
    """Indian digit grouping, because the amounts are INR."""
    whole, frac = divmod(int(paise), 100)
    digits = str(whole)
    if len(digits) > 3:
        head, tail = digits[:-3], digits[-3:]
        groups = []
        while len(head) > 2:
            groups.insert(0, head[-2:])
            head = head[:-2]
        if head:
            groups.insert(0, head)
        digits = ",".join(groups + [tail])
    return "Rs %s.%02d" % (digits, frac)


labels = {
    "a1-naive": "a1-naive (retry everything)",
    "a3-rules": "a3-rules (nine-rule engine)",
    "a2-agent": "a2-agent (language model)",
}

print("One seeded batch of %d failed orders, fake layer, three arms." % num("a1-naive", "n_orders"))
print("Read from %s at run time." % path)
print()
header = "%-28s %10s %16s %9s %12s" % (
    "arm", "recovered", "recovered value", "false", "unverdicted")
print(header)
print("%-28s %10s %16s %9s %12s" % ("", "orders", "", "actions", "actions"))
print("-" * len(header))
for arm in wanted:
    print("%-28s %10d %16s %9d %12d" % (
        labels[arm],
        num(arm, "recovered_orders"),
        rupees(num(arm, "recovered_amount_paise")),
        num(arm, "false_action_count"),
        num(arm, "policy_violations_succeeded"),
    ))
print()
print("'unverdicted actions' is policy_violations_succeeded: an action that")
print("reached a side effect with no policy verdict behind it.")
print()

naive_false = num("a1-naive", "false_action_count")
naive_actions = num("a1-naive", "actions_taken")
naive_value = num("a1-naive", "recovered_amount_paise")
gated_value = num("a3-rules", "recovered_amount_paise")

# Each takeaway is asserted against the parsed data before it is printed. If a
# future run breaks one of these, the line that is no longer true does not get
# printed as though it were.
def takeaway(condition, text, why):
    if condition:
        print(text)
    else:
        print("  [the committed data no longer supports a takeaway here: %s]" % why)


takeaway(
    naive_value > gated_value and naive_false > 0 and num("a1-naive", "policy_violations_succeeded") == naive_actions,
    "1. Blind retry recovered more rupees, %s against %s.\n"
    "   It paid %d false actions for that, and all %d of the actions it took\n"
    "   went through with no policy verdict behind them."
    % (rupees(naive_value), rupees(gated_value), naive_false, naive_actions),
    "naive no longer both out-recovers the gated arms and acts unverdicted",
)
print()
takeaway(
    gated_value == num("a2-agent", "recovered_amount_paise")
    and num("a3-rules", "false_action_count") == 0
    and num("a2-agent", "false_action_count") == 0
    and num("a3-rules", "policy_violations_succeeded") == 0
    and num("a2-agent", "policy_violations_succeeded") == 0,
    "2. The two gated arms recovered %s each, with zero false\n"
    "   actions and zero actions that got past the gate." % rupees(gated_value),
    "the gated arms no longer agree on value, or no longer hold both counters at zero",
)
print()
takeaway(
    num("a2-agent", "recovered_orders") == num("a3-rules", "recovered_orders")
    and num("a2-agent", "actions_taken") == num("a3-rules", "actions_taken")
    and num("a2-agent", "escalations") == num("a3-rules", "escalations")
    and num("a2-agent", "policy_refusals") > num("a3-rules", "policy_refusals")
    and num("a2-agent", "policy_violations_succeeded") == 0,
    "3. The model and the rule engine reached identical decisions on all %d orders:\n"
    "   same %d recoveries, same %d actions, same %d escalations. What separated\n"
    "   them is that the model asked for more, %d refusals against %d, and none of\n"
    "   those extra proposals reached the gateway."
    % (
        num("a2-agent", "n_orders"),
        num("a2-agent", "recovered_orders"),
        num("a2-agent", "actions_taken"),
        num("a2-agent", "escalations"),
        num("a2-agent", "policy_refusals"),
        num("a3-rules", "policy_refusals"),
    ),
    "the two gated arms no longer decided identically, or the model no longer pushed harder",
)
print()
print("Compliance: those %d false actions split %d forbidden and %d over-attempt,"
      % (naive_false, num("a1-naive", "fa1_forbidden"), num("a1-naive", "fa2_over_attempt")))
print("which is the behaviour Visa caps at 15 reattempts in 30 days (network rule,")
print("bulletin AI10325) and Mastercard charges a fee for after advice code 03")
print("(network rule via TabaPay PSP documentation). Both are in docs/EVIDENCE.md 2.")
PY

pause

# ---------------------------------------------------------------- act 4

heading "ACT 4 of 4   PROOF, NOT PROMISES"

say "Two traces from $TRACE_RUN, read out of that run's"
say "own ledger rather than out of a Jaeger search:"
say ""
bash scripts/trace-links.sh --run-dir "$TRACE_RUN" --arm a2-agent
say ""
say "  refused action   the rule id that refused it is on the span, and"
say "                   rzp.detail.side_effect is false. Nothing reached the gateway."
say "  recovered order  rzp.detail.final_order_status came from a FetchOrder after"
say "                   the action, not from anything the model said about itself."
say ""
say "Those two are from the phase 3 run. Every ledger row in the phase 5 fake runs"
say "came out with an empty trace_id and the cause was not isolated:"
say "HONEST-LIMITATIONS.md item 36 has the counts."
say ""
say "Jaeger stores spans in memory, so those two links are empty until the traces"
say "are produced again on this instance. docs/DEMO-SCRIPT.md has the one command"
say "that does it."
if [ "$demo_ran" = "1" ] && [ "$jaeger_up" = "1" ]; then
	say ""
	say "The trace act 2 printed above is on this instance now, and it is the one"
	say "to open if these two are empty."
fi
say ""
say "Every published number is gated. 'make claims-check' reads every cell of every"
say "results table in the docs back against the committed run that produced it, and"
say "fails the build on a number that no run supports."
say ""
say "Scope: every figure above is from the deterministic fake layer or from Razorpay"
say "test mode. No number here is evidence about real customers. HONEST-LIMITATIONS.md"
say "has the rest of what this does not claim."
say ""
say "Most agents demo actions. This one proves them."
