package app

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// oauthTokenFor runs register -> consent approve -> token for the user
// behind cookie, asking for scope, and returns the access token.
func oauthTokenFor(t *testing.T, s *Server, c *http.Cookie, scope string) string {
	t.Helper()
	redirect := "http://127.0.0.1:8976/callback"
	reg := do(t, s, nil, http.MethodPost, "/oauth/register", map[string]any{"client_name": "t", "redirect_uris": []string{redirect}})
	var client struct {
		ClientID string `json:"client_id"`
	}
	_ = json.Unmarshal(reg.Body.Bytes(), &client)
	verifier := strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	form := url.Values{
		"response_type": {"code"}, "client_id": {client.ClientID}, "redirect_uri": {redirect},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"},
		"scope": {scope}, "decision": {"approve"},
	}
	r := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(c)
	rr := httptest.NewRecorder()
	s.HTTP().Handler.ServeHTTP(rr, r)
	loc, _ := url.Parse(rr.Header().Get("Location"))
	code := loc.Query().Get("code")
	if code == "" {
		t.Fatalf("no code: %d %s", rr.Code, rr.Header().Get("Location"))
	}
	tok := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "client_id": {client.ClientID}, "redirect_uri": {redirect}}
	r = httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(tok.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rr = httptest.NewRecorder()
	s.HTTP().Handler.ServeHTTP(rr, r)
	var out struct {
		AccessToken string `json:"access_token"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &out)
	if out.AccessToken == "" {
		t.Fatalf("token: %d %s", rr.Code, rr.Body.String())
	}
	return out.AccessToken
}

// An OAuth token never outranks its user's CURRENT role: demotion and
// suspension apply to tokens already issued (docs/roles.md principle 2).
func TestOAuthTokenFollowsUsersCurrentRole(t *testing.T) {
	s := newMCPServer(t)
	admin := passwordLogin(t, s)
	user, _ := oidcLogin(t, s, "otto", "otto@example.com")
	setRole(t, s, admin, "otto", "editor")
	tok := oauthTokenFor(t, s, user, "write")

	if _, e := mcpCall(t, s, tok, "write_page", map[string]any{"name": "o", "content": "v1"}); e != "" {
		t.Fatalf("editor token write: %s", e)
	}
	setRole(t, s, admin, "otto", "viewer")
	if _, e := mcpCall(t, s, tok, "write_page", map[string]any{"name": "o", "content": "v2"}); !strings.Contains(e, "forbidden") {
		t.Errorf("token after demotion: error %q, want forbidden", e)
	}
	if _, e := mcpCall(t, s, tok, "read_page", map[string]any{"name": "o"}); e != "" {
		t.Errorf("token after demotion can still read: %s", e)
	}
	var id int64
	_ = s.store.DB().QueryRow(`SELECT id FROM users WHERE oidc_sub='otto'`).Scan(&id)
	do(t, s, admin, http.MethodPost, "/api/admin/users/"+itoa(id)+"/suspend", nil)
	rr := doBearer(t, s, tok, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("token of suspended user = %d, want 401", rr.Code)
	}
	// The password admin (no users row) keeps its token working.
	atok := oauthTokenFor(t, s, admin, "write")
	if _, e := mcpCall(t, s, atok, "write_page", map[string]any{"name": "a", "content": "x"}); e != "" {
		t.Errorf("admin token: %s", e)
	}
}
