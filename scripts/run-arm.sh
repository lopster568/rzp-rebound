#!/usr/bin/env bash
# Runs one arm over one batch and writes its outcomes and its ledger.
#
# Usage:
#   scripts/run-arm.sh --arm a3-rules --batch results/batches/b-1234-40.json \
#                      --run-dir results/runs/r-20260831-201500 [--layer fake]
#                      [--order-sequence PATH] [--kill-switch-file PATH]
#
# The live layer needs RAZORPAY_KEY_ID and RAZORPAY_KEY_SECRET, which come from
# .env. Test-mode keys only. Nothing here echoes what it loaded.
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

ARM=""
BATCH=""
RUN_DIR=""
LAYER="fake"
SEQUENCE=""
KILL_SWITCH=""

while [ $# -gt 0 ]; do
	case "$1" in
	--arm) ARM=${2:-}; shift 2 ;;
	--batch) BATCH=${2:-}; shift 2 ;;
	--run-dir) RUN_DIR=${2:-}; shift 2 ;;
	--layer) LAYER=${2:-}; shift 2 ;;
	--order-sequence) SEQUENCE=${2:-}; shift 2 ;;
	--kill-switch-file) KILL_SWITCH=${2:-}; shift 2 ;;
	-h | --help)
		sed -n '2,11p' "$0"
		exit 0
		;;
	*) die "unknown argument: $1" ;;
	esac
done

[ -n "$ARM" ] || die "--arm is required"
[ -n "$BATCH" ] || die "--batch is required"
[ -n "$RUN_DIR" ] || die "--run-dir is required"
[ -f "$BATCH" ] || die "no batch manifest at $BATCH"

require_cmd go "the runner is a Go binary"

if [ "$LAYER" = "live" ]; then
	load_dotenv "$ROOT/.env"
	require_env RAZORPAY_KEY_ID "the live layer talks to Razorpay test mode"
	require_env RAZORPAY_KEY_SECRET "the live layer talks to Razorpay test mode"
	say "run-arm: layer live, against Razorpay TEST MODE"
fi

BIN="$ROOT/bin/rzp"
go build -o "$BIN" ./cmd/rzp || die "the runner did not build"

set -- run -arm "$ARM" -layer "$LAYER" -batch "$BATCH" -run-dir "$RUN_DIR"
if [ -n "$SEQUENCE" ]; then set -- "$@" -order-sequence "$SEQUENCE"; fi
if [ -n "$KILL_SWITCH" ]; then set -- "$@" -kill-switch-file "$KILL_SWITCH"; fi

"$BIN" "$@" || die "the $ARM arm did not finish"
