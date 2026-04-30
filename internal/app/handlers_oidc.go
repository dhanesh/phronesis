package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/dhanesh/phronesis/internal/audit"
	"github.com/dhanesh/phronesis/internal/auth"
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
	if s.oidc == nil {
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
	p, err := s.oidc.Authenticate(r.Context(), req.IDToken)
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

	// Issue a session using the same store-backed Login path as password auth.
	// We directly persist a session record keyed by a random token so the
	// Principal's id becomes the session's username.
	token, err := issueOIDCSession(s.sessionStore, p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session issue failed")
		return
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
	writeJSON(w, http.StatusOK, map[string]any{"username": p.ID})
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
