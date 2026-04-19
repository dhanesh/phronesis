// Package principal defines the canonical identity abstraction that unifies
// users, user-owned PATs, workspace service accounts, and OIDC-authenticated
// sessions behind a single type.
//
// Satisfies: RT-5, S1, B2 (TN8 propagation: principal_type field for audit),
// DRT-9 (single abstraction across four identity types).
//
// Every authorization decision in the collab-wiki feature consumes a Principal.
// The four authentication paths (password session, user PAT, service account
// token, OIDC id_token) each produce a Principal; the authorization layer does
// not need to know which path produced it.
package principal

import (
	"context"
	"errors"
)

// Type names the principal class. These strings appear in the S2 audit log
// (TN8 propagation: every audit record carries principal_type alongside
// principal_id).
type Type string

const (
	// TypeUser is a human logged in via password or OIDC. The principal id
	// is the stable user id (not email).
	TypeUser Type = "user"
	// TypeServiceAccount is a workspace-scoped non-human principal. Its API
	// keys are distinct from user PATs.
	TypeServiceAccount Type = "service_account"
)

// Role is the workspace-scoped RBAC role.
//
// Satisfies: B2 (three roles only), TN8 (service accounts take same roles).
type Role string

const (
	RoleViewer Role = "viewer"
	RoleEditor Role = "editor"
	RoleAdmin  Role = "admin"
)

// Principal is a request-scoped authenticated identity.
//
// All four authentication paths produce the same shape so the authorization
// function does not need a type switch on auth mechanism.
type Principal struct {
	Type        Type
	ID          string            // stable id within Type: user id or service-account id
	WorkspaceID string            // workspace this principal is authenticated against
	Role        Role              // role within WorkspaceID
	Claims      map[string]string // auth-path-specific extras (provider, token label, etc.)
}

// IsUser reports whether this principal is a human user.
func (p Principal) IsUser() bool { return p.Type == TypeUser }

// IsServiceAccount reports whether this principal is a non-human workspace
// service account.
func (p Principal) IsServiceAccount() bool { return p.Type == TypeServiceAccount }

// Can reports whether the principal is authorized for the given action on
// its workspace. Actions: "read", "write", "admin".
//
// Satisfies: B2 (RBAC), TN8 (same role semantics for both principal types).
func (p Principal) Can(action string) bool {
	if p.WorkspaceID == "" {
		return false
	}
	switch action {
	case "read":
		return p.Role == RoleViewer || p.Role == RoleEditor || p.Role == RoleAdmin
	case "write":
		return p.Role == RoleEditor || p.Role == RoleAdmin
	case "admin":
		return p.Role == RoleAdmin
	}
	return false
}

// ErrNotAuthenticated is returned when the request context has no principal.
var ErrNotAuthenticated = errors.New("principal: not authenticated")

// ErrWrongWorkspace is returned when an authenticated principal tries to act
// against a workspace they are not authenticated against.
var ErrWrongWorkspace = errors.New("principal: wrong workspace")

type ctxKey struct{}

// WithPrincipal returns a child context carrying p. Middleware that resolves
// authentication uses this to attach the principal to the request context.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxKey{}, p)
}

// FromContext returns the principal attached to ctx, or ErrNotAuthenticated.
func FromContext(ctx context.Context) (Principal, error) {
	p, ok := ctx.Value(ctxKey{}).(Principal)
	if !ok {
		return Principal{}, ErrNotAuthenticated
	}
	return p, nil
}

// Require returns ErrNotAuthenticated if no principal is attached to ctx, or
// ErrWrongWorkspace if the attached principal is for a different workspace.
// Otherwise returns the principal.
//
// Authorization layers use Require at the boundary of every protected route.
func Require(ctx context.Context, workspaceID string) (Principal, error) {
	p, err := FromContext(ctx)
	if err != nil {
		return Principal{}, err
	}
	if p.WorkspaceID != workspaceID {
		return Principal{}, ErrWrongWorkspace
	}
	return p, nil
}
