#!/usr/bin/env bash
# Bundle-size CI gate for the silverbullet-like-live-preview manifold.
# Satisfies: O2 (≤30 KB gzipped delta), RT-13 (CI gate), TN8.
#
# Compares the gzipped size of frontend/dist/assets/index-*.js against the
# baseline recorded in scripts/.bundle-size-baseline. Exits non-zero (and
# prints a clear failure message) if the delta exceeds BUDGET_KB.
#
# Usage:
#   scripts/check-bundle-size.sh                 # uses default budget 30 KB
#   BUDGET_KB=50 scripts/check-bundle-size.sh    # override budget
#   UPDATE_BASELINE=1 scripts/check-bundle-size.sh   # accept current size as new baseline
#
# Run AFTER the frontend has been built (vite build / make build).

set -euo pipefail

BUDGET_KB="${BUDGET_KB:-30}"
BASELINE_FILE="$(dirname "$0")/.bundle-size-baseline"
DIST_GLOB="frontend/dist/assets/index-*.js"

shopt -s nullglob
BUNDLES=( $DIST_GLOB )
shopt -u nullglob

if [ ${#BUNDLES[@]} -eq 0 ]; then
  echo "ERROR: no frontend bundle found at $DIST_GLOB" >&2
  echo "       run 'make build' or 'cd frontend && npm run build' first" >&2
  exit 2
fi

if [ ${#BUNDLES[@]} -gt 1 ]; then
  echo "ERROR: multiple bundles found at $DIST_GLOB:" >&2
  printf '  %s\n' "${BUNDLES[@]}" >&2
  echo "       expected exactly one; clean dist and rebuild" >&2
  exit 2
fi

BUNDLE="${BUNDLES[0]}"
SIZE_GZ_BYTES="$(gzip -c "$BUNDLE" | wc -c | tr -d ' ')"
SIZE_GZ_KB="$(awk -v b="$SIZE_GZ_BYTES" 'BEGIN { printf "%.2f", b / 1024 }')"

if [ "${UPDATE_BASELINE:-0}" = "1" ]; then
  echo "$SIZE_GZ_BYTES" > "$BASELINE_FILE"
  echo "Updated baseline: ${SIZE_GZ_KB} KB gzipped"
  exit 0
fi

if [ ! -f "$BASELINE_FILE" ]; then
  echo "ERROR: baseline file missing at $BASELINE_FILE" >&2
  echo "       run 'UPDATE_BASELINE=1 scripts/check-bundle-size.sh' to seed it" >&2
  exit 2
fi

BASELINE_BYTES="$(cat "$BASELINE_FILE" | tr -d ' \n')"
BASELINE_KB="$(awk -v b="$BASELINE_BYTES" 'BEGIN { printf "%.2f", b / 1024 }')"
DELTA_BYTES=$(( SIZE_GZ_BYTES - BASELINE_BYTES ))
DELTA_KB="$(awk -v b="$DELTA_BYTES" 'BEGIN { printf "%+.2f", b / 1024 }')"
BUDGET_BYTES=$(( BUDGET_KB * 1024 ))

echo "Bundle:    $BUNDLE"
echo "Current:   ${SIZE_GZ_KB} KB gzipped"
echo "Baseline:  ${BASELINE_KB} KB gzipped"
echo "Delta:     ${DELTA_KB} KB"
echo "Budget:    +${BUDGET_KB}.00 KB"

if [ "$DELTA_BYTES" -gt "$BUDGET_BYTES" ]; then
  echo "" >&2
  echo "FAIL: bundle grew by ${DELTA_KB} KB, exceeding the +${BUDGET_KB} KB budget." >&2
  echo "      If the regression is intentional, update the baseline:" >&2
  echo "        UPDATE_BASELINE=1 scripts/check-bundle-size.sh" >&2
  exit 1
fi

echo "PASS: under budget."
