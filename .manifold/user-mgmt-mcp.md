# user-mgmt-mcp

## Outcome

Phronesis becomes a multi-user, multi-workspace knowledge base where humans and AI agents both operate as first-class principals. Three threads are bundled because they share the same authorization substrate (workspace-scoped principals + capability tokens) and a single user story would split awkwardly otherwise:

1. **User Management** — admins can create, invite, suspend, and remove human users; users can manage their own profile (display name, password / OIDC binding, sessions). Roles already exist (`admin` vs regular user via `internal/principal`) but there is no end-to-end CRUD path or admin UI for them today; the seeded credentials path in `internal/auth` is a v1 stub.

2. **Workspace-specific user API Keys** — every user can mint one or more API keys scoped to a single workspace and a chosen capability set (read / write / admin). Keys are revocable, list-able, and never appear in plaintext after creation. Service-account principals (already represented in `internal/principal` as `kind: service`) are the natural carrier — keys map to a service principal that inherits the user's identity but is workspace-pinned and revocable independently of the parent session.

3. **Incorporated MCP server reachable from Claude Code over HTTPS transport** — the binary exposes an MCP endpoint (HTTP streaming, not stdio) that authenticates via the same workspace-scoped API keys. A user pastes one URL + key into their Claude Code MCP config and gets read/write access to that workspace's pages, blobs, audit log, and search — with the same RBAC the HTTP API enforces. Tools surface page CRUD, full-text search, link/backlink graph, and blob upload.

The unifying outcome: **a single phronesis instance can be safely shared by a small team of humans plus the AI agents they each authorize, where every action is attributable to a named principal and revocable in seconds**. Read-from-the-codebase grounding: `internal/principal`, `internal/auth`, `internal/auth/oidc`, `internal/audit`, `internal/wiki/workspaces.go`, and the workspace plumbing committed during the silverbullet-like-live-preview iteration are the substrate this feature builds on.

What this feature is **not** scoped to in v1:
- Federation across phronesis instances (one server, one trust domain).
- Per-page ACLs (workspace remains the authorization unit).
- SSO group sync (workspaces are mapped manually; OIDC just authenticates the human).
- An MCP transport other than streamable HTTPS (no stdio, no SSE-only fallback).

---

## Constraints

### Business

#### B1: Every authenticated action is attributable to a named principal

Every request that mutates state OR returns workspace-scoped data MUST carry a resolved principal (user, OIDC-projected user, or service-principal-via-key) and produce an audit row that names the principal, the workspace, the action, and the timestamp. There is no "anonymous mutating action" path.

> **Rationale:** The whole reason for adding users + keys is attribution. If we ship a path where actions go uncredited, the feature has no point. Failure mode: an AI agent misbehaves; we can't tell which one because the request was attributed to "system".
> **Quality:** 3 / 3 / 3.

#### B2: 90% of new human users complete first-MCP-key in <10 minutes

Within their first 30 days, ≥90% of newly-created users (whether OIDC-projected on first login or manually provisioned) successfully reach a working state where Claude Code is reading and writing their workspace via MCP. "Working state" = at least one MCP tool call has returned 2xx with an audit row.

> **Rationale:** The whole point is "humans + AI agents collaborate". If onboarding to the AI side takes weeks, the value prop breaks. Statistical, not invariant — some users will be slower; we're optimising for the median, not the worst case.
> **Quality:** 3 / 3 / 2 — testability is "2" because validation requires user-time tracking, which is observable but not unit-testable.

#### B3: Single-instance scale ceiling — 250 humans, 1000 service principals

A single phronesis binary on commodity hardware (4 vCPU, 8 GB RAM, SSD) supports up to 250 active human users and up to 1000 active service principals (API keys with active=true) before any constraint (perf, audit-tail latency, list-users page render) starts breaking budget. Beyond that, an architectural change is required (read replicas, sharded audit, etc.).

> **Rationale:** From scoping question (Round 1). Sets the upper bound that all other technical constraints have to clear. Anything bigger pushes us toward Postgres, which conflicts with the dependency-light promise in CLAUDE.md.
> **Quality:** 3 / 3 / 2 — load test required to validate.

---

### Technical

#### T1: MCP endpoint authenticates via OAuth 2.1 with PKCE per the MCP authorization spec

The MCP server's HTTP transport implements the authorization profile defined in the MCP spec (currently 2025-06-18+): OAuth 2.1 authorization code flow with PKCE (S256), the `Mcp-Session-Id` response header, and proper resource-server semantics. Bearer tokens issued by phronesis carry the workspace + scope claims.

