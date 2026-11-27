#!/usr/bin/env bash
# Helm smoke test (make helm-test): installs Dex and the phronesis chart into
# a Kubernetes cluster (Colima k3s), runs the browser smoke test through
# port-forwards, deletes the pod, and checks data survived on the PVC.
# Removes ONLY its own namespace at the end (KEEP=1 leaves it).
#
#   KUBE_CONTEXT=colima-phronesis-k8s IMAGE=phronesis:dev deploy/helm/smoke.sh
set -euo pipefail
cd "$(dirname "$0")"
# OUT_DIR=<dir> also saves each phase's JSON as <dir>/<phase>.json.
run_smoke() {
  if [ -n "${OUT_DIR:-}" ]; then
    mkdir -p "$OUT_DIR"
    node ../smoke/smoke.mjs "$BASE" "$ADMIN_PW" "$1" | tee "$OUT_DIR/$1.json"
  else
    node ../smoke/smoke.mjs "$BASE" "$ADMIN_PW" "$1"
  fi
}
KCTX="${KUBE_CONTEXT:-colima-phronesis-k8s}"
NS="${NAMESPACE:-phronesis-smoke}"
IMAGE="${IMAGE:-phronesis:dev}"
WIKI_PORT="${WIKI_PORT:-18081}" DEX_PORT="${DEX_PORT:-15557}"
ADMIN_PW="${PHRONESIS_ADMIN_PASSWORD:-smoke-$(date +%s)-pw}"
BASE="http://localhost:${WIKI_PORT}"
k() { kubectl --context "$KCTX" -n "$NS" "$@"; }
PF=()
PFLOG="${TMPDIR:-/tmp}/phronesis-helm-smoke-pf.$$"
stop_pf() {
  for pid in "${PF[@]:-}"; do
    [ -n "$pid" ] || continue
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  done
  PF=()
}
start_pf() {
  stop_pf
  # Call kubectl directly (not the k function): `k ... &` would fork a
  # subshell, and killing that would leave kubectl holding the port.
  kubectl --context "$KCTX" -n "$NS" port-forward svc/dex "${DEX_PORT}:5556" >"$PFLOG.dex" 2>&1 & PF+=($!)
  kubectl --context "$KCTX" -n "$NS" port-forward svc/phronesis "${WIKI_PORT}:80" >"$PFLOG.wiki" 2>&1 & PF+=($!)
  for _ in $(seq 1 60); do
    curl -sf "$BASE/api/readyz" >/dev/null && curl -sf "http://localhost:${DEX_PORT}/dex/healthz" >/dev/null && return 0
    sleep 1
  done
  echo "port-forwards not ready" >&2; tail -3 "$PFLOG.dex" "$PFLOG.wiki" >&2; return 1
}
cleanup() {
  stop_pf
  if [ "${KEEP:-0}" = 1 ]; then echo "teardown: skipped (KEEP=1), namespace $NS left"; return; fi
  kubectl --context "$KCTX" delete namespace "$NS" --wait=false >/dev/null 2>&1 && echo "teardown: namespace $NS deleted" || echo "teardown: could not delete namespace $NS"
}
trap cleanup EXIT

# Preflight: the cluster answers, and k3s's Docker has the image built from
# HEAD (pullPolicy=Never would otherwise run a stale or missing image).
DCTX="${DOCKER_CONTEXT:-$KCTX}"
kubectl --context "$KCTX" get nodes >/dev/null 2>&1 || { echo "error: kube context $KCTX is not reachable" >&2; exit 1; }
REV=$(docker --context "$DCTX" image inspect "$IMAGE" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' 2>/dev/null || true)
HEAD=$(git rev-parse HEAD)
if [ "$REV" != "$HEAD" ]; then
  echo "error: $IMAGE in docker context $DCTX is revision '${REV:-missing}', HEAD is $HEAD (run: make image DOCKER_CONTEXT=$DCTX)" >&2
  exit 1
fi
# A namespace left Terminating by an earlier run must finish first.
kubectl --context "$KCTX" wait --for=delete "namespace/$NS" --timeout=300s >/dev/null 2>&1 || true
kubectl --context "$KCTX" create namespace "$NS" --dry-run=client -o yaml | kubectl --context "$KCTX" apply -f - >/dev/null
sed -e "s/__DEX_PORT__/${DEX_PORT}/" -e "s/__WIKI_PORT__/${WIKI_PORT}/" test/dex.yaml | k apply -f - >/dev/null
k rollout status deploy/dex --timeout=180s

helm --kube-context "$KCTX" -n "$NS" upgrade --install phronesis ./phronesis \
  --set image.repository="${IMAGE%%:*}" --set image.tag="${IMAGE##*:}" --set image.pullPolicy=Never \
  --set publicURL="$BASE" \
  --set auth.adminPassword="$ADMIN_PW" \
  --set oidc.enabled=true \
  --set oidc.issuer="http://localhost:${DEX_PORT}/dex" \
  --set oidc.providerURL="http://dex:5556/dex" \
  --set oidc.clientSecret=phronesis-dex-client-secret \
  --set persistence.size=1Gi \
  --wait --timeout 180s
k get pods -o wide

start_pf
run_smoke full

# Persistence: delete the pod; the Deployment recreates it on the same PVC.
old=$(k get pod -l app.kubernetes.io/instance=phronesis -o jsonpath='{.items[0].metadata.name}')
k delete pod "$old" --wait=true
k rollout status deploy/phronesis --timeout=180s
new=$(k get pod -l app.kubernetes.io/instance=phronesis -o jsonpath='{.items[0].metadata.name}')
echo "pod replaced: $old -> $new"
[ "$old" != "$new" ]
start_pf
run_smoke persist
echo "HELM_SMOKE: PASS"
