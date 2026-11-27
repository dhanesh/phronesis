#!/usr/bin/env bash
# Security scans for phronesis. The same script runs locally (in Colima)
# and in CI (.github/workflows/security.yml), so a green local run means a
# green CI run.
#
#   scripts/security-scan.sh [all|secrets|sast|deps|image|sonar]...
#
# Scanners (versions pinned below; run in containers, except govulncheck):
#   secrets  gitleaks over the full git history          (.gitleaks.toml)
#   sast     semgrep registry rulesets                   (.semgrepignore)
#   deps     govulncheck (reachable Go vulns) + trivy fs: dependency vulns,
#            secrets, Dockerfile/Helm/k8s misconfig      (.trivyignore.yaml)
#   image    trivy image scan of $IMAGE (default phronesis:dev; `make image`)
#   sonar    SonarQube scanner; needs SONAR_HOST_URL and SONAR_TOKEN
#
# Gate (decision 2026-11-27, docs/decisions.md): fail on any gitleaks leak,
# any semgrep ERROR, trivy HIGH/CRITICAL vulns that have a fix, trivy
# HIGH/CRITICAL misconfig or secrets, and any reachable govulncheck vuln.
# Lower severities are written to the reports only.
#
# Environment: DOCKER_CONTEXT (default: colima locally, unset in CI),
# OUT (report dir, default dist/scan), IMAGE.
set -uo pipefail

GITLEAKS_IMAGE=zricethezav/gitleaks:v8.30.1
SEMGREP_IMAGE=semgrep/semgrep:1.187.0
TRIVY_IMAGE=aquasec/trivy:0.76.2
SONAR_SCANNER_VERSION=8.5.0.16571
GOVULNCHECK=golang.org/x/vuln/cmd/govulncheck@v1.1.4

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
OUT=${OUT:-dist/scan}
IMAGE=${IMAGE:-phronesis:dev}
if [ -z "${CI:-}" ]; then DOCKER_CONTEXT=${DOCKER_CONTEXT:-colima}; fi
mkdir -p "$OUT"

dk() { if [ -n "${DOCKER_CONTEXT:-}" ]; then docker --context "$DOCKER_CONTEXT" "$@"; else docker "$@"; fi; }
FAILED=()
step() { echo; echo "=== $1"; }
gate() { if [ "$2" -ne 0 ]; then FAILED+=("$1"); echo "FAIL: $1"; else echo "PASS: $1"; fi; }

scan_secrets() {
  step "gitleaks (git history)"
  dk run --rm -v "$ROOT:/repo" "$GITLEAKS_IMAGE" git /repo \
    --config /repo/.gitleaks.toml --redact --no-banner \
    --report-format json --report-path "/repo/$OUT/gitleaks.json" --exit-code 1
  gate gitleaks $?
}

scan_sast() {
  step "semgrep"
  dk run --rm -v "$ROOT:/src" -w /src "$SEMGREP_IMAGE" semgrep scan \
    --config p/default --config p/golang --config p/secrets \
    --config p/dockerfile --config p/kubernetes --config p/github-actions \
    --metrics=off --json -o "$OUT/semgrep.json" --quiet
  local rc=$?
  [ $rc -ne 0 ] && { gate semgrep-run $rc; return; }
  python3 - "$OUT/semgrep.json" <<'EOF'
import json, sys
d = json.load(open(sys.argv[1]))
res = d["results"]
for r in res:
    print(f'  {r["extra"]["severity"]:7} {r["check_id"].split(".")[-1]}  {r["path"]}:{r["start"]["line"]}')
errs = [r for r in res if r["extra"]["severity"] == "ERROR"]
print(f'semgrep: {len(res)} findings, {len(errs)} ERROR')
sys.exit(1 if errs else 0)
EOF
  gate semgrep $?
}

trivy() {
  dk run --rm -v trivy-cache:/root/.cache -v "$ROOT:/src" -w /src "$TRIVY_IMAGE" "$@"
}

scan_deps() {
  step "govulncheck (reachable Go vulnerabilities)"
  go run "$GOVULNCHECK" ./... | tee "$OUT/govulncheck.txt" | tail -20
  gate govulncheck "${PIPESTATUS[0]}"

  step "trivy fs (deps, secrets, misconfig incl. Helm chart)"
  # The chart refuses to render without these; values are placeholders.
  local helmset="auth.adminPassword=scan,publicURL=https://wiki.example.com,oidc.enabled=true,oidc.issuer=https://idp.example.com,oidc.clientSecret=scan,ingress.enabled=true,ingress.host=wiki.example.com"
  trivy fs --quiet --scanners vuln,secret,misconfig \
    --skip-dirs dist,frontend/node_modules,frontend/dist,internal/webfs/dist \
    --helm-set "$helmset" --ignorefile .trivyignore.yaml \
    --format json -o "$OUT/trivy-fs.json" .
  local rc=$?
  [ $rc -ne 0 ] && { gate trivy-fs-run $rc; return; }
  trivy_report "$OUT/trivy-fs.json"
  gate trivy-fs $?
}

