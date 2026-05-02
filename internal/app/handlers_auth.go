package app

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/dhanesh/phronesis/internal/audit"
	"github.com/dhanesh/phronesis/internal/auth"
	"github.com/dhanesh/phronesis/internal/principal"
)

// requestIsSecure reports whether the cookie attached to this response is
// being delivered over a connection that justifies the Secure attribute.
// True for direct TLS terminations and for reverse-proxy hops that set
// X-Forwarded-Proto: https (the common pattern behind nginx/Caddy/Cloudflare).
func requestIsSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	username, ok := s.auth.Username(r)
	role := ""
	if p, err := principal.FromContext(r.Context()); err == nil {
		role = string(p.Role)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": ok,
		"username":      username,
		"role":          role,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid login request")
		return
	}
	token, err := s.auth.Login(req.Username, req.Password)
	if err != nil {
		// INT-5: record failed login attempts. Useful for S2 audit and for
		// future RT-10 rate-limit floor (attacker-visibility via audit).
		s.recordAudit(audit.Event{
			At:       time.Now().UTC(),
			Action:   "auth.login_failed",
			Metadata: map[string]string{"username": req.Username, "reason": err.Error()},
		})
		writeError(w, http.StatusUnauthorized, err.Error())
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
	// INT-5: record successful login. Principal not yet attached to ctx
	// (login is pre-auth), so we record the resolved username directly.
	s.recordAudit(audit.Event{
		At:            time.Now().UTC(),
		Action:        "auth.login",
		PrincipalID:   req.Username,
		PrincipalType: string(principal.TypeUser),
		WorkspaceID:   defaultWorkspaceID,
	})
	writeJSON(w, http.StatusOK, map[string]any{"username": req.Username})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	cookie, err := r.Cookie(auth.CookieName)
	if err == nil {
		s.auth.Logout(cookie.Value)
	}
	// INT-5: logout event (Principal still attached to ctx via attachPrincipal).
	s.auditEnqueue("auth.logout", r, "", nil)
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// principalFromRequest resolves the request's credentials to a Principal.
// Returns (Principal{}, false) if no valid credential is present.
//
// Satisfies: RT-5 (canonical Principal over multiple auth planes), INT-2
//
// V1 resolution order:
//  1. Cookie session from auth.Manager  -> user principal (admin of default workspace)
//  2. API-KEY header (if PHRONESIS_API_KEY configured) -> service_account principal (editor)
//
// Both paths converge on principal.Principal, so downstream authz is identical
// regardless of auth mechanism.
func (s *Server) principalFromRequest(r *http.Request) (principal.Principal, bool) {
	if resolved, ok := s.auth.Resolve(r); ok {
		// Prefer the store-backed session fields (OIDC sets PrincipalType +
		// WorkspaceID + auth_method=oidc metadata) and fall back to defaults
		// for the legacy in-memory path which only carries username.
		// Satisfies: I1 review fix, RT-5 (correct auth_method preserved in audit).
		ptype := principal.Type(resolved.PrincipalType)
		if ptype == "" {
			ptype = principal.TypeUser
		}
		id := resolved.UserID
		if id == "" {
			id = resolved.Username
		}
		wsID := resolved.WorkspaceID
		if wsID == "" {
			wsID = defaultWorkspaceID
		}
		claims := map[string]string{"auth_method": "password"}
		if resolved.Metadata != nil {
			if am, ok := resolved.Metadata["auth_method"]; ok && am != "" {
				claims["auth_method"] = am
			}
		}
		return principal.Principal{
			Type:        ptype,
			ID:          id,
			WorkspaceID: wsID,
			Role:        principal.RoleAdmin,
			Claims:      claims,
		}, true
	}
	if s.cfg.APIKey != "" {
		if key := r.Header.Get("API-KEY"); key != "" && key == s.cfg.APIKey {
			return principal.Principal{
				Type:        principal.TypeServiceAccount,
				ID:          "default-api-key",
				WorkspaceID: defaultWorkspaceID,
				Role:        principal.RoleEditor,
				Claims:      map[string]string{"auth_method": "api_key"},
			}, true
		}
	}
	return principal.Principal{}, false
}
