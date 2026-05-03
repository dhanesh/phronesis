-- 001_init.sql — initial schema for user-mgmt-mcp Stage 1a.
--
-- Satisfies: RT-3 (workspace-scoped principal model — users + api_keys
--                  carry workspace + capability),
--            RT-8 (forward-only migrations from a versioned set),
--            S1 (api_keys store only Argon2id hash, never plaintext),
--            B1 (audit_events table; per-call audit grain),
--            TN7 (key_requests table backs admin request->approve flow).
--
-- Forward-only contract: NEVER edit this file once another migration
-- has been authored. Adjustments to this schema land in 002_*.sql, not
-- here. RT-8's no-edit rule on prior migrations is what keeps the
-- "is_applied?" check in schema_version meaningful.

-- Users projected from OIDC claims. Identity is OIDC-canonical (T2);
-- this table is local state keyed by OIDC `sub`.
CREATE TABLE users (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    oidc_sub     TEXT    NOT NULL UNIQUE,                              -- canonical OIDC identifier
    email        TEXT,                                                  -- projected from claims, may be NULL
    display_name TEXT,                                                  -- projected from `name` or `preferred_username`
    role         TEXT    NOT NULL DEFAULT 'user',                       -- 'user' | 'admin'; derived from OIDC group claim
    status       TEXT    NOT NULL DEFAULT 'active',                     -- 'active' | 'suspended'
    created_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_seen_at TEXT,
    CHECK (role IN ('user', 'admin')),
    CHECK (status IN ('active', 'suspended'))
);

CREATE INDEX idx_users_status ON users(status);

-- API keys (workspace-scoped service principals).
-- Plaintext is shown ONCE at creation; only the Argon2id hash persists (S1).
CREATE TABLE api_keys (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_slug  TEXT    NOT NULL,                                  -- workspace pin (RT-3)
    scope           TEXT    NOT NULL,                                  -- 'read' | 'write' | 'admin' (RBAC tier, S3)
    label           TEXT    NOT NULL,                                  -- human-readable label
    key_prefix      TEXT    NOT NULL UNIQUE,                           -- e.g. 'phr_live_abcd1234' — non-secret display id
    key_hash        BLOB    NOT NULL,                                  -- Argon2id(plaintext) (S1)
    created_at      TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    expires_at      TEXT,                                              -- NULL = never expires (long-lived); else ISO timestamp
    revoked_at      TEXT,                                              -- NULL = active; non-null = revoked
    last_used_at    TEXT,
    CHECK (scope IN ('read', 'write', 'admin'))
);

CREATE INDEX idx_api_keys_user ON api_keys(user_id);
CREATE INDEX idx_api_keys_workspace ON api_keys(workspace_slug);
CREATE INDEX idx_api_keys_active ON api_keys(revoked_at) WHERE revoked_at IS NULL;

-- Key requests: user submits, admin approves (TN7 resolution).
CREATE TABLE key_requests (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id            INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_slug     TEXT    NOT NULL,
    requested_scope    TEXT    NOT NULL,
    requested_label    TEXT    NOT NULL,
    requested_at       TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    decided_at         TEXT,                                           -- NULL = pending
    decided_by_user_id INTEGER REFERENCES users(id),
    decision           TEXT,                                           -- NULL | 'approved' | 'denied'
    resulting_key_id   INTEGER REFERENCES api_keys(id),
    CHECK (requested_scope IN ('read', 'write', 'admin')),
    CHECK (decision IS NULL OR decision IN ('approved', 'denied'))
);

CREATE INDEX idx_key_requests_pending ON key_requests(workspace_slug, requested_at)
    WHERE decided_at IS NULL;

-- Audit events (B1 + O1). Raw rows here; aggregates land in
-- audit_aggregates in a future migration when the compactor is wired.
CREATE TABLE audit_events (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    ts             TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    workspace_slug TEXT,
    principal_type TEXT    NOT NULL,                                   -- 'user' | 'service' | 'break-glass'
    principal_id   TEXT    NOT NULL,                                   -- user.id (string) or 'phr_live_abcd1234' prefix
    action         TEXT    NOT NULL,                                   -- e.g. 'page.write', 'workspace.create', 'breakglass.use'
    target         TEXT,                                               -- resource being acted on (page name, key prefix, ...)
    severity       TEXT    NOT NULL DEFAULT 'info',                    -- 'info' | 'high'
    body           TEXT,                                               -- JSON-encoded payload (already redacted via internal/redact)
    CHECK (principal_type IN ('user', 'service', 'break-glass')),
    CHECK (severity IN ('info', 'high'))
);

CREATE INDEX idx_audit_workspace_ts ON audit_events(workspace_slug, ts);
CREATE INDEX idx_audit_principal    ON audit_events(principal_type, principal_id);
CREATE INDEX idx_audit_severity     ON audit_events(severity) WHERE severity = 'high';