trivy_report() {
  python3 - "$1" <<'EOF'
import json, sys
d = json.load(open(sys.argv[1]))
bad = 0
for r in d.get("Results", []):
    t = r["Target"]
    for v in r.get("Vulnerabilities") or []:
        block = v["Severity"] in ("HIGH", "CRITICAL") and v.get("FixedVersion")
        bad += bool(block)
        print(f'  {"BLOCK" if block else "info "} vuln {v["Severity"]:8} {v["PkgName"]} {v["InstalledVersion"]} -> {v.get("FixedVersion") or "-"} {v["VulnerabilityID"]} ({t})')
    for m in r.get("Misconfigurations") or []:
        if m["Status"] != "FAIL":
            continue
        block = m["Severity"] in ("HIGH", "CRITICAL")
        bad += block
        print(f'  {"BLOCK" if block else "info "} misconfig {m["Severity"]:8} {m["ID"]} {m["Title"]} ({t})')
    for s in r.get("Secrets") or []:
        block = s["Severity"] in ("HIGH", "CRITICAL")
        bad += block
        print(f'  {"BLOCK" if block else "info "} secret {s["Severity"]:8} {s["RuleID"]} ({t}:{s["StartLine"]})')
print(f"trivy: {bad} blocking")
sys.exit(1 if bad else 0)
EOF
}

scan_image() {
  step "trivy image ($IMAGE)"
  dk image inspect "$IMAGE" >/dev/null 2>&1 || { echo "image $IMAGE not found: run 'make image' first"; gate trivy-image 1; return; }
  dk save "$IMAGE" -o "$OUT/image.tar"
  trivy image --quiet --input "$OUT/image.tar" --format json -o "$OUT/trivy-image.json"
  local rc=$?
  rm -f "$OUT/image.tar"
  [ $rc -ne 0 ] && { gate trivy-image-run $rc; return; }
  trivy_report "$OUT/trivy-image.json"
  gate trivy-image $?
}

scan_sonar() {
  step "sonarqube"
  if [ -z "${SONAR_HOST_URL:-}" ] || [ -z "${SONAR_TOKEN:-}" ]; then
    echo "skip: set SONAR_HOST_URL and SONAR_TOKEN (see docs/security-scans.md)"; return
  fi
  go test -coverprofile="$OUT/coverage.out" ./... >/dev/null || { gate sonar-coverage 1; return; }
  # The official scanner image is amd64-only, and its Go analyzer crashes
  # under emulation on arm64 hosts, so build a small native scanner image.
  local img="local/sonar-scanner:$SONAR_SCANNER_VERSION"
  if ! dk image inspect "$img" >/dev/null 2>&1; then
    printf '%s\n' \
      'FROM eclipse-temurin:21-jre' \
      "ARG V=$SONAR_SCANNER_VERSION" \
      'RUN apt-get update && apt-get install -y --no-install-recommends unzip curl nodejs && rm -rf /var/lib/apt/lists/* \' \
      ' && curl -fsSLo /tmp/s.zip https://binaries.sonarsource.com/Distribution/sonar-scanner-cli/sonar-scanner-cli-${V}.zip \' \
      ' && unzip -q /tmp/s.zip -d /opt && mv /opt/sonar-scanner-${V} /opt/sonar-scanner && rm /tmp/s.zip' \
      'ENV PATH=/opt/sonar-scanner/bin:$PATH' \
      'WORKDIR /usr/src' \
      'ENTRYPOINT ["sonar-scanner"]' | dk build -q -t "$img" - >/dev/null || { gate sonar-image 1; return; }
  fi
  # SONAR_DOCKER_NETWORK lets a local scan reach a SonarQube container
  # (e.g. container:phronesis-sonarqube with SONAR_HOST_URL=http://localhost:9000).
  dk run --rm ${SONAR_DOCKER_NETWORK:+--network "$SONAR_DOCKER_NETWORK"} \
    -e SONAR_HOST_URL -e SONAR_TOKEN -v "$ROOT:/usr/src" "$img" \
    -Dsonar.go.coverage.reportPaths="$OUT/coverage.out" \
    -Dsonar.working.directory="$OUT/.scannerwork" \
    -Dsonar.qualitygate.wait=true > "$OUT/sonar.log" 2>&1
  local rc=$?
  tail -3 "$OUT/sonar.log"
  gate sonar $rc
}

want=("$@"); [ ${#want[@]} -eq 0 ] && want=(all)
for w in "${want[@]}"; do
  case "$w" in
    all) scan_secrets; scan_sast; scan_deps; scan_image; scan_sonar ;;
    secrets) scan_secrets ;; sast) scan_sast ;; deps) scan_deps ;;
    image) scan_image ;; sonar) scan_sonar ;;
    *) echo "unknown scan: $w" >&2; exit 2 ;;
  esac
done

echo
if [ ${#FAILED[@]} -gt 0 ]; then echo "SECURITY_SCAN: FAIL (${FAILED[*]}); reports in $OUT/"; exit 1; fi
echo "SECURITY_SCAN: PASS; reports in $OUT/"
