#!/usr/bin/env bash
# Seeds a batch of failed orders and writes its ground-truth manifest.
#
# Skeleton. It exits non-zero on purpose, because the CLI subcommand it needs
# does not exist yet.
#
# Usage, once cmd/rzp grows the subcommand:
#   scripts/seed-batch.sh [--seed N] [--bait N]
#
# Everything the batch itself needs is already written and tested:
# internal/batch has the seeded generator, the ground-truth Manifest, and the
# four-field AgentVisibleOrder projection, covered by five tests since phase 0.
# What is missing is the `rzp seed` subcommand that calls it and writes the
# manifest under results/batches/. PRD section 6 puts `make seed` in phase 2,
# and phase 1 left it there rather than half-building it: a seeder that writes
# a manifest before the scoring pass that reads it exists would fix the file
# format with nothing to check it against.
set -uo pipefail

ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
# shellcheck source=scripts/lib.sh
. "$ROOT/scripts/lib.sh"
cd "$ROOT" || die "cannot cd to $ROOT"

say "seed-batch: pending cmd/rzp seed implementation (phase 2, PRD section 6)"
say "seed-batch: the generator it will call is internal/batch.Generate, already tested"
die "no batch was written"
