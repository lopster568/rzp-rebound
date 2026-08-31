#!/usr/bin/env bash
# Prints the Jaeger URL of one refused action and one recovered order, from a
# run's audit ledger.
#
# Usage:
#   scripts/trace-links.sh --run-dir results/runs/phase-3-fake [--arm a2-agent]
#
# The two links are the demo artefacts: a reviewer clicks the first and sees
# the rule id that refused an action, and clicks the second and sees a
# recovery from the failed payment to the paid order. Both come out of the
# ledger rather than out of a search, so the link points at the trace the table
# row was computed from.
#
# It needs RZP_JAEGER_UI_URL, which scripts/jaeger-up.sh prints.
set -uo pipefail

ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
# shellcheck source=scripts/lib.sh
. "$ROOT/scripts/lib.sh" || {
	printf 'error: cannot source %s/scripts/lib.sh\n' "$ROOT" >&2
	exit 1
}
cd "$ROOT" || die "cannot cd to $ROOT"

RUN_DIR=""
ARM="a2-agent"

while [ $# -gt 0 ]; do
	case "$1" in
	--run-dir) RUN_DIR=${2:-}; shift 2 ;;
	--arm) ARM=${2:-}; shift 2 ;;
	-h | --help)
		sed -n '2,14p' "$0"
		exit 0
		;;
	*) die "unknown argument: $1" ;;
	esac
done

[ -n "$RUN_DIR" ] || die "--run-dir is required"
LEDGER="$RUN_DIR/$ARM/ledger.jsonl"
[ -f "$LEDGER" ] || die "no ledger at $LEDGER"

require_cmd python3 "the ledger is jsonl"

UI=${RZP_JAEGER_UI_URL:-}
[ -n "$UI" ] || say "trace-links: RZP_JAEGER_UI_URL is unset, printing trace ids only"

python3 - "$LEDGER" "$UI" <<'PY'
import json
import sys

ledger, ui = sys.argv[1], sys.argv[2]

refused = None
recovered = None
with open(ledger, encoding="utf-8") as handle:
    for line in handle:
        line = line.strip()
        if not line:
            continue
        row = json.loads(line)
        trace = row.get("trace_id") or ""
        if not trace:
            continue
        detail = row.get("detail") or {}
        verdict = row.get("policy_verdict") or ""

        # A refused action, from either gate layer. The rule id is on the row
        # and on the span, so the link lands on a trace where a reviewer can
        # read which rule refused what.
        if refused is None and verdict in ("deny", "escalate"):
            refused = (trace, row.get("kind"), detail.get("tool", ""), row.get("policy_rule", ""))

        if recovered is None and row.get("kind") == "outcome_observed":
            if str(detail.get("recovered", "")) == "true":
                recovered = (trace, row.get("order_id", ""), detail.get("final_order_status", ""))

def link(trace):
    return (ui.rstrip("/") + "/trace/" + trace) if ui else trace

if refused:
    trace, kind, tool, rule = refused
    print("refused action  " + link(trace))
    print("                " + kind + " " + tool + " " + rule)
else:
    print("refused action  none in this ledger")

if recovered:
    trace, order, status = recovered
    print("recovered order " + link(trace))
    print("                " + order + " " + status)
else:
    print("recovered order none in this ledger")
PY
