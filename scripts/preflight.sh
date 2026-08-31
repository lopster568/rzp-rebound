#!/usr/bin/env bash
# Reports whether this machine can run the project. Toolchain problems are
# hard failures; missing Razorpay keys are warnings, because the offline tests
# and the fake gateway need no credentials.
set -uo pipefail

ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
# shellcheck source=scripts/lib.sh
. "$ROOT/scripts/lib.sh"
load_dotenv

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

# DOCKER_SSH_HOST moves the daemon check to another machine. The environment
# phase 1's live half ran in had no docker CLI at all, so checking for a local
# one there would report a hard failure about a daemon nothing was going to
# use. What matters is that some daemon can be driven, and which one is a
# configuration detail.
if [ -n "${DOCKER_SSH_HOST:-}" ]; then
	if ! command -v ssh >/dev/null 2>&1; then
		bad "DOCKER_SSH_HOST is set to $DOCKER_SSH_HOST but ssh is not on PATH"
	elif ! ssh -o BatchMode=yes -o ConnectTimeout=10 "$DOCKER_SSH_HOST" true >/dev/null 2>&1; then
		bad "$DOCKER_SSH_HOST is not reachable over ssh without a prompt"
	elif ! ssh -o BatchMode=yes -o ConnectTimeout=10 "$DOCKER_SSH_HOST" 'command -v docker' >/dev/null 2>&1; then
		bad "$DOCKER_SSH_HOST is reachable but has no docker CLI"
	elif ! ssh -o BatchMode=yes -o ConnectTimeout=10 "$DOCKER_SSH_HOST" 'docker info' >/dev/null 2>&1; then
		bad "the docker daemon on $DOCKER_SSH_HOST is not reachable"
	else
		ok "docker daemon reachable on $DOCKER_SSH_HOST ($(ssh -o BatchMode=yes -o ConnectTimeout=10 "$DOCKER_SSH_HOST" 'docker --version' 2>/dev/null))"
	fi
elif command -v docker >/dev/null 2>&1; then
	if docker info >/dev/null 2>&1; then
		ok "docker daemon reachable"
	else
		bad "docker is installed but the daemon is not reachable"
	fi
else
	bad "docker not on PATH and DOCKER_SSH_HOST is unset"
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
