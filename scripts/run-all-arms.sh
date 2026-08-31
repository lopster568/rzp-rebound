#!/usr/bin/env bash
# Runs every arm over one batch, in a seeded shuffle, and writes a run manifest.
#
# Usage:
#   scripts/run-all-arms.sh --batch results/batches/b-1234-40.json
#                           [--arms a0-control,a1-naive,a3-rules]
#                           [--layer fake] [--seed 42] [--run-id ID]
#
# The shuffle and the run manifest come from harness/orchestrator.py, which is
# the one place the cell order is decided. It invokes bin/rzp run per arm.
#
# The live layer needs RAZORPAY_KEY_ID and RAZORPAY_KEY_SECRET from .env.
# Test-mode keys only, and nothing here echoes what it loaded.
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

BATCH=""
ARMS="a0-control,a1-naive,a3-rules"
LAYER="fake"
SEED="42"
RUN_ID=""
EXTRA=""

while [ $# -gt 0 ]; do
	case "$1" in
	--batch) BATCH=${2:-}; shift 2 ;;
	--arms) ARMS=${2:-}; shift 2 ;;
	--layer) LAYER=${2:-}; shift 2 ;;
	--seed) SEED=${2:-}; shift 2 ;;
	--run-id) RUN_ID=${2:-}; shift 2 ;;
	--dry-run) EXTRA="--dry-run"; shift ;;
	-h | --help)
		sed -n '2,13p' "$0"
		exit 0
		;;
	*) die "unknown argument: $1" ;;
	esac
done

[ -n "$BATCH" ] || die "--batch is required, and scripts/seed-batch.sh writes one"
[ -f "$BATCH" ] || die "no batch manifest at $BATCH"

require_cmd go "the runner is a Go binary"
require_cmd python3 "the orchestrator and the scorer are Python"

if [ "$LAYER" = "live" ]; then
	load_dotenv "$ROOT/.env"
	require_env RAZORPAY_KEY_ID "the live layer talks to Razorpay test mode"
	require_env RAZORPAY_KEY_SECRET "the live layer talks to Razorpay test mode"
	say "run-all-arms: layer live, against Razorpay TEST MODE. Every number from"
	say "run-all-arms: this layer is a test-mode number and says so on its row."
fi

go build -o "$ROOT/bin/rzp" ./cmd/rzp || die "the runner did not build"

set -- --batch "$BATCH" --arms "$ARMS" --layer "$LAYER" --seed "$SEED" --rzp-bin "$ROOT/bin/rzp"
if [ -n "$RUN_ID" ]; then set -- "$@" --run-id "$RUN_ID"; fi
if [ -n "$EXTRA" ]; then set -- "$@" "$EXTRA"; fi

python3 -m harness.orchestrator "$@" || die "the run did not finish"
