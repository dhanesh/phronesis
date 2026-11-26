#!/usr/bin/env bash
# Stop one instance by its pid file, release its port claim and remove
# its run state under .verify-run/<instance>/. Never touches .verify/.
#
#   cleanup.sh <instance>
set -uo pipefail

INSTANCE="${1:?usage: cleanup.sh <instance>}"
ROOT="$(git rev-parse --show-toplevel)"
SKILL="$ROOT/.claude/skills/verify-phronesis"
PY="${SKILL_PYTHON:-python3}"
RUN="$ROOT/.verify-run/$INSTANCE"

if [ -f "$RUN/pid" ]; then
  PID="$(cat "$RUN/pid")"
  kill -TERM "$PID" 2>/dev/null || true
  for _ in $(seq 1 30); do kill -0 "$PID" 2>/dev/null || break; sleep 0.1; done
  kill -KILL "$PID" 2>/dev/null || true
fi
"$PY" "$SKILL/scripts/verify_evidence.py" release --instance "$INSTANCE" >/dev/null 2>&1 || true
rm -rf -- "$RUN"
echo "{\"instance\":\"$INSTANCE\",\"stopped\":true}"
