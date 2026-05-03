package app

import (
	"net/http"
)

// handleOAuthMetadata serves /.well-known/oauth-authorization-server.
//
// Shape per RFC 8414 + the MCP authorization profile additions:
//
//	{
//	  "issuer": "https://phronesis.local",
//	  "authorization_endpoint": "https://phronesis.local/oauth/authorize",
//	  "token_endpoint": "https://phronesis.local/oauth/token",
//	  "registration_endpoint": "https://phronesis.local/oauth/register",
//	  "jwks_uri": "https://phronesis.local/.well-known/jwks.json",
//	  "response_types_supported": ["code"],
//	  "grant_types_supported": ["authorization_code", "refresh_token"],
//	  "code_challenge_methods_supported": ["S256"],
//	  "token_endpoint_auth_methods_supported": ["none"],
//	  "scopes_supported": ["read", "write"]
//	}
//
// Satisfies: RT-2 (OAuth 2.1 spec compliance), T1 (regulation —
//
//	MCP authorization profile mandates discovery).
func (s *Server) handleOAuthMetadata(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeError(w, http.StatusServiceUnavailable, "oauth is not configured")
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	iss := stripTrailingSlash(s.oauth.issuer)
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                iss,
		"authorization_endpoint":                iss + "/oauth/authorize",
		"token_endpoint":                        iss + "/oauth/token",
		"registration_endpoint":                 iss + "/oauth/register",
		"jwks_uri":                              iss + "/.well-known/jwks.json",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{"read", "write"},
	})
}

// handleJWKS serves /.well-known/jwks.json — the public side of the
// signing key. MCP clients (and any verifier built on the existing
// internal/auth/oidc.RS256Verifier) can fetch this URL and validate
// access tokens issued by this phronesis instance without a shared
// secret.
//
// Satisfies: RT-2 (resource-server self-introspection),
//
//	S2 — only public-key material crosses the boundary; no
//	     private-key bytes ever leak through this handler.
func (s *Server) handleJWKS(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil || s.oauth.signer == nil {
		writeError(w, http.StatusServiceUnavailable, "oauth is not configured")
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	doc, err := s.oauth.signer.JWKSDocument()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(doc)
}
