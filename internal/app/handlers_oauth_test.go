package app

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/dhanesh/phronesis/internal/auth"
	"github.com/dhanesh/phronesis/internal/auth/oauth"
	"github.com/dhanesh/phronesis/internal/auth/oidc"
	"github.com/dhanesh/phronesis/internal/sessions"
)

// newOAuthTestServer builds a minimal Server wired with an oauth
// substrate but without the SQLite store / audit pipeline. Tests
// that need cookie-based auth use the returned auth.Manager helper.
func newOAuthTestServer(t *testing.T) (*Server, *auth.Manager) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	signer, err := oidc.NewRS256Signer(key, "test-key-1")
	if err != nil {
		t.Fatalf("NewRS256Signer: %v", err)
	}
	now := time.Now
	store := oauth.NewStore(now)

	m := auth.NewManager("admin", "admin123").WithStore(sessions.NewMemStore())
	s := &Server{
		auth: m,
		oauth: &oauthHandlers{
			store:    store,
			signer:   signer,
			issuer:   "https://phronesis.test",
			audience: "https://phronesis.test",
			now:      now,
		},
	}
	return s, m
}

func loginCookie(t *testing.T, m *auth.Manager) *http.Cookie {
	t.Helper()
	tok, err := m.Login("admin", "admin123")
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	return &http.Cookie{Name: auth.CookieName, Value: tok, Path: "/"}
}

func challengeFor(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// @constraint RT-2 — POST /oauth/register accepts a redirect_uri and
// assigns a client_id (RFC 7591).
func TestHandleOAuthRegisterAssignsClientID(t *testing.T) {
	s, _ := newOAuthTestServer(t)
	body := strings.NewReader(`{"redirect_uris":["http://localhost/cb"],"client_name":"claude"}`)
	req := httptest.NewRequest(http.MethodPost, "/oauth/register", body)
	rec := httptest.NewRecorder()
	s.handleOAuthRegister(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d; want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if id, _ := resp["client_id"].(string); id == "" {
		t.Error("client_id missing in response")
	}
	if m, _ := resp["token_endpoint_auth_method"].(string); m != "none" {
		t.Errorf("token_endpoint_auth_method = %q; want none", m)
	}
}

// @constraint RT-2 — register rejects missing redirect_uris with
// invalid_redirect_uri (RFC 7591 §3.2.2).
func TestHandleOAuthRegisterRejectsMissingRedirectURIs(t *testing.T) {
	s, _ := newOAuthTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	s.handleOAuthRegister(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid_redirect_uri") {
		t.Errorf("body should contain invalid_redirect_uri; got %s", rec.Body.String())
	}
}

// @constraint RT-2 — golden flow: register → authorize (redirects with
// code+state) → token (returns access_token + refresh_token).
func TestHandleOAuthGoldenFlow(t *testing.T) {
	s, m := newOAuthTestServer(t)
	cookie := loginCookie(t, m)

	client, err := s.oauth.store.RegisterClient(t.Context(), oauth.Client{
		RedirectURIs: []string{"http://localhost/cb"},
	})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}

	verifier := "claude-pkce-verifier-1234567890abcdefghij"
	authzQ := url.Values{
		"response_type":         {"code"},
		"client_id":             {client.ID},
		"redirect_uri":          {"http://localhost/cb"},
		"code_challenge":        {challengeFor(verifier)},
		"code_challenge_method": {"S256"},
		"state":                 {"abc"},
		"scope":                 {"read"},
	}
	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+authzQ.Encode(), nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.handleOAuthAuthorize(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("authorize status = %d; want 302; body=%s", rec.Code, rec.Body.String())
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if loc.Query().Get("state") != "abc" {
		t.Errorf("state = %q; want abc", loc.Query().Get("state"))
	}
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("no code in redirect; got %s", loc.String())
	}

	tokQ := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {client.ID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {"http://localhost/cb"},
	}
	tokReq := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(tokQ.Encode()))
	tokReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokRec := httptest.NewRecorder()
	s.handleOAuthToken(tokRec, tokReq)

	if tokRec.Code != http.StatusOK {
		t.Fatalf("token status = %d; want 200; body=%s", tokRec.Code, tokRec.Body.String())
	}
	var tokResp struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(tokRec.Body.Bytes(), &tokResp); err != nil {
		t.Fatalf("parse token body: %v", err)
	}
	if tokResp.TokenType != "Bearer" {
		t.Errorf("token_type = %q; want Bearer", tokResp.TokenType)
	}
	if strings.Count(tokResp.AccessToken, ".") != 2 {
		t.Errorf("access_token is not a compact JWT: %q", tokResp.AccessToken)
	}
	if tokResp.RefreshToken == "" {
		t.Error("refresh_token missing")
	}
	if tokResp.ExpiresIn != int(AccessTokenTTL.Seconds()) {
		t.Errorf("expires_in = %d; want %d", tokResp.ExpiresIn, int(AccessTokenTTL.Seconds()))
	}
}

