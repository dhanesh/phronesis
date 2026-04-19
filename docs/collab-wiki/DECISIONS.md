# Architectural Decisions: collab-wiki

_Distilled from `.manifold/collab-wiki.json` · Cross-reference: [PRD](PRD.md) · [STORIES](STORIES.md)_

This file records the 10 architecturally-consequential decisions made during constraint tensioning (m2) and the solution choice made during anchoring (m3). Each decision names the tension it resolves, the option taken, the alternatives rejected, and the propagation consequences.

---

## D1 (from TN1): Bounded-loss redefinition of B1 "collab is INVARIANT"

**Decision:** B1's "working state" for CRDT collab is defined concretely as "edits acknowledged as durable within the O8 window (≤3s idle OR ≤100 ops per room, whichever first)". Crash-loss up to that window is contract-compliant, not a B1 violation.

**Rejected:** Persisted CRDT sidecar (violates T1); sync-fsync-per-op (kills T2/T3).

**Consequence:** O8 is elevated from a "good idea" to a load-bearing INVARIANT. Relaxing O8's 3s/100-op window is equivalent to weakening B1. The binding constraint RT-2 carries the observability obligation: both `op_acked` and `op_saved` events must be emitted so B1's contract is falsifiable.

---

## D2 (from TN2): Async read-audit off the hot path

**Decision:** S9's async audit buffer pattern is sufficient to satisfy both S2 (full audit including reads) and T3 (p95 < 100ms at 1K rps). T3 is measured on the read response path, excluding audit enqueue cost.

**Rejected:** Read-audit sampling (changes S2 semantics); conditional per-doc sensitivity audit (changes S2 semantics).

**Consequence:** The audit buffer is one more thing to drain at SIGTERM; TN10's journal-spill pattern applies if drain times out with an audit backlog. Bounded audit loss on crash is accepted and surfaced via a `audit_events_dropped_total` counter.

---

## D3 (from TN3): v1 k8s as single-replica with interface readiness

**Decision:** T8's "k8s-friendly" claim is honored as single-replica-on-k8s for v1, with all stateful subsystems (session store, CRDT broadcast, snapshot target, audit sink, blob store) abstracted behind interfaces with in-process defaults so v2 can drop in Redis pubsub or sticky routing without code rewrites.

