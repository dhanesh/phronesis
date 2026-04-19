# PRD: collab-wiki

| Field | Value |
|-------|-------|
| **Author** | Manifold-generated (m4) |
| **Status** | Draft |
| **Created** | 2026-04-18 |
| **Last Updated** | 2026-04-18 |
| **Manifold** | `.manifold/collab-wiki.json` |
| **Phase** | GENERATED · solution option C · binding RT-2 |

## 1. Problem Statement

Phronesis today is a single-user Markdown wiki with a filesystem source-of-truth, cookie password auth, SSE-driven live updates, and a CodeMirror-based Svelte editor. To grow into a self-hosted project knowledge system, it must support real-time multi-user collaboration, programmatic edits from agents/scripts, enterprise-compatible SSO, off-site durability via git, and enough throughput to serve org-scale teams — without regressing the existing markdown-as-truth design or single-binary operability.

**Who is affected:** Small-to-org-scale teams (≤500 concurrent editors, ≤10K docs per workspace) who self-host a knowledge base and want a git-backed, multi-user editor that is usable by both humans and agents.

**Current impact:** Single-user autosave; no API-key edit surface; no SSO; no collaborative editing; no git integration. Current phronesis is functional but single-player.

**Why now:** User-driven evolution toward a knowledge platform where agent automation, multi-human collaboration, and durable backup converge.

## 2. Business Objectives

- **Strategic alignment:** Graduate phronesis from "nice local wiki" to "team knowledge platform" while preserving markdown-as-truth and single-binary operability.
- **Success criteria:** All four primary capabilities (CRDT multi-user collab, API-key edit API, password+OIDC auth, push-only git sync) simultaneously working at launch at the stated throughput target, under the stated durability and security constraints.

## 3. Success Metrics

| Metric | Target | Baseline | Constraint |
|---|---|---|---|
| Collab latency (keystroke A → peer B) | p95 < 300ms | N/A (no collab) | T2 |
| Read latency at 500 concurrent editors, 1K req/s | p95 < 100ms | N/A | T3 |
| Per-doc size cap | ≤ 2MB | — | T5 |
| Per-workspace docs supported | ≥ 10,000 | — | T4 |
| Client bundle (first load, gzipped) | ≤ 500KB | Current bundle (measure) | U5 |
| Edit-loss window on crash | ≤ 3s or ≤ 100 ops per room | Unbounded (autosave debounce) | O8, RT-2 |
| Git push backlog | ≤ 1000 commits / 100MB; alert at 100 | — | T10 |
| Per-workspace media quota (default) | 5GB | — | S6 |
| Audit retention (default) | 180 days | — | S2 |
| Repo-size thresholds | warn 1GB / alert 5GB | — | O9 |

## 4. Target Users & Personas

### Persona 1: Team Knowledge Curator
- **Needs:** Fast editor, live collaboration visibility, confidence that "saved" means saved.
- **Pain Points:** Losing edits on flaky connections; not knowing who else is in a doc.
- **Key Workflows:** Edit daily-notes doc, see presence list of colleagues, rename a wiki page and expect backlinks to update cleanly.

### Persona 2: Agent / Integration Operator
- **Needs:** Stable HTTP API keyed by API token, rate-limit clarity, idempotent edits.
- **Pain Points:** Conflating agent identity with a human user's account; unclear audit trail for automated edits.
- **Key Workflows:** Script that bulk-updates docs via the API, logs structured events, and can be revoked without touching user accounts.

### Persona 3: Workspace Admin
- **Needs:** SSO integration (OIDC), audit visibility, quota control, recovery playbook, predictable upgrade.
- **Pain Points:** Silent sync failures; ambiguous readiness state in k8s; media storage runaway.
- **Key Workflows:** Point the server at an OIDC IdP; watch alerts for push-queue depth; run snapshot backups.

## 5. Assumptions & Constraints

**Assumptions:**
- Deployment is always single-tenant with multiple workspaces (B3).
- Reverse proxy handles general rate-limiting; server supplies auth-endpoint floor only (S5 + TN7).
- v1 is single-replica on k8s; multi-replica deferred (TN3).
- Git sync is push-only — no inbound merges (T6).

**Constraints (boundaries summarized; see manifold for full list):**
- Per-doc size ≤ 2MB (T5) · client bundle ≤ 500KB gzipped (U5) · push queue ≤ 1000/100MB (T10) · media quota 5GB default per workspace (S6) · p95 collab latency < 300ms (T2) · p95 read latency < 100ms at scale (T3) · edit-loss window ≤ 3s / 100 ops (O8).

## 6. Requirements (MoSCoW)

### Must Have (Invariants — 24 constraints)

