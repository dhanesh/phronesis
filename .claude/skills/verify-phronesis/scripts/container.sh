#!/usr/bin/env bash
# Drive a container deployment feature for one instance and print the same
# JSON shape as drive.mjs ({"feature","instance","ok","checks","artifacts"}).
#
#   scripts/container.sh deploy-compose <instance>   # Docker Compose in Colima
#   scripts/container.sh deploy-helm    <instance>   # Helm chart in Colima k3s
#
# The instance name is the compose project / Kubernetes namespace, so two
# owners never share one. Ports come from verify_evidence.py port. The image
# must carry org.opencontainers.image.revision == HEAD (build it with
# `make image`, DOCKER_CONTEXT=colima-phronesis-k8s for Helm). The smoke
# scripts remove only their own project/namespace; nothing under .verify/.
# Exit 0 = all checks passed, 1 = a check failed, 2 = usage/precondition.
set -uo pipefail
FEATURE="${1:-}" INSTANCE="${2:-}"
SKILL="$(cd "$(dirname "$0")/.." && pwd)"
ROOT="$(git -C "$SKILL" rev-parse --show-toplevel)"
case "$FEATURE" in deploy-compose|deploy-helm) ;; *) echo "usage: container.sh deploy-compose|deploy-helm <instance>" >&2; exit 2 ;; esac
[[ "$INSTANCE" =~ ^[a-z0-9][a-z0-9-]{0,40}$ ]] || { echo "bad instance name" >&2; exit 2; }
IMAGE="${IMAGE:-phronesis:dev}"
if [ "$FEATURE" = deploy-compose ]; then DCTX="${DOCKER_CONTEXT:-colima}"; else DCTX="${DOCKER_CONTEXT:-colima-phronesis-k8s}"; fi
HEAD=$(git -C "$ROOT" rev-parse HEAD)
REV=$(docker --context "$DCTX" image inspect "$IMAGE" --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' 2>/dev/null || true)
if [ "$REV" != "$HEAD" ]; then
  echo "image $IMAGE in context $DCTX is revision '${REV:-none}', HEAD is $HEAD: run make image DOCKER_CONTEXT=$DCTX" >&2
  exit 2
fi
EV="python3 $SKILL/scripts/verify_evidence.py"
P1=$($EV port --instance "$INSTANCE" --base 4300 --span 200 | sed -n 's/^PORT: *//p')
P2=$($EV port --instance "$INSTANCE-idp" --base 4300 --span 200 | sed -n 's/^PORT: *//p')
[ -n "$P1" ] && [ -n "$P2" ] || { echo "could not claim ports" >&2; exit 2; }
OUT="$ROOT/.verify-run/$INSTANCE/artifacts/$FEATURE"
mkdir -p "$OUT"
LOG="$OUT/smoke.log"
if [ "$FEATURE" = deploy-compose ]; then
  PROJECT="$INSTANCE" PHRONESIS_PORT="$P1" DEX_PORT="$P2" DOCKER_CONTEXT="$DCTX" PHRONESIS_IMAGE="$IMAGE" OUT_DIR="$OUT" \
    "$ROOT/deploy/compose/smoke.sh" >"$LOG" 2>&1
else
  NAMESPACE="$INSTANCE" WIKI_PORT="$P1" DEX_PORT="$P2" IMAGE="$IMAGE" OUT_DIR="$OUT" \
    "$ROOT/deploy/helm/smoke.sh" >"$LOG" 2>&1
fi
RC=$?
$EV release --instance "$INSTANCE" >/dev/null 2>&1 || true
$EV release --instance "$INSTANCE-idp" >/dev/null 2>&1 || true
python3 - "$FEATURE" "$INSTANCE" "$OUT" "$RC" "$IMAGE" "$REV" <<'PY'
import json, os, sys
feature, instance, out, rc, image, rev = sys.argv[1:7]
checks, arts = [], []
for phase in ("full", "persist"):
    p = os.path.join(out, phase + ".json")
    if os.path.exists(p):
        arts.append(p)
        try:
            d = json.load(open(p))
            checks += [{"name": f"{phase}:{c['name']}", "ok": c["ok"], "detail": c.get("detail", "")} for c in d["checks"]]
        except Exception as e:
            checks.append({"name": f"{phase}:parse", "ok": False, "detail": str(e)})
    else:
        checks.append({"name": f"{phase}:ran", "ok": False, "detail": "no output (see smoke.log)"})
log = os.path.join(out, "smoke.log")
text = open(log).read() if os.path.exists(log) else ""
marker = "COMPOSE_SMOKE: PASS" if feature == "deploy-compose" else "HELM_SMOKE: PASS"
checks.append({"name": "smoke_script_pass", "ok": rc == "0" and marker in text, "detail": f"exit {rc}"})
checks.append({"name": "image_revision_is_head", "ok": True, "detail": f"{image} {rev}"})
if feature == "deploy-compose":
    checks.append({"name": "container_healthy", "ok": "container health: healthy" in text})
else:
    checks.append({"name": "pod_replaced_on_same_pvc", "ok": "pod replaced:" in text})
arts.append(log)
res = {"feature": feature, "instance": instance, "ok": all(c["ok"] for c in checks), "checks": checks}
rp = os.path.join(out, "result.json")
res["artifacts"] = arts + [rp]
json.dump(res, open(rp, "w"), indent=1)
print(json.dumps(res))
sys.exit(0 if res["ok"] else 1)
PY
