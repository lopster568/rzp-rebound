#!/usr/bin/env bash
# Prose gate. Fails with file:line on dashes, dishonest wording, leaked keys,
# relative dates, or a phrase from scripts/slop-patterns.txt.
#
# Usage: scripts/check-docs.sh [file ...]
# With no arguments it checks every tracked .md and .txt file.
# Paths may be absolute or relative; both are normalised to repo-relative.
#
# This script and scripts/slop-patterns.txt are excluded from the scan, because
# both have to contain the very strings they ban.
set -uo pipefail

ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
# shellcheck source=scripts/lib.sh
. "$ROOT/scripts/lib.sh"
cd "$ROOT" || die "cannot cd to $ROOT"

PATTERNS="$ROOT/scripts/slop-patterns.txt"
EXCLUDED_FILES="scripts/check-docs.sh scripts/slop-patterns.txt"

failures=0

# Strip comments and blank lines from the slop list once.
SLOP_LIST=$(mktemp)
trap 'rm -f "$SLOP_LIST"' EXIT
if [ -f "$PATTERNS" ]; then
	grep -v '^[[:space:]]*#' "$PATTERNS" | grep -v '^[[:space:]]*$' >"$SLOP_LIST"
fi

fail() {
	# fail <rule> <grep output, one file:line:text per line>
	local rule=$1 hits=$2
	while IFS= read -r hit; do
		[ -n "$hit" ] && printf '%s  [%s]\n' "$hit" "$rule" >&2
	done <<<"$hits"
	failures=$((failures + 1))
}

rel_path() {
	local f=${1#./}
	case "$f" in
	"$ROOT"/*) f=${f#"$ROOT"/} ;;
	esac
	printf '%s' "$f"
}

excluded() {
	local f=$1 e
	for e in $EXCLUDED_FILES; do
		[ "$f" = "$e" ] && return 0
	done
	return 1
}

check_file() {
	local f=$1 hits

	# 1. em dash / en dash
	hits=$(grep -nP '\x{2013}|\x{2014}' "$f" | sed "s|^|$f:|")
	[ -n "$hits" ] && fail "em or en dash, use a plain hyphen" "$hits"

	# 2. dishonest delivery wording. We observe an API call succeeding, not a
	#    human reading a message. The allowed phrase is
	#    "notification API call succeeded".
	hits=$(grep -niE 'customer[[:space:]]+notified' "$f" | sed "s|^|$f:|")
	[ -n "$hits" ] && fail 'write "notification API call succeeded" instead' "$hits"

	# 3. live-looking Razorpay keys
	hits=$(grep -nE 'rzp_test_[A-Za-z0-9]{8,}|rzp_live_' "$f" | sed "s|^|$f:|")
	[ -n "$hits" ] && fail "looks like a real Razorpay key, remove it" "$hits"

	# 4. relative dates. Docs outlive the day they were written.
	hits=$(grep -niE '\<(yesterday|tomorrow|next week|last week)\>' "$f" | sed "s|^|$f:|")
	[ -n "$hits" ] && fail "relative date, write an absolute date like 2026-08-31" "$hits"

	# 5. curated slop phrases
	if [ -s "$SLOP_LIST" ]; then
		hits=$(grep -nFif "$SLOP_LIST" "$f" | sed "s|^|$f:|")
		[ -n "$hits" ] && fail "slop phrase from scripts/slop-patterns.txt" "$hits"
	fi

	return 0
}

collect_files() {
	if [ "$#" -gt 0 ]; then
		printf '%s\n' "$@"
	else
		git ls-files '*.md' '*.txt'
	fi
}

checked=0
while IFS= read -r raw; do
	[ -n "$raw" ] || continue
	f=$(rel_path "$raw")
	excluded "$f" && continue
	[ -f "$f" ] || continue
	case "$f" in
	*.md | *.txt) ;;
	*) continue ;;
	esac
	check_file "$f"
	checked=$((checked + 1))
done < <(collect_files "$@")

if [ "$failures" -gt 0 ]; then
	die "check-docs: $failures rule violation(s) across $checked file(s)"
fi

say "check-docs: $checked file(s) clean"
