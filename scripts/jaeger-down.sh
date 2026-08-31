#!/usr/bin/env bash
# Tears down the Jaeger container and its volumes.
#
# Usage: scripts/jaeger-down.sh
#
# Traces are not evidence: every number this project reports comes from
# results/, not from a span store. So this removes volumes rather than keeping
# them, and a torn-down Jaeger costs nothing that has to be reproduced.
set -uo pipefail

ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
# shellcheck source=scripts/lib.sh
. "$ROOT/scripts/lib.sh"
cd "$ROOT" || die "cannot cd to $ROOT"

COMPOSE_FILE=compose/docker-compose.yml

require_cmd docker "install docker to manage the trace backend"

if ! docker info >/dev/null 2>&1; then
	say "jaeger-down: the docker daemon is not reachable, so there is nothing to stop"
	exit 0
fi

if docker compose version >/dev/null 2>&1; then
	compose() { docker compose "$@"; }
elif command -v docker-compose >/dev/null 2>&1; then
	compose() { docker-compose "$@"; }
else
	die "neither 'docker compose' nor 'docker-compose' is available"
fi

say "jaeger-down: stopping $COMPOSE_FILE"
compose -f "$COMPOSE_FILE" down -v || die "compose down failed"
say "jaeger-down: stopped"
