#!/usr/bin/env bash
# Brings up the Jaeger container from compose/docker-compose.yml and waits for
# both ports a run needs: the query API, so a reviewer can open a trace, and the
# OTLP gRPC port, so an exporter started right after this one has somewhere to
# send spans. Waiting only on the query API would return before the collector is
# listening, which is the port that actually matters to a run.
#
# Usage: scripts/jaeger-up.sh [seconds-to-wait]
# Default wait: 60 seconds.
#
# DOCKER_SSH_HOST, when set to an ssh destination, runs the container on that
# machine's docker daemon and the health wait then checks that machine's
# published ports. The environment this phase ran in has no docker CLI at all,
# which is why the seam exists. Empty means local docker, which is what every
# earlier phase assumed.
#
# With no reachable daemon this exits non-zero and says so. That is not a
# reason a run cannot happen: internal/telemetry falls back to the stdout
# exporter when OTEL_EXPORTER_OTLP_ENDPOINT is unset, which is how phase 0
# finished.
set -uo pipefail

ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
# shellcheck source=scripts/lib.sh
. "$ROOT/scripts/lib.sh"
cd "$ROOT" || die "cannot cd to $ROOT"
load_dotenv

COMPOSE_FILE=compose/docker-compose.yml
UI_PORT=${JAEGER_UI_PORT:-16686}
OTLP_GRPC_PORT=${JAEGER_OTLP_GRPC_PORT:-4317}
WAIT_SECONDS=${1:-60}
HOST=$(docker_host)

case $WAIT_SECONDS in
'' | *[!0-9]*) die "the wait in seconds must be a whole number, got '$WAIT_SECONDS'" ;;
esac

if [ -n "${DOCKER_SSH_HOST:-}" ]; then
	require_cmd ssh "DOCKER_SSH_HOST is set, so the daemon is driven over ssh"
	say "jaeger-up: using the docker daemon on $DOCKER_SSH_HOST"
	if ! docker_ssh 'command -v docker' >/dev/null 2>&1; then
		die "no docker CLI on $DOCKER_SSH_HOST. Unset DOCKER_SSH_HOST to use a local daemon."
	fi
	if ! docker_ssh 'docker info' >/dev/null 2>&1; then
		die "the docker daemon on $DOCKER_SSH_HOST is not reachable. Start it and run this again, or run without it: with OTEL_EXPORTER_OTLP_ENDPOINT unset, traces go to the stdout exporter."
	fi
else
	require_cmd docker "install docker to run the trace backend, or set DOCKER_SSH_HOST to drive a remote daemon"
	if ! docker info >/dev/null 2>&1; then
		die "the docker daemon is not reachable. Start it, set DOCKER_SSH_HOST to a machine that has one, or run without it: with OTEL_EXPORTER_OTLP_ENDPOINT unset, traces go to the stdout exporter."
	fi
fi

say "jaeger-up: starting $COMPOSE_FILE as project $COMPOSE_PROJECT"
compose_run up -d || die "compose up failed"

if ! command -v curl >/dev/null 2>&1; then
	say "jaeger-up: curl is not on PATH, so the health wait is skipped"
	say "jaeger-up: check it yourself at http://$HOST:$UI_PORT"
	exit 0
fi

# otlp_listening opens a TCP connection to the collector port through bash's
# own /dev/tcp, so no extra tool is needed to check the port a run sends to. It
# works against a remote host for the same reason curl does: the port is
# published on the docker host, which is what HOST names.
otlp_listening() {
	(exec 3<>"/dev/tcp/$HOST/$OTLP_GRPC_PORT") 2>/dev/null && exec 3>&- 3<&-
}

say "jaeger-up: waiting up to ${WAIT_SECONDS}s for the query API on $HOST:$UI_PORT and OTLP on $HOST:$OTLP_GRPC_PORT"
deadline=$((SECONDS + WAIT_SECONDS))
while [ "$SECONDS" -lt "$deadline" ]; do
	if curl -fsS "http://$HOST:$UI_PORT/api/services" >/dev/null 2>&1 && otlp_listening; then
		say "jaeger-up: ready"
		say "  ui    http://$HOST:$UI_PORT"
		say "  otlp  OTEL_EXPORTER_OTLP_ENDPOINT=$HOST:$OTLP_GRPC_PORT"
		say "        (host:port with no scheme, which is what otlptracegrpc.WithEndpoint takes)"
		say "  link  RZP_JAEGER_UI_URL=http://$HOST:$UI_PORT"
		exit 0
	fi
	sleep 1
done

say "jaeger-up: the container is up but it did not answer on both ports in ${WAIT_SECONDS}s"
say "jaeger-up: last 20 lines of container output"
compose_run logs --tail 20 jaeger
die "jaeger did not become healthy"
