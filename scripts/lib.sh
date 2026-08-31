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
