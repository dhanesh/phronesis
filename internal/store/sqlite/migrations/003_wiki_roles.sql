-- phronesis:foreign-keys-off
-- 003_wiki_roles.sql — widen users.role to the wiki RBAC ladder.
--
-- 001 allowed only 'user' | 'admin'. Page RBAC (decision 2026-11-27,
-- docs/roles.md) needs viewer < editor < admin. Legacy 'user' rows become
-- 'viewer' (the role they already authorised as).
--
-- SQLite cannot alter a CHECK constraint, so this is the documented 12-step
-- table rebuild. The directive on line 1 makes the runner switch
-- foreign_keys OFF on a pinned connection outside the transaction; without
-- it, DROP TABLE users would cascade-delete api_keys and key_requests. The
-- runner runs PRAGMA foreign_key_check before commit.
CREATE TABLE users_new (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    oidc_sub     TEXT    NOT NULL UNIQUE,
    email        TEXT,
    display_name TEXT,
    role         TEXT    NOT NULL DEFAULT 'viewer',                     -- 'viewer' | 'editor' | 'admin'
    status       TEXT    NOT NULL DEFAULT 'active',                     -- 'active' | 'suspended'
    created_at   TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_seen_at TEXT,
    CHECK (role IN ('viewer', 'editor', 'admin')),
    CHECK (status IN ('active', 'suspended'))
);

INSERT INTO users_new (id, oidc_sub, email, display_name, role, status, created_at, last_seen_at)
SELECT id, oidc_sub, email, display_name,
       CASE role WHEN 'admin' THEN 'admin' ELSE 'viewer' END,
       status, created_at, last_seen_at
  FROM users;

DROP TABLE users;
ALTER TABLE users_new RENAME TO users;
CREATE INDEX idx_users_status ON users(status);
