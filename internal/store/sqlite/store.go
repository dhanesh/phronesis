// Package sqlite implements the embedded SQLite-backed projection store
// for users, API keys, key requests, and (Stage 2+) audit events.
//
// Satisfies: RT-8 (SQLite-backed store with forward-only schema migrations),
//
//	T3 (embedded pure-Go SQLite driver),
//	O3 (forward-only migrations; binary refuses to start on
//	    migration failure).
//
// Design:
//   - One DB file (default `data/phronesis.db`) per instance. SQLite
//     serializes writes; readers run in parallel under WAL.
//   - Driver: modernc.org/sqlite (pure Go). Avoids the cgo headache the
//     dist-packaging manifold worked hard to avoid (T1: deterministic
//     builds across darwin-arm64, darwin-amd64, linux-amd64,
//     linux-arm64).
//   - Migrations live as embedded SQL files (migrations/NNN_name.sql).
//     Forward-only: there is no "down". A binary that cannot complete
//     its migrations refuses to bind a port.
//   - The `schema_version` table is the source of truth for "what's
//     applied". Migration is idempotent: re-running with no pending
//     files is a no-op.
package sqlite

import (
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite" // register the pure-Go SQLite driver
)

// Store wraps a *sql.DB whose schema is fully migrated.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) a SQLite database file at path, runs all
// pending migrations, and returns a ready-to-use Store.
//
// On any migration failure, the database handle is closed and an error
// is returned WITHOUT bringing the Store online. The caller (server
// startup) is expected to log the error and exit non-zero — this
// honours O3's "binary refuses to start on migration failure"
// invariant.
//
// The DSN sets:
//   - _journal_mode=WAL: separate readers from writers
//   - _foreign_keys=on:  enforce FK constraints (SQLite default is OFF)
//   - _busy_timeout=5000: 5s SQLITE_BUSY backoff before returning error
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("sqlite: path is required")
	}
	dsn := path + "?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open %q: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: ping %q: %w", path, err)
	}
	if err := applyMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite: migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// DB returns the underlying *sql.DB for callers that need to issue
// queries directly. The Store retains ownership; do not Close the
// returned handle.
func (s *Store) DB() *sql.DB { return s.db }

// Close releases the database handle. Safe to call multiple times; the
// second call is a no-op.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}
