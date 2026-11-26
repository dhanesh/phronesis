#!/usr/bin/env bash
# Compose smoke test (make compose-test): phronesis + Dex in Colima.
# Runs the browser smoke (SSO, RBAC, edit, keys), recreates the phronesis
# container, checks data survived, checks the container health check, and
# removes ONLY its own compose project (KEEP=1 leaves it running).
set -euo pipefail
cd "$(dirname "$0")"
CTX="${DOCKER_CONTEXT:-colima}"
PROJECT="${PROJECT:-phronesis-smoke}"
export PHRONESIS_PORT="${PHRONESIS_PORT:-18080}" DEX_PORT="${DEX_PORT:-15556}"
export PHRONESIS_ADMIN_PASSWORD="${PHRONESIS_ADMIN_PASSWORD:-smoke-$(date +%s)-pw}"
BASE="http://localhost:${PHRONESIS_PORT}"
dc() { docker --context "$CTX" compose -p "$PROJECT" -f compose.yaml "$@"; }
wait_ready() {
  for _ in $(seq 1 60); do curl -sf "$BASE/api/readyz" >/dev/null && return 0; sleep 1; done
  echo "phronesis not ready at $BASE" >&2; dc logs --tail 50 >&2; return 1
}
cleanup() { [ "${KEEP:-0}" = 1 ] || dc down -v --remove-orphans >/dev/null 2>&1 || true; }
trap cleanup EXIT

dc down -v --remove-orphans >/dev/null 2>&1 || true
dc up -d
wait_ready
node ../smoke/smoke.mjs "$BASE" "$PHRONESIS_ADMIN_PASSWORD" full
dc up -d --force-recreate phronesis
wait_ready
node ../smoke/smoke.mjs "$BASE" "$PHRONESIS_ADMIN_PASSWORD" persist
for _ in $(seq 1 60); do
  [ "$(docker --context "$CTX" inspect "$PROJECT-phronesis-1" --format '{{.State.Health.Status}}')" = healthy ] && break
  sleep 2
done
status=$(docker --context "$CTX" inspect "$PROJECT-phronesis-1" --format '{{.State.Health.Status}}')
echo "container health: $status"
[ "$status" = healthy ]
echo "COMPOSE_SMOKE: PASS"
