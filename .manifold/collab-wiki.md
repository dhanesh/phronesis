# collab-wiki

## Outcome

A document server serving Markdown as HTML with the following capabilities:

- Provides an HTTP API for editing documents, authenticated via `API_KEY` for programmatic/agent access.
- End-users can log in via password or SSO (human auth path, distinct from API-key path).
- Multi-user real-time collaboration on Markdown files using CRDTs for conflict-free concurrent editing.
- Git sync is used to synchronize the document server's content root with a configurable git remote (push-only: server is authoritative).
- High throughput for read and edit operations.
- Ease of use for both end-users (browser editing) and integrators (API consumers).

Source of truth remains Markdown files on disk, consistent with the existing `phronesis` architecture.

---

## Constraints

### Business

#### B1: All four primary capabilities are equal INVARIANTs

CRDT multi-user collaboration, API-key edit API, password+OIDC authentication, and git sync of the content root must each remain in working state for the system to be considered functional. Any one failing constitutes system failure.

> **Rationale:** User explicitly selected "all four are equal INVARIANTs" when asked which capability was non-negotiable. This is a high-bar commitment that drives the density of the overall constraint set and will force explicit tension resolution in m2.
> **Quality:** specificity 2 / measurability 2 / testability 2 — concrete "degradation" definitions live in DRT-1.

#### B2: Workspace-level RBAC with viewer / editor / admin

Authorization is governed by a fixed three-role model per workspace (viewer, editor, admin). No per-document ACLs or arbitrary groups in v1.

> **Rationale:** User explicitly scoped RBAC to workspace-level three-role. Fine-grained ACLs are intentionally out of scope; revisiting requires a new constraint iteration.
> **Quality:** 3 / 3 / 3.

#### B3: Single deployment, multiple workspaces, shared user pool

A single server instance hosts multiple logical workspaces (each with its own document root and git repo), backed by one shared user/auth pool. Not multi-tenant SaaS (no cross-tenant isolation guarantees).

> **Rationale:** User chose "single-tenant, multi-workspace" over single-tenant-only and multi-tenant-SaaS.
> **Quality:** 3 / 3 / 2 — "shared user pool" semantics need confirmation in m3 (all workspaces see all users? Or opt-in?).

---

### Technical

#### T1: Ephemeral CRDT, .md on disk is source of truth

CRDT state lives in memory; the `.md` file on disk is the single canonical representation. CRDT state flushes to `.md` on quiescence (see O8 for flush policy). No CRDT sidecar files, no external CRDT store.

> **Rationale:** User chose "Ephemeral CRDT, .md is truth" over persisted-sidecar and external-store options. Simplifies storage model and git sync, but constrains offline/long-lived sessions.
> **Quality:** 3 / 3 / 3.

#### T2: Collab latency — p95 keystroke-to-peer < 300ms

A keystroke by User A must appear on User B's screen within 300ms at p95, measured end-to-end across the same server instance.

> **Rationale:** User chose "< 300ms p95 (feels live)" over instant and near-live bands. Enables SSE or WebSocket transport; rejects polling-only designs.
> **Threshold:** statistical, p95 = 300ms.
> **Quality:** 3 / 3 / 3.

#### T3: Read throughput — p95 < 100ms at 500 concurrent editors, 1K req/s

At the stated scale target (500 concurrent editors, 1K req/s sustained read traffic), document read latency must hold at p95 < 100ms.

> **Rationale:** "Org-scale" throughput target (500 users, 10K docs) selected. Sets the bar for caching, indexing, and the audit hot-path (see S9).
> **Threshold:** statistical, p95 = 100ms; concurrency = 500 editors; throughput = 1000 req/s.
> **Quality:** 3 / 3 / 3.

#### T4: Supports 500 concurrent editors and 10K documents per workspace

The system must sustain the stated scale (500 concurrent editors, 10K documents per workspace) without regression on T2 or T3 metrics.

> **Rationale:** Scale floor chosen at org-scale target.
> **Quality:** 3 / 3 / 3.

#### T5: Per-document size cap = 2MB

Writes exceeding 2MB of markdown source are rejected with HTTP 413. Clients must surface this to users clearly.

> **Rationale:** "Medium (<2MB)" selected. Protects CRDT memory budget and input-parsing costs.
> **Threshold:** deterministic, max 2MB.
> **Quality:** 3 / 3 / 3.

#### T6: Git sync is PUSH-ONLY

The server is authoritative. Git is an outbound mirror/backup channel: periodic push of server state to the configured remote. No inbound git pulls are applied to the working tree at runtime. Operators who want to seed content from git do so offline, before start.

> **Rationale:** User selected "Push-only (server → remote)" over bidirectional options. Eliminates the CRDT-rebase-onto-remote-merge problem entirely. Note: this narrows the surface of "git sync" semantics from the original outcome language.
> **Quality:** 3 / 3 / 3.

#### T7: One workspace = one git repository

Each workspace maps 1:1 to a git repository with its own configurable remote URL and credentials. Workspaces do not share a repo.

> **Rationale:** User selected "One workspace = one repo" for isolation.
> **Quality:** 3 / 3 / 3.

#### T8: Deployable as single binary, Docker container, AND k8s

All three deployment postures must work: (a) a single Go binary + data directory, (b) a Docker container with a mounted volume, and (c) a k8s-friendly deployment that can use external stores. Defaults are in-process/embedded; external-store adapters are opt-in.

> **Rationale:** User selected "1, 2 and 3" (all three). Drives storage/session/pubsub abstractions (see DRT-5).
> **Quality:** 3 / 2 / 2 — "k8s-friendly" requires concrete readiness/replica guarantees spelled out in m3.

