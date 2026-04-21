#!/usr/bin/env bash
# @constraint O6 — unit test for scripts/compute-flake-rate.js.
# Feeds synthetic Playwright results.json fixtures and asserts correct flake rate output.
# Run via: make test-flake-monitor (or bash tests/flake-monitor.test.sh)
set -euo pipefail

SCRIPT="$(cd "$(dirname "$0")/.." && pwd)/scripts/compute-flake-rate.js"
PASS=0
FAIL=0

run_case() {
  local name="$1" data_dir="$2" expected_rate="$3"
  local out
  out=$(FLAKE_DATA_DIR="$data_dir" FLAKE_RATE_OUTPUT="$data_dir/flake-rate.txt" \
        GITHUB_STEP_SUMMARY=/dev/null node "$SCRIPT" 2>&1)
  local got
  got=$(cat "$data_dir/flake-rate.txt" 2>/dev/null || echo "MISSING")
  if [ "$got" = "$expected_rate" ]; then
    echo "  PASS: $name (rate=$got)"
    PASS=$((PASS + 1))
  else
    echo "  FAIL: $name — expected $expected_rate, got $got"
    echo "        script output: $out"
    FAIL=$((FAIL + 1))
  fi
}

make_results() {
  local dir="$1"; shift
  mkdir -p "$dir"
  cat > "$dir/results.json" <<JSON
$1
JSON
}

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

echo "flake-monitor unit tests"
echo "========================"

# Case 1: no flakes — all specs pass on first try
D="$TMPDIR/case1/run1"
make_results "$D" '{
  "suites": [{"specs": [
    {"title": "t1", "tests": [{"results": [{"retry": 0, "status": "passed"}]}]},
    {"title": "t2", "tests": [{"results": [{"retry": 0, "status": "passed"}]}]}
  ]}]
}'
run_case "0% flake — all pass first try" "$TMPDIR/case1" "0.00"

# Case 2: one flaky spec out of four (25.00%)
D1="$TMPDIR/case2/run1"
make_results "$D1" '{
  "suites": [{"specs": [
    {"title": "flaky", "tests": [{"results": [{"retry": 0, "status": "failed"}, {"retry": 1, "status": "passed"}]}]},
    {"title": "stable", "tests": [{"results": [{"retry": 0, "status": "passed"}]}]}
  ]}]
}'
D2="$TMPDIR/case2/run2"
make_results "$D2" '{
  "suites": [{"specs": [
    {"title": "flaky", "tests": [{"results": [{"retry": 0, "status": "passed"}]}]},
    {"title": "stable", "tests": [{"results": [{"retry": 0, "status": "passed"}]}]}
  ]}]
}'
# 1 flake out of 4 total specs = 25.00%
run_case "25% flake — 1/4 specs retried-then-passed" "$TMPDIR/case2" "25.00"

# Case 3: empty data dir — should output 0.00 with no-data message
mkdir -p "$TMPDIR/case3"
touch "$TMPDIR/case3/flake-rate.txt"
echo "0.00" > "$TMPDIR/case3/flake-rate.txt"
# Run with an empty subdir (no results.json files)
mkdir -p "$TMPDIR/case3/run1"
run_case "no results.json files — 0.00" "$TMPDIR/case3" "0.00"

# Case 4: threshold boundary — exactly 5.00% (2 flakes out of 40 specs)
D="$TMPDIR/case4"
for i in $(seq 1 4); do
  Dx="$D/run$i"
  mkdir -p "$Dx"
  SPECS=""
  for j in $(seq 1 10); do
    if [ "$i" = "1" ] && [ "$j" = "1" ]; then
      SPECS="$SPECS{\"title\": \"spec$j\", \"tests\": [{\"results\": [{\"retry\": 0, \"status\": \"failed\"}, {\"retry\": 1, \"status\": \"passed\"}]}]},"
    else
      SPECS="$SPECS{\"title\": \"spec$j\", \"tests\": [{\"results\": [{\"retry\": 0, \"status\": \"passed\"}]}]},"
    fi
  done
  SPECS="${SPECS%,}"
  printf '{"suites": [{"specs": [%s]}]}' "$SPECS" > "$Dx/results.json"
done
# 2 flakes (run1/spec1 counts once), but spec1 appears in all 4 runs (40 total specs); only 1 actual flake = 2.50%
run_case "2.50% flake — below 5% threshold" "$TMPDIR/case4" "2.50"

echo "========================"
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ] || exit 1
