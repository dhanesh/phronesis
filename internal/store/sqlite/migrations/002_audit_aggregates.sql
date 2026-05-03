-- 002_audit_aggregates.sql — Stage 2b-retention.
--
-- Satisfies: O1 (audit retention; per-day aggregates retained
--                indefinitely after raw rows are pruned),
--            RT-10 (audit drainer + retention substrate — aggregate
--                   table is the long-term home),
--            B1 (per-day aggregates preserve attribution at the
--                workspace + action level even after raw rows are
--                compacted).
--
-- Forward-only contract: NEVER edit this file once another
-- migration has been authored. Adjustments to this schema land in
-- 003_*.sql. RT-8's no-edit rule on prior migrations is what
-- keeps the schema_version "is_applied?" check meaningful.

-- Per-day, per-(workspace, action, severity) audit summary.
-- Compactor groups raw audit_events rows older than the retention
-- threshold (90 days default) into these rows, then deletes the
-- raw rows.
CREATE TABLE audit_aggregates (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    day                 TEXT    NOT NULL,                  -- YYYY-MM-DD UTC
    workspace_slug      TEXT,
    action              TEXT    NOT NULL,
    severity            TEXT    NOT NULL DEFAULT 'info',
    count               INTEGER NOT NULL,
    distinct_principals INTEGER NOT NULL,
    distinct_workspaces INTEGER NOT NULL,
    CHECK (severity IN ('info', 'high')),
    UNIQUE (day, workspace_slug, action, severity)
);

-- Day-first index drives "show me activity for last N days" queries.
CREATE INDEX idx_audit_aggregates_day ON audit_aggregates(day);

-- Workspace + action drives "show me page.write activity for
-- workspace X across all time" queries — the long-window analyses
-- aggregates are designed to serve.
CREATE INDEX idx_audit_aggregates_workspace_action
    ON audit_aggregates(workspace_slug, action);
