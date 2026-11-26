#!/usr/bin/env bash
# Launch one isolated phronesis instance for verification.
#
#   launch.sh <instance> [--oauth]
#
# Each instance gets its own claimed port, its own data root under
# .verify-run/<instance>/data, its own cookie jar and its own pid file.
# --oauth turns on the OAuth 2.1 server + /mcp transport for this instance.
# Prints one JSON line: {"instance","port","base_url","pid","data","cookies","oauth"}.
# Exit 0 = ready and logged in; non-zero = not ready (log path in stderr).
set -euo pipefail

INSTANCE="${1:?usage: launch.sh <instance> [--oauth]}"
OAUTH=0
[ "${2:-}" = "--oauth" ] && OAUTH=1

ROOT="$(git rev-parse --show-toplevel)"
SKILL="$ROOT/.claude/skills/verify-phronesis"
PY="${SKILL_PYTHON:-python3}"
BIN="${PHRONESIS_BIN:-$ROOT/phronesis}"
RUN="$ROOT/.verify-run/$INSTANCE"
DATA="$RUN/data"

if [ -f "$RUN/pid" ] && kill -0 "$(cat "$RUN/pid")" 2>/dev/null; then
  echo "launch: instance $INSTANCE already running (pid $(cat "$RUN/pid")); run cleanup.sh first" >&2
  exit 1
fi

# Stale-binary guard: the binary must be built from HEAD. A leftover
# ./phronesis from an older commit is the classic false pass.
DIRTY="$(git -C "$ROOT" status --porcelain -- cmd internal frontend/src go.mod go.sum)"
if [ -n "$DIRTY" ]; then
  echo "launch: uncommitted product changes; evidence would not describe HEAD. Commit first:" >&2
  echo "$DIRTY" >&2
  exit 1
fi
HEAD_SHA="$(git -C "$ROOT" rev-parse HEAD)"
BIN_COMMIT="$("$BIN" --version 2>/dev/null | sed -n 's/.*commit=\([0-9a-f]*\).*/\1/p')"
if [ "$BIN_COMMIT" != "$HEAD_SHA" ]; then
  echo "launch: $BIN reports commit=$BIN_COMMIT but HEAD is $HEAD_SHA; run 'make build' first" >&2
  exit 1
fi

PORT="$("$PY" "$SKILL/scripts/verify_evidence.py" port --instance "$INSTANCE" --base 4300 --span 200 | sed -n 's/^PORT: *//p')"
BASE="http://127.0.0.1:$PORT"
mkdir -p "$DATA/pages" "$DATA/blobs" "$DATA/snapshots"
echo "$PORT" > "$RUN/port"

# Every path default in internal/app/server.go is overridden so two
# instances never share a file (data/, oauth-key.pem, audit.log, DB).
env_args=(
  PHRONESIS_ADDR="127.0.0.1:$PORT"
  PHRONESIS_PAGES_DIR="$DATA/pages"
  PHRONESIS_BLOB_DIR="$DATA/blobs"
  PHRONESIS_AUDIT_LOG="$DATA/audit.log"
  PHRONESIS_STORE_PATH="$DATA/phronesis.db"
  PHRONESIS_OAUTH_KEY_PATH="$DATA/oauth-key.pem"
  PHRONESIS_JOURNAL_PATH="$DATA/journal.log"
  PHRONESIS_SNAPSHOT_DIR="$DATA/snapshots"
  PHRONESIS_ADMIN_USER=admin
  PHRONESIS_ADMIN_PASSWORD=verify-admin-pw
)
if [ "$OAUTH" = 1 ]; then
  env_args+=(PHRONESIS_OAUTH_ENABLED=1 PHRONESIS_OAUTH_ISSUER="$BASE")
fi

env "${env_args[@]}" nohup "$BIN" > "$RUN/server.log" 2>&1 &
PID=$!
echo "$PID" > "$RUN/pid"

# Readiness: /api/health answers AND the port is owned by the pid we
# started (health does not identify the process, so lsof proves it).
# Timeout: 15 s.
for _ in $(seq 1 75); do
  if ! kill -0 "$PID" 2>/dev/null; then
    echo "launch: server exited early; see $RUN/server.log" >&2
    "$PY" "$SKILL/scripts/verify_evidence.py" release --instance "$INSTANCE" >/dev/null || true
    exit 1
  fi
  if curl -fsS "$BASE/api/health" >/dev/null 2>&1 \
     && lsof -nP -iTCP:"$PORT" -sTCP:LISTEN -t 2>/dev/null | grep -qx "$PID"; then
    READY=1; break
  fi
  sleep 0.2
done
if [ "${READY:-0}" != 1 ]; then
  echo "launch: not ready after 15s; see $RUN/server.log" >&2
  exit 1
fi

# Per-instance auth session: log in once into this instance's cookie jar.
curl -fsS -c "$RUN/cookies.txt" -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"verify-admin-pw"}' "$BASE/api/login" >/dev/null

printf '{"instance":"%s","port":%s,"base_url":"%s","pid":%s,"data":"%s","cookies":"%s","oauth":%s}\n' \
  "$INSTANCE" "$PORT" "$BASE" "$PID" "$DATA" "$RUN/cookies.txt" "$([ $OAUTH = 1 ] && echo true || echo false)"
