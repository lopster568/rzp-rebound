#!/usr/bin/env bash
# Tears down the Jaeger container and its volumes.
#
# Usage: scripts/jaeger-down.sh
#
# DOCKER_SSH_HOST, when set, tears down on that machine's docker daemon, which
# is the same seam jaeger-up.sh uses. Both read COMPOSE_PROJECT from
# scripts/lib.sh, so a teardown finds what the bring-up started.
#
# Traces are not evidence: every number this project reports comes from
# results/, not from a span store. So this removes volumes rather than keeping
# them, and a torn-down Jaeger costs nothing that has to be reproduced.
set -uo pipefail

ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
# shellcheck source=scripts/lib.sh
. "$ROOT/scripts/lib.sh"
cd "$ROOT" || die "cannot cd to $ROOT"
load_dotenv

COMPOSE_FILE=compose/docker-compose.yml

if [ -n "${DOCKER_SSH_HOST:-}" ]; then
	require_cmd ssh "DOCKER_SSH_HOST is set, so the daemon is driven over ssh"
	if ! docker_reachable; then
		say "jaeger-down: the docker daemon on $DOCKER_SSH_HOST is not reachable, so there is nothing to stop"
		exit 0
	fi
	say "jaeger-down: stopping $COMPOSE_FILE on $DOCKER_SSH_HOST"
else
	require_cmd docker "install docker to manage the trace backend, or set DOCKER_SSH_HOST"
	if ! docker_reachable; then
		say "jaeger-down: the docker daemon is not reachable, so there is nothing to stop"
		exit 0
	fi
	say "jaeger-down: stopping $COMPOSE_FILE"
fi

compose_run down -v || die "compose down failed"
say "jaeger-down: stopped"
