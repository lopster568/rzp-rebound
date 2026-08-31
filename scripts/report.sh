#!/usr/bin/env bash
# Scores a run against its batch manifest and writes the comparison table.
#
# Usage:
#   scripts/report.sh [--run-dir results/runs/<run_id>] [--out-dir results/tables]
#                     [--no-assert-contained]
#
# With no --run-dir it takes the most recently modified directory under
# results/runs/.
#
# It exits non-zero when policy_violations_succeeded is not 0 for a3-rules.
# That number is the containment claim, and a report that printed it and
# carried on would be publishing a broken claim in a green build.
set -uo pipefail

ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
# shellcheck source=scripts/lib.sh
. "$ROOT/scripts/lib.sh"
cd "$ROOT" || die "cannot cd to $ROOT"

RUN_DIR=""
OUT_DIR="results/tables"
ASSERT="--assert-contained"

while [ $# -gt 0 ]; do
	case "$1" in
	--run-dir) RUN_DIR=${2:-}; shift 2 ;;
	--out-dir) OUT_DIR=${2:-}; shift 2 ;;
	--no-assert-contained) ASSERT="--no-assert-contained"; shift ;;
	-h | --help)
		sed -n '2,14p' "$0"
		exit 0
		;;
	*) die "unknown argument: $1" ;;
	esac
done

require_cmd python3 "the scorer is Python"

if [ -z "$RUN_DIR" ]; then
	# The newest run directory. `ls -td` sorts by modification time, and the
	# run directory's mtime moves when the last arm writes into it.
	RUN_DIR=$(ls -td results/runs/*/ 2>/dev/null | head -1)
	[ -n "$RUN_DIR" ] || die "no run under results/runs/, so there is nothing to score"
	RUN_DIR=${RUN_DIR%/}
	say "report: scoring the newest run, $RUN_DIR"
fi

[ -d "$RUN_DIR" ] || die "no run directory at $RUN_DIR"
[ -f "$RUN_DIR/manifest.json" ] || die "$RUN_DIR has no manifest.json, so it was not written by the orchestrator"

python3 -m harness.aggregate --run-dir "$RUN_DIR" --out-dir "$OUT_DIR" "$ASSERT" || die "the report did not pass"
