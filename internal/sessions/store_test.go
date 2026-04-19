package sessions

import (
	"context"
	"errors"
	"testing"
	"time"
)

// @constraint RT-4.1 S1
// MemStore round-trips a session by ID, including the principal_type field
// that RT-5 requires for cross-principal-class authorization.
func TestMemStorePutGetRoundTrip(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()

	want := Session{
		ID:            "sess-abc",
		UserID:        "user-alice",
		WorkspaceID:   "ws-docs",
		PrincipalType: "user",
		CreatedAt:     time.Now().UTC(),
		ExpiresAt:     time.Now().Add(time.Hour).UTC(),
	}
	if err := s.Put(ctx, want); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := s.Get(ctx, want.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != want.ID || got.UserID != want.UserID || got.PrincipalType != want.PrincipalType {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, want)
	}
}

// @constraint RT-4.1
// Get must return ErrNotFound for unknown ids.
func TestMemStoreGetMissing(t *testing.T) {
	s := NewMemStore()
	_, err := s.Get(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// @constraint RT-4.1 S3
// Expired sessions must be treated as not found on Get (defense-in-depth so a
// stale cookie cannot re-authenticate even if DeleteExpired has not yet run).
func TestMemStoreExpiredTreatedAsMissing(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	expired := Session{ID: "e1", ExpiresAt: time.Now().Add(-time.Minute)}
	_ = s.Put(ctx, expired)
	if _, err := s.Get(ctx, "e1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for expired session, got %v", err)
	}
}

// @constraint RT-4.1
// DeleteExpired removes only sessions whose ExpiresAt <= now.
func TestMemStoreDeleteExpired(t *testing.T) {
	s := NewMemStore()
	ctx := context.Background()
	now := time.Now().UTC()

	_ = s.Put(ctx, Session{ID: "past", ExpiresAt: now.Add(-time.Hour)})
	_ = s.Put(ctx, Session{ID: "now", ExpiresAt: now})
	_ = s.Put(ctx, Session{ID: "future", ExpiresAt: now.Add(time.Hour)})

	removed, err := s.DeleteExpired(ctx, now)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed: got %d, want 2", removed)
	}
	if _, err := s.Get(ctx, "future"); err != nil {
		t.Errorf("future session missing after DeleteExpired: %v", err)
	}
}

// @constraint RT-4.1
// Delete on a missing session must not error.
func TestMemStoreDeleteMissing(t *testing.T) {
	s := NewMemStore()
	if err := s.Delete(context.Background(), "nope"); err != nil {
		t.Errorf("Delete missing: want nil error, got %v", err)
	}
}
