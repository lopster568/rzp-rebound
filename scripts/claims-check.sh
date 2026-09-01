#!/usr/bin/env bash
# The claims gate. Every number in a published document has to come from a
# committed run, and every cell of every results table has to match the CSV
# that produced it.
#
# Usage:
#   scripts/claims-check.sh [file ...]
#
# With no arguments it checks README.md, RESULTS.md, ARCHITECTURE.md,
# HONEST-LIMITATIONS.md, and docs/DEMO-SCRIPT.md.
#
# The logic is in scripts/claims_check.py, because the work is CSV, markdown,
# and JSONL parsing. Standard library only, no install, like everything under
# harness/ (ADR-0007). Read its module docstring for the three checks.
#
# Numbers that are settings or protocol constants rather than run output live
# in scripts/claims-allow.txt, one per line with its reason.
set -uo pipefail

ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
# shellcheck source=scripts/lib.sh
. "$ROOT/scripts/lib.sh" || {
	printf 'error: cannot source %s/scripts/lib.sh\n' "$ROOT" >&2
	exit 1
}
cd "$ROOT" || die "cannot cd to $ROOT"

case "${1:-}" in
-h | --help)
	sed -n '2,17p' "$0"
	exit 0
	;;
esac

require_cmd python3 "the claims gate parses the committed CSV and JSONL"

python3 scripts/claims_check.py "$@"
