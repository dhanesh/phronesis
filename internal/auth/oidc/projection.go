package oidc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ProjectedUser is the local SQLite-backed projection of an OIDC
// identity. The canonical identity continues to live in the IdP (T2);
// this row holds per-user state the IdP doesn't know (preferences,
// active key count, last_seen_at) plus a snapshot of mutable claim
// fields (email, display_name, role) refreshed on every successful
// login.
//
// Satisfies: RT-5 (OIDC claim->user projection),
//
//	T2 (OIDC canonical, SQLite projection only),
//	T3 (SQLite-backed store).
type ProjectedUser struct {
	ID          int64
	OIDCSub     string
	Email       string
	DisplayName string
	Role        string
	Status      string
	CreatedAt   string
	LastSeenAt  sql.NullString
}

// ProjectClaims upserts a user record keyed by `oidc_sub`. Idempotent:
// same sub always produces the same row.id; subsequent calls update
// the mutable fields (email, display_name, role) and bump
// last_seen_at.
//
// IMPORTANT: a suspended user (status='suspended') is NOT reactivated
// by re-login. Admin must explicitly reactivate via /api/admin/users/
// {id}/reactivate. This is part of S5's revocation contract — a
// successful OIDC login on a suspended account must NOT silently
// re-grant access.
//
// Role argument MUST be one of "user" or "admin" — the schema CHECK
// constraint rejects anything else. The OIDC adapter is responsible
// for mapping raw group claims to one of these two values; this
// function does not know about IdP-specific group conventions.
//
// Returns ErrUserSuspended (with the row populated) when the existing
// row has status='suspended'. Callers MUST check this error and refuse
// to issue a session in that case.
func ProjectClaims(ctx context.Context, db *sql.DB, sub, email, displayName, role string) (*ProjectedUser, error) {
	if db == nil {
		return nil, errors.New("oidc: ProjectClaims requires a non-nil db")
	}
	if sub == "" {
		return nil, errors.New("oidc: ProjectClaims requires a non-empty sub")
	}
	if role != "user" && role != "admin" {
		return nil, fmt.Errorf("oidc: role must be 'user' or 'admin', got %q", role)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("oidc: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Try to fetch existing row to learn whether status is suspended
	// (we must NOT clobber that on update).
	var existingStatus sql.NullString
	err = tx.QueryRowContext(ctx,
		`SELECT status FROM users WHERE oidc_sub = ?`, sub,
	).Scan(&existingStatus)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// First-time projection.
		_, err = tx.ExecContext(ctx,
			`INSERT INTO users (oidc_sub, email, display_name, role, status, last_seen_at)
			 VALUES (?, ?, ?, ?, 'active', strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))`,
			sub, nullableString(email), nullableString(displayName), role,
		)
		if err != nil {
			return nil, fmt.Errorf("oidc: insert: %w", err)
		}
	case err != nil:
		return nil, fmt.Errorf("oidc: read existing: %w", err)
	default:
		// Update mutable fields. Status is preserved (S5 contract:
		// suspended stays suspended until admin reactivates).
		_, err = tx.ExecContext(ctx,
			`UPDATE users
			   SET email = ?,
			       display_name = ?,
			       role = ?,
			       last_seen_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
			 WHERE oidc_sub = ?`,
			nullableString(email), nullableString(displayName), role, sub,
		)
		if err != nil {
			return nil, fmt.Errorf("oidc: update: %w", err)
		}
	}

	row := &ProjectedUser{}
	err = tx.QueryRowContext(ctx,
		`SELECT id, oidc_sub, COALESCE(email, ''), COALESCE(display_name, ''),
		        role, status, created_at, last_seen_at
		   FROM users WHERE oidc_sub = ?`, sub,
	).Scan(&row.ID, &row.OIDCSub, &row.Email, &row.DisplayName,
		&row.Role, &row.Status, &row.CreatedAt, &row.LastSeenAt)
	if err != nil {
		return nil, fmt.Errorf("oidc: read back: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("oidc: commit: %w", err)
	}

	if row.Status == "suspended" {
		return row, ErrUserSuspended
	}
	return row, nil
}

// ErrUserSuspended is returned by ProjectClaims when the existing user
// row has status='suspended'. The error wraps a populated *ProjectedUser
// so callers can audit the attempt with the right principal id.
var ErrUserSuspended = errors.New("oidc: user is suspended")

// nullableString converts an empty string to a NULL sql.NullString —
// the schema treats email and display_name as nullable, and storing
// "" loses the nullability semantics on read.
func nullableString(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