- **All four primary capabilities are equal INVARIANTs** (B1) — reinterpreted via TN1 as "durable within the O8 window".
- **Workspace-level RBAC (viewer/editor/admin)** (B2) — unified across users and service accounts per TN8.
- **Single deployment, multiple workspaces, shared user pool** (B3).
- **Ephemeral CRDT, .md on disk is source of truth** (T1).
- **Git sync is PUSH-ONLY** (T6).
- **One workspace = one git repository** (T7).
- **Deployable as single binary, Docker, and k8s (single-replica v1)** (T8).
- **Presence list per open document** (U1).
- **Editor read-only on disconnect** (U2).
- **Wiki-links / tags / task-lists / embedded media render live** (U4).
- **Dual principal model (user PATs + workspace service accounts)** (S1).
- **Comprehensive audit log (auth/write/admin/read)** (S2) — read path is async per S9.
- **Argon2id password auth + CSRF + secure cookies** (S3).
- **OIDC integration validates signature, issuer, audience; JWKS rotation** (S4).
- **Media content-type allow-list + per-workspace quota** (S6).
- **XSS defense at store AND render; CSP** (S7).
- **OIDC claim mapping configurable + schema-versioned** (S8).
- **Prometheus `/metrics`** (O1).
- **OpenTelemetry tracing** (O2).
- **Structured JSON logging (slog)** (O3).
- **`/healthz` + `/readyz`** (O4) — readiness scoped to traffic-serving per TN9.
- **Graceful shutdown drains state before exit** (O5) — bounded drain + journal spill per TN10.
- **Atomic disk writes before ACK** (O6).
- **CRDT quiescence flush ≤ 3s or 100 ops** (O8).

### Should Have (Boundaries — 8 constraints)

- **Collab latency p95 < 300ms** (T2).
- **Read latency p95 < 100ms at 500 concurrent editors** (T3).
- **Per-doc size ≤ 2MB** (T5).
- **Push queue bounded 1000 commits / 100MB** (T10).
- **Wiki-link rename is single atomic CRDT op** (T11).
- **Client bundle ≤ 500KB gzipped** (U5).
- **Input-size bounds + server-side auth RL floor** (S5 + TN7).
- **Read-audit is async + bounded-loss** (S9).

### Could Have (Goals — 5 constraints)

- **10K docs / 500 editors per workspace no-regression** (T4).
- **Phased OIDC enablement** (T9) — architecturally ready at launch per TN5.
- **300ms autosave indicator** (U3) — reinterpreted as two-state per TN6.
- **Periodic snapshot to S3/restic** (O7).
- **Repo-size monitoring + LFS guidance** (O9).

### Won't Have (This Release)

- **SAML 2.0 SSO** — OIDC only (explicit scope cut from T9).
- **Multi-replica k8s / HA across nodes** — single-replica v1; abstractions in place per TN3.
- **Bidirectional git sync / inbound merges** — push-only per T6.
- **Per-document ACLs / fine-grained scopes** — 3-role RBAC only per TN8.
- **Offline client editing / local-CRDT-buffer-and-replay** — read-only on disconnect per U2.
- **Remote cursors / per-user authorship highlights** — presence-list only per U1.
- **Embedded-media inside git working tree** — media lives out-of-git per TN4.

## 7. User Flows & Design

### Editor flow (human user)

1. User opens a document → server spins up (or joins) a CRDT Room.
2. User types → each op emits to server → server broadcasts to peer clients (`op_acked` within 300ms p95) → client's DurabilityIndicator shows "Synced".
3. Server flushes ops to disk per O8 (≤3s idle or ≤100 ops) → emits `op_saved` → client indicator upgrades to "Saved".
4. Presence list updates as users join/leave the doc.
5. On network loss, client receives no heartbeat → editor goes read-only, indicator shows "Offline". Reconnect restores editability.

### API flow (agent)

1. Agent makes a request with `API-KEY: <token>` header.
2. Server resolves the token to either a user PAT (acts as that user) or a workspace service account (distinct principal).
3. Authorization applies the workspace role (viewer/editor/admin).
4. Edit is applied through the same CRDT room as human edits; agent ops are audit-logged with `principal_type: service_account`.

### Admin flow

1. Admin configures the workspace's git remote and (optionally) OIDC provider.
2. Admin monitors `/metrics` — push queue depth, CRDT room count, audit buffer depth, repo-size.
3. Alerts fire on push-queue saturation, git remote unreachability, audit drops, repo > 1GB.
4. Admin runs graceful upgrade: SIGTERM → drain → journal-spill → restart → journal replay → ready.

## 8. Out of Scope

See "Won't Have (This Release)" above. Additionally:
- Markdown rendering extensions beyond the current wiki dialect + task lists.
- Client-side CRDT library (all CRDT state is server-authoritative in this release).
- Cross-workspace search / global indexing.

## 9. Risks & Mitigations

