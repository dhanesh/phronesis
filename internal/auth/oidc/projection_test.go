package oidc

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/dhanesh/phronesis/internal/store/sqlite"
)

// newProjectionTestStore opens a fresh SQLite store with the
// user-mgmt-mcp schema applied. Returns the underlying *sql.DB-backed
// store; the schema includes the users table required by
// ProjectClaims.
func newProjectionTestStore(t *testing.T) *sqlite.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	store, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

// TestProjectClaimsIsIdempotent
//
// @constraint RT-5 — same sub + email always produce the same row id.
// Satisfies the projection idempotency evidence E11
// (claim_set_idempotent_user_projection).
func TestProjectClaimsIsIdempotent(t *testing.T) {
	store := newProjectionTestStore(t)
	ctx := context.Background()

	// First projection
	u1, err := ProjectClaims(ctx, store.DB(), "sub-aaa", "alice@example.com", "Alice", "user")
	if err != nil {
		t.Fatalf("first ProjectClaims: %v", err)
	}
	if u1.ID == 0 {
		t.Fatal("first projection returned id=0")
	}

	// Second projection with same sub + same claims
	u2, err := ProjectClaims(ctx, store.DB(), "sub-aaa", "alice@example.com", "Alice", "user")
	if err != nil {
		t.Fatalf("second ProjectClaims: %v", err)
	}
	if u2.ID != u1.ID {
		t.Fatalf("idempotency broken: id1=%d, id2=%d", u1.ID, u2.ID)
	}
}

// TestProjectClaimsUpdatesMutableFieldsOnReturn
//
// @constraint RT-5 — claim-set updates flow through to the projection
// on subsequent logins.
func TestProjectClaimsUpdatesMutableFieldsOnReturn(t *testing.T) {
	store := newProjectionTestStore(t)
	ctx := context.Background()

	_, err := ProjectClaims(ctx, store.DB(), "sub-bbb", "bob@example.com", "Bob", "user")
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	// User changed email + display name + got promoted to admin in IdP.
	updated, err := ProjectClaims(ctx, store.DB(), "sub-bbb", "robert@example.com", "Robert", "admin")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if updated.Email != "robert@example.com" {
		t.Errorf("email not updated: %q", updated.Email)
	}
	if updated.DisplayName != "Robert" {
		t.Errorf("display_name not updated: %q", updated.DisplayName)
	}
	if updated.Role != "admin" {
		t.Errorf("role not updated: %q", updated.Role)
	}
}

// TestProjectClaimsPreservesSuspendedStatus
//
// @constraint S5 — admin-suspended user must NOT be reactivated by a
// successful OIDC login. ProjectClaims returns ErrUserSuspended with
// the row populated so the caller can refuse the session and audit
// the attempt.
func TestProjectClaimsPreservesSuspendedStatus(t *testing.T) {
	store := newProjectionTestStore(t)
	ctx := context.Background()

	// Initial projection
	_, err := ProjectClaims(ctx, store.DB(), "sub-ccc", "c@example.com", "C", "user")
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	// Admin suspends the user.
	_, err = store.DB().ExecContext(ctx, `UPDATE users SET status='suspended' WHERE oidc_sub=?`, "sub-ccc")
	if err != nil {
		t.Fatalf("suspend: %v", err)
	}

	// Re-login attempts.
	row, err := ProjectClaims(ctx, store.DB(), "sub-ccc", "c@example.com", "C", "user")
	if !errors.Is(err, ErrUserSuspended) {
		t.Fatalf("expected ErrUserSuspended, got %v", err)
	}
	if row == nil {
		t.Fatal("expected populated row alongside ErrUserSuspended for audit purposes")
	}
	if row.Status != "suspended" {
		t.Errorf("expected status=suspended, got %q", row.Status)
	}
}

// TestProjectClaimsRejectsInvalidRole
//
// @constraint S3 — role tier is enforced at the boundary; only 'user'
// and 'admin' are valid. The OIDC adapter is responsible for mapping
// raw IdP group claims to one of these.
func TestProjectClaimsRejectsInvalidRole(t *testing.T) {
	store := newProjectionTestStore(t)
	ctx := context.Background()

	_, err := ProjectClaims(ctx, store.DB(), "sub-ddd", "d@example.com", "D", "superadmin")
	if err == nil {
		t.Fatal("expected error on invalid role, got nil")
	}
}

func TestProjectClaimsHandlesMissingEmailAndName(t *testing.T) {
	store := newProjectionTestStore(t)
	ctx := context.Background()

	row, err := ProjectClaims(ctx, store.DB(), "sub-eee", "", "", "user")
	if err != nil {
		t.Fatalf("ProjectClaims with empty email/name: %v", err)
	}
	if row.OIDCSub != "sub-eee" {
		t.Errorf("oidc_sub not set: %q", row.OIDCSub)
	}
	// Empty strings are stored as NULL and read back via COALESCE.
	if row.Email != "" || row.DisplayName != "" {
		t.Errorf("expected empty strings on read-back, got email=%q name=%q", row.Email, row.DisplayName)
	}
}
