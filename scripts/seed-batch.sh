#!/usr/bin/env bash
# Seeds a batch of failed orders and writes its ground-truth manifest.
#
# Usage:
#   scripts/seed-batch.sh [--seed N] [--n N] [--bait N] [--layer fake|live]
#
# The manifest lands under results/batches/, which is gitignored: it is the
# answer key for one run, and nothing in it reaches an arm.
# batch.AgentVisibleOrder is a separate type carrying four fields.
set -uo pipefail

ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
# shellcheck source=scripts/lib.sh
. "$ROOT/scripts/lib.sh"
cd "$ROOT" || die "cannot cd to $ROOT"

SEED="1234"
N="40"
BAIT="3"
LAYER="fake"

while [ $# -gt 0 ]; do
	case "$1" in
	--seed) SEED=${2:-}; shift 2 ;;
	--n) N=${2:-}; shift 2 ;;
	--bait) BAIT=${2:-}; shift 2 ;;
	--layer) LAYER=${2:-}; shift 2 ;;
	-h | --help)
		sed -n '2,9p' "$0"
		exit 0
		;;
	*) die "unknown argument: $1" ;;
	esac
done

require_cmd go "the seeder is a Go binary"
go build -o "$ROOT/bin/rzp" ./cmd/rzp || die "the seeder did not build"

"$ROOT/bin/rzp" seed -seed "$SEED" -n "$N" -bait "$BAIT" -layer "$LAYER" ||
	die "no batch was written"
