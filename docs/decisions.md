# Decisions

Decisions made by the project owner, newest last. Agents re-read this after a
resume or context compaction.

## 2026-11-26

- **Verify, then fix broken features before anything else.** Asked for a check
  of what works, a `verify-phronesis` skill built with verification-skill-forge,
  and fixes for the current feature set before any new work.
  - Done: the login `?next` bug that broke the MCP OAuth flow for signed-out
    users (commit c88c9ca).
  - Open, waiting on the owner's design input: API key requests (no create
    endpoint) and OIDC RBAC (every session is admin; users are never projected).

## 2026-11-27

- **OIDC default role is `viewer`.** A new OIDC user whose claims map to no
  role is projected as `viewer`; admins promote. Group-claim mapping (S8) is
  deferred. Every session's role comes from the projected users row, never a
  hardcoded admin.
- **API keys follow the spec request -> approve flow (TN7).** Signed-in users
  with a users row submit `POST /api/keys/requests` from a small request form;
  the admin approves in the existing Keys panel and sees the plaintext once.
- **API keys work on `/mcp` and the HTTP API alike.** A valid bearer key (or
  OAuth access token) authenticates `/api/*` the same way a session cookie does,
  bounded by the key's scope and workspace.
- **RBAC is enforced on pages.** Every page, workspace-page and media route
  checks the caller's role (`read` / `write`), for sessions and keys alike.
- **Write a comprehensive role catalog** for a wiki that may run in an
  enterprise setup (docs/roles.md): instance, workspace, page-level and
  non-human roles, with a permission matrix and what is implemented today.
- **Open design calls taken unattended (owner: "run with Jev's and the
  advisor's decisions").** Provisional; the owner may revisit.
  - A key never outranks its owner: effective role = min(key scope, owner
    role). (Jev: cap 0.70, confidence 0.55 - low.)
  - OAuth consent step as an SPA page; approve/deny posts back to
    `/oauth/authorize`. (Jev 0.79.)
  - Dynamic client registration stays open only for loopback redirect URIs;
    other redirect URIs need an admin session. (Jev 0.48 vs admin-only 0.47 -
    low confidence; the stricter of the two that keeps local MCP clients
    working.)
  - OAuth access tokens stay limited to `/mcp`; only sessions and API keys
    reach `/api/*`. OAuth token role is capped at the user's role. (Jev 0.37
    "no"; advisor agreed.)
- **Goal: a beta release.** A full-featured wiki, OIDC tested against a real
  Dex IdP, verified working end to end, ready to gather feedback.
- **All local testing happens in Colima.** Docker and Kubernetes (Helm)
  deployment are supported out of the box and tested (Colima's k3s).
- **Beta scope (Jev ranking, rule-bounded, unattended).** Rule: a beta MUST is
  something without which a team cannot do basic wiki work for a week, cannot
  operate it safely, or cannot test the "humans AND agents" promise.
  - In: Dex SSO (browser auth-code + PKCE), Docker Compose + Helm tested in
    Colima, wiki MCP tools (list/read/write/search; Jev 1.76), production
    config guards (1.42, low confidence; cheap safety), page delete/rename
    (1.35, low confidence; cheap), backup/restore docs (1.12).
  - Deferred: search (0.90), history (1.04), group-to-role mapping (0.95),
    audit viewer, comments, CRDT wiring, git sync, feedback link.
- **Implementation calls made unattended (2026-11-27), revisit freely.**
  - Editors may rename and delete pages (no maintainer role yet; roles.md
    lists moving `page.delete` to a maintainer role as a next step).
  - Real-IdP OIDC is selected by `PHRONESIS_OIDC_CLIENT_ID`; the HMAC stub is
    never active alongside it, and `PHRONESIS_ENV=production` (set in the
    image) refuses the stub and the default admin password.
  - Local Kubernetes tests run in a separate Colima profile `phronesis-k8s`
    so the default profile's running containers are untouched; `helm` and
    `kubectl` were installed with Homebrew for this.
  - Sessions stay in memory for the beta (documented: users sign in again
    after a restart).
- **Security scans before publishing (user, 2026-11-27).** "does the ci have
  gitsecrets, semgrep and trivy? also sonarqube scanner? run it locally first
  to fix things before publishing." None were in CI. All now run from one
  script (`scripts/security-scan.sh`, `make security`), locally in Colima
  and in `.github/workflows/security.yml`. Secret scanning uses gitleaks
  (it covers git-secrets' AWS patterns and many more). Jev calls
  (unattended, all confident):
  - Toolchain `go1.26.11` rather than `go1.27.4` (Choice 0.88): smallest
    change that clears 18 reachable stdlib CVEs. `golang.org/x/crypto`
    v0.59 raised the module floor to Go 1.26.
  - CI gate (0.90): fail on any gitleaks leak, any semgrep ERROR, trivy
    HIGH/CRITICAL vulns with a fix, HIGH/CRITICAL misconfig or secrets, and
    any reachable govulncheck vuln; everything else is report-only.
  - SonarQube job driven by `SONAR_TOKEN` + `SONAR_HOST_URL` (SonarQube
    Cloud or self-hosted), skipped with a notice when unset (0.90).
  - Pin every action to a commit SHA, kept current by dependabot's new
    `github-actions` entry with a 7-day cooldown (Noul 0.86).
  - Upgrade Playwright now (0.85): 1.50.0 -> 1.55.1, the smallest release
    fixing GHSA-7mvr-c777-76hp; validated by the full e2e suite.
  - False positives, suppressed inline or by path with a reason: cookies
    with `Secure: requestIsSecure(r)` (0.73), same-origin redirects behind
    `safeNext` (0.79), Dockerfile DS031 on a key *path* (0.84).
  - Rewrite the two super-linear editor regexes (0.77): a collaborator's
    crafted line could stall every viewer's editor.
