#!/usr/bin/env bash
# Brings up the Jaeger container from compose/docker-compose.yml and waits for
# its query API to answer, so a run started right after this one has somewhere
# to send spans.
#
# Usage: scripts/jaeger-up.sh [seconds-to-wait]
# Default wait: 60 seconds.
#
# With the docker daemon down this exits non-zero and says so. That is not a
# reason a run cannot happen: internal/telemetry falls back to the stdout
# exporter when OTEL_EXPORTER_OTLP_ENDPOINT is unset, which is how phase 0
# finished.
set -uo pipefail

ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
# shellcheck source=scripts/lib.sh
. "$ROOT/scripts/lib.sh"
cd "$ROOT" || die "cannot cd to $ROOT"

COMPOSE_FILE=compose/docker-compose.yml
UI_PORT=${JAEGER_UI_PORT:-16686}
OTLP_GRPC_PORT=${JAEGER_OTLP_GRPC_PORT:-4317}
WAIT_SECONDS=${1:-60}

require_cmd docker "install docker to run the trace backend"

if ! docker info >/dev/null 2>&1; then
	die "the docker daemon is not reachable. Start it and run this again, or run without it: with OTEL_EXPORTER_OTLP_ENDPOINT unset, traces go to the stdout exporter."
fi

if docker compose version >/dev/null 2>&1; then
	compose() { docker compose "$@"; }
elif command -v docker-compose >/dev/null 2>&1; then
	compose() { docker-compose "$@"; }
else
	die "neither 'docker compose' nor 'docker-compose' is available"
fi

say "jaeger-up: starting $COMPOSE_FILE"
compose -f "$COMPOSE_FILE" up -d || die "compose up failed"

if ! command -v curl >/dev/null 2>&1; then
	say "jaeger-up: curl is not on PATH, so the health wait is skipped"
	say "jaeger-up: check it yourself at http://localhost:$UI_PORT"
	exit 0
fi

say "jaeger-up: waiting up to ${WAIT_SECONDS}s for the query API on port $UI_PORT"
deadline=$((SECONDS + WAIT_SECONDS))
while [ "$SECONDS" -lt "$deadline" ]; do
	if curl -fsS "http://localhost:$UI_PORT/api/services" >/dev/null 2>&1; then
		say "jaeger-up: ready"
		say "  ui    http://localhost:$UI_PORT"
		say "  otlp  OTEL_EXPORTER_OTLP_ENDPOINT=localhost:$OTLP_GRPC_PORT"
		exit 0
	fi
	sleep 1
done

say "jaeger-up: the container is up but the query API did not answer in ${WAIT_SECONDS}s"
say "jaeger-up: last 20 lines of container output"
compose -f "$COMPOSE_FILE" logs --tail 20 jaeger
die "jaeger did not become healthy"
