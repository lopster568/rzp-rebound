#!/usr/bin/env bash
# Captures real Razorpay test-mode responses into testdata/recorded/.
#
# Usage: scripts/capture-fixtures.sh [output-directory]
# Default output: testdata/recorded
#
# Every file it writes is a response Razorpay actually sent. The client's own
# capture hook scrubs each body on the way out, and this script refuses to
# finish if anything key-shaped survived into a file, because a fixture is a
# committed artefact and the pre-commit hook is the last line rather than the
# only one.
#
# It spends real test-mode API calls: two orders, one payment attempt driven to
# a decline, one payment link, and one resend. Nothing it creates involves real
# money, and the payment link is created with notification off and no customer
# on it, so it asks Razorpay to contact nobody.
set -uo pipefail

ROOT=$(git rev-parse --show-toplevel 2>/dev/null || pwd)
# shellcheck source=scripts/lib.sh
. "$ROOT/scripts/lib.sh"
cd "$ROOT" || die "cannot cd to $ROOT"

load_dotenv

OUT_DIR=${1:-testdata/recorded}

require_cmd go "the capture runs through cmd/rzp"
require_env RAZORPAY_KEY_ID "test-mode credentials are needed to capture real responses"
require_env RAZORPAY_KEY_SECRET "test-mode credentials are needed to capture real responses"

case "$RAZORPAY_KEY_ID" in
rzp_live_*) die "RAZORPAY_KEY_ID is a live-mode key. This project is test mode only." ;;
esac

say "capture-fixtures: writing to $OUT_DIR"
go run ./cmd/rzp capture -out "$OUT_DIR" || die "the capture run failed"

# The scan runs over the files that were just written. It looks for the two key
# prefixes and for the configured credentials themselves, because a key secret
# has no shape a pattern can find and the only way to look for one is to
# already know it.
say "capture-fixtures: scanning $OUT_DIR for anything credential shaped"
[ -d "$OUT_DIR" ] || die "capture-fixtures: $OUT_DIR does not exist, so nothing was captured"
leaks=0
scanned=0
while IFS= read -r file; do
	scanned=$((scanned + 1))
	if grep -qE 'rzp_test_[A-Za-z0-9]{8,}|rzp_live_' "$file"; then
		say "  LEAK  $file carries something shaped like a Razorpay key"
		leaks=$((leaks + 1))
		continue
	fi
	if grep -qF -- "$RAZORPAY_KEY_ID" "$file" || grep -qF -- "$RAZORPAY_KEY_SECRET" "$file"; then
		say "  LEAK  $file carries a configured credential"
		leaks=$((leaks + 1))
	fi
done < <(find "$OUT_DIR" -name '*.json' -type f)

if [ "$leaks" -gt 0 ]; then
	die "capture-fixtures: $leaks file(s) carry credentials. Do not commit them. Fix the redaction path first."
fi

# A control that reports success without having inspected anything is worse
# than no control. set -uo pipefail has no -e, and the loop above reads from a
# process substitution nothing checks, so a find that matched nothing used to
# print a clean result and exit 0. Review finding, 2026-08-31.
if [ "$scanned" -eq 0 ]; then
	die "capture-fixtures: scanned 0 files in $OUT_DIR. The scan proves nothing, so this is a failure."
fi

say "capture-fixtures: no credential found in any of the $scanned file(s) scanned in $OUT_DIR"
