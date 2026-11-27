# Security scans

`make security` (or `scripts/security-scan.sh [secrets|sast|deps|image|sonar]`)
runs every scanner. CI runs the same script in
`.github/workflows/security.yml` on each pull request, on pushes to `main`
and weekly. Reports go to `dist/scan/`.

| Scan | Tool (pinned in the script) | What it checks | Config |
|---|---|---|---|
| secrets | gitleaks | the whole git history | `.gitleaks.toml` |
| sast | semgrep (default, golang, secrets, dockerfile, kubernetes, github-actions rulesets) | source, workflows, manifests | `.semgrepignore`, inline `nosemgrep` with a reason |
| deps | govulncheck | Go vulnerabilities your code reaches | - |
| deps | trivy fs | Go and npm dependency CVEs, secrets, Dockerfile, Helm chart (rendered), k8s manifests | `.trivyignore.yaml` |
| image | trivy image | the release image (`make image` first) | `.trivyignore.yaml` |
| sonar | SonarQube scanner | bugs, vulnerabilities, hotspots, coverage | `sonar-project.properties` |

## What fails the build

Any gitleaks leak; any semgrep `ERROR`; trivy HIGH or CRITICAL
vulnerabilities that have a fixed version; trivy HIGH or CRITICAL
misconfigurations or secrets; any vulnerability govulncheck finds reachable;
a failed SonarQube quality gate. Everything else is reported only.

## Suppressing a finding

Suppress only a verified false positive, as narrowly as the tool allows,
and always with the reason: `// nosemgrep: <rule> -- <why>` on or above the
line, a path-scoped entry with a `statement` in `.trivyignore.yaml`, or a
path-and-pattern allowlist in `.gitleaks.toml`. Do not add bare rule IDs.

## SonarQube

CI: set the repository secret `SONAR_TOKEN` and `SONAR_HOST_URL` (a secret
or a variable; `https://sonarcloud.io` for SonarQube Cloud, where you also
add `sonar.organization` to `sonar-project.properties`). Without them the
job is skipped with a notice.

Local, with a throwaway server in Colima:

```bash
docker --context colima run -d --name phronesis-sonarqube -p 19000:9000 sonarqube:community
# wait for http://localhost:19000/api/system/status to say UP, sign in as
# admin/admin, change the password, create a global analysis token, then:
SONAR_HOST_URL=http://localhost:9000 SONAR_TOKEN=<token> \
  SONAR_DOCKER_NETWORK=container:phronesis-sonarqube scripts/security-scan.sh sonar
docker --context colima rm -f phronesis-sonarqube
```

The official scanner image is amd64-only and its Go analyzer crashes under
emulation on Apple silicon, so the script builds a small native scanner
image (`local/sonar-scanner:<version>`) the first time.

## Known, accepted

- The distroless base image carries Debian `ca-certificates`/`tzdata`
  updates (severity UNKNOWN) until upstream rebuilds `:nonroot`;
  `make image` pulls the newest base each time.
