package audit

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// CompactorConfig tunes the retention sweep.
type CompactorConfig struct {
	// Retention is the maximum age of a raw audit_events row before
	// it is folded into audit_aggregates. Rows newer than this are
	// untouched. Default: 90 days.
	Retention time.Duration
	// Interval is how often the compactor's scheduler ticks. The
	// sweep itself is cheap when nothing's eligible. Default: 24h.
	Interval time.Duration
	// Now is injectable for tests; defaults to time.Now.
	Now func() time.Time
}

// Compactor walks audit_events rows older than Retention, groups
// them by (date, workspace_slug, action, severity), upserts the
// per-group counts into audit_aggregates, and deletes the raw rows.
//
// Satisfies: O1 (audit retention — 90d raw + per-day aggregates),
//
//	RT-10 (audit drainer + retention complete),
//	B1 (per-day aggregates preserve attribution at
//	    workspace + action granularity).
//
// Concurrency: Run is one-shot; Start spawns a goroutine that ticks
// at Interval until Stop is called. The compactor takes a single
// transaction per sweep so partial state is impossible.
type Compactor struct {
	db  *sql.DB
	cfg CompactorConfig

	mu      sync.Mutex
	cancel  context.CancelFunc
	done    chan struct{}
	running bool
}

// NewCompactor returns a Compactor with defaults applied.
func NewCompactor(db *sql.DB, cfg CompactorConfig) *Compactor {
	if cfg.Retention <= 0 {
		cfg.Retention = 90 * 24 * time.Hour
	}
	if cfg.Interval <= 0 {
		cfg.Interval = 24 * time.Hour
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Compactor{db: db, cfg: cfg}
}

// Start launches the background ticker. Calling Start twice without
// Stop in between is a no-op.
func (c *Compactor) Start(ctx context.Context) {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return
	}
	c.running = true
	ctx, c.cancel = context.WithCancel(ctx)
	c.done = make(chan struct{})
	c.mu.Unlock()

	go c.runLoop(ctx)
}

// Stop halts the ticker and waits for the current sweep (if any)
// to finish.
func (c *Compactor) Stop() {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return
	}
	c.cancel()
	doneCh := c.done
	c.running = false
	c.mu.Unlock()
	<-doneCh
}

func (c *Compactor) runLoop(ctx context.Context) {
	defer close(c.done)
	t := time.NewTicker(c.cfg.Interval)
	defer t.Stop()

	// Run an initial sweep so a freshly-started server doesn't have
	// to wait for the first tick to clear stale data.
	if rows, err := c.CompactNow(ctx); err != nil {
		slog.Warn("audit compactor: initial sweep failed",
			slog.String("component", "audit"),
			slog.String("err", err.Error()))
	} else if rows > 0 {
		slog.Info("audit compactor: initial sweep",
			slog.String("component", "audit"),
			slog.Int("rows_compacted", rows))
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rows, err := c.CompactNow(ctx)
			if err != nil {
				slog.Warn("audit compactor: tick sweep failed",
					slog.String("component", "audit"),
					slog.String("err", err.Error()))
			} else if rows > 0 {
				slog.Info("audit compactor: tick sweep",
					slog.String("component", "audit"),
					slog.Int("rows_compacted", rows))
			}
		}
	}
}

// CompactNow runs one sweep synchronously and returns the number
// of raw audit_events rows folded into audit_aggregates. Safe to
// call from tests + ad-hoc operator scripts.
//
// SQL strategy:
//  1. SELECT grouped counts from audit_events older than cutoff.
//  2. For each group, INSERT OR REPLACE into audit_aggregates with
//     SUMmed counts so a re-run on the same day's rows produces
//     the same aggregate (idempotent under retry).
//  3. DELETE raw audit_events rows older than cutoff.
//
// Wrapped in a single tx so a crash mid-sweep can't leave the
// table half-compacted.
func (c *Compactor) CompactNow(ctx context.Context) (int, error) {
	cutoff := c.cfg.Now().Add(-c.cfg.Retention).UTC().Format("2006-01-02T15:04:05.000Z")

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("audit compactor: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// 1. Aggregate older-than-cutoff rows by day + workspace + action + severity.
	//    SQLite's date(ts) extracts YYYY-MM-DD.
	rows, err := tx.QueryContext(ctx, `
		SELECT date(ts)               AS day,
		       COALESCE(workspace_slug, '') AS workspace_slug,
		       action,
		       severity,
		       COUNT(*)               AS cnt,
		       COUNT(DISTINCT principal_id)   AS distinct_principals,
		       COUNT(DISTINCT workspace_slug) AS distinct_workspaces
		  FROM audit_events
		 WHERE ts < ?
		 GROUP BY day, workspace_slug, action, severity
	`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("audit compactor: aggregate query: %w", err)
	}

	type agg struct {
		day, ws, action, severity                     string
		count, distinctPrincipals, distinctWorkspaces int
	}
	var aggs []agg
	for rows.Next() {
		var a agg
		if err := rows.Scan(&a.day, &a.ws, &a.action, &a.severity,
			&a.count, &a.distinctPrincipals, &a.distinctWorkspaces); err != nil {
			rows.Close()
			return 0, fmt.Errorf("audit compactor: scan agg: %w", err)
		}
		aggs = append(aggs, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("audit compactor: rows: %w", err)
	}

	if len(aggs) == 0 {
		// Nothing to compact; commit the empty tx so we keep the
		// "single sweep, atomic" semantic.
		if err := tx.Commit(); err != nil {
			return 0, fmt.Errorf("audit compactor: commit no-op: %w", err)
		}
		return 0, nil
	}

	// 2. Upsert into audit_aggregates. ON CONFLICT(day, workspace_slug,
	//    action, severity) means a re-sweep on the same boundary day
	//    sums into the existing row rather than failing — idempotent
	//    under retry / overlapping retention windows.
	upsert, err := tx.PrepareContext(ctx, `
		INSERT INTO audit_aggregates
			(day, workspace_slug, action, severity, count,
			 distinct_principals, distinct_workspaces)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(day, workspace_slug, action, severity)
		DO UPDATE SET
			count = audit_aggregates.count + excluded.count,
			distinct_principals = MAX(audit_aggregates.distinct_principals, excluded.distinct_principals),
			distinct_workspaces = MAX(audit_aggregates.distinct_workspaces, excluded.distinct_workspaces)
	`)
	if err != nil {
		return 0, fmt.Errorf("audit compactor: prepare upsert: %w", err)
	}
	defer upsert.Close()

	totalRaw := 0
	for _, a := range aggs {
		var ws any
		if a.ws != "" {
			ws = a.ws
		}
		if _, err := upsert.ExecContext(ctx, a.day, ws, a.action, a.severity,
			a.count, a.distinctPrincipals, a.distinctWorkspaces); err != nil {
			return 0, fmt.Errorf("audit compactor: upsert: %w", err)
		}
		totalRaw += a.count
	}

	// 3. Delete the raw rows we just folded in.
	if _, err := tx.ExecContext(ctx, `DELETE FROM audit_events WHERE ts < ?`, cutoff); err != nil {
		return 0, fmt.Errorf("audit compactor: delete raw: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("audit compactor: commit: %w", err)
	}
	return totalRaw, nil
}
