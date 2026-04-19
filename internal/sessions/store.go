// Package sessions defines the session store abstraction for collab-wiki.
//
// Satisfies: RT-4.1, T8
//
// The interface is used by the auth layer (password + OIDC paths) to persist
// and retrieve session state. v1 ships an in-process MemStore default so the
// single-binary deployment mode (T8) works without external infrastructure;
// a Postgres or Redis adapter can be dropped in later via the same interface.
//
// Principal-type field anticipates RT-5 (canonical Principal abstraction) so
// the session schema does not need to change when Wave 3 introduces service
// accounts and OIDC sessions.
package sessions

import (
	"context"
	"errors"
	"time"
)

// Session is a persisted authentication session.
//
// Satisfies: S1 (principal-type captured), S3 (session fields needed for
// secure-cookie emission; cookie flags are the HTTP layer's concern).
type Session struct {
	ID            string            // opaque token; the cookie value or bearer principal id
	UserID        string            // empty for service-account principals
	WorkspaceID   string            // the workspace this session is scoped to
	PrincipalType string            // "user" or "service_account" (RT-5)
	CreatedAt     time.Time
	ExpiresAt     time.Time         // hard expiry (absolute)
	Metadata      map[string]string // free-form (e.g., oidc_provider, pat_label)
}

// ErrNotFound is returned when a lookup does not find a matching session, or
// when the session is found but has already expired.
var ErrNotFound = errors.New("sessions: not found")

// Store is the persistence interface for sessions.
//
// Implementations MUST be safe for concurrent use.
type Store interface {
	// Get returns the session for id, or ErrNotFound if it does not exist or
	// has expired. Expired sessions MAY be returned by the store, but this
	// method MUST treat them as not found.
	Get(ctx context.Context, id string) (Session, error)

	// Put stores or replaces the session keyed by s.ID.
	Put(ctx context.Context, s Session) error

	// Delete removes the session with the given id. Missing is not an error.
	Delete(ctx context.Context, id string) error

	// DeleteExpired removes sessions whose ExpiresAt <= now and returns the
	// number removed. Implementations may run this opportunistically.
	DeleteExpired(ctx context.Context, now time.Time) (int, error)
}