> **Rationale:** Spec-compliant interoperability with Claude Code and future MCP clients. The user explicitly chose OAuth 2.1 over a bearer-key shortcut (Round 1) — accept the heavier auth surface to be future-proof. Tagged `regulation` (cannot challenge: this is a spec we're conforming to, not a stakeholder opinion).
> **Quality:** 3 / 3 / 3.

#### T2: OIDC is the canonical identity source; local SQLite stores only projected user state

User identity (sub, preferred_username, email, name, role-via-group-claim) is canonically derived from OIDC at login. The local users table holds a projection keyed by OIDC `sub` plus per-user state that the IdP doesn't know (preferences, key list, last-active workspace). Identity facts the IdP owns (display name, email, group) are NEVER edited locally — they refresh from OIDC on next login.

> **Rationale:** From Round 1 ("OIDC IdP is source of truth, no local user table" + "embedded SQLite"). Combined: SQLite for state, OIDC for identity. Avoids a "user edited their email locally; doesn't match IdP" drift bug. Also makes IdP-driven offboarding (suspend in IdP → user becomes inactive on next session refresh) the natural path.
> **Quality:** 3 / 3 / 3.

#### T3: Embedded pure-Go SQLite store, schema migrations on startup

Persistent state (users projection, api_keys, sessions, audit if we choose to migrate from journal-based audit, capability_assignments) lives in a single SQLite file under `data/`. Use a pure-Go driver (modernc.org/sqlite) so no cgo, no separate libsqlite3 install, builds stay deterministic per the dist-packaging manifold. Schema migrations are forward-only and run on startup; a binary that can't migrate refuses to serve.

> **Rationale:** The medium-scale ceiling (B3) needs indexed key/user lookups; JSON files don't index. Pure-Go avoids the cgo headache the dist-packaging manifold worked hard to avoid (T1: deterministic builds). modernc.org/sqlite is the standard "I want SQLite without cgo" choice in 2026.
> **Quality:** 3 / 3 / 3.

#### T4: API key auth verification adds ≤5ms p95 cached / ≤100ms p95 cold

Resolving a presented bearer key to a `principal.Principal` (look up key by hash, verify Argon2id at OWASP-2023 production parameters, check `active && now < expires_at`, fetch capability set) must add ≤5ms p95 to a request whose key/user is in the in-process cache, and ≤100ms p95 on a cold cache path that hits SQLite. Cache invalidation is event-driven (S5).

> **Rationale:** AI agents will issue many small requests in burst; any per-call auth overhead multiplies fast. 5ms cached is a cheap-feeling ceiling. Cold-path budget set to 100ms after empirical measurement: OWASP-2023 Argon2id (m=64MiB, t=3, p=4) takes ~43ms verify on Apple M1 and ~80-90ms on slower CI runners. 100ms accommodates production-grade hashing on commodity infrastructure with WAL contention headroom. The cache hit is the common path; cold-path cost is intentional defense-in-depth against offline cracking.
> **Quality:** 3 / 3 / 3 — measurable via Go bench (see `internal/auth/keyverify_bench_test.go`).
> **History:** Original budget was ≤50ms cold (m1, source=assumption). Bench in user-mgmt-mcp Stage 1c (G3 closure, 2026-05-04) measured 49.1ms mean cold-path on M1; relaxed to ≤100ms with empirical justification. Argon2id production params confirmed at OWASP-2023 baseline.

#### T5: MCP tool response payload bounded by a server-enforced 10 MB ceiling

An MCP tool that would return >10 MB MUST instead return a reference (e.g. blob URL + size + content-type) and stream the bytes via an out-of-band download path. Hard server-side ceiling, not client-side; an over-budget response fails closed with a structured error rather than truncating silently.

> **Rationale:** From pre-mortem B3 (an MCP tool returns enough payload to OOM the server). Bounded response = bounded memory per request. 10 MB is a guess at "large enough to never need streaming for sane page content; small enough that 10 concurrent calls fit comfortably in 1 GB".
> **Quality:** 3 / 3 / 3.

#### T6: MCP sub-handler runs with isolated panic-recover; cannot crash main HTTP API

The MCP transport handler is a sub-mux behind its own `recover()` boundary, its own rate-limit budget, and its own metrics. A panic in any MCP tool dispatcher MUST NOT propagate to the main `net/http.Server` (which would otherwise tear down the whole listener). Test: a deliberate panic in a tool returns 500 to the MCP client and leaves the HTTP wiki API responsive.

> **Rationale:** From Round 3 (failure isolation = hard invariant). The MCP server is new code touching new dependencies (OAuth, JWT, MCP framing); it has the highest panic risk. The wiki API is the load-bearing piece; it must keep serving even while we debug an MCP regression.
> **Quality:** 3 / 3 / 3.

#### T7: MCP protocol version is pinned; a Claude-Code-compat smoke test gates CI

The binary advertises a single MCP protocol version (e.g. `2025-06-18`). A smoke test in CI spawns the binary, performs an MCP handshake using a vendored or fixture-pinned Claude-Code-equivalent client transport, and asserts the well-known tool schemas come back. If the spec changes underneath us, this test fails loudly before users notice.

> **Rationale:** From pre-mortem B1 (MCP spec churn breaks Claude Code). The MCP spec is on a 3-month revision cadence in 2026; we need a tripwire.
> **Quality:** 3 / 2 / 2 — "spec churn detection" is necessarily approximate; the smoke test catches breakage but not all kinds of drift.

#### T8: OIDC IdP calls have a 5s timeout and survive transient outage via cached JWKS

OIDC Discovery, JWKS fetch, token introspection, and userinfo calls each carry a configurable 5s timeout (default). The JWKS is cached in-process for 24 hours; expired-but-recent JWKS is preferred over a hard-fail when the IdP is unreachable. Token verification continues to work for the cached JWKS lifetime even if the IdP is down.

> **Rationale:** From GAP-11 (external dependency resilience) and pre-mortem C1 (IdP outage locks everyone out). Combined with S4 (break-glass admin), this lets the instance keep serving authenticated users through a transient IdP outage.
> **Quality:** 3 / 3 / 3.

---

### User Experience

#### U1: Adding phronesis as a Claude Code MCP server = paste 1 URL, complete OAuth flow once

The user copies one URL from phronesis Settings → MCP, pastes it into Claude Code's MCP config, completes the OAuth flow in their browser once, and the MCP server is live. No per-tool config, no API-key-paste step (the OAuth flow handles credentialing). Re-auth happens silently as long as refresh tokens are valid.

> **Rationale:** From outcome ("paste one URL + complete OAuth"). The whole UX premise is "this is the easy way to grant Claude Code workspace access". If it takes more steps than reading the manual, users won't do it.
> **Quality:** 2 / 2 / 2 — "the easy way" is fundamentally subjective; we'll proxy via "user-test stopwatch < 3 min" in m4.

#### U2: Admin → Users page lists users with status, last-seen, and active-key count

Admin role (derived from OIDC group claim, see U-route) sees a Users page at `/admin/users` listing every OIDC-projected user with: display name, email, status (`active` / `suspended`), last-seen timestamp, and count of active API keys. Each row is clickable to a user detail view with full key list and audit-event history.

> **Rationale:** From Round 2 (admin surface = Web UI + OIDC-driven role assignment). The page is the canonical "what's happening" surface for admins.
> **Quality:** 3 / 3 / 3.

#### U3: Admin → Keys page lists all keys in the workspace; one-click revoke

Admin (or workspace owner) sees `/admin/keys` listing every API key in the workspace: owner display name, scope tier, created timestamp, expires timestamp (or "never"), last-used timestamp, and a Revoke button. Revoke is one click and triggers S5's propagation contract.

> **Rationale:** From Round 2 (key minting = admin-only) — admins need the surface to manage what they've issued. Self-service users get "request a key" but not "revoke other people's keys".
> **Quality:** 3 / 3 / 3.

#### U4: First-run setup from clone to first OIDC sign-in completes in ≤15 minutes

A user following the README from a fresh `git clone` of phronesis can reach "first user signed in via OIDC, first workspace key minted" in ≤15 minutes wall-clock. The dev-stub OIDC verifier (already in `internal/auth/oidc`) is supported as the local-eval path so users can try the feature without configuring a real IdP first.

> **Rationale:** From pre-mortem A1 (OAuth 2.1 + PKCE setup is too hard, nobody actually configures it). Mitigation: ship a credible dev-stub path so users can evaluate, then graduate to a real IdP. 15 min is a stretch for a real IdP setup; it's achievable for the stub path.
> **Quality:** 3 / 3 / 2 — "user follows the README in 15 min" requires user-time tracking.

---

### Security

#### S1: API key plaintext is shown exactly once at creation; only Argon2id hash persisted

When admin mints a key on behalf of a user, the plaintext token is returned in the creation response and shown in the UI exactly once. The database stores ONLY the Argon2id hash (cost params chosen for the T4 budget). Subsequent listings show a non-reversible identifier (e.g. `phr_live_********abcd`); the plaintext is not recoverable from the DB.

> **Rationale:** Industry-standard. If keys could be re-displayed, a database compromise = key compromise. Tagged `regulation` because all reasonable compliance frameworks (SOC 2, ISO 27001) require this.
> **Quality:** 3 / 3 / 3.

#### S2: Bearer tokens / API keys / OAuth state never appear in logs, audit bodies, or error responses

A redaction pass at every log/audit/error-response writer matches and replaces bearer tokens, OAuth state tokens, and key plaintext before any egress. Tested by deliberately panicking on a request carrying a key and asserting the panic stack trace + error response + audit row contain none of it.

> **Rationale:** Industry-classic leakage path: someone logs `Authorization` header or returns the request in an error body. `regulation` because GDPR + standard secrets-handling require it.
> **Quality:** 3 / 3 / 3.

#### S3: Key scope is enforced at the request boundary, never silently downgraded

A read-scoped key MUST refuse a write-scoped tool with HTTP 403 and a structured `permission_denied` error. Never silently no-op, never auto-elevate, never partial-success. RBAC tier checks happen as the first thing after principal resolution, before any business logic.

> **Rationale:** RBAC sanity. Silent no-op is the worst-of-both-worlds: caller thinks success, side effect didn't happen, audit shows neither cleanly. `regulation` because least-privilege is non-negotiable in any auth model.
> **Quality:** 3 / 3 / 3.

#### S4: Local break-glass admin credential, env-gated, audit-emitting on use

A single break-glass admin credential is loadable independently of OIDC and the user/key tables: `PHRONESIS_BREAKGLASS=<hashed-secret>` env var. When set, a `/admin/break-glass` HTTP path accepts the secret in a header and returns an admin session token. EVERY use emits an audit row tagged `severity: high`. Default state is unset (no break-glass available).

> **Rationale:** From pre-mortem C1 (IdP outage) AND pre-mortem A1 (OIDC setup hard). Without a break-glass, an IdP outage = total lockout, including the admin who could fix it. Audit-emitting prevents abuse: any use is visible in retroactive forensics.
> **Quality:** 3 / 2 / 3 — testable; "audit-emitting on use" measurable; "abuse detection" requires monitoring practice.

#### S5: User suspension or key revocation propagates within 60 seconds

When an admin suspends a user OR revokes a key, all in-memory caches (key-to-principal, OIDC-claim cache, MCP session cache) MUST reflect the change within 60 seconds. The implementation choice is open: a fan-out signal channel, a 30s cache TTL, or DB-side change-data-capture polling — but the ceiling is fixed at 60s.

> **Rationale:** From pre-mortem C2 (suspended user's session still works). 60s is much better than the "1 hour" pre-mortem story and within the same order as CAEP propagation. `regulation` because suspension-honour is a security control whose latency budget is non-negotiable.
> **Quality:** 3 / 3 / 3.

#### S6: Per-key sliding-window rate limit + per-IP global ceiling

Each key gets a sliding-window quota (default 60 req/min, per-key configurable). The existing per-IP rate limit in `internal/ratelimit` continues to apply as a global ceiling. A key over its budget gets `429 Too Many Requests` with a `Retry-After` header. A per-key concurrency cap (default 5 in-flight) prevents a single AI agent from monopolising the worker pool.

> **Rationale:** From Round 3 + pre-mortem-adjacent "AI agent runs amok". Per-key isolation prevents one rogue key from starving others; per-IP global is a backstop. Defaults are configurable per workspace.
> **Quality:** 3 / 3 / 3.

---

### Operational

#### O1: Audit log retention — 90 days raw, then per-day aggregates; pruning is automatic

Raw audit rows older than 90 days (default, configurable) are compacted into per-day aggregates (count + distinct-principal-count + distinct-workspace-count + by-action histogram) then removed from the raw table. The compactor runs on a scheduled tick (default daily). Aggregates retained indefinitely (cheap).

> **Rationale:** From pre-mortem A3 (per-call audit grain blows up the journal). At 1000 service principals × 100 calls/day × 90 days = 9M raw rows worst-case — manageable with an index, painful without compaction. 90 days raw covers post-incident forensics; aggregates cover trend analysis.
> **Quality:** 3 / 3 / 3.

#### O2: /metrics Prometheus endpoint exposes auth + key + MCP counters

A `/metrics` endpoint (workspace-admin auth required, see B1) exposes at minimum: auth_attempts_total, auth_failures_total, key_uses_total{scope,workspace}, mcp_tool_calls_total{tool}, mcp_tool_latency_seconds (histogram), audit_drainer_lag_rows, oidc_outage_seconds_total. Authenticated probes; the endpoint is NOT publicly exposed.

> **Rationale:** From Round 3 (logs + Prometheus). Authenticated because the metrics include workspace-distinguishing labels — leaking would tell an attacker which workspaces are active.
> **Quality:** 3 / 3 / 3.

#### O3: SQLite schema migrations are forward-only; binary refuses to start on migration failure

On startup, T3's SQLite store opens, checks the schema version, and applies any pending migrations in order. Migrations are forward-only (no down). On any migration failure, the binary logs an error and exits non-zero before binding any port — never serves with a half-migrated schema.

> **Rationale:** Database hygiene. Forward-only avoids the "did we migrate up or down" ambiguity; refuse-to-serve avoids the "DB is half there, requests 500 randomly" mode.
> **Quality:** 3 / 3 / 3.
> **Source: assumption.** Specifically the assumption that we want forward-only (vs reversible) migrations; m4 should confirm before generation.

---

## Tensions

### TN1: OAuth 2.1 setup heaviness vs first-run ease

**Between:** T1 (OAuth 2.1 + PKCE — `regulation`) × U4 (≤15-min first-run — `stakeholder`).

The MCP spec mandates OAuth 2.1; setting up an external IdP is heavy enough that pre-mortem A1 predicted "nobody actually configures it". U4 wants a 15-minute path from `git clone` to first sign-in — incompatible with full OAuth setup.

> **Resolution:** P1 Segmentation by environment. The existing `internal/auth/oidc` HMAC stub serves dev/eval; setting `PHRONESIS_OIDC_ISSUER=<real-url>` swaps to spec-compliant OAuth 2.1 + PKCE without code changes. Stub mode emits a loud startup warning mirroring the `webfs.IsStub()` pattern. Prod deployments use a real IdP; the dist-packaging manifold's RT-9 loud-warning convention extends here.
> **TRIZ:** Technical contradiction. Parameters: simplicity vs capability. Match: exact. Principles: P1 Segmentation (split eval/prod paths), P15 Dynamization (env-driven mode switch), P35 Parameter changes (the OIDC mode is the parameter).
> **Validation:** `make run` boots with stub by default · setting the issuer env swaps to real OAuth without rebuild · stub mode prints a warning at startup.

### TN2: Per-call audit (B1) vs audit volume at B3 scale

**Between:** B1 (every authenticated action audited — `stakeholder`) × O1 (90-day retention — `stakeholder`); context: B3 (1000 service principals).

At B3 scale (1000 service principals × 100 calls/day worst case) audit grows ~3M raw rows / month. List-views and backups become slow if uncompacted; full retention forever is unaffordable.

> **Resolution:** Default O1 plan: 90 days raw with `(workspace_id, ts)` index, then per-day aggregates (count, distinct-principals, by-action histogram). Aggregates retained indefinitely (cheap). Compactor runs daily.
> **TRIZ:** Technical contradiction. Parameters: cost vs quality. Match: exact. Principles: P1 Segmentation (raw vs aggregate tiers), P10 Prior action (compact ahead of pressure), P27 Cheap short-living (raw rows are ephemeral — aggregates are permanent).
> **Validation:** synthetic-load test holds raw table ≤5 GB at B3 ceiling · compactor purges raw rows past retention after aggregate write · audit-list page renders latest 1000 rows in ≤500ms p95.

### TN3: OIDC canonical (T2) vs break-glass (S4) — physical contradiction

**Between:** T2 (OIDC is canonical identity source — `stakeholder`) × S4 (break-glass admin works without OIDC — `stakeholder`).

T2 says all identity flows through OIDC. S4 says one path must work when OIDC doesn't. "Must require OIDC AND must not require OIDC" is a textbook physical contradiction.

> **Resolution:** P1 Segmentation. Break-glass is a fully separate auth path with its own handler, its own middleware (no OIDC verifier), its own audit principal type (`break-glass`), its own env gate (`PHRONESIS_BREAKGLASS=<hashed-secret>`). When the env var is unset the path returns 404 — not 401, because the route doesn't exist. Default state: disabled.
> **TRIZ:** Physical contradiction. Match: exact. Principles: P1 Segmentation (separate paths in space/role), P3 Local quality (each path optimised for its purpose).
> **Validation:** break-glass handler shares no middleware with OIDC path · every use audited at `severity: high` · disabled state returns 404, not 401 · audit views can filter by principal type.

### TN4: Fast cached auth (T4) vs revocation propagation ≤60s (S5)

**Between:** T4 (≤5ms p95 cached auth — `assumption`) × S5 (revocation ≤60s — `regulation`).

A purely TTL-based cache forces a 60s window where a revoked key still works. Pure "no cache" violates T4. Functions as a blocking dependency from S5 onto T4's cache design.

> **Resolution:** P10 + P11. Event-driven invalidation: revoke action publishes to an in-process channel; all caches flush specific entries. 30s TTL belt covers any missed signal. Real-world propagation sub-second; worst case 30s — well under S5's 60s ceiling. Cache also invalidates on Argon2id verify failure (defense in depth).
> **TRIZ:** Technical contradiction. Parameters: speed vs correctness. Match: exact. Principles: P10 Prior action (invalidate proactively on revoke), P11 Beforehand cushioning (TTL belt covers missed signals), P25 Self-service (cache evicts itself on signal).
> **Validation:** revoke → next request with that key returns 401 within 30s p99 · pre-warmed cache observes revoke via channel without polling · TTL=30s · GAP-16 sub-constraints staged: invalidation-on-validation-failure, credential rotation, forced flush.
> **GAP-16 sub-constraints** (staged in `suggested_constraints`): see S9-suggested.

### TN5: MCP isolation (T6) vs shared substrate (T3 / S6)

**Between:** T6 (MCP panic isolation — `technical-reality`) × T3 (single SQLite — `stakeholder`); context: S6 (rate-limit pool), B1 (audit drainer).

Full duplication (separate SQLite, separate audit drainer, separate rate-limit pool) wastes resources. Full sharing means an MCP panic could tear down the listener for the whole binary — violates T6.

> **Resolution:** Share read-only / pooled substrate (SQLite connection pool, audit drainer, principal resolver, rate-limit buckets). Isolate per-handler dispatch (panic-recover, request budget, /metrics labels). MCP runs as a sub-mux behind its own `recover()`; a tool-handler panic returns 500 to the MCP client and never propagates to the main `net/http.Server`.
> **TRIZ:** Technical contradiction. Parameters: standardisation vs flexibility. Match: exact. Principles: P1 Segmentation (substrate vs dispatch), P3 Local quality (each handler has its own recover boundary), P15 Dynamization (per-key budgets within a shared pool).
> **Validation:** deliberate panic in MCP tool → 500 to MCP client + HTTP /readyz still 200 + no goroutine leak.

### TN6: Per-call audit (B1) vs ≤5ms cached auth budget (T4)

**Between:** B1 (every authenticated action audited — `stakeholder`) × T4 (≤5ms p95 — `assumption`).

A synchronous audit-write on every request adds 5–20ms — already over T4's cached-path budget alone. Purely sampled audit degrades B1's "every action" guarantee.

> **Resolution:** P10 + P24. Async audit drainer (already exists in `internal/audit`). Audit enqueue is a non-blocking channel send; if the channel is full, increment a drop counter (`audit_drainer_drops_total` exposed via /metrics) — never stall the request path. Drainer flushes on a 1s tick and on shutdown.
> **TRIZ:** Technical contradiction. Parameters: speed vs safety. Match: exact. Principles: P10 Prior action (enqueue first, write later), P24 Intermediary (the drainer is the intermediary), P11 Beforehand cushioning (drop counter as visible safety net).
> **Validation:** audit enqueue is non-blocking · drop counter exposed via /metrics · drainer flushes on tick + shutdown.
> **Failure cascade:**
> - Q: drainer can't keep up? → A: channel fills → drops increment → alert fires → operator scales/throttles. Constraint upheld in expectation; loss is visible.
> - Q: crash before drain? → A: up to 1 batch interval (≈1s) of audit lost. **Mitigation candidate:** spillover journal mirroring `internal/journal` — staged as O4-suggested.
> - Q: disk full → SQLite write fails? → A: drainer logs + retries with backoff; /readyz fails after N retries; operator intervenes.

### TN7: Admin-only key provisioning vs B2 (90% onboard <10 min)

**Between:** U3 (admin-only key flow — `stakeholder`, Round 2 decision) × B2 (90% onboard <10 min — `stakeholder`).

Admin-only minting risks becoming the bottleneck — if admin response is slow, B2's 10-minute onboard target slips. Pure self-service revises the Round 2 decision.

> **Resolution:** P10. Request → approve flow. User submits a key request via `/settings/keys/request`; row inserted into `key_requests` with `status=pending`. Admin sees a pending-request badge on the Users page; one-click Approve → key minted (S1's plaintext-once flow) → plaintext delivered to the requesting user via in-app notification (+ email if configured). Audit row links request_id, admin principal, and key_id.
> **TRIZ:** Technical contradiction. Parameters: autonomy vs control. Match: exact. Principles: P10 Prior action (admin pre-acts via approve-buttons, not form-fills), P15 Dynamization (request flow has explicit states), P35 Parameter changes (admin response time becomes the lever, surfaced as B4-suggested).
> **Validation:** request flow exists at `/settings/keys/request` · pending count badge on `/admin/users` · one-click approve mints key · audit row links request and approval.
> **Surfaced sub-constraint:** B4-suggested — admin response SLA: median 1 business hour, 90% within 24 business hours, for B2 to remain meetable.

### TN8: T1 (OAuth 2.1) hidden-depends on S2 (no token leakage)

**Between:** T1 (OAuth 2.1 — `regulation`) × S2 (no bearer leakage — `regulation`). Build-order dependency, not a true contradiction.

OAuth 2.1's bearer tokens, refresh tokens, and PKCE code_verifier are exactly the kind of secret S2 forbids leaking. If T1 ships before S2's redaction layer, any panic stack or error response could leak credentials.

> **Resolution:** Implement S2's redaction pass FIRST (covering: `Authorization` header, `?code=`, `?state=`, `?token=`, `refresh_token`, `code_verifier`, `phr_live_*` patterns). Unit-test the pass with a deliberate panic carrying a bearer token. Only then build T1's OAuth handlers on top.
> **TRIZ:** No strong TRIZ mapping — supportive dependency, resolved via build order.
> **Validation:** S2 redaction pass implemented + unit-tested before T1 OAuth handlers ship · deliberate-panic test asserts panic stack, error response, and audit row contain no token material · S2's pattern set explicitly covers OAuth-specific identifiers.
> **Build-order recorded as a blocking dependency for m3.**

---

## Required Truths

### RT-1: Universal audit-write at every authenticated request boundary

For "every action attributable to a named principal" to hold, every handler that mutates state OR returns workspace-scoped data MUST resolve a principal first and enqueue an audit row before responding. The audit-write must be on the path of every authenticated handler — HTTP wiki API, MCP tool dispatcher, OAuth callback, admin endpoints — without exception.

**Maps to:** B1, S3, T6.
**Gap:** today only some handlers (e.g. workspace create/delete from the silverbullet-like-live-preview iteration) explicitly enqueue audit rows. There is no central middleware that guarantees every authenticated handler does so.

### RT-2: Spec-compliant MCP server with OAuth 2.1 + PKCE

For "Claude Code over HTTPS transport" to work, an MCP HTTP server must speak the 2025-06-18+ MCP authorization profile: OAuth 2.1 Authorization Code flow with PKCE (S256), `Mcp-Session-Id` header, structured error envelopes, and the standard `tools/list` + `tools/call` JSON-RPC method set.

**Maps to:** T1, T7.
**Gap:** no MCP server exists today. No OAuth 2.1 authorization endpoint exists today. No JSON-RPC framing exists today.

### RT-3: Workspace-scoped principal model

For "key resolves to a service principal pinned to a single workspace with a capability tier" to hold, `internal/principal` must carry workspace + capability fields, the auth pipeline must resolve a presented bearer key (or OAuth token) to a workspace-pinned principal, and downstream handlers must use that principal as the authorization root.

**Maps to:** T2, S1, S3, B3.
**Gap:** `internal/principal` already has `kind: user|service` and a `Role` enum but no workspace pinning + no capability tier. Key→principal resolution doesn't exist; only cookie-session→user resolution does.

### RT-4: Event-driven cache invalidation with 30s TTL belt

For "revocable in seconds" to hold against the T4 cached-auth budget, the in-process cache from key/token to principal must be invalidatable by signal AND time-bounded. Revoke action publishes to an in-process channel; caches evict specific entries on signal; a 30s TTL covers any missed signal. Cache also invalidates on Argon2id verify failure (defense in depth).

**Maps to:** S5, T4.
**Gap:** no auth cache exists today (because keys don't exist). Need to design the cache + invalidation channel before Stage 2.

### RT-5: OIDC claim-set → user projection with cached JWKS

For "OIDC is canonical identity" to hold practically, OIDC Discovery + JWKS must be cached (≤24h), token verification must work for the cached JWKS lifetime even during IdP outage, and a deterministic, idempotent mapping must convert claim sets to local user projections (same `sub` + `email` always produce the same row).

**Maps to:** T2, T8.
**Gap:** existing `internal/auth/oidc` has the verifier scaffold but no JWKS cache, no projection layer, and no IdP-outage tolerance. The HMAC stub path exists.

### RT-6: Bearer-token redaction at every egress (BINDING CONSTRAINT)

For ANY of the auth-bearing flows above to ship safely, a redaction pass must exist at every log writer, every error-response builder, every audit-body emitter, and every panic-recover handler. Patterns covered: `Authorization` header values, `?code=`/`?state=`/`?token=` query params, `refresh_token`, `code_verifier`, and the `phr_live_*` API key prefix.

**Maps to:** S2, T1.
**Gap:** no redaction layer exists. `slog` access logs, error responses, and panic stack handlers all currently emit raw values. **This is the binding constraint** — see "Binding Constraint" section below.

### RT-7: Per-key sliding-window rate limit + per-IP global ceiling

For S6 (per-key rate budget + per-IP ceiling) to hold, the rate-limit middleware must support per-key buckets in addition to the existing per-IP buckets. Per-key concurrency cap (default 5 in-flight) prevents a single AI agent from monopolising the worker pool.

**Maps to:** S6.
**Gap:** `internal/ratelimit` is per-IP only today. Need to extend to per-key (keyed off resolved principal ID).

### RT-8: SQLite-backed store with forward-only schema migrations

For T3 + O3 to hold, the binary embeds a pure-Go SQLite driver (modernc.org/sqlite), opens `data/phronesis.db` on startup, runs forward-only migrations from a `migrations/` package, and refuses to start (logs error, exits non-zero) on migration failure.

**Maps to:** T3, O3.
**Gap:** no SQLite store exists today. Workspaces use `data/workspaces.json`; pages use the on-disk filesystem; audit uses an in-memory ring + journal. SQLite needs to be added without disrupting these.

### RT-9: Admin Web UI surfaces — Users, Keys, Key-Request approval

For U2, U3, and TN7 to hold, the frontend exposes `/admin/users` (list users with status, last-seen, active-key count), `/admin/keys` (list workspace keys with one-click revoke), and `/admin/users` shows a pending-key-request badge that opens a one-click approve flow. Roles derived from OIDC group claims.

**Maps to:** U2, U3.
**Gap:** no admin UI today (the v1 silverbullet iteration added an admin-only WorkspaceManager modal; that pattern extends here). Admin role exists in `internal/principal` but is not surfaced in the UI shell beyond the Cmd-K palette gating.

### RT-10: Async audit drainer + 90d/aggregate retention + bounded loss-on-crash

For B1 + O1 + TN6 to hold simultaneously, the audit drainer is non-blocking (channel send, drop counter on full), retains raw rows for 90 days then compacts to per-day aggregates, and bounds loss-on-crash (TN6's failure cascade) via a spillover journal (mirroring `internal/journal`'s pattern).

**Maps to:** B1, O1.
**Gap:** existing `internal/audit` has the buffered async drainer pattern but only writes to an append-only file today. Schema migration to SQLite + retention/compaction is new. Spillover journal-on-crash is staged as O4-suggested.

### RT-11: MCP sub-handler isolation — panic-recover + per-handler budget

For T6 + TN5 to hold, the MCP server runs as a sub-mux behind its own `recover()` boundary, with its own request budget (separate per-key rate-limit pool) and its own `/metrics` labels. A panic in any tool-dispatcher returns 500 to the MCP client and never propagates to the main `net/http.Server`.

**Maps to:** T6.
**Gap:** the entire MCP server is new code. The isolation pattern is straightforward (one `http.Handler` mounted at `/mcp` with `recover()` middleware), but it must be done from day one — retrofitting is harder than building it in.

### RT-12: Stub OIDC dev path supported with loud startup warning

For TN1 + U4 to hold, the existing `internal/auth/oidc` HMAC stub remains a first-class boot mode (`PHRONESIS_OIDC_MODE=stub` or unset). Setting `PHRONESIS_OIDC_ISSUER=<real-url>` swaps to real OAuth 2.1. Stub mode emits a slog warning at server startup, mirroring the `webfs.IsStub()` pattern from the dist-packaging manifold.

**Maps to:** U4, T1.
**Gap:** stub exists in `internal/auth/oidc` but is not surfaced as a documented eval path; no startup warning today. Documentation needs to land alongside the constraint.

### RT-13: Break-glass admin path segmented from OIDC, env-gated, audit-emitting

For S4 + TN3 to hold, a break-glass admin handler (`/admin/break-glass`) is mounted only when `PHRONESIS_BREAKGLASS=<argon2id-hashed-secret>` is set; default state returns 404 (not 401) — the route doesn't exist when not configured. Every successful authentication via this path emits an audit row at `severity: high` with `principal.type=break-glass`.

**Maps to:** S4.
**Gap:** no break-glass path exists today. Must share NO middleware with the OIDC auth path (TN3's segmentation requirement).

### RT-14: MCP tool inputs validate against per-tool JSON schema before dispatch

For T5 + S7-suggested + GAP-14 to hold, every MCP tool registers a JSON schema for its input. Inputs failing validation return `400 invalid_arguments` with NO side effect (no audit row, no partial mutation, no downstream call).

**Maps to:** T5.
**Gap:** no MCP tools exist today; the validation-before-dispatch pattern needs to be built into the tool framework from day one.

---

## Binding Constraint

**RT-6: Bearer-token redaction at every egress.**

> **Reason:** Build-order gate from TN8. Cross-cutting (every log/audit/error-response writer must route through it). Not in the codebase today. A single missed egress point is a credential-leak incident. Closing it unlocks RT-2 (OAuth handlers can ship safely), which in turn unlocks RT-1 (audit), RT-3 (principals), and the rest. Verification is hardest because completeness is the hard property.
>
> **Dependency chain:** RT-6 → RT-2 → RT-1 → RT-3 → RT-7 → RT-9.
>
> **m4 implication:** generate the redaction layer FIRST when context is freshest. Tag those artifacts `priority: binding`.

---

## Solution Space

### Option A: Full v1 — all 14 RTs end-to-end

- **Satisfies:** RT-1 through RT-14.
- **Gaps:** none (modulo time).
- **Complexity:** High.
- **Reversibility:** REVERSIBLE_WITH_COST. Auth-schema changes are migration-able but painful once external IdP + Claude Code clients exist in the wild.
- **Estimate:** 4-6 weeks dedicated focus.
- **Tradeoff:** highest cost, highest value, longest time-to-first-user-feedback. Touches OIDC + OAuth 2.1 + MCP + admin UI + key flow + audit retention in one sweep — every regression hits at once.

### Option B: Stage-gated rollout ← Recommended

- **Satisfies:** RT-1 through RT-14, in three coherent stages.
- **Gaps:** intermediate gaps at each stage boundary (deliberate; each stage leaves a runnable system).
- **Complexity:** Medium.
- **Reversibility:** TWO_WAY at each stage boundary.
- **Estimate:** Stage 1 ≈ 1.5 wk · Stage 2 ≈ 1.5 wk · Stage 3 ≈ 2-3 wk.

| Stage | Title | RTs satisfied | Why this order |
|-------|-------|---------------|-----------------|
| **1** | User store + admin UI + OIDC + redaction substrate | RT-5, RT-6, RT-8, RT-9, RT-12, RT-13 | Lay the foundation. RT-6 (binding constraint) goes in early when context is freshest. SQLite store + OIDC projection + admin Web UI + break-glass + stub path. Existing cookie-session users keep working; OIDC overlay is opt-in via env. |
| **2** | Workspace API keys + service principals + per-key rate limit | RT-1, RT-3, RT-7, RT-10 | Service principals come online. HTTP API now supports `Authorization: Bearer phr_live_...`. Audit drainer + retention productionised. Key-request → admin-approve flow live. RT-6 redaction is already enforced — keys can ship safely. |
| **3** | MCP server + OAuth 2.1 + tool framework + spec smoke test | RT-2, RT-4, RT-11, RT-14 | The biggest, riskiest stage — but sits atop a stable substrate. OAuth handlers safe (RT-6 done). Cache invalidation event channel built (RT-4). MCP sub-handler isolation from day one (RT-11). Tool input validation in the framework (RT-14). |

### Option C: MCP-first, defer admin Web UI to v1.1

- **Satisfies:** RT-1, RT-2, RT-3, RT-4, RT-5, RT-6, RT-7, RT-8, RT-10, RT-11, RT-12, RT-13, RT-14.
- **Gaps:** RT-9 (admin Web UI deferred; CLI-only key/user management in v1).
- **Complexity:** Medium-High.
- **Reversibility:** TWO_WAY but UX-incomplete. Users without CLI access are stuck on key-request approval until v1.1.
- **Estimate:** 3-4 weeks.
- **REOPENS TN7:** the request-approve flow becomes CLI-only (`phronesis user approve-key <request-id>`). Onboard time (B2) at risk because admins without terminal access can't process requests.

---

## Recommendation

**Option B (stage-gated rollout)** — recommended.

It satisfies all 14 RTs without reopening any tension, sequences the work so RT-6 (binding constraint) is closed first while context is freshest, and provides three coherent stage boundaries where the project can pause, ship, or re-evaluate. The same total scope as Option A but with less coupled risk and earlier user feedback (Stage 1 is shippable as "user-management v1" even before MCP exists).
