#!/usr/bin/env bash
# Shared helpers for the scripts in this directory. Source it, do not run it.

say() {
	printf '%s\n' "$*"
}

die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}

repo_root() {
	git rev-parse --show-toplevel 2>/dev/null || die "not inside a git repository"
}

require_cmd() {
	command -v "$1" >/dev/null 2>&1 || die "$1 not found on PATH${2:+ ($2)}"
}

require_env() {
	local name=$1
	if [ -z "${!name:-}" ]; then
		die "$name is not set${2:+ ($2)}"
	fi
}

# load_dotenv exports the settings in .env, without overwriting anything the
# caller already exported. A variable set on the command line wins, so a script
# can be pointed somewhere else for one run.
#
# It never echoes a value. The file holds the test-mode key pair, and a script
# that printed what it loaded would put a credential in every terminal
# scrollback and CI log it ever ran in.
load_dotenv() {
	local file=${1:-$ROOT/.env} line name value
	[ -f "$file" ] || return 0
	# The `|| [ -n "$line" ]` keeps a final line with no trailing newline,
	# which read otherwise drops. Losing the last variable in .env is a
	# credential that is silently unset, and the resulting 401 looks like a
	# wrong key rather than a missing one.
	while IFS= read -r line || [ -n "$line" ]; do
		line=${line%$'\r'}
		line=${line#"${line%%[![:space:]]*}"}
		case "$line" in
		'' | '#'*) continue ;;
		esac
		# `export NAME=value` is a valid .env line and used to be skipped in
		# silence, which is the same failure as dropping the last line.
		case "$line" in
		'export '*) line=${line#export } ;;
		esac
		name=${line%%=*}
		value=${line#*=}
		[ "$name" = "$line" ] && continue
		# Trim spaces around the name, so `NAME = value` is read rather than
		# silently ignored.
		name=${name%"${name##*[![:space:]]}"}
		name=${name#"${name%%[![:space:]]*}"}
		case "$name" in
		*[!A-Za-z0-9_]* | '') continue ;;
		esac
		value=${value#"${value%%[![:space:]]*}"}
		value=${value%"${value##*[![:space:]]}"}
		# Strip one matched pair of surrounding quotes. Keeping them meant a
		# quoted secret authenticated with the quotes included, producing a
		# 401 an operator cannot explain. Review finding, 2026-08-31.
		case "$value" in
		'"'*'"') value=${value#\"} value=${value%\"} ;;
		"'"*"'") value=${value#\'} value=${value%\'} ;;
		esac
		if [ -z "${!name:-}" ]; then
			export "$name=$value"
		fi
	done <"$file"
}

# Remote docker. DOCKER_SSH_HOST, when set, is an ssh destination whose docker
# daemon runs the containers. Empty means the local docker CLI, which is what
# every earlier phase assumed.

# COMPOSE_PROJECT is fixed rather than derived from the working directory,
# because the remote path pipes the compose file over stdin and docker would
# otherwise name the project after whatever directory the ssh session landed
# in. Up and down have to agree on it or a teardown finds nothing.
COMPOSE_PROJECT=rzp

# docker_host returns the host the container ports are published on: the ssh
# destination with any user@ stripped when DOCKER_SSH_HOST is set, and
# localhost when it is not.
docker_host() {
	if [ -n "${DOCKER_SSH_HOST:-}" ]; then
		printf '%s' "${DOCKER_SSH_HOST##*@}"
	else
		printf 'localhost'
	fi
}

# docker_ssh runs one command on the remote docker host. BatchMode makes an
# unreachable host fail immediately rather than hanging a script on a
# passphrase prompt nobody is watching.
docker_ssh() {
	ssh -o BatchMode=yes -o ConnectTimeout=10 "$DOCKER_SSH_HOST" "$@"
}

# docker_reachable reports whether a docker daemon can be driven at all,
# locally or over ssh.
docker_reachable() {
	if [ -n "${DOCKER_SSH_HOST:-}" ]; then
		command -v ssh >/dev/null 2>&1 || return 1
		docker_ssh 'docker info' >/dev/null 2>&1
	else
		command -v docker >/dev/null 2>&1 || return 1
		docker info >/dev/null 2>&1
	fi
}

# compose_run runs one docker compose subcommand against COMPOSE_FILE, which
# the caller sets.
#
# The remote form pipes the compose file over stdin, because the build machine
# has no checkout of this repository. Copying one there would give the run two
# compose files that could drift, and the one under version control is the one
# that should be describing what came up.
compose_run() {
	if [ -n "${DOCKER_SSH_HOST:-}" ]; then
		# ssh takes one command string, so each argument is quoted for the
		# remote shell rather than flattened with $*. Today's callers pass no
		# argument with a space or a glob in it, and the two branches of this
		# function should not have different argument semantics waiting for
		# the first caller that does.
		local quoted=""
		local arg
		for arg in "$@"; do
			quoted="$quoted $(printf '%q' "$arg")"
		done
		docker_ssh "docker compose -p $COMPOSE_PROJECT -f -$quoted" <"$COMPOSE_FILE"
	elif docker compose version >/dev/null 2>&1; then
		docker compose -p "$COMPOSE_PROJECT" -f "$COMPOSE_FILE" "$@"
	elif command -v docker-compose >/dev/null 2>&1; then
		docker-compose -p "$COMPOSE_PROJECT" -f "$COMPOSE_FILE" "$@"
	else
		die "neither 'docker compose' nor 'docker-compose' is available"
	fi
}
