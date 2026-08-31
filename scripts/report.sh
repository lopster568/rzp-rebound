#!/usr/bin/env bash
# Scores a run against its batch manifest and writes the comparison table.
#
# Usage:
#   scripts/report.sh [--run-dir results/runs/<run_id>] [--out-dir results/tables]
#                     [--no-assert-contained]
#
# With no --run-dir it scores the run named in results/runs/LAST_RUN, which the
# orchestrator writes when a run finishes.
#
# It exits non-zero when policy_violations_succeeded is not 0 for a3-rules.
# That number is the containment claim, and a report that printed it and
# carried on would be publishing a broken claim in a green build.
set -uo pipefail

ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
# Sourcing lib.sh is the one command here whose failure has to stop the script.
# These scripts run without `set -e`, so an unguarded source that failed left
# `die` an unknown command whose exit 127 is discarded, `require_env` and
# `load_dotenv` silent no-ops, and a live run proceeding with no credentials to
# surface a 401 instead of the real cause. Review finding, 2026-08-31.
# shellcheck source=scripts/lib.sh
. "$ROOT/scripts/lib.sh" || {
	printf 'error: cannot source %s/scripts/lib.sh\n' "$ROOT" >&2
	exit 1
}
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
		sed -n '2,13p' "$0"
		exit 0
		;;
	*) die "unknown argument: $1" ;;
	esac
done

require_cmd python3 "the scorer is Python"

if [ -z "$RUN_DIR" ]; then
	# The run the orchestrator finished last, by name.
	#
	# This used to be `ls -td results/runs/*/ | head -1`, the newest by
	# modification time, so anything that touched an older run made this score
	# the wrong one and overwrite that run's table in place. A committed table
	# is an artefact a document cites, and it should not move because a file
	# was read. Review finding, 2026-08-31.
	[ -f results/runs/LAST_RUN ] ||
		die "no results/runs/LAST_RUN, so no run has finished here. Pass --run-dir, or run scripts/run-all-arms.sh first"
	LAST=$(cat results/runs/LAST_RUN)
	[ -n "$LAST" ] || die "results/runs/LAST_RUN is empty"
	RUN_DIR="results/runs/$LAST"
	say "report: scoring the last completed run, $RUN_DIR"
fi

[ -d "$RUN_DIR" ] || die "no run directory at $RUN_DIR"
[ -f "$RUN_DIR/manifest.json" ] || die "$RUN_DIR has no manifest.json, so it was not written by the orchestrator"

python3 -m harness.aggregate --run-dir "$RUN_DIR" --out-dir "$OUT_DIR" "$ASSERT" || die "the report did not pass"
