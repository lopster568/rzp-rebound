#!/usr/bin/env bash
# Seeds a batch of failed orders and writes its ground-truth manifest.
#
# Usage:
#   scripts/seed-batch.sh [--seed N] [--n N] [--bait N] [--layer fake|live]
#                         [--profile NAME]
#
# --profile names the failure mix: uniform-invented (the default, and the shares
# every batch before phase 5 used), ethoca-card-mix-2019 (published card-decline
# shares, which set their own bait count and refuse --bait), or
# observed-live-mix (reads RZP_OBSERVED_MIX_FILE and errors when it is unset).
#
# The manifest lands under results/batches/, which is gitignored apart from the
# one batch the committed results table is recomputed from. It is the answer key
# for a run, and nothing in it reaches an arm: batch.AgentVisibleOrder is a
# separate type carrying four fields.
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

SEED="1234"
N="40"
BAIT="3"
LAYER="fake"
PROFILE=""

while [ $# -gt 0 ]; do
	case "$1" in
	--seed) SEED=${2:-}; shift 2 ;;
	--n) N=${2:-}; shift 2 ;;
	--bait) BAIT=${2:-}; shift 2 ;;
	--layer) LAYER=${2:-}; shift 2 ;;
	--profile) PROFILE=${2:-}; shift 2 ;;
	-h | --help)
		sed -n '2,15p' "$0"
		exit 0
		;;
	*) die "unknown argument: $1" ;;
	esac
done

require_cmd go "the seeder is a Go binary"
go build -o "$ROOT/bin/rzp" ./cmd/rzp || die "the seeder did not build"

# --bait is only passed on when the caller asked for it or when no profile was
# named, because a profile that sets its own bait share refuses the flag rather
# than ignoring it.
ARGS=(seed -seed "$SEED" -n "$N" -layer "$LAYER")
if [ -n "$PROFILE" ]; then
	ARGS+=(-profile "$PROFILE")
fi
if [ -z "$PROFILE" ] || [ "$PROFILE" = "uniform-invented" ]; then
	ARGS+=(-bait "$BAIT")
fi

"$ROOT/bin/rzp" "${ARGS[@]}" || die "no batch was written"