// @constraint RT-2 — unauthenticated /authorize redirects through /
// (NOT a JSON 401 — the MCP profile expects a browser-redirect step).
func TestHandleOAuthAuthorizeRedirectsUnauthenticated(t *testing.T) {
	s, _ := newOAuthTestServer(t)
	client, _ := s.oauth.store.RegisterClient(t.Context(), oauth.Client{
		RedirectURIs: []string{"http://localhost/cb"},
	})
	authzQ := url.Values{
		"response_type": {"code"},
		"client_id":     {client.ID},
		"redirect_uri":  {"http://localhost/cb"},
	}
	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+authzQ.Encode(), nil)
	rec := httptest.NewRecorder()
	s.handleOAuthAuthorize(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d; want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, "/?next=") {
		t.Errorf("Location = %q; want /?next=...", loc)
	}
}

// @constraint RT-2 — wrong PKCE verifier gets invalid_grant.
func TestHandleOAuthTokenRejectsBadPKCE(t *testing.T) {
	s, m := newOAuthTestServer(t)
	cookie := loginCookie(t, m)
	client, _ := s.oauth.store.RegisterClient(t.Context(), oauth.Client{
		RedirectURIs: []string{"http://localhost/cb"},
	})
	authzQ := url.Values{
		"response_type":         {"code"},
		"client_id":             {client.ID},
		"redirect_uri":          {"http://localhost/cb"},
		"code_challenge":        {challengeFor("right")},
		"code_challenge_method": {"S256"},
	}
	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+authzQ.Encode(), nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.handleOAuthAuthorize(rec, req)
	loc, _ := url.Parse(rec.Header().Get("Location"))
	code := loc.Query().Get("code")

	tokQ := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {client.ID},
		"code":          {code},
		"code_verifier": {"wrong"},
		"redirect_uri":  {"http://localhost/cb"},
	}
	tokReq := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(tokQ.Encode()))
	tokReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokRec := httptest.NewRecorder()
	s.handleOAuthToken(tokRec, tokReq)
	if tokRec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d; want 400", tokRec.Code)
	}
	if !strings.Contains(tokRec.Body.String(), "invalid_grant") {
		t.Errorf("body should contain invalid_grant; got %s", tokRec.Body.String())
	}
}

// @constraint RT-2 — code_challenge_method != S256 gets bounced back
// to redirect_uri with error=invalid_request.
func TestHandleOAuthAuthorizeRejectsPlainPKCE(t *testing.T) {
	s, m := newOAuthTestServer(t)
	cookie := loginCookie(t, m)
	client, _ := s.oauth.store.RegisterClient(t.Context(), oauth.Client{
		RedirectURIs: []string{"http://localhost/cb"},
	})
	authzQ := url.Values{
		"response_type":         {"code"},
		"client_id":             {client.ID},
		"redirect_uri":          {"http://localhost/cb"},
		"code_challenge":        {"literal"},
		"code_challenge_method": {"plain"},
		"state":                 {"abc"},
	}
	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+authzQ.Encode(), nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.handleOAuthAuthorize(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d; want 302 (redirect with error)", rec.Code)
	}
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Query().Get("error") != "invalid_request" {
		t.Errorf("error = %q; want invalid_request", loc.Query().Get("error"))
	}
	if loc.Query().Get("state") != "abc" {
		t.Errorf("state = %q; want abc", loc.Query().Get("state"))
	}
}