| Risk | Severity | Source | Mitigation |
|---|---|---|---|
| Crash loses in-memory CRDT state | High | T1 × B1 (TN1) | O8 flush policy + two-state indicator; redefine B1 bounded-loss |
| Full-read audit crushes throughput | High | S2 × T3 (TN2) | S9 async audit buffer + drop-oldest + metric |
| k8s multi-replica breaks in-proc CRDT | Medium | T8 × T1 (TN3) | v1 single-replica + interface abstractions for v2 |
| Media bloats git repo | High | U4 × T6 × O9 (TN4) | Out-of-git blob store; snapshot covers blob bytes |
| OIDC not ready at launch violates B1 | Medium | T9 × B1 (TN5) | Adapter interface complete with stub provider in CI |
| "Saved" indicator lies about durability | Medium | O8 × U3 (TN6) | Two-state UX: synced → saved |
| Reverse-proxy misconfig → /login DoS | Medium | S5 × B1 (TN7) | Always-on server-side auth rate-limit floor |
| Service account scope too coarse | Low | S1 × B2 (TN8) | Accepted v1 limitation; v2 may add path scoping |
| /readyz flaps on remote hiccup | Medium | O4 × T6 (TN9) | Remote health = metric + alert; not a readiness signal |
| SIGTERM drain exceeds k8s window | High | O5 × T10 (TN10) | Bounded 30s drain + persistent journal spill + startup replay |

## 10. Dependencies

| Dependency | Type | Owner | Status |
|---|---|---|---|
| CRDT engine (Yjs FFI, Automerge, or custom) | External library | TBD in Wave 2 | Pending selection |
| OIDC provider (stub for CI, real for launch) | External | Operator | Stub in-scope for Wave 3 |
| S3-compatible object store (optional snapshot) | External | Operator | Optional |
| Reverse proxy (nginx / Caddy) for production rate-limiting | Operator-supplied | Operator | Docs deliverable |

## 11. Timeline & Milestones

Ordered by solution option C (ToC-aligned, binding-first). Dates are illustrative; actual cadence depends on capacity.

| Wave | Milestone | Depends on | Target |
|---|---|---|---|
| 1 | RT-2 durability spike: two-state events + O8 flush + client indicator | — | **Current wave (generated here)** |
| 2 | RT-4 interfaces: session, CRDT broadcast, snapshot, audit, blob | Wave 1 | Post-spike review |
| 3 | RT-5 Principal · RT-6 media · RT-11 OIDC adapter (parallel-safe) | Wave 2 | +2 waves |
| 4 | RT-7 journal · RT-10 auth RL floor · RT-12 bounded drain | Wave 3 | +1 wave |
| 5 | RT-3 async audit drainer · RT-8 /readyz scope · RT-9 XSS+CSP · RT-1 degradation doc | Wave 4 | +1 wave |

## 12. Open Questions

| # | Question | Impact | Decision Needed By |
|---|---|---|---|
| 1 | CRDT engine selection (Yjs FFI vs Automerge vs minimal custom)? | RT-2 Wave-2 integration; payload wire-format | Start of Wave 2 |
| 2 | Stub OIDC provider: custom vs. a community fixture (e.g., dex)? | CI complexity | Start of Wave 3 |
| 3 | Blob store default: filesystem only, or include SQLite-backed option? | T8 deployability surface | Start of Wave 3 |
| 4 | Snapshot destination: should we ship a restic adapter or only S3-API? | O7 scope | Start of Wave 4 |

---

## Appendix A: Constraint Traceability Matrix

| PRD Section | Constraint IDs |
|---|---|
| 1. Problem Statement | outcome, B1, B3 |
| 2. Business Objectives | B1, B2, B3 |
| 3. Success Metrics | T2, T3, T4, T5, U5, O8, T10, S6, S2, O9 |
| 4. Target Users & Personas | U1, U2, U4, S1, S4 |
| 5. Assumptions & Constraints | B3, S5, T6, T8 |
| 6. Must Have | All invariant constraints (24) |
| 6. Should Have | All boundary constraints (8) |
| 6. Could Have | All goal constraints (5) |
| 6. Won't Have | T9 (SAML cut), TN3, U2 (no offline), U1 (no cursors), TN4 |
| 7. User Flows | T1, T2, T6, U1, U2, U3, S1, S2 |
| 9. Risks & Mitigations | TN1..TN10 |
| 10. Dependencies | T6, S4, O7, S5 |
| 11. Timeline | RT-1..RT-12 (Option C waves) |
| 12. Open Questions | Wave-2 CRDT selection, stub OIDC, blob store, snapshot target |

## Appendix B: Manifold Reference

- Source: `.manifold/collab-wiki.json` + `.manifold/collab-wiki.md`
- Schema version: 3
- Phase: GENERATED
- Constraint coverage: 37/37 constraints · 10/10 tensions resolved · 12/12 required truths derived
- Binding constraint: RT-2 (op_acked vs op_saved distinction + enforceable O8 flush)
- Selected solution option: C (ToC-aligned, binding-first, TWO_WAY)
