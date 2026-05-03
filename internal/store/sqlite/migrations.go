package sqlite

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// schemaVersionDDL is the bootstrap table that tracks which migrations
// have been applied. It is created by applyMigrations itself and is NOT
// a member of migrations/ — that would be a chicken-and-egg.
const schemaVersionDDL = `
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    name TEXT NOT NULL
);
`

// migration is a single forward-only step. The version is parsed from
// the filename prefix (e.g. "001_init.sql" -> version 1).
type migration struct {
	version int
	name    string
	sql     string
}

// applyMigrations reads every migrations/NNN_*.sql file (embedded), and
// applies any not yet recorded in schema_version. Each migration runs
// in its own transaction.
//
// On any failure (parse error, SQL error, duplicate version) the
// function returns an error and aborts. The caller (Open) closes the
// database and surfaces the error. Forward-only: there is no rollback.
func applyMigrations(db *sql.DB) error {
	if _, err := db.Exec(schemaVersionDDL); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}
	available, err := loadMigrations()
	if err != nil {
		return err
	}
	applied, err := loadAppliedVersions(db)
	if err != nil {
		return err
	}
	for _, m := range available {
		if _, ok := applied[m.version]; ok {
			continue
		}
		if err := runMigration(db, m); err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
	}
	return nil
}

// loadMigrations reads migrations/*.sql from the embedded FS and parses
// out the version number from the filename prefix. Returns the set
// sorted ascending by version.
//
// Naming: NNN_human_readable_name.sql. Files that don't match are an
// error — silent skip would let a typo break ordering.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	var out []migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		idx := strings.IndexByte(name, '_')
		if idx <= 0 {
			return nil, fmt.Errorf("migration file %q: expected NNN_name.sql", name)
		}
		v, err := strconv.Atoi(name[:idx])
		if err != nil {
			return nil, fmt.Errorf("migration file %q: bad version prefix: %w", name, err)
		}
		body, err := fs.ReadFile(migrationsFS, "migrations/"+name)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", name, err)
		}
		out = append(out, migration{
			version: v,
			name:    strings.TrimSuffix(name[idx+1:], ".sql"),
			sql:     string(body),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i := 1; i < len(out); i++ {
		if out[i-1].version == out[i].version {
			return nil, fmt.Errorf("duplicate migration version %d (%q and %q)",
				out[i].version, out[i-1].name, out[i].name)
		}
	}
	return out, nil
}

func loadAppliedVersions(db *sql.DB) (map[int]struct{}, error) {
	rows, err := db.Query(`SELECT version FROM schema_version`)
	if err != nil {
		return nil, fmt.Errorf("read schema_version: %w", err)
	}
	defer rows.Close()
	out := make(map[int]struct{})
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = struct{}{}
	}
	return out, rows.Err()
}

func runMigration(db *sql.DB, m migration) error {
	if strings.TrimSpace(m.sql) == "" {
		return errors.New("empty migration body")
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	if _, err := tx.Exec(m.sql); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("exec: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_version (version, name) VALUES (?, ?)`, m.version, m.name); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("record version: %w", err)
	}
	return tx.Commit()
}
