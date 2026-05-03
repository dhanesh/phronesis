package audit

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/dhanesh/phronesis/internal/store/sqlite"
)

// newCompactorTestDB opens a fresh SQLite store with both
// migrations applied (audit_events + audit_aggregates tables).
func newCompactorTestDB(t *testing.T) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "compactor.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// seedAuditEvents inserts n raw rows with the given timestamp. Used
// to set up "old" rows the compactor should fold + delete.
func seedAuditEvents(t *testing.T, store *sqlite.Store, ts time.Time, action, ws string, n int) {
	t.Helper()
	stmt, err := store.DB().Prepare(`
		INSERT INTO audit_events (ts, workspace_slug, principal_type, principal_id, action, severity)
		VALUES (?, ?, 'user', ?, ?, 'info')
	`)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	defer stmt.Close()
	tsStr := ts.UTC().Format("2006-01-02T15:04:05.000Z")
	for i := 0; i < n; i++ {
		pid := "user-" + itoa(int64(i%5)) // 5 distinct principals
		if _, err := stmt.Exec(tsStr, ws, pid, action); err != nil {
			t.Fatalf("seed insert: %v", err)
		}
	}
}

// itoa is a small helper local to compactor_test (the audit pkg
// doesn't otherwise need it).
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	b := []byte{}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// @constraint O1 — raw audit rows older than retention are folded
// into audit_aggregates and deleted.
// Satisfies RT-10 (retention complete).
func TestCompactorFoldsOldRowsAndDeletesRaw(t *testing.T) {
	store := newCompactorTestDB(t)

	// Fixed "now" at 2026-06-01T00:00:00Z. Old rows: 100 days back.
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	old := now.Add(-100 * 24 * time.Hour)  // > 90 days
	young := now.Add(-30 * 24 * time.Hour) // < 90 days; should NOT be touched

	seedAuditEvents(t, store, old, "page.write", "default", 10)
	seedAuditEvents(t, store, old, "page.read", "default", 5)
	seedAuditEvents(t, store, young, "page.write", "default", 7) // stays raw

	c := NewCompactor(store.DB(), CompactorConfig{
		Retention: 90 * 24 * time.Hour,
		Now:       func() time.Time { return now },
	})
	rowsCompacted, err := c.CompactNow(context.Background())
	if err != nil {
		t.Fatalf("CompactNow: %v", err)
	}
	if rowsCompacted != 15 {
		t.Errorf("expected 15 raw rows compacted (10+5), got %d", rowsCompacted)
	}

	// Raw table now has only the young rows.
	var rawCount int
	_ = store.DB().QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&rawCount)
	if rawCount != 7 {
		t.Errorf("expected 7 raw rows remaining (young), got %d", rawCount)
	}

	// Aggregate table has two rows (page.write + page.read for that day).
	var aggCount int
	_ = store.DB().QueryRow(`SELECT COUNT(*) FROM audit_aggregates`).Scan(&aggCount)
	if aggCount != 2 {
		t.Errorf("expected 2 aggregate rows, got %d", aggCount)
	}

	// Verify counts on the aggregate row for page.write.
	var n int
	err = store.DB().QueryRow(
		`SELECT count FROM audit_aggregates WHERE action = ? AND workspace_slug = ?`,
		"page.write", "default",
	).Scan(&n)
	if err != nil {
		t.Fatalf("read agg: %v", err)
	}
	if n != 10 {
		t.Errorf("page.write aggregate count = %d, want 10", n)
	}
}

// @constraint O1 — re-running CompactNow on a window that overlaps
// existing aggregates SUMs into the row rather than failing the
// UNIQUE constraint. Idempotent under retry.
func TestCompactorIsIdempotentUnderRetry(t *testing.T) {
	store := newCompactorTestDB(t)

	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	old := now.Add(-100 * 24 * time.Hour)

	seedAuditEvents(t, store, old, "page.write", "default", 5)
	c := NewCompactor(store.DB(), CompactorConfig{
		Retention: 90 * 24 * time.Hour,
		Now:       func() time.Time { return now },
	})
	if _, err := c.CompactNow(context.Background()); err != nil {
		t.Fatalf("first sweep: %v", err)
	}

	// Seed the SAME old day with more rows; second sweep should
	// fold them in via UPSERT (count = 5 + 3 = 8).
	seedAuditEvents(t, store, old, "page.write", "default", 3)
	if _, err := c.CompactNow(context.Background()); err != nil {
		t.Fatalf("second sweep: %v", err)
	}

	var n int
	_ = store.DB().QueryRow(
		`SELECT count FROM audit_aggregates WHERE action = ? AND workspace_slug = ?`,
		"page.write", "default",
	).Scan(&n)
	if n != 8 {
		t.Errorf("expected count=8 after second sweep, got %d", n)
	}
	var aggCount int
	_ = store.DB().QueryRow(`SELECT COUNT(*) FROM audit_aggregates`).Scan(&aggCount)
	if aggCount != 1 {
		t.Errorf("expected 1 aggregate row (no duplicate), got %d", aggCount)
	}
}

func TestCompactorEmptyDatabaseIsNoop(t *testing.T) {
	store := newCompactorTestDB(t)
	c := NewCompactor(store.DB(), CompactorConfig{})
	rows, err := c.CompactNow(context.Background())
	if err != nil {
		t.Fatalf("CompactNow on empty db: %v", err)
	}
	if rows != 0 {
		t.Errorf("expected 0 rows compacted, got %d", rows)
	}
}

// @constraint O1 — distinct-principals count survives compaction.
func TestCompactorPreservesDistinctCounts(t *testing.T) {
	store := newCompactorTestDB(t)

	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	old := now.Add(-100 * 24 * time.Hour)
	// seedAuditEvents uses 5 distinct principals.
	seedAuditEvents(t, store, old, "page.write", "default", 20)

	c := NewCompactor(store.DB(), CompactorConfig{
		Retention: 90 * 24 * time.Hour,
		Now:       func() time.Time { return now },
	})
	if _, err := c.CompactNow(context.Background()); err != nil {
		t.Fatalf("CompactNow: %v", err)
	}
	var distinctPrincipals int
	_ = store.DB().QueryRow(
		`SELECT distinct_principals FROM audit_aggregates`,
	).Scan(&distinctPrincipals)
	if distinctPrincipals != 5 {
		t.Errorf("expected 5 distinct principals, got %d", distinctPrincipals)
	}
}

func TestCompactorStartStopIsClean(t *testing.T) {
	store := newCompactorTestDB(t)
	c := NewCompactor(store.DB(), CompactorConfig{
		Retention: time.Hour,
		Interval:  10 * time.Millisecond, // fast tick for the test
	})
	c.Start(context.Background())
	// Double-Start is a no-op.
	c.Start(context.Background())
	time.Sleep(30 * time.Millisecond) // let at least one tick fire
	c.Stop()
	// Double-Stop is a no-op.
	c.Stop()
}