**Rejected:** Multi-replica with sticky per-document routing (premature complexity); multi-replica with Redis pubsub (violates T8's "in-process defaults" posture).

**Consequence:** Documentation MUST call out "k8s single-replica for v1" as the supported posture. RT-4 interfaces are mandatory Wave-2 deliverables; without them, v2 migration cost balloons.

---

## D4 (from TN4): Media lives outside git in a pluggable blob store

**Decision:** Embedded media (U4) is stored in a pluggable blob store (default: local filesystem under data dir; optional: S3-compatible) and referenced from markdown by content-addressed URL (`/media/<sha>`). Git repos hold only markdown.

**Rejected:** Git LFS mandate per workspace (hard deployment dependency); operator-choosable hybrid (doubles test matrix).

**Consequence:** O7 snapshot MUST cover blob store bytes, not just markdown. S6's per-workspace quota scope is the blob store, not the git repo. O9's repo-size alerts no longer need to police media.

---

## D5 (from TN5): "Launch" = password live + OIDC adapter architecturally complete

**Decision:** B1's "password+OIDC is INVARIANT" is honored by shipping password auth fully operational AND the OIDC adapter interface complete with a stub provider passing integration tests in CI. Enabling a real IdP is config-only.

**Rejected:** Reclassifying OIDC as a GOAL (narrows B1's INVARIANT promise); delaying launch until a real IdP is integrated (slows time-to-first-release).

**Consequence:** S4's OIDC contract (JWKS handling, rotation, S8 claim mapping) must be fully implemented at launch, not deferred. If the OIDC adapter has post-launch bugs, password-auth fallback is always available.

---

## D6 (from TN6): Two-state durability indicator (synced → saved)

**Decision:** The UI shows **"Synced"** when the server has acked the op and broadcast it to peers (≤300ms p95), and upgrades to **"Saved"** when the disk flush has committed (≤3s per O8). This is the established Google Docs / Notion pattern for collaborative editors.

**Rejected:** Force fsync within 300ms of keystroke cessation (fsync storms at scale); tighten O8 to sub-second (potentially violates T3 at 1K rps).

**Consequence:** U3's 300ms SLO now governs the "synced" state; durability is a separate visual signal. Missing synced ack within a client timeout (default 2s) transitions the editor to read-only per U2.

---

## D7 (from TN7): Server-side auth-endpoint rate-limit floor

**Decision:** The server runs an always-on per-IP sliding-window rate limiter on `/login`, `/auth/callback`, and `/auth/*`. Operator cannot disable it. X-Forwarded-For honors a configurable trusted-proxy allow-list. Reverse proxy remains the primary defense for other endpoints per S5; the floor is a backstop against operator misconfig.

**Rejected:** Startup self-check that probes whether an external proxy is present (fragile); docs-only (leaves pre-mortem failure mode latent).

**Consequence:** S5 NARROWS: the server is no longer "only input-size bounds." Documentation still emphasizes reverse-proxy WAF/IP-reputation as the production primary defense, especially against distributed-IP attacks the floor cannot stop alone.

---

## D8 (from TN8): Workspace service accounts take the same 3 RBAC roles

**Decision:** Workspace service accounts use the SAME `viewer / editor / admin` role enum as users. A single authorization function evaluates both principal classes. Finer-grained scoping (e.g., "editor-but-only-on-docs/api/\*\*") is explicitly deferred to v2.

**Rejected:** Per-scope capability model (diverges from B2's simplicity); hybrid 3-role + path-glob (non-trivial v1 surface).

**Consequence:** Audit log schema gains `principal_type` (user vs service_account) alongside `principal_id` so operators can filter by identity class. This TIGHTENS S2's schema by one column.

---

## D9 (from TN9): /readyz tracks traffic-serving ability only; remote health is a metric

**Decision:** `/readyz` returns 200 only when data dir writable, CRDT manager alive, audit buffer OK, and journal replay complete (from D10). Git remote health is exposed via a dedicated `/metrics` gauge and alert channel; remote unreachability does NOT affect /readyz.

**Rejected:** Fail /readyz on sustained remote failure (tuning complexity, still flappy); keep /readyz strict on remote (flap disrupts live collab under k8s).

**Consequence:** O4 LOOSENED on remote conditions; TIGHTENED on journal-replay (see D10). Readiness becomes a clean "can I serve traffic right now" signal; durability health is a separate concern.

---

## D10 (from TN10): Bounded SIGTERM drain + persistent journal spill + startup replay

**Decision:** SIGTERM triggers: (1) always flush active CRDT rooms (in-memory loss otherwise), (2) drain git push queue up to a configurable 30s window, (3) spill any remaining queue to a persistent journal file under the data dir, (4) exit cleanly. On next startup, the journal is replayed BEFORE `/readyz` returns 200.

**Rejected:** Unbounded drain (violates k8s graceful-termination windows); drop unflushed pushes (violates push-eventually semantic of T6+O7).

**Consequence:** O4 TIGHTENED: journal-replay-complete is a readiness precondition. O5 TIGHTENED: journal-spill is a required code path. If journal WRITE fails (disk full), shutdown blocks with a loud alert — explicit human-intervention path. If journal REPLAY fails at startup, startup errors with a clear message.

---

## Binding Constraint (from m3)

**RT-2 — `op_acked` vs `op_saved` distinction with enforceable O8 flush** is the single required truth that, if not closed, silently invalidates B1, breaks U3, and strands RT-7's journal semantics. It is the highest-risk artifact in the plan and is generated FIRST in Wave 1.

### Why RT-2 is binding

- **Touches four layers:** CRDT engine, server event emission, client state machine, UX indicator.
- **Cascades clarity:** Closing it makes B1 observable (RT-1), U3 well-defined (TN6), and RT-7/RT-12 durability semantics consistent.
- **Cannot be retrofitted cheaply:** the durability-and-UX state machine is encoded across wire protocol, server, client, and user perception simultaneously. Refactoring it late is expensive.

---

## Selected Solution Option: **C (ToC-aligned, binding-first)** — Reversibility: TWO_WAY

Ordered waves with all 12 RTs satisfied:

1. **Wave 1 (current):** RT-2 spike — prove two-state events + O8 flush + client indicator.
2. **Wave 2:** RT-4 interfaces — session, CRDT broadcast, snapshot, audit, blob.
3. **Wave 3 (parallel-safe):** RT-5 Principal · RT-6 blob-store media · RT-11 OIDC adapter.
4. **Wave 4:** RT-7 journal · RT-10 auth RL floor · RT-12 bounded drain.
5. **Wave 5:** RT-3 async audit drainer · RT-8 /readyz scope · RT-9 XSS+CSP · RT-1 degradation contract doc.

All 10 tensions CONFIRMED by Option C (see PRD §9 Risks for per-tension mitigation).
