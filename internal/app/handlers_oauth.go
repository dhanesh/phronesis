package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/dhanesh/phronesis/internal/auth/oauth"
	"github.com/dhanesh/phronesis/internal/auth/oidc"
)

// oauthHandlers groups the runtime dependencies of the OAuth endpoints
// so handler methods don't reach for a half-dozen Server fields each
// call.
//
// Satisfies: RT-2 (OAuth 2.1 + PKCE substrate), T1 (regulation —
//
//	conform to MCP authorization profile).
type oauthHandlers struct {
	store    *oauth.Store
	signer   *oidc.RS256Signer
	issuer   string // canonical iss claim — must match discovery doc
	audience string // canonical aud claim — set to issuer URL by default
	now      func() time.Time

	// Stage 3b: verifier for self-issued access tokens. Pre-seeded
	// with the signer's public key. principalFromRequest calls this
	// for any 3-segment Bearer that isn't a phr_live_ key.
	tokenVerifier *oauth.Verifier
}

// AccessTokenTTL is the lifetime of an issued access token. 1 hour is
// the OAuth-default conservative ceiling — short enough that revoke
// propagation lands before most agents' next request, long enough that
// healthy agents don't refresh on every operation.
const AccessTokenTTL = time.Hour

// handleOAuthAuthorize implements GET/POST /oauth/authorize.
//
// Required query/body params (RFC 6749 §4.1.1):
//
//	response_type=code
//	client_id           — must match a registered client
//	redirect_uri        — must exactly match a registered redirect_uri
//	code_challenge      — base64url(SHA256(code_verifier))
//	code_challenge_method=S256
//	state               — opaque, returned to caller (CSRF)
//	scope               — optional
//
// Auth gate: the request MUST carry a valid cookie session. An
// unauthenticated caller is redirected to / so the front-end can drive
// the human through password / OIDC login first. The MCP authorization
// profile expects a browser-redirect step here; we honour that by NOT
// returning a JSON 401.
func (s *Server) handleOAuthAuthorize(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeError(w, http.StatusServiceUnavailable, "oauth is not configured")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Pre-extract redirect_uri and state so we can build error redirects
	// per RFC 6749 §4.1.2.1: parameter errors that we have a redirect_uri
	// for go back to the client; pre-validation errors return JSON.
	redirectURI := r.Form.Get("redirect_uri")
	state := r.Form.Get("state")

	username, ok := s.auth.Username(r)
	if !ok {
		// Bounce through the homepage so the cookie auth flow runs and
		// the browser comes back with the same query string. Path-and-
		// query are preserved via the absolute URL.
		http.Redirect(w, r, "/?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
		return
	}

	clientID := r.Form.Get("client_id")
	client, err := s.oauth.store.Client(clientID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid client_id")
		return
	}
	if !redirectURIRegistered(client.RedirectURIs, redirectURI) {
		writeError(w, http.StatusBadRequest, "invalid redirect_uri")
		return
	}
	// Past this point we have a trusted redirect_uri, so error replies
	// can ride home on the redirect rather than as JSON.
	if rt := r.Form.Get("response_type"); rt != "code" {
		oauthErrorRedirect(w, r, redirectURI, state, "unsupported_response_type",
			"only response_type=code is supported")
		return
	}
	if m := r.Form.Get("code_challenge_method"); m != "S256" {
		oauthErrorRedirect(w, r, redirectURI, state, "invalid_request",
			"code_challenge_method must be S256")
		return
	}
	if r.Form.Get("code_challenge") == "" {
		oauthErrorRedirect(w, r, redirectURI, state, "invalid_request",
			"code_challenge is required")
		return
	}

	code, err := s.oauth.store.MintCode(oauth.AuthorizationCode{
		ClientID:            client.ID,
		UserSub:             username,
		WorkspaceID:         defaultWorkspaceID,
		RedirectURI:         redirectURI,
		Scope:               r.Form.Get("scope"),
		CodeChallenge:       r.Form.Get("code_challenge"),
		CodeChallengeMethod: "S256",
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not mint code")
		return
	}

	// Build the success redirect: <redirect_uri>?code=...&state=...
	u, err := url.Parse(redirectURI)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid redirect_uri")
		return
	}
	q := u.Query()
	q.Set("code", code.Code)
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	s.auditEnqueue("oauth.authorize", r, "", map[string]string{
		"client_id": client.ID,
		"user_sub":  username,
	})
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// handleOAuthToken implements POST /oauth/token.
//
// Supported grants:
//
//	grant_type=authorization_code (PKCE) — exchanges (code, code_verifier,
//	    client_id, redirect_uri) for (access_token, refresh_token).
//	grant_type=refresh_token              — rotates the refresh token and
//	    issues a fresh access token.
//
// Errors follow RFC 6749 §5.2 — JSON body, 400-class, with `error`
// (and optional `error_description`) fields.
func (s *Server) handleOAuthToken(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeError(w, http.StatusServiceUnavailable, "oauth is not configured")
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	switch r.Form.Get("grant_type") {
	case "authorization_code":
		s.handleOAuthTokenCode(w, r)
	case "refresh_token":
		s.handleOAuthTokenRefresh(w, r)
	default:
		oauthJSONError(w, http.StatusBadRequest, "unsupported_grant_type",
			"grant_type must be authorization_code or refresh_token")
	}
}

func (s *Server) handleOAuthTokenCode(w http.ResponseWriter, r *http.Request) {
	clientID := r.Form.Get("client_id")
	if _, err := s.oauth.store.Client(clientID); err != nil {
		oauthJSONError(w, http.StatusUnauthorized, "invalid_client", "")
		return
	}
	code := r.Form.Get("code")
	verifier := r.Form.Get("code_verifier")
	redirectURI := r.Form.Get("redirect_uri")

	authCode, err := s.oauth.store.ConsumeCode(code, clientID, redirectURI, verifier)
	switch {
	case errors.Is(err, oauth.ErrPKCEMismatch):
		oauthJSONError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	case err != nil:
		oauthJSONError(w, http.StatusBadRequest, "invalid_grant", "code is invalid or expired")
		return
	}
	s.issueTokenPair(w, r, clientID, authCode.UserSub, authCode.WorkspaceID, authCode.Scope, "code")
}

func (s *Server) handleOAuthTokenRefresh(w http.ResponseWriter, r *http.Request) {
	clientID := r.Form.Get("client_id")
	if _, err := s.oauth.store.Client(clientID); err != nil {
		oauthJSONError(w, http.StatusUnauthorized, "invalid_client", "")
		return
	}
	refresh, err := s.oauth.store.ConsumeRefreshToken(r.Form.Get("refresh_token"), clientID)
	if err != nil {
		oauthJSONError(w, http.StatusBadRequest, "invalid_grant", "refresh token is invalid or expired")
		return
	}
	s.issueTokenPair(w, r, clientID, refresh.UserSub, refresh.WorkspaceID, refresh.Scope, "refresh")
}

// issueTokenPair mints a new (access_token, refresh_token) pair and
// writes the OAuth-shaped JSON response. Centralising the body shape
// keeps both grant paths in lock-step.
func (s *Server) issueTokenPair(w http.ResponseWriter, r *http.Request,
	clientID, userSub, workspaceID, scope, grant string,
) {
	now := s.oauth.now()
	access := map[string]any{
		"iss":       s.oauth.issuer,
		"aud":       s.oauth.audience,
		"sub":       userSub,
		"client_id": clientID,
		"workspace": workspaceID,
		"scope":     scope,
		"iat":       now.Unix(),
		"exp":       now.Add(AccessTokenTTL).Unix(),
	}
	accessTok, err := s.oauth.signer.Sign(access)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not sign access token")
		return
	}
	refresh, err := s.oauth.store.MintRefreshToken(oauth.RefreshToken{
		ClientID:    clientID,
		UserSub:     userSub,
		WorkspaceID: workspaceID,
		Scope:       scope,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not mint refresh token")
		return
	}

	s.auditEnqueue("oauth.token."+grant, r, "", map[string]string{
		"client_id": clientID,
		"user_sub":  userSub,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  accessTok,
		"token_type":    "Bearer",
		"expires_in":    int(AccessTokenTTL.Seconds()),
		"refresh_token": refresh.Token,
		"scope":         scope,
	})
}

// handleOAuthRegister implements POST /oauth/register (RFC 7591).
//
// Body shape (JSON):
//
//	{ "redirect_uris": ["…"], "client_name": "...", "scope": "..." }
//
// Returns 201 with the assigned client_id and the persisted metadata.
// The MCP authorization profile mandates dynamic client registration
// for clients that do not have a static config — Claude Code is one.
func (s *Server) handleOAuthRegister(w http.ResponseWriter, r *http.Request) {
	if s.oauth == nil {
		writeError(w, http.StatusServiceUnavailable, "oauth is not configured")
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	defer r.Body.Close()

	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
		Scope        string   `json:"scope"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		oauthJSONError(w, http.StatusBadRequest, "invalid_client_metadata",
			"request body must be JSON")
		return
	}
	if len(req.RedirectURIs) == 0 {
		oauthJSONError(w, http.StatusBadRequest, "invalid_redirect_uri",
			"at least one redirect_uri is required")
		return
	}
	for _, u := range req.RedirectURIs {
		if !validRedirectURI(u) {
			oauthJSONError(w, http.StatusBadRequest, "invalid_redirect_uri",
				"redirect_uri must be an absolute URL with scheme http(s) or a fixed loopback")
			return
		}
	}

	client, err := s.oauth.store.RegisterClient(r.Context(), oauth.Client{
		Name:         req.ClientName,
		RedirectURIs: req.RedirectURIs,
		Scope:        req.Scope,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditEnqueue("oauth.register", r, "", map[string]string{
		"client_id": client.ID,
	})
	writeJSON(w, http.StatusCreated, client)
}

func redirectURIRegistered(allowed []string, candidate string) bool {
	for _, a := range allowed {
		if a == candidate {
			return true
		}
	}
	return false
}

// validRedirectURI accepts http/https URLs with non-empty host. The
// MCP profile permits loopback redirects (http://127.0.0.1 / localhost)
// for native clients, which is why we don't enforce https.
func validRedirectURI(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	if u.Host == "" {
		return false
	}
	return true
}

// oauthJSONError writes an RFC 6749 §5.2-shaped error body.
func oauthJSONError(w http.ResponseWriter, status int, code, description string) {
	body := map[string]any{"error": code}
	if description != "" {
		body["error_description"] = description
	}
	writeJSON(w, status, body)
}

// oauthErrorRedirect bounces an /authorize error back to the client's
// redirect_uri carrying error= + state= per RFC 6749 §4.1.2.1.
func oauthErrorRedirect(w http.ResponseWriter, r *http.Request, redirectURI, state, code, description string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		// Should not happen — we vet redirect_uri before this point.
		writeError(w, http.StatusBadRequest, "invalid redirect_uri")
		return
	}
	q := u.Query()
	q.Set("error", code)
	if description != "" {
		q.Set("error_description", description)
	}
	if state != "" {
		q.Set("state", state)
	}
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// stripTrailingSlash is used by the discovery handler to keep the
// issuer canonical — issuers without trailing slashes match the
// shape MCP clients expect.
func stripTrailingSlash(s string) string {
	return strings.TrimRight(s, "/")
}
