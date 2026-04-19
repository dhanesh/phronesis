# User Stories: collab-wiki

_See also: [PRD](PRD.md) for business context · [DECISIONS](DECISIONS.md) for architectural rationale._

## Epic

Evolve phronesis from a single-user Markdown wiki into a multi-user, API-scriptable, git-backed knowledge platform, where "saved" means truly saved, collaboration is visible, and agents are first-class principals.

---

### US-1: Two-state durability awareness

**As a** Team Knowledge Curator _(PRD Persona 1)_
**I want** the editor to clearly show when my latest edit is visible to peers vs. truly on disk
**So that** I know whether it's safe to close the tab or expect loss on crash

**Priority:** P0 (binding RT)
**Estimate:** _To be estimated by team_

**Acceptance Criteria:**
- [ ] When I type, indicator transitions idle → dirty within one frame (U3 mental model)
- [ ] When the server acks my op (peer broadcast complete), indicator shows "Synced" within 300ms p95 (T2, RT-2.1)
- [ ] When the server flushes to disk (O8: 3s idle or 100 ops), indicator upgrades to "Saved" (RT-2.3)
- [ ] "Saved" is visually distinct from "Synced" (bolder weight) so the two states are not confusable (TN6)
- [ ] On network loss (no heartbeat within 5s), indicator shows "Offline" and editor becomes read-only (U2, DRT-6)

**Traces to:** RT-2, RT-2.1, RT-2.2, RT-2.3, T2, U2, U3, O8, TN1, TN6
**PRD Sections:** 3 (Success Metrics), 6 (Must Have, Boundaries), 7 (User Flows), 9 (Risks)

---

### US-2: Presence awareness in shared docs

**As a** Team Knowledge Curator
**I want** to see who else is currently viewing/editing the document I'm in
**So that** I know when coordination is needed and avoid stepping on in-flight edits

**Priority:** P0 (invariant)
**Estimate:** _To be estimated by team_

**Acceptance Criteria:**
- [ ] When I open a doc, a presence list appears showing all other editors currently in it (U1)
- [ ] When another user joins, their entry appears within ≤ 1s (adequate for U1; stricter SLO is not invariant)
- [ ] When another user disconnects or closes the tab, they disappear from the list within heartbeat-timeout (5s)
- [ ] Remote cursors are NOT shown in v1 (explicitly out of scope per PRD §8)

**Traces to:** U1
**PRD Sections:** 4 (Personas), 7 (User Flows), 8 (Out of Scope)

---

### US-3: Offline-safe editing

**As a** Team Knowledge Curator
**I want** the editor to stop accepting input when my connection drops
**So that** I don't silently lose changes I thought were being collaborated

**Priority:** P0 (invariant)
**Estimate:** _To be estimated by team_

**Acceptance Criteria:**
- [ ] On disconnect (no heartbeat within HEARTBEAT_TIMEOUT_MS), editor enters read-only mode (U2)
- [ ] Durability indicator shows "Offline"
- [ ] On reconnect, editor becomes editable again; latest remote state is fetched before resuming edits
- [ ] Client does NOT buffer ops locally during disconnect (explicit v1 scope decision)

**Traces to:** U2, DRT-6
**PRD Sections:** 6 (Must Have), 7 (User Flows)

---

### US-4: Programmatic edit via API key

**As an** Agent / Integration Operator _(PRD Persona 2)_
**I want** to edit documents via HTTP requests authenticated by an API key
**So that** automated scripts and agents can update wiki content without logging in as a human

**Priority:** P0 (invariant)
**Estimate:** _To be estimated by team_

**Acceptance Criteria:**
- [ ] API request with `API-KEY: <token>` is authenticated against both token types: user-owned PAT and workspace service account (S1)
- [ ] User PATs act as the owning user for authz + audit purposes
- [ ] Workspace service accounts appear as a distinct principal class in audit logs (`principal_type: service_account`, TN8)
- [ ] API keys can be individually revoked without affecting other keys or user sessions
- [ ] Rate limiting applies (TN7: always-on auth-endpoint floor at minimum)

**Traces to:** S1, S2, S5, S8 (audit schema), RT-5
**PRD Sections:** 4 (Persona 2), 6 (Must Have), 7 (API flow)

---

### US-5: Password authentication with secure session

**As a** Team Knowledge Curator
**I want** to log in with a password over HTTPS
**So that** my human identity is recognized and my edits are attributed to me

