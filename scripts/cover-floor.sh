#!/usr/bin/env bash
# cover-floor — enforce the aggregate statement-coverage floor from a profile.
#
# Codecov's project status is advisory here (fail_ci_if_error: false and the
# status is not in ci-ok), so this is the enforceable floor. It is set a few
# points under the measured baseline (71.3% on 2026-10-28) so ordinary
# platform noise does not trip it, while a wholesale deleted package does.
#
# Usage: bash scripts/cover-floor.sh [coverage.out]
set -euo pipefail

profile=${1:-coverage.out}
floor=${KERN_COVER_FLOOR:-68}

if [ ! -f "$profile" ]; then
  echo "cover-floor: missing profile $profile" >&2
  exit 1
fi

total=$(go tool cover -func="$profile" | tail -1 | awk '{print $3}' | tr -d '%')
if [ -z "$total" ]; then
  echo "cover-floor: could not read the total from $profile" >&2
  exit 1
fi

awk -v got="$total" -v want="$floor" 'BEGIN {
  if (got + 0 < want + 0) {
    printf "cover-floor: FAIL %.2f%% < %.2f%%\n", got, want
    exit 1
  }
  printf "cover-floor: PASS %.2f%% >= %.2f%%\n", got, want
}'
