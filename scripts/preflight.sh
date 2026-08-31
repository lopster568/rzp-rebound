#!/usr/bin/env bash
# Reports whether this machine can run the project. Toolchain problems are
# hard failures; missing Razorpay keys are warnings, because the offline tests
# and the fake gateway need no credentials.
set -uo pipefail

ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
# shellcheck source=scripts/lib.sh
. "$ROOT/scripts/lib.sh"

hard=0
warn=0

ok() { say "  ok    $*"; }
bad() {
	say "  FAIL  $*"
	hard=$((hard + 1))
}
warning() {
	say "  warn  $*"
	warn=$((warn + 1))
}

say "preflight: toolchain"

if command -v go >/dev/null 2>&1; then
	ok "go $(go version | awk '{print $3}')"
else
	bad "go not on PATH"
fi

if command -v jq >/dev/null 2>&1; then
	ok "jq $(jq --version)"
else
	bad "jq not on PATH"
fi

if command -v claude >/dev/null 2>&1; then
	ok "claude CLI at $(command -v claude)"
else
	bad "claude CLI not on PATH"
fi

if command -v docker >/dev/null 2>&1; then
	if docker info >/dev/null 2>&1; then
		ok "docker daemon reachable"
	else
		bad "docker is installed but the daemon is not reachable"
	fi
else
	bad "docker not on PATH"
fi

say "preflight: credentials (test mode)"
for var in RAZORPAY_KEY_ID RAZORPAY_KEY_SECRET; do
	if [ -n "${!var:-}" ]; then
		ok "$var is set"
	else
		warning "$var is unset. Offline tests still run; live test-mode calls will not."
	fi
done

say ""
if [ "$hard" -gt 0 ]; then
	die "preflight: $hard hard failure(s), $warn warning(s)"
fi
say "preflight: passed with $warn warning(s)"