**Priority:** P0 (invariant)
**Estimate:** _To be estimated by team_

**Acceptance Criteria:**
- [ ] Passwords are stored as argon2id hashes (S3)
- [ ] Session cookie is Secure, HttpOnly, SameSite=Lax (S3)
- [ ] State-changing requests require a CSRF token (S3)
- [ ] Brute-force attempts on /login are rate-limited server-side regardless of reverse-proxy config (TN7, RT-10)

**Traces to:** S3, S5, RT-10, TN7
**PRD Sections:** 5 (Assumptions), 6 (Must Have)

---

### US-6: SSO login via OIDC

**As a** Workspace Admin _(PRD Persona 3)_
**I want** to configure an OIDC IdP so my organization's users can log in with corporate SSO
**So that** my users don't maintain a separate password and access control follows org policy

**Priority:** P0 (invariant — see TN5 for phasing interpretation)
**Estimate:** _To be estimated by team_

**Acceptance Criteria:**
- [ ] OIDC adapter is present at launch with a working stub provider in CI (TN5, RT-11.3)
- [ ] id_token signature validated against JWKS (TTL-cached, refreshed on unknown `kid`) (S4)
- [ ] `iss` and `aud` claims are validated (S4)
- [ ] Claim mapping is configurable per-provider with schema versioning (S8, RT-11.2)
- [ ] Enabling a real IdP is a config change, not a code change
- [ ] SAML is explicitly NOT supported in v1 (PRD §6 Won't Have)

**Traces to:** S4, S8, RT-11, TN5
**PRD Sections:** 6 (Must Have, Won't Have), 9 (Risks — TN5)

---

### US-7: Workspace RBAC (viewer / editor / admin)

**As a** Workspace Admin
**I want** to assign viewer / editor / admin roles to users and service accounts within a workspace
**So that** access follows the principle of least privilege without custom ACL overhead

**Priority:** P0 (invariant)
**Estimate:** _To be estimated by team_

**Acceptance Criteria:**
- [ ] Three roles exist: viewer (read), editor (read+write), admin (read+write+manage workspace)
- [ ] Both users AND workspace service accounts use the same three roles (TN8, RT-5)
- [ ] A single authorization function evaluates the role for every request path
- [ ] Audit log records `principal_type` alongside `principal_id` (S2, TN8 propagation)

**Traces to:** B2, S1, S2, TN8, RT-5
**PRD Sections:** 6 (Must Have)

---

### US-8: Media in-editor with safe storage

**As a** Team Knowledge Curator
**I want** to paste or upload images and attachments into my markdown, and see them render in place
**So that** my notes can include diagrams, screenshots, and docs

**Priority:** P0 (invariant on rendering; implementation details per TN4)
**Estimate:** _To be estimated by team_

**Acceptance Criteria:**
- [ ] Media renders live inside the editor (U4)
- [ ] Upload content-type is enforced against the allow-list (S6): png, jpeg, gif, webp, mp4, pdf (configurable)
- [ ] Per-workspace storage quota is enforced (default 5GB, S6); over-quota uploads return HTTP 413
- [ ] Media is stored in a blob store OUTSIDE git; markdown references media by hash URL (`/media/<sha>`, TN4, RT-6)
- [ ] Snapshot backup (O7) includes blob bytes alongside markdown (TN4 propagation)

**Traces to:** U4, S6, O7, TN4, RT-6
**PRD Sections:** 6 (Must Have), 9 (Risks — TN4)

---

### US-9: XSS-safe rendering

**As a** Workspace Admin
**I want** pasted/injected HTML to be neutralized so users can't XSS each other
**So that** a shared workspace stays safe even when one user pastes from an untrusted source

**Priority:** P0 (invariant)
**Estimate:** _To be estimated by team_

**Acceptance Criteria:**
- [ ] Markdown store rejects/strips raw HTML attributes by default (S7, RT-9.1)
- [ ] Rendered HTML passes an allow-list sanitizer (S7, RT-9.2)
- [ ] Server emits a strict Content-Security-Policy header on responses (S7, RT-9.3)

**Traces to:** S7, RT-9
**PRD Sections:** 6 (Must Have)

---

### US-10: Reliable git backup

**As a** Workspace Admin
**I want** my workspace's markdown to push to a configured git remote on a rolling basis
**So that** I have off-site durability and version history even if the server disk is lost

**Priority:** P0 (invariant)
**Estimate:** _To be estimated by team_

**Acceptance Criteria:**
- [ ] Push direction is ONE-WAY (server → remote); remote pulls are not applied at runtime (T6)
- [ ] One workspace maps to exactly one git repo with its own remote URL (T7)
- [ ] Push queue is bounded (1000 commits / 100MB); alert fires at 100 pending (T10)
- [ ] On SIGTERM, unpushed commits spill to a persistent journal; next startup replays them before /readyz goes green (TN10, RT-7, RT-12)
- [ ] /readyz does NOT fail on transient remote unreachability (TN9, RT-8)

**Traces to:** T6, T7, T10, O5, RT-7, RT-8, RT-12, TN9, TN10
**PRD Sections:** 6 (Must Have), 9 (Risks — TN9, TN10)

---

### US-11: Operational observability

**As a** Workspace Admin
**I want** Prometheus metrics, OpenTelemetry traces, structured logs, and health endpoints
**So that** I can detect problems, trace failures, and integrate with standard monitoring stacks

**Priority:** P0 (invariant)
**Estimate:** _To be estimated by team_

**Acceptance Criteria:**
- [ ] `/metrics` endpoint in Prometheus exposition format (O1)
- [ ] OpenTelemetry traces cover HTTP → auth → CRDT op → flush → push pipeline (O2)
- [ ] All server logs are slog-JSON with `request_id`, `principal_id`, `workspace_id` (O3)
- [ ] `/healthz` (liveness) distinct from `/readyz` (readiness) (O4)
- [ ] Git remote health is a separate metric + alert, NOT a /readyz condition (TN9, RT-8)

**Traces to:** O1, O2, O3, O4, TN9, RT-8
**PRD Sections:** 6 (Must Have), 7 (Admin flow)

---

### US-12: Graceful upgrade

**As a** Workspace Admin
**I want** the server to shut down cleanly on SIGTERM so I can upgrade without data loss
**So that** rolling upgrades (especially on k8s) don't lose edits or pending pushes

**Priority:** P0 (invariant)
**Estimate:** _To be estimated by team_

**Acceptance Criteria:**
- [ ] SIGTERM stops new connections, notifies clients of pending shutdown (editor goes read-only per U2)
- [ ] All active CRDT rooms flush to disk (O8)
- [ ] Git push queue drains within configurable window (default 30s)
- [ ] Any remaining queue spills to a persistent journal file (RT-7, TN10)
- [ ] Exit code is non-zero if drain timed out with state outstanding
- [ ] Next startup replays the journal before declaring readiness

**Traces to:** O5, O6, RT-7, RT-12, TN10
**PRD Sections:** 6 (Must Have)

---

## Story Map

| Priority | Story | Constraints | Dependencies | Wave | Status |
|---|---|---|---|---|---|
| P0 | US-1 Two-state durability | RT-2, T2, U3, O8 | — (binding) | 1 | In Wave 1 |
| P0 | US-2 Presence | U1 | US-1 | 3 | Planned |
| P0 | US-3 Offline read-only | U2 | US-1 | 1–3 | Planned |
| P0 | US-4 API key edits | S1, RT-5 | US-7 | 3 | Planned |
| P0 | US-5 Password auth | S3, RT-10 | — | 3 | Planned |
| P0 | US-6 OIDC SSO | S4, RT-11 | US-5 | 3 | Planned |
| P0 | US-7 Workspace RBAC | B2, RT-5 | — | 3 | Planned |
| P0 | US-8 Safe media | U4, S6, RT-6 | US-7 | 3 | Planned |
| P0 | US-9 XSS safety | S7, RT-9 | — | 5 | Planned |
| P0 | US-10 Git backup | T6, T10, RT-7 | US-7 | 4 | Planned |
| P0 | US-11 Observability | O1–O4, RT-8 | — | 5 | Planned |
| P0 | US-12 Graceful upgrade | O5, RT-12 | US-10 | 4 | Planned |

## Dependencies Graph

```
              US-1 (binding RT-2)
                /    |    \
             US-3  US-2   US-4
              |      \     |
              |       US-7  <-- US-5 --> US-6
              |        |     \       \
              |       US-8    US-10   |
              |                 |     |
              v                 v     v
            US-11 <- US-9 <- US-12 <-+
```

---
_Generated from `.manifold/collab-wiki.json` + `.manifold/collab-wiki.md` · Cross-reference: [PRD](PRD.md)_
