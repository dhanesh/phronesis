package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestMigration003WidensRolesWithoutLosingRows seeds a v2 database (users,
// api_keys, key_requests), applies 003, and checks that no row is lost to
// an ON DELETE CASCADE during the users table rebuild, that legacy 'user'
// becomes 'viewer', and that 'editor' is now accepted.
func TestMigration003WidensRolesWithoutLosingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	db, err := sql.Open("sqlite", path+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schemaVersionDDL); err != nil {
		t.Fatal(err)
	}
	all, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range all {
		if m.version >= 3 {
			break
		}
		if err := runMigration(db, m); err != nil {
			t.Fatalf("migration %d: %v", m.version, err)
		}
	}
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO users (id, oidc_sub, role) VALUES (1, 'alice', 'user'), (2, 'root', 'admin')`)
	mustExec(`INSERT INTO api_keys (id, user_id, workspace_slug, scope, label, key_prefix, key_hash) VALUES (1, 1, 'default', 'read', 'l', 'phr_live_x', x'00')`)
	mustExec(`INSERT INTO key_requests (user_id, workspace_slug, requested_scope, requested_label, decided_by_user_id, decision, resulting_key_id) VALUES (1, 'default', 'read', 'l', 2, 'approved', 1)`)

	if err := applyMigrations(db); err != nil {
		t.Fatalf("apply 003: %v", err)
	}
	count := func(q string) int {
		var n int
		if err := db.QueryRow(q).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	if n := count(`SELECT COUNT(*) FROM api_keys`); n != 1 {
		t.Errorf("api_keys rows = %d, want 1 (cascade on rebuild?)", n)
	}
	if n := count(`SELECT COUNT(*) FROM key_requests`); n != 1 {
		t.Errorf("key_requests rows = %d, want 1", n)
	}
	var role string
	_ = db.QueryRow(`SELECT role FROM users WHERE oidc_sub='alice'`).Scan(&role)
	if role != "viewer" {
		t.Errorf("legacy 'user' migrated to %q, want viewer", role)
	}
	_ = db.QueryRow(`SELECT role FROM users WHERE oidc_sub='root'`).Scan(&role)
	if role != "admin" {
		t.Errorf("admin migrated to %q", role)
	}
	mustExec(`UPDATE users SET role='editor' WHERE oidc_sub='alice'`)
	if _, err := db.Exec(`UPDATE users SET role='user' WHERE oidc_sub='alice'`); err == nil {
		t.Error("legacy role 'user' still accepted after 003")
	}
	if n := count(`SELECT COUNT(*) FROM pragma_foreign_key_check`); n != 0 {
		t.Errorf("foreign_key_check reports %d violations", n)
	}
	var fk int
	_ = db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk)
	if fk != 1 {
		t.Errorf("foreign_keys left off after migration")
	}
	// Default for new rows is viewer.
	mustExec(`INSERT INTO users (oidc_sub) VALUES ('newbie')`)
	_ = db.QueryRow(`SELECT role FROM users WHERE oidc_sub='newbie'`).Scan(&role)
	if role != "viewer" {
		t.Errorf("default role = %q, want viewer", role)
	}
}
