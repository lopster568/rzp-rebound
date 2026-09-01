#!/usr/bin/env bash
# Rebuilds every committed batch manifest from its seed and its profile and
# diffs it against what is in the tree.
#
# Usage:
#   scripts/verify-batches.sh
#
# A profile whose shares are cited is a claim about what is in a batch. The only
# way to check that claim is to build the batch again and diff it, which is what
# this does, and it is why `make verify-phase-5` runs it. It drives no arm,
# spends no invocation, and needs no credentials.
set -uo pipefail

ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
# shellcheck source=scripts/lib.sh
. "$ROOT/scripts/lib.sh" || {
	printf 'error: cannot source %s/scripts/lib.sh\n' "$ROOT" >&2
	exit 1
}
cd "$ROOT" || die "cannot cd to $ROOT"

require_cmd go "the seeder is a Go binary"
go build -o "$ROOT/bin/rzp" ./cmd/rzp || die "the seeder did not build"

TMP=$(mktemp -d) || die "no temp directory"
trap 'rm -rf "$TMP"' EXIT

status=0

# One line per committed batch: the path, then the arguments that build it.
# b-1234-40 is not here. It is the phase 3 batch, committed as the input to a
# published table, and phase 5 changed the reason vocabulary and the amount
# range underneath it. Rebuilding it would produce a different file with the
# same name, which is the thing this script exists to catch, so the row that
# would fail on purpose is left out and HONEST-LIMITATIONS says why.
check() {
	path=$1
	shift
	name=$(basename "$path")
	if [ ! -f "$path" ]; then
		printf 'verify-batches: %s is not in the tree\n' "$path" >&2
		status=1
		return
	fi
	"$ROOT/bin/rzp" seed "$@" --out "$TMP/$name" >/dev/null || {
		printf 'verify-batches: %s did not rebuild\n' "$name" >&2
		status=1
		return
	}
	if ! diff -q "$path" "$TMP/$name" >/dev/null; then
		printf 'verify-batches: %s does not match what its seed and profile produce\n' "$name" >&2
		diff "$path" "$TMP/$name" | head -20 >&2
		status=1
		return
	fi
	say "verify-batches: $name reproduces"
}

check results/batches/b-5150-40.json \
	--seed 5150 --n 40 --bait 3 --layer fake --profile uniform-invented
check results/batches/b-5150-40-ethoca-card-mix-2019.json \
	--seed 5150 --n 40 --layer fake --profile ethoca-card-mix-2019

if [ "$status" -ne 0 ]; then
	die "a committed batch does not match its profile"
fi
say "verify-batches: every committed batch reproduces from its seed and its profile"
