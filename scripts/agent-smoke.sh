#!/usr/bin/env bash
# Drives the a2-agent arm over the first N orders of a batch, on the fake
# layer, and prints what each invocation did.
#
# Usage:
#   scripts/agent-smoke.sh --batch results/batches/b-1234-40.json [--n 2]
#                          [--run-dir results/runs/agent-smoke]
#
# It spends N headless invocations of the claude CLI. That is a subscription,
# so N defaults to 2 and the script says how many it is about to spend before
# it spends them.
set -uo pipefail

ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
# shellcheck source=scripts/lib.sh
. "$ROOT/scripts/lib.sh" || {
	printf 'error: cannot source %s/scripts/lib.sh\n' "$ROOT" >&2
	exit 1
}
cd "$ROOT" || die "cannot cd to $ROOT"

BATCH=""
N="2"
RUN_DIR=""

while [ $# -gt 0 ]; do
	case "$1" in
	--batch) BATCH=${2:-}; shift 2 ;;
	--n) N=${2:-}; shift 2 ;;
	--run-dir) RUN_DIR=${2:-}; shift 2 ;;
	-h | --help)
		sed -n '2,11p' "$0"
		exit 0
		;;
	*) die "unknown argument: $1" ;;
	esac
done

[ -n "$BATCH" ] || die "--batch is required, and scripts/seed-batch.sh writes one"
[ -f "$BATCH" ] || die "no batch manifest at $BATCH"
[ -n "$RUN_DIR" ] || RUN_DIR="results/runs/agent-smoke"

require_cmd go "the mcp server is a Go binary"
require_cmd python3 "the agent driver is Python"
require_cmd claude "the agent arm runs the claude cli headless"

go build -o "$ROOT/bin/rzp-mcp" ./cmd/rzp-mcp || die "the mcp server did not build"

mkdir -p "$RUN_DIR/a2-agent" || die "cannot make $RUN_DIR/a2-agent"
SEQ="$RUN_DIR/a2-agent/order_sequence.txt"
python3 -c '
import json, sys
orders = json.load(open(sys.argv[1]))["orders"]
sys.stdout.write("\n".join(o["order_id"] for o in orders[: int(sys.argv[2])]) + "\n")
' "$BATCH" "$N" >"$SEQ" || die "could not read the batch"

say "agent-smoke: about to spend $N headless invocation(s)"
python3 -m harness.agent_runner \
	--batch "$BATCH" \
	--layer fake \
	--run-dir "$RUN_DIR" \
	--order-sequence "$SEQ" \
	--server-binary "$ROOT/bin/rzp-mcp" \
	--prompt prompts/agent_system.md ||
	die "the agent smoke did not finish"

say "agent-smoke: outcomes $RUN_DIR/a2-agent/outcomes.jsonl"