#### T9: Password auth is INVARIANT; OIDC is a same-release GOAL

Password authentication must ship and always work. OIDC is required in the same release plan (user selected both "OIDC" and "None — password only for now"), but is permitted to land after password auth in a phased rollout, provided the architecture supports it from the start. SAML is explicitly out of scope.

> **Rationale:** User multi-selected "OIDC / OAuth2" AND "None — password only for now", which we interpret as: password is the hard floor, OIDC is the explicit goal, no SAML.
> **Quality:** 2 / 3 / 2 — phasing semantics to be firmed up in m3 (DRT-9).

#### T10: Git push backlog is bounded

The outbound git push queue is capped (default: 1000 commits OR 100MB of diff bytes, whichever first). Alerts fire when backlog exceeds 100 queued commits. Queue saturation triggers backpressure, not silent loss.

> **Rationale:** Pre-mortem #1 (obvious failure): remote unreachable → unbounded queue → disk fills. Confirmed by user selection.
> **Threshold:** deterministic, max 1000 commits / 100MB, alert at 100.
> **Quality:** 3 / 3 / 3.

#### T11: Wiki-link rename is a single atomic CRDT operation

Renaming a wiki-linked page produces ONE CRDT op that updates all backlinks, not N individual edit ops. Partial application must not be possible.

> **Rationale:** Pre-mortem #2 (surprise): 500+ backlink rewrites as individual CRDT ops would create lag spikes and partial-rename inconsistency. Confirmed by user.
> **Quality:** 3 / 3 / 3.

---

### User Experience

#### U1: Presence list visible on every open document

A compact list of users currently viewing/editing the document is visible to all editors. Remote cursors/selections and per-user authorship highlights are explicitly NOT in scope for v1.

> **Rationale:** User selected "Presence list per doc" only. Minimal UX surface; keeps client bundle small (see U5).
> **Quality:** 3 / 3 / 3.

#### U2: Editor is read-only while disconnected

When the client loses its live connection to the server, the editor transitions to read-only and displays a "reconnecting" state. Edits are NOT buffered client-side. On reconnection, the editor becomes editable again with the latest remote state.

> **Rationale:** User selected "Editor becomes read-only" (simplest) over buffer-and-replay options. Avoids client-side CRDT library + offline-merge complexity entirely.
> **Quality:** 3 / 3 / 3.

#### U3: Autosave indicator reflects durability within 300ms

The UI shows "saved" within 300ms p95 of the user stopping typing. "Saved" means disk-durable (O6), not just broadcast-acked.

> **Rationale:** Preserves the current phronesis autosave UX contract. Combined with T2, drives the broadcast-vs-durability distinction captured in DRT-2.
> **Threshold:** statistical, p95 = 300ms.
> **Quality:** 3 / 3 / 3.

#### U4: Wiki-links, tags, task-lists, and embedded media render live

`[[Wiki-links]]`, `#tags`, GitHub-style task list checkboxes, and embedded media (images / attachments) render in-place inside the editor during editing sessions, not only in a separate preview.

> **Rationale:** User multi-selected all four. Embedded media is a scope delta from current phronesis — drives S6 (quota + content-type) and DRT-10 (git-LFS vs external media store).
> **Quality:** 2 / 2 / 2 — "renders live" is measurable; "in-place" is partially subjective until m4 defines CodeMirror widget strategy.

#### U5: Client bundle ≤ 500KB gzipped on first load

The initial JS/CSS bundle served to an unauthenticated visitor (or first-load authenticated user) must be ≤ 500KB gzipped, to preserve mobile / low-bandwidth usability.

> **Rationale:** Pre-mortem #2 (surprise): CodeMirror + CRDT client + presence could bloat past 2MB gzipped. User confirmed this failure mode.
> **Threshold:** deterministic, 500KB gzipped.
> **Quality:** 3 / 3 / 3.

---

### Security

#### S1: Dual principal model — user PATs and workspace service accounts

The system supports two distinct principal classes:
- **User-owned PATs**: personal access tokens tied to a user; API calls act as that user.
- **Workspace service accounts**: separate principals attached to a workspace, with explicit scopes and their own audit trail, distinct from any user.

Both must be provisionable, revocable, auditable, and rate-limitable independently.

> **Rationale:** User selected "Both" for API-key model. Agent traffic gets proper non-human identity without coupling to a user account.
> **Quality:** 3 / 3 / 3.

#### S2: Comprehensive audit log

A durable, append-only audit log records:
- Auth events: login, logout, failures, SSO callbacks, API-key use
- Write events: document create, edit (CRDT op batches), delete, media upload
- Admin actions: workspace creation, user/key provisioning, role assignment, permission changes
- Read events: document view / fetch (see S9 for hot-path constraint)

Every entry carries principal (with principal-class distinction), timestamp, workspace, and document/resource. Default retention: 180 days, operator-configurable.

> **Rationale:** User selected all four audit categories. Read-event audit is the heaviest of these and creates the T3↔S2 tension resolved by S9.
> **Threshold:** deterministic, retention default 180 days.
> **Quality:** 3 / 3 / 3.

#### S3: Password auth hardened

Password authentication uses argon2id for hashing. Sessions are cookie-based with Secure + HttpOnly + SameSite=Lax. CSRF tokens are required for state-changing requests. Password policy (minimum length, complexity) is enforced server-side.

> **Rationale:** Reflects current web auth best practices for 2026. argon2id chosen over bcrypt for resistance to GPU attacks.
> **Quality:** 3 / 2 / 3 — password-policy specifics deferred to m3 (can be a simple length floor).

