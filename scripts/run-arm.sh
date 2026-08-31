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
# shellcheck source=scripts/lib.sh
. "$ROOT/scripts/lib.sh"
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
		sed -n '2,12p' "$0"
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
[ -n "$SEQUENCE" ] && set -- "$@" -order-sequence "$SEQUENCE"
[ -n "$KILL_SWITCH" ] && set -- "$@" -kill-switch-file "$KILL_SWITCH"

"$BIN" "$@" || die "the $ARM arm did not finish"
