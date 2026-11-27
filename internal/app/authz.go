package app

import (
	"net/http"

	"github.com/dhanesh/phronesis/internal/principal"
	"github.com/dhanesh/phronesis/internal/wiki"
)

// authorize enforces page RBAC (decision 2026-11-27, docs/roles.md) for
// sessions and API keys alike. action is "read" or "write"; slug is the
// target workspace ("" means the default workspace).
//
// Order: authenticated (401) -> role allows action (403) -> an API key is
// pinned to its workspace (403). Cookie sessions carry the default
// workspace but may switch workspaces in the UI, so the pin applies to
// service-account principals only.
func (s *Server) authorize(w http.ResponseWriter, r *http.Request, action, slug string) (principal.Principal, bool) {
	p, err := principal.FromContext(r.Context())
	if err != nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return p, false
	}
	if !p.Can(action) {
		writeError(w, http.StatusForbidden, "role "+string(p.Role)+" cannot "+action+" pages")
		return p, false
	}
	if slug == "" {
		slug = wiki.DefaultWorkspaceSlug
	}
	if p.IsServiceAccount() && p.WorkspaceID != slug {
		writeError(w, http.StatusForbidden, "this key is not valid for workspace "+slug)
		return p, false
	}
	return p, true
}

// apiCredentialAllowed reports whether a principal may use the HTTP API.
// OAuth access tokens are issued to MCP clients and stay limited to /mcp
// (decision 2026-11-27); sessions and API keys are allowed.
func apiCredentialAllowed(p principal.Principal) bool {
	return p.Claims["auth_method"] != "oauth_jwt"
}