#### S4: OIDC integration validates signature, issuer, audience, and honors rotation

For OIDC SSO: id_token signatures are validated against JWKS (cached with TTL, refreshed on unknown kid); issuer (iss) and audience (aud) claims are verified; expired/not-yet-valid tokens rejected. Key rotation by the IdP must not require a server restart.

> **Rationale:** OIDC path is T9-required. Key rotation specifically called out because pre-mortem #2 surfaced it.
> **Quality:** 3 / 3 / 3.

#### S5: Input-size bounds enforced server-side; rate-limiting delegated to reverse proxy

The server enforces a per-request body cap of 2MB (matches T5). Rate-limiting of auth endpoints and abuse-prone surfaces is the reverse-proxy's responsibility; the server ships a documented reference reverse-proxy config (nginx + Caddy) that covers the minimum rate-limit requirements. Pre-mortem #3 (external) flagged the "operator forgot to configure it" failure mode — see open question below.

> **Rationale:** User chose "Reverse-proxy responsibility (docs only)." Open m2 question: does this need a server-side fallback floor to prevent the "unconfigured reverse-proxy" disaster?
> **Quality:** 3 / 2 / 3.

#### S6: Media uploads — content-type allow-list + per-workspace quota

Uploaded media (backing U4) is restricted to a content-type allow-list (default: `image/png`, `image/jpeg`, `image/gif`, `image/webp`, `video/mp4`, `application/pdf`; operator-configurable). A per-workspace storage quota is enforced (default 5GB, configurable). Over-quota uploads are rejected with HTTP 413.

> **Rationale:** Pre-mortem #1 (obvious): unbounded media = disk exhaustion. Confirmed.
> **Threshold:** deterministic, default quota 5GB.
> **Quality:** 3 / 3 / 3.

#### S7: Dual-layer XSS defense — sanitize at store AND at render

User-supplied markdown is sanitized at BOTH the store path (reject or strip dangerous HTML attributes / raw `<script>`, etc. before persistence) AND at the render path (rendered HTML passes through a strict allow-list sanitizer; responses carry a server-enforced Content-Security-Policy).

> **Rationale:** Pre-mortem #2 (surprise): paste-from-Word XSS via markdown. Single-layer defense is insufficient; defense-in-depth required.
> **Quality:** 3 / 3 / 3.

#### S8: OIDC claim mapping is configurable per provider, with schema versioning

The server does not hardcode claim names. Each OIDC provider configuration specifies which claim is the user identifier (e.g., `email` vs `preferred_username` vs `upn`), which is the display name, and which carries group membership. A schema version is pinned per provider so breaking claim-schema changes surface as config errors, not silent misattribution.

> **Rationale:** Pre-mortem #3 (external): IdP changed claim schema; users locked out.
> **Quality:** 3 / 2 / 2 — "schema version" semantics need firming in m3.

#### S9: Read-event audit is strictly async

The read-event audit entries produced for S2 must not block the read hot path. Reads return from an in-memory / cached representation and queue an audit event that is flushed asynchronously (with batching and sampling permitted). Read latency budget (T3: p95 < 100ms) is measured without audit-flush cost.

> **Rationale:** Pre-mortem #1 (obvious): synchronous read-audit = throughput collapse. Resolves the direct tension between S2 and T3.
> **Quality:** 3 / 3 / 3.

---

### Operational

#### O1: Prometheus metrics at /metrics

Metrics exposed in Prometheus exposition format: request counts by endpoint+method+status, latency histograms (matching T2, T3, U3 SLOs), CRDT room count and per-room member count, CRDT op rate, git-push queue depth, media storage bytes per workspace, audit buffer depth.

> **Rationale:** User selected all four observability signals; `/metrics` is the de facto scrape endpoint.
> **Quality:** 3 / 3 / 3.

#### O2: OpenTelemetry tracing end-to-end

Tracing spans cover: HTTP handler → auth → RBAC check → CRDT op or document read → disk flush → (async) git-push or snapshot. Trace context propagates across goroutines used for async work (flush workers, push workers, audit flusher).

> **Rationale:** User selected OpenTelemetry. Goroutine propagation called out because Go's default `context` doesn't propagate OTel across goroutines without explicit handoff.
> **Quality:** 3 / 3 / 3.

#### O3: Structured JSON logging (slog)

All server logging uses `log/slog` with a JSON handler. Every request-scoped log line carries `request_id`, `principal_id` (with principal class), `workspace_id`, and where relevant `document_id`.

> **Rationale:** Standard Go ecosystem; already present in the project's implicit conventions.
> **Quality:** 3 / 3 / 3.

#### O4: /healthz (liveness) and /readyz (readiness)

`/healthz` returns 200 when the process is alive and responding (trivial check). `/readyz` returns 200 only when: the data directory is writable, the CRDT manager goroutine is alive, the git-push worker is alive (or recently alive), and the configured git remote is reachable within a bounded probe timeout.

> **Rationale:** User selected health + readiness endpoints. Distinguishing liveness from readiness is essential for k8s (T8 option c).
> **Quality:** 3 / 3 / 3.

#### O5: Graceful shutdown drains state before exit

On SIGTERM: (1) stop accepting new HTTP connections, (2) broadcast a "shutting down" notice to connected editors (per U2, their editors go read-only), (3) flush every active CRDT room to disk, (4) drain the git-push queue with a bounded timeout (default 30s), (5) exit cleanly. If the drain timeout expires, the server exits with a non-zero status code and logs the remaining queue for operator attention.

