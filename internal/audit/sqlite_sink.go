package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
)

// SQLiteSink implements Sink by inserting rows into the audit_events
// table provided by internal/store/sqlite migration 001_init.sql.
//
// Satisfies: RT-10 (audit drainer + retention substrate — the SQLite
//
//	sink replaces / supplements the JSONL FileSink),
//	B1 (per-call audit grain, attributable to named principal),
//	O1 (audit_events is the canonical raw table — retention
//	    + compactor land alongside in a follow-up pass).
//
// Concurrency: SQLite serialises writes; this sink uses a single Tx
// per Write call to batch-insert with one fsync. Safe for concurrent
// callers because *sql.DB internally pools connections.
type SQLiteSink struct {
	db *sql.DB

	mu     sync.Mutex
	closed bool
}

// NewSQLiteSink returns a Sink writing to the audit_events table on
// the given *sql.DB. The caller retains ownership of db; Sink.Close
// is a no-op for the underlying handle.
func NewSQLiteSink(db *sql.DB) *SQLiteSink {
	return &SQLiteSink{db: db}
}

// principalTypeForSchema maps the canonical principal.Type values
// (`user`, `service_account`, plus the synthetic `break-glass`) to
// the audit_events.principal_type CHECK constraint values
// (`user`, `service`, `break-glass`).
//
// The schema uses the shorter `service` form to keep indexed columns
// small. Translation happens at the storage boundary so callers can
// continue to use the canonical principal.Type strings.
func principalTypeForSchema(t string) string {
	switch t {
	case "service_account":
		return "service"
	case "":
		// An unauthenticated event (shouldn't normally reach the
		// sink — see auditMiddleware's principal guard) is recorded
		// as `user` to satisfy the CHECK constraint without losing
		// the row entirely. The PrincipalID will be empty so it
		// remains distinguishable in audit views.
		return "user"
	case "user", "service", "break-glass":
		return t
	default:
		// Unknown type — best-effort fallback to `user` rather than
		// failing the whole batch insert. Future principal types
		// SHOULD be added to this mapping AND to the schema CHECK
		// via a forward-only migration.
		return "user"
	}
}

// Write inserts events as a single transaction. A failed batch
// returns the error; the drainer's drop-counter pattern ensures
// hot-path enqueue isn't impacted.
//
// Each event's Metadata map is JSON-encoded into the body column.
// ResourceID maps to target. At is stored as RFC3339Nano UTC.
func (s *SQLiteSink) Write(ctx context.Context, events []Event) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("audit: SQLiteSink closed")
	}
	s.mu.Unlock()

	if len(events) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("audit: begin tx: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO audit_events
			(ts, workspace_slug, principal_type, principal_id, action, target, severity, body)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("audit: prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, e := range events {
		ts := e.At.UTC().Format("2006-01-02T15:04:05.000Z")
		ptype := principalTypeForSchema(e.PrincipalType)
		var body any
		if len(e.Metadata) > 0 {
			b, err := json.Marshal(e.Metadata)
			if err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("audit: marshal metadata for action %q: %w", e.Action, err)
			}
			body = string(b)
		}
		var workspace any
		if e.WorkspaceID != "" {
			workspace = e.WorkspaceID
		}
		var target any
		if e.ResourceID != "" {
			target = e.ResourceID
		}
		// Severity is currently always "info" via this sink. The
		// break-glass admin path emits via a slog.Warn (severity
		// communicated out-of-band) until Stage 2 retrofits it to
		// emit a structured audit row with severity="high".
		if _, err := stmt.ExecContext(ctx, ts, workspace, ptype, e.PrincipalID,
			e.Action, target, "info", body); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("audit: insert event %q: %w", e.Action, err)
		}
	}
	return tx.Commit()
}

// Close marks the sink closed; subsequent Write calls return an
// error. The underlying *sql.DB is NOT closed (the caller owns it).
func (s *SQLiteSink) Close(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}
