#!/usr/bin/env bash
# Read-only health check for one instance. Prints JSON; records the
# result with verify_evidence.py doctor (the only write, into .verify/).
#
#   doctor.sh <instance> [verifier]
#
# Checks: process up, port owned by this instance's pid, binary built
# from HEAD, /api/health ok, cookie session valid (GET /api/session).
set -uo pipefail

INSTANCE="${1:?usage: doctor.sh <instance> [verifier]}"
VERIFIER="${2:-${VERIFIER:-verifier}}"
ROOT="$(git rev-parse --show-toplevel)"
SKILL="$ROOT/.claude/skills/verify-phronesis"
PY="${SKILL_PYTHON:-python3}"
BIN="${PHRONESIS_BIN:-$ROOT/phronesis}"
RUN="$ROOT/.verify-run/$INSTANCE"

PID="$(cat "$RUN/pid" 2>/dev/null || echo 0)"
PORT="$(cat "$RUN/port" 2>/dev/null || echo 0)"
BASE="http://127.0.0.1:$PORT"

check() { if eval "$2" >/dev/null 2>&1; then echo "$1=pass"; else echo "$1=fail"; fi; }

C_PROC=$(check process "kill -0 $PID")
C_PORT=$(check port_owned "lsof -nP -iTCP:$PORT -sTCP:LISTEN -t | grep -qx $PID")
C_BUILD=$(check build_is_head "\"$BIN\" --version | grep -q \"commit=$(git -C "$ROOT" rev-parse HEAD)\"")
C_HEALTH=$(check health "curl -fsS $BASE/api/health | grep -q '\"ok\":true'")
C_AUTH=$(check auth "curl -fsS -b \"$RUN/cookies.txt\" $BASE/api/session | grep -q '\"authenticated\":true'")

ALL="$C_PROC $C_PORT $C_BUILD $C_HEALTH $C_AUTH"
if echo "$ALL" | grep -q '=fail'; then FLAG=--fail; RC=1; else FLAG=--ok; RC=0; fi
args=()
for c in $ALL; do args+=(--check "$c"); done
"$PY" "$SKILL/scripts/verify_evidence.py" doctor --instance "$INSTANCE" "$FLAG" "${args[@]}" --verifier "$VERIFIER"
exit $RC