> **Rationale:** User selected "graceful drain." Non-zero exit on incomplete drain is important for k8s-style restart semantics.
> **Quality:** 3 / 3 / 3.

#### O6: Atomic disk writes before ACK

Persistence to a `.md` file uses: write-to-tempfile-in-same-dir → fsync(tempfile) → rename(tempfile, final) → fsync(dir). The server ACKs a write (and emits the U3 "saved" signal) only after the rename + dir fsync have returned.

> **Rationale:** User selected "Atomic write + snapshot store". Matches POSIX atomicity guarantees needed to avoid half-written `.md` files on crash.
> **Quality:** 3 / 3 / 3.

#### O7: Periodic full snapshot to S3-compatible or restic destination

In addition to git push (T6), the content root is periodically snapshot to a configured snapshot destination (S3-compatible object store or restic repository). Default interval: hourly, configurable.

> **Rationale:** User selected "Atomic write + snapshot store" — this is the snapshot half. Provides a recovery path independent of git (answers pre-mortem #3 "S3 bucket deleted" by still having git; answers "git repo corrupted" by still having snapshots).
> **Threshold:** deterministic, default snapshot interval = 1h.
> **Quality:** 3 / 3 / 3.

#### O8: CRDT quiescence flush — at most every 3 seconds OR every 100 ops

Active CRDT rooms flush to disk on whichever fires first: (a) 3 seconds of quiescence (no new ops) OR (b) 100 accumulated ops since last flush. This bounds the edit-loss window on crash to at most ~3 seconds of active typing per room.

> **Rationale:** Pre-mortem #1 (obvious): crash = lost edits. Ephemeral-CRDT (T1) design demands a bounded-loss flush policy.
> **Threshold:** deterministic, max 3s / 100 ops.
> **Quality:** 3 / 3 / 3.

#### O9: Repo-size monitoring + git-LFS guidance for media

Per-workspace repository size is monitored. Warning at 1GB, alert at 5GB. Documentation (and, in m4, installer tooling) recommends git-LFS — or external media storage — for any workspace expected to hold media, because plain git + large binaries + push-only → repo bloat → push timeout → silent sync failure.

> **Rationale:** Pre-mortem #3 (external): no git gc / no LFS → 50GB repo. Also ties to DRT-10.
> **Threshold:** deterministic, warn 1GB / alert 5GB.
> **Quality:** 3 / 3 / 2 — "guidance" is a doc deliverable; actual enforcement is in m4.

---

## Tensions

All 10 tensions resolved in iteration 2 with the cleanest architectural interpretation that preserves the user's "all four INVARIANTs" scope.

### TN1: Ephemeral CRDT vs. collab-as-INVARIANT (T1 × B1)

**Type:** trade_off · **Status:** resolved · **Decision:** A

T1 states CRDT is ephemeral in-memory; B1 says collab is INVARIANT. A server crash could lose recent edits, which on a literal reading would be a B1 violation.

**TRIZ:** Speed vs Correctness / Performance vs Reliability — principles P11 (beforehand cushioning), P10 (prior action).

> **Resolution:** (Option A) Redefine B1's "working state" to mean "edits acknowledged as durable within the O8 window". Crash-loss up to O8's bound (≤3s of idle time OR ≤100 ops per room, whichever first) is contract-compliant. This promotes O8 from a "good idea" to a load-bearing INVARIANT: relaxing O8 = weakening B1.
>
> **Propagation:** O8 TIGHTENED — O8 now carries the bounded-loss INVARIANT weight for B1.
>
> **Failure cascade:** If fsync fails on flush, O5 graceful-drain refuses to exit without success; repeated failure surfaces as /readyz non-ready + alert. Simultaneous in-memory loss + disk-physical failure is an explicit accept-loss edge case.

### TN2: Comprehensive audit vs. read latency (S2 × T3)

**Type:** trade_off · **Status:** resolved · **Decision:** A

S2 mandates audit of read events; T3 requires p95 read < 100ms at 1K req/s. Synchronous read-audit writes on the hot path would saturate the audit store and blow through T3.

**TRIZ:** Speed vs Correctness — principles P10 (prior action), P17 (another dimension).

> **Resolution:** (Option A) S9 (async buffered read-audit) is sufficient. Reads return from in-memory/cached state; audit events enqueue to an async buffer drained by a dedicated writer. T3 is measured on the read response path, excluding audit enqueue cost. Bounded audit loss on crash is accepted.
>
> **Propagation:** O8 TIGHTENED — the audit buffer is now another "thing to drain" at SIGTERM; TN10's journal-spill pattern applies if drain times out with audit backlog.
>
> **Failure cascade:** If buffer saturates, drop-oldest policy with an `audit_events_dropped_total` metric + alert. Documented as an accepted loss mode for the bounded read-audit trail.

### TN3: Tri-deployment vs. in-process CRDT (T8 × T1 × T2)

**Type:** hidden_dependency · **Status:** resolved · **Decision:** A

T8 requires k8s-friendly deployment (which typically implies multi-replica for HA). T1 keeps CRDT state in-process, and T2 demands sub-300ms peer fan-out. Multi-replica with in-process CRDT requires either shared pubsub or sticky per-document routing.

**TRIZ:** Simplicity vs Capability / Standardisation vs Flexibility — principles P1 (segmentation), P15 (dynamization), P24 (intermediary).

> **Resolution:** (Option A) v1 ships k8s as SINGLE-REPLICA (Deployment `replicas=1` + PVC + graceful-termination ≥30s). All stateful subsystems (session store, CRDT broadcast, snapshot target, audit sink) are abstracted behind interfaces with in-process defaults (DRT-5), so a future v2 can drop in Redis pubsub or sticky routing without code rewrites. This honors T8 literally (it runs on k8s) while preserving T1+T2 without premature infrastructure.
>
> **Propagation:** T8 TIGHTENED — documentation must explicitly state "k8s single-replica for v1" as the supported posture.
>
> **Failure cascade:** If the replica dies, k8s restarts it; active rooms reload from last flush (O8 bounded loss window applies); clients reconnect per U2.

### TN4: Embedded media × push-only git × bounded repo size (U4 × T6 × O9)

**Type:** resource_tension · **Status:** resolved · **Decision:** A

U4 puts embedded media in markdown; T6 means git is push-only; O9 caps repo size. Naive design (commit media into git, push) blows O9 quickly for any real media usage.

**TRIZ:** Cost vs Quality / Simplicity vs Capability — principles P1 (segmentation), P2 (extraction).

> **Resolution:** (Option A) Media lives OUTSIDE git in a pluggable blob store. Default implementation: local filesystem under the data dir (keeps single-binary operable). Pluggable: S3-compatible. Markdown references media by content-addressed URL (`/media/<sha>`), not by embedded blob. Git repo holds only markdown.
>
> **Propagation:** O7 TIGHTENED (snapshots must include blob-store bytes); S6 TIGHTENED (per-workspace quota applies to blob-store bytes, not git bytes); O9 LOOSENED (repo-size alerts no longer need to police media).
>
> **Failure cascade:** If blob-store unreachable, media URL returns 404; markdown renders with a visible placeholder; text editing continues. If local filesystem corrupts, restore from O7 snapshot (bounded loss = snapshot interval, default 1h).

### TN5: OIDC phased vs. auth-INVARIANT-at-launch (T9 × B1)

**Type:** trade_off · **Status:** resolved · **Decision:** A

B1 says password+OIDC auth is INVARIANT; T9 allows OIDC in a phased rollout. Without explicit reconciliation, "launch" is ambiguous.

**TRIZ:** Simplicity vs Capability — principles P1 (segmentation), P15 (dynamization).

> **Resolution:** (Option A) At launch, password auth is fully operational AND the OIDC adapter interface is complete with a working stub provider wired in CI. Enabling a real IdP is a config-only change (no rebuild). B1 is satisfied because the auth plane works end-to-end for both mechanisms in principle; activating OIDC for a specific IdP is operational, not release-blocking.
>
> **Propagation:** S4 TIGHTENED — S4's OIDC contract (JWKS handling, S8 claim mapping) must be fully implemented at launch, not deferred.
>
> **Failure cascade:** If the OIDC adapter has post-launch bugs, users fall back to password auth (which is INVARIANT per B1+S3). No user lockout.

### TN6: 3s flush vs. 300ms saved-indicator (O8 × U3)

**Type:** hidden_dependency · **Status:** resolved · **Decision:** A

O8 flushes to disk every ≤3s of idle OR ≤100 ops. U3 requires the "saved" indicator within 300ms. The literal reading (saved = on-disk) is infeasible without sync-on-every-op.

**TRIZ:** Speed vs Correctness — principles P24 (intermediary), P17 (another dimension).

> **Resolution:** (Option A) Two-state indicator. The UI shows **"synced"** when the server has acknowledged the op and broadcast it to peers (≤300ms p95 per T2) and upgrades to **"saved"** when disk flush has committed (≤3s per O8). This matches the Google Docs / Notion mental model, where "all changes saved" is a two-phase affordance in practice.
>
> **Propagation:** U3 TIGHTENED (semantic change) — U3's 300ms target now governs the "synced" state. A separate durability marker tracks the disk commit.
>
> **Failure cascade:** If the synced ack never arrives within a client-side timeout (default 2s), the UI shows "reconnecting", and the editor transitions to read-only per U2.

### TN7: Reverse-proxy rate-limit delegation vs. auth-as-INVARIANT (S5 × B1)

**Type:** hidden_dependency · **Status:** resolved · **Decision:** A

S5 delegates rate-limiting entirely to the reverse proxy. Pre-mortem #3 flagged "operator forgets to configure it → /login scraped → account lockout spiral", which would render the password/OIDC auth plane unusable and silently violate B1 (auth is INVARIANT).

**TRIZ:** Global vs Local optimum — principles P11 (beforehand cushioning), P22 (blessing in disguise).

> **Resolution:** (Option A) Add a server-side per-IP sliding-window rate limiter on `/login`, `/auth/callback`, and `/auth/*`. It is always active, cannot be disabled, and respects a configurable trusted-proxy list for X-Forwarded-For extraction. The reverse proxy remains the primary defense (as originally intended by S5); the server-side floor is a backstop specifically for credential-stuffing vectors.
>
> **Propagation:** S5 TIGHTENED — S5's wording ("only input-size bounds") narrows and must be refined in m3 to read approximately: "server enforces input-size bounds AND a per-IP auth-endpoint rate-limit floor; finer limits delegated to reverse proxy".
>
> **Failure cascade:** If an attacker uses many distinct IPs, the floor is weak; documentation makes reverse-proxy WAF/IP-reputation the primary production defense.

### TN8: Dual principal × simple RBAC (S1 × B2)

**Type:** hidden_dependency · **Status:** resolved · **Decision:** A

S1 introduces two principal classes (users + workspace service accounts). B2 defines a 3-role RBAC (viewer/editor/admin) originally intended for users. Without a decision, the service-account scope model is underspecified.

**TRIZ:** Standardisation vs Flexibility — principle P1 (segmentation).

> **Resolution:** (Option A) Workspace service accounts take exactly the same three roles as users. One authorization function covers both principal classes using the same role enum. This maximally preserves B2's "simple" promise, at the cost of coarser service-account scoping (e.g., "editor-but-only-on-docs/api/**" is not expressible in v1). Finer scoping is explicitly deferred to v2.
>
> **Propagation:** S2 TIGHTENED — audit log schema gains a `principal_type` field (user vs service-account) alongside `principal_id`, so auditors can filter by identity class.
>
> **Failure cascade:** N/A (scope limitation is an accepted v1 constraint, not a failure mode).

### TN9: /readyz scope vs. push-only git remote health (O4 × T6)

**Type:** hidden_dependency · **Status:** resolved · **Decision:** A

Originally O4 listed "git remote reachable" as a readiness condition. T6 makes the remote a durability-behind-the-scenes dependency, not a traffic-serving dependency. Transient remote flap would cause /readyz flapping, triggering k8s to re-route traffic and disrupting live collab sessions.

**TRIZ:** Availability vs Consistency — principle P11 (beforehand cushioning).

> **Resolution:** (Option A) /readyz reflects only "can I serve traffic right now": data dir writable, CRDT manager goroutine alive, audit buffer not saturated, journal replay complete (see TN10). Git remote health is surfaced via a dedicated `/metrics` gauge and fires alerts on sustained failure, but is NOT a /readyz condition.
>
> **Propagation:** O4 LOOSENED (fewer conditions can fail /readyz). Offset by TN10's tightening (journal-replay must complete before ready).
>
> **Failure cascade:** If the local filesystem becomes unwritable, /readyz fails (service truly cannot serve); k8s re-routes; local alerts fire.

### TN10: Graceful drain bound vs. push backlog (O5 × T10)

**Type:** hidden_dependency · **Status:** resolved · **Decision:** A

O5 requires graceful drain on SIGTERM (flush CRDT + drain git push queue). T10 bounds push queue at 1000 commits or 100MB. Under a slow remote, drain could take minutes — incompatible with k8s graceful-termination windows (typically 30s).

**TRIZ:** Cost vs Quality — principles P11 (beforehand cushioning), P17 (another dimension).

> **Resolution:** (Option A) On SIGTERM: (1) always flush active CRDT rooms (in-memory loss otherwise), (2) attempt to drain the git push queue for a bounded window (default 30s, configurable), (3) spill any remaining queue to a persistent journal file under the data dir, (4) exit cleanly. On the next startup, the journal is replayed BEFORE /readyz returns 200. If replay fails, startup errors with a clear message and /readyz stays non-ready.
>
> **Propagation:** O4 TIGHTENED (readiness now requires "journal replay complete"); O5 TIGHTENED (drain window is bounded; journal-spill is a required code path).
>
> **Failure cascade:** If the journal WRITE fails (e.g., disk full during spill), BLOCK shutdown, emit a loud alert, escalate to human operator — explicit human-intervention path. If the journal REPLAY fails at startup, startup errors out; operator must intervene (runbook entry required).

---

---

## Required Truths

Twelve required truths derived by backward reasoning from the outcome. Seeded from the 12 DRTs and 7 blocking-dependencies extracted in m2. Binding constraint: **RT-2** (bolded below).

### RT-1: Degradation is concretely defined per capability

B1 claims "all four capabilities are equal INVARIANTs". For this to be testable, each capability (CRDT collab, API edit, password+OIDC auth, git sync) must have: a concrete definition of "degraded", a detection signal (metric or probe), and an alert threshold.

**Maps to:** B1 · **Status:** NOT_SATISFIED
**Gap:** No per-capability degradation taxonomy exists. Must produce: a one-page "capability health contract" with thresholds and alert bindings before launch acceptance.

### RT-2: ⭐ BINDING — op_acked vs op_saved distinction with enforceable O8 flush

The server emits two distinct per-op events: `op_acked` (broadcast to peers complete) and `op_saved` (disk flush per O8 complete). The client's state machine models both states and renders a two-state indicator. O8's flush policy (≤3s idle OR ≤100 ops per room) is enforced at the room level with a timer + counter primitive and is instrumented via metrics.

**Maps to:** T1, O8, U3, B1 · **Status:** NOT_SATISFIED · **Depth-2 tree:**
- **RT-2.1** Server emits `op_acked` and `op_saved` as separate events per op — NOT_SATISFIED
    - RT-2.1.1 CRDT engine exposes post-broadcast + post-flush hooks — PRIMITIVE (verify once CRDT lib chosen)
- **RT-2.2** Client state machine models both states; CodeMirror surfaces a two-state indicator — NOT_SATISFIED
    - RT-2.2.1 CodeMirror widget can host a state-indicator component — PRIMITIVE (verify in POC)
- **RT-2.3** O8 flush policy enforced at room level (3s idle OR 100 ops) — NOT_SATISFIED
    - RT-2.3.1 Go std lib provides timers + atomic counters — SATISFIED
- **RT-2.4** Flush failure is observable (fsync errors → alert + /readyz fails per TN10) — PARTIAL
    - RT-2.4.1 `log/slog` + Prometheus counter infra — SATISFIED

**Gap:** Entirely greenfield. Phronesis today has a single-user autosave model with no CRDT, no broadcast layer, no flush policy. This is the highest-complexity single build target in the plan.

> **Why binding:** Touches 4 layers (CRDT engine, server event emission, client state machine, UX). Its absence silently invalidates B1's INVARIANT contract and makes U3 either wrong or infeasible. Closing it unblocks RT-1 (observability), RT-7 (journal semantics), RT-12 (drain contract).

### RT-3: Read-audit is strictly async, off the hot read path

Read-event audit (S2) is a strict off-path concern: the read response returns from in-memory / cached state before any audit enqueue cost is accumulated. The audit buffer has bounded capacity with a drop-oldest policy; drops emit a Prometheus counter.

**Maps to:** S2, S9, T3 · **Status:** SPECIFICATION_READY
- RT-3.1 Audit buffer has bounded capacity + drop-oldest — NOT_SATISFIED (impl)
- RT-3.2 Audit enqueue is O(1), no mutex contention on read path — NOT_SATISFIED (impl)

**Gap:** Design decided (TN2); implementation pending.

### RT-4: Stateful subsystems behind interfaces with in-process defaults

Five subsystems must be abstracted: session store, CRDT broadcast, snapshot target, audit sink, blob store. Each has an in-process default (filesystem / bbolt / goroutine-local) so the single-binary deployment works out of the box. External-store adapters (Redis, S3, Postgres) are optional.

**Maps to:** T1, T8 · **Status:** NOT_SATISFIED
- RT-4.1 Session store interface + filesystem/bbolt default — NOT_SATISFIED
- RT-4.2 CRDT broadcast interface + in-process goroutine-fanout default — NOT_SATISFIED
- RT-4.3 Snapshot target interface + local filesystem default — NOT_SATISFIED
- RT-4.4 Audit sink interface + local file default — NOT_SATISFIED

**Gap:** Current phronesis has direct filesystem calls and in-process wiring. Needs one refactor pass to introduce interfaces without changing behavior, followed by implementations per sub-truth.

### RT-5: Canonical Principal abstraction

A single `Principal` type — `(principal_type, principal_id, workspace_role)` — is produced by every authentication path (password sessions, PATs, OIDC sessions, workspace service accounts). A single authorization function consumes it. Audit log records `principal_type`.

**Maps to:** S1, S4, T9, B2 · **Status:** NOT_SATISFIED
- RT-5.1 Principal carries (principal_type, principal_id, workspace_role) — NOT_SATISFIED
- RT-5.2 Password, PAT, OIDC, SA auth all produce Principal — NOT_SATISFIED
- RT-5.3 Audit log records principal_type — NOT_SATISFIED

**Gap:** Phronesis today only has password cookie sessions. PAT, OIDC, and service-account paths are greenfield. Shape of Principal must be fixed before those paths are built, to avoid divergent authz code.

### RT-6: Media lives outside git in a content-addressed blob store

Markdown references media via `/media/<sha>` URLs served by a pluggable blob-store adapter. Default: local filesystem blob store under data dir. Optional: S3-compatible. Git sees only markdown. Snapshot (O7) covers both markdown and blob-store bytes.

**Maps to:** U4, T6, O7, O9, S6 · **Status:** NOT_SATISFIED
- RT-6.1 Blob-store interface + local FS default — NOT_SATISFIED
- RT-6.2 Markdown references media by content-addressed hash URL — NOT_SATISFIED
- RT-6.3 O7 snapshot includes blob-store bytes — NOT_SATISFIED

**Gap:** Phronesis currently has no media surface at all. Entirely greenfield.

### RT-7: Persistent push-journal with startup replay

The git-push queue spills to a durable journal file under data dir when SIGTERM drain exceeds its bounded window. On startup, journal replay completes before `/readyz` returns 200. Journal write failure escalates to a block-shutdown + alert path.

**Maps to:** O5, T10, O4 · **Status:** NOT_SATISFIED
- RT-7.1 Journal append is durable before SIGTERM exit — NOT_SATISFIED
- RT-7.2 Startup replay completes before /readyz returns 200 — NOT_SATISFIED

**Gap:** New subsystem. Must be atomic + fsync-safe.

### RT-8: Readiness ≠ durability health

`/readyz` checks only: data dir writable, CRDT manager goroutine alive, audit buffer not saturated, journal replay complete. Git remote health is a separate Prometheus gauge + alert; it does NOT affect `/readyz`.

**Maps to:** O4, T6 · **Status:** SPECIFICATION_READY
- RT-8.1 /readyz conditions strictly scoped — NOT_SATISFIED (impl)
- RT-8.2 Git remote health surfaces via separate metric + alert — NOT_SATISFIED (impl)

**Gap:** Design decided (TN9); implementation pending.

### RT-9: XSS defense at store AND render, plus CSP

The markdown parser does NOT emit raw HTML attributes by default (store-time sanitization). The rendered HTML passes an allow-list sanitizer (render-time). Server responses carry a strict `Content-Security-Policy` header.

**Maps to:** S7 · **Status:** NOT_SATISFIED
- RT-9.1 Markdown parser rejects/strips raw HTML by default — NOT_SATISFIED
- RT-9.2 Rendered HTML passes allow-list sanitizer — NOT_SATISFIED
- RT-9.3 Server emits strict CSP — NOT_SATISFIED

**Gap:** Phronesis has a custom renderer; needs explicit sanitization layer. CSP header configuration is new.

### RT-10: Always-on auth-endpoint rate-limit floor

A per-IP sliding-window limiter is always active on `/login`, `/auth/callback`, and `/auth/*`. Operator cannot disable it. X-Forwarded-For extraction honors a configurable trusted-proxy allow-list.

**Maps to:** S5, B1 · **Status:** NOT_SATISFIED
- RT-10.1 Per-IP sliding-window limiter on auth endpoints — NOT_SATISFIED
- RT-10.2 X-Forwarded-For trusted-proxy allow-list — NOT_SATISFIED

**Gap:** New subsystem. Needs care around IPv6 and shared-IP scenarios.

### RT-11: OIDC adapter contract complete at launch with stub provider tests

The OIDC auth adapter implements JWKS fetch + TTL cache + rotation handling, issuer and audience validation, and claim mapping per provider (schema-versioned per S8). A stub OIDC provider in CI exercises the full flow.

**Maps to:** S4, S8, T9 · **Status:** NOT_SATISFIED
- RT-11.1 JWKS fetch + TTL cache + rotation handler — NOT_SATISFIED
- RT-11.2 Claim mapping configurable per provider, schema-versioned — NOT_SATISFIED
- RT-11.3 Stub OIDC provider in CI exercises full flow — NOT_SATISFIED

**Gap:** Entirely greenfield. Stub-provider test harness is a one-time build.

### RT-12: Bounded SIGTERM drain with bounded-loss guarantee

Drain has a configurable timeout (default 30s). CRDT flush ALWAYS completes (no skip). Push spillover writes journal atomically. Exit is clean in all cases.

**Maps to:** O5, T10 · **Status:** NOT_SATISFIED
- RT-12.1 Configurable drain timeout (default 30s) — NOT_SATISFIED
- RT-12.2 CRDT flush always completes — NOT_SATISFIED
- RT-12.3 Push spillover writes journal atomically — NOT_SATISFIED

**Gap:** Tight coupling with RT-2 (flush correctness) and RT-7 (journal).

---

## Solution Space

Four options evaluated. All 10 m2 tensions validated against each; only Option C confirms all without reopening.

### Option A: Interface-first

Build all RT-4 interfaces (5 subsystems) in an upfront plumbing pass. Only then begin feature work on RT-2 / RT-5 / etc.

- **Satisfies first:** RT-4
- **Reversibility:** TWO_WAY
- **Trade-off:** Defers correctness signal on the binding constraint (RT-2) by weeks. Interfaces without consumers invite over-design.

### Option B: Thin vertical slice

Implement end-to-end one-doc / one-user / text-only / no-SSO / no-media collab. Then widen to presence, media, SSO, service accounts.

- **Satisfies first:** RT-2 (implicitly, via the thin slice)
- **Reversibility:** REVERSIBLE_WITH_COST
- **Trade-off:** Early API shapes (auth, handler signatures) get locked in under time pressure; RT-5 (Principal) retrofit is painful if deferred.

### Option C: ToC-aligned (binding-first) — ⭐ RECOMMENDED & SELECTED

Ordered waves:

1. **Wave 1 — Binding proof:** RT-2 (op_acked vs op_saved + O8 flush + two-state UX). Built as a standalone spike first; proves the durability model end-to-end with minimal scope (no auth, no media, no workspaces yet).
2. **Wave 2 — Subsystem abstractions:** RT-4 (session, CRDT broadcast, snapshot, audit, blob). Interfaces introduced around the working Wave-1 code, not ahead of it.
3. **Wave 3 — Identity + storage + media (parallel-safe):** RT-5 (Principal + authz) · RT-6 (blob store media) · RT-11 (OIDC adapter + stub).
4. **Wave 4 — Durability hardening:** RT-7 (journal) · RT-10 (auth rate-limit floor) · RT-12 (bounded drain).
5. **Wave 5 — Polish + observability:** RT-3 (async audit drainer) · RT-8 (readyz scope) · RT-9 (XSS + CSP) · RT-1 (degradation contract doc).

- **Satisfies:** All 12 RTs (in ordered waves)
- **Reversibility:** TWO_WAY (each wave is a standalone milestone; course-correctable)
- **Trade-off:** None relative to other options for this scope. Matches the consistent "A = cleanest architectural choice" pattern established in m1+m2.

### Option D: Parallel tracks

Three concurrent workstreams: identity (RT-5, RT-11, RT-10), storage (RT-4, RT-6, RT-7), CRDT (RT-2, RT-3).

- **Satisfies first:** RT-2, RT-4, RT-5, RT-6, RT-11 (concurrent)
- **Reversibility:** ONE_WAY ⚠️ — early cross-track API contracts become hard to undo.
- **Trade-off:** Requires ≥3-person team with strong integration discipline. Integration risk surfaces late. Not aligned with a small-team greenfield context.

### Tension Validation (selected: Option C)

All 10 m2 tensions CONFIRMED by Option C:

| Tension | Resolved via RT | Status under Option C |
|---|---|---|
| TN1 CRDT loss × B1 | RT-2 (O8 flush + two-state) | CONFIRMED |
| TN2 audit × T3 | RT-3 (async audit) | CONFIRMED |
| TN3 k8s × in-proc CRDT | RT-4 (abstractions) | CONFIRMED |
| TN4 media × git × repo | RT-6 (out-of-git blob) | CONFIRMED |
| TN5 OIDC phasing × B1 | RT-11 (adapter at launch) | CONFIRMED |
| TN6 flush × saved-indicator | RT-2 (two-state) | CONFIRMED |
| TN7 proxy RL × B1 | RT-10 (auth floor) | CONFIRMED |
| TN8 dual principal × RBAC | RT-5 (canonical Principal) | CONFIRMED |
| TN9 readyz × remote | RT-8 (scoped readiness) | CONFIRMED |
| TN10 drain × backlog | RT-7 + RT-12 (journal) | CONFIRMED |

---

