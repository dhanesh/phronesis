package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/dhanesh/phronesis/internal/audit"
	"github.com/dhanesh/phronesis/internal/auth"
	"github.com/dhanesh/phronesis/internal/auth/oidc"
	"github.com/dhanesh/phronesis/internal/principal"
	"github.com/dhanesh/phronesis/internal/sessions"
)

// handleOIDCLogin is the token-first OIDC login handler (INT-8).
//
// Request:  POST /api/auth/oidc/login  Body: {"id_token": "..."}
// Success: 200 + session cookie + {"username": "<principal-id>"}
// Failure: 401 with a sanitized error message (never echoes the token back).
//
// Satisfies: RT-11, S4, S8, TN5
func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil && s.sso == nil {
		http.Error(w, "oidc not enabled", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var req struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.IDToken == "" {
		writeError(w, http.StatusBadRequest, "invalid oidc login request")
		return
	}
	adapter, err := s.oidcAdapterFor(r.Context())
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "oidc provider unavailable")
		return
	}
	p, err := adapter.Authenticate(r.Context(), req.IDToken)
	if err != nil {
		// Audit the failure but do not leak error details to the client.
		s.recordAudit(audit.Event{
			At:       time.Now().UTC(),
			Action:   "auth.oidc_failed",
			Metadata: map[string]string{"reason": "invalid id_token"},
		})
		writeError(w, http.StatusUnauthorized, "invalid id_token")
		return
	}

	if !s.establishOIDCSession(w, r, p) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"username": p.ID})
}

// establishOIDCSession projects an authenticated OIDC principal into the
// users table, refuses suspended accounts, and sets the session cookie.
// It writes the error response itself and returns false on failure.
func (s *Server) establishOIDCSession(w http.ResponseWriter, r *http.Request, p principal.Principal) bool {
	// Project the user into the SQLite store (user-mgmt-mcp T2). A new
	// user is role 'viewer'; an existing row keeps
	// the role an admin gave it. A suspended account gets no session.
	if s.store != nil {
		if err := s.projectOIDCUser(r.Context(), p); err != nil {
			if errors.Is(err, oidc.ErrUserSuspended) {
				s.recordAudit(audit.Event{
					At:          time.Now().UTC(),
					Action:      "auth.oidc_suspended",
					PrincipalID: p.ID,
				})
				writeError(w, http.StatusForbidden, "account suspended")
				return false
			}
			writeError(w, http.StatusInternalServerError, "user projection failed")
			return false
		}
	}

	// Issue a session using the same store-backed Login path as password auth.
	// We directly persist a session record keyed by a random token so the
	// Principal's id becomes the session's username.
	token, err := issueOIDCSession(s.sessionStore, p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session issue failed")
		return false
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
	s.recordAudit(audit.Event{
		At:            time.Now().UTC(),
		Action:        "auth.oidc_login",
		PrincipalID:   p.ID,
		PrincipalType: string(p.Type),
		WorkspaceID:   p.WorkspaceID,
	})
	return true
}

// projectOIDCUser upserts the users row for an authenticated OIDC
// principal, preserving any role already stored for it.
func (s *Server) projectOIDCUser(ctx context.Context, p principal.Principal) error {
	role := string(principal.RoleViewer)
	var existing string
	err := s.store.DB().QueryRowContext(ctx, `SELECT role FROM users WHERE oidc_sub = ?`, p.ID).Scan(&existing)
	switch {
	case err == nil:
		role = existing
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	_, err = oidc.ProjectClaims(ctx, s.store.DB(), p.ID, p.Claims["email"], p.Claims["display_name"], role)
	return err
}

// oidcUserRole reads the projected role for an OIDC session. ok is false
// when the user row is missing or not active, which revokes the session.
func (s *Server) oidcUserRole(ctx context.Context, sub string) (principal.Role, bool) {
	var role, status string
	err := s.store.DB().QueryRowContext(ctx,
		`SELECT role, status FROM users WHERE oidc_sub = ?`, sub).Scan(&role, &status)
	if err != nil || status != "active" {
		return "", false
	}
	return principal.ParseRole(role), true
}

// issueOIDCSession creates a cookie-session record in the supplied store for
// the given principal. Kept as a package-private helper to avoid coupling
// Server internals to auth.Manager's password-specific login path.
func issueOIDCSession(store sessions.Store, p principal.Principal) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	err := store.Put(context.Background(), sessions.Session{
		ID:            token,
		UserID:        p.ID,
		WorkspaceID:   p.WorkspaceID,
		PrincipalType: string(p.Type),
		CreatedAt:     time.Now().UTC(),
		ExpiresAt:     time.Now().Add(24 * time.Hour).UTC(),
		Metadata:      map[string]string{"auth_method": "oidc"},
	})
	return token, err
}