// @constraint RT-2 — redirect_uri not in registered set is rejected
// with JSON 400 (NOT a redirect — a malicious URI would otherwise
// receive the error).
func TestHandleOAuthAuthorizeRejectsUnregisteredRedirectURI(t *testing.T) {
	s, m := newOAuthTestServer(t)
	cookie := loginCookie(t, m)
	client, _ := s.oauth.store.RegisterClient(t.Context(), oauth.Client{
		RedirectURIs: []string{"http://localhost/cb"},
	})
	authzQ := url.Values{
		"response_type":         {"code"},
		"client_id":             {client.ID},
		"redirect_uri":          {"http://attacker/cb"},
		"code_challenge":        {challengeFor("v")},
		"code_challenge_method": {"S256"},
	}
	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+authzQ.Encode(), nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.handleOAuthAuthorize(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400; Location=%s", rec.Code, rec.Header().Get("Location"))
	}
}

// @constraint RT-2 — refresh_token grant rotates: a redeemed refresh
// token cannot be redeemed again.
func TestHandleOAuthTokenRefreshRotates(t *testing.T) {
	s, m := newOAuthTestServer(t)
	cookie := loginCookie(t, m)
	client, _ := s.oauth.store.RegisterClient(t.Context(), oauth.Client{
		RedirectURIs: []string{"http://localhost/cb"},
	})
	verifier := "v"
	authzQ := url.Values{
		"response_type":         {"code"},
		"client_id":             {client.ID},
		"redirect_uri":          {"http://localhost/cb"},
		"code_challenge":        {challengeFor(verifier)},
		"code_challenge_method": {"S256"},
	}
	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+authzQ.Encode(), nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	s.handleOAuthAuthorize(rec, req)
	loc, _ := url.Parse(rec.Header().Get("Location"))
	code := loc.Query().Get("code")

	tokQ := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {client.ID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {"http://localhost/cb"},
	}
	tokReq := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(tokQ.Encode()))
	tokReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokRec := httptest.NewRecorder()
	s.handleOAuthToken(tokRec, tokReq)
	var pair struct {
		RefreshToken string `json:"refresh_token"`
	}
	_ = json.Unmarshal(tokRec.Body.Bytes(), &pair)
	if pair.RefreshToken == "" {
		t.Fatal("missing refresh_token")
	}

	rt := pair.RefreshToken
	for i := 0; i < 2; i++ {
		refQ := url.Values{
			"grant_type":    {"refresh_token"},
			"client_id":     {client.ID},
			"refresh_token": {rt},
		}
		refReq := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(refQ.Encode()))
		refReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		refRec := httptest.NewRecorder()
		s.handleOAuthToken(refRec, refReq)

		if i == 0 {
			if refRec.Code != http.StatusOK {
				t.Fatalf("first refresh status = %d; want 200; body=%s", refRec.Code, refRec.Body.String())
			}
			var next struct {
				RefreshToken string `json:"refresh_token"`
			}
			if err := json.Unmarshal(refRec.Body.Bytes(), &next); err != nil {
				t.Fatalf("parse: %v", err)
			}
			if next.RefreshToken == rt {
				t.Error("refresh token did not rotate")
			}
			rt = pair.RefreshToken // attempt to replay the original
		} else {
			if refRec.Code != http.StatusBadRequest {
				t.Errorf("replayed refresh status = %d; want 400", refRec.Code)
			}
		}
	}
}

// @constraint RT-2 — every OAuth handler returns 503 when the
// substrate is not configured (server fails closed when an operator
// forgets to flip the toggle).
func TestOAuthHandlersReturn503WhenDisabled(t *testing.T) {
	s := &Server{}
	for _, tc := range []struct {
		name string
		path string
		fn   http.HandlerFunc
	}{
		{"authorize", "/oauth/authorize", s.handleOAuthAuthorize},
		{"token", "/oauth/token", s.handleOAuthToken},
		{"register", "/oauth/register", s.handleOAuthRegister},
		{"metadata", "/.well-known/oauth-authorization-server", s.handleOAuthMetadata},
		{"jwks", "/.well-known/jwks.json", s.handleJWKS},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			tc.fn(rec, req)
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("%s status = %d; want 503", tc.name, rec.Code)
			}
		})
	}
}
