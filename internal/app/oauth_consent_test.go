package app

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/dhanesh/phronesis/internal/auth/oauth"
)

// Finding mcp-oauth (no consent step; anonymous registration of any
// redirect URI), fixed per decision 2026-11-27.

func consentFixture(t *testing.T) (*Server, *http.Cookie, url.Values) {
	t.Helper()
	s, m := newOAuthTestServer(t)
	client, err := s.oauth.store.RegisterClient(t.Context(), oauth.Client{
		Name: "Some MCP client", RedirectURIs: []string{"http://127.0.0.1:8976/callback"},
	})
	if err != nil {
		t.Fatal(err)
	}
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {client.ID},
		"redirect_uri":          {"http://127.0.0.1:8976/callback"},
		"code_challenge":        {challengeFor("verifier-verifier-verifier-verifier-1234")},
		"code_challenge_method": {"S256"},
		"state":                 {"st"},
		"scope":                 {"write"},
	}
	return s, loginCookie(t, m), q
}

func TestAuthorizeGETShowsConsentInsteadOfMintingCode(t *testing.T) {
	s, c, q := consentFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	s.handleOAuthAuthorize(rec, req)
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusFound || !strings.HasPrefix(loc, "/oauth/consent?") {
		t.Fatalf("GET authorize = %d Location=%q, want 302 to /oauth/consent", rec.Code, loc)
	}
	if strings.Contains(loc, "code=") && !strings.Contains(loc, "code_challenge") {
		t.Errorf("consent redirect carries a code: %s", loc)
	}
}

func postDecision(s *Server, c *http.Cookie, q url.Values, decision string, hdr map[string]string) *httptest.ResponseRecorder {
	q = url.Values(q)
	q.Set("decision", decision)
	req := httptest.NewRequest(http.MethodPost, "/oauth/authorize", strings.NewReader(q.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	s.handleOAuthAuthorize(rec, req)
	return rec
}

func TestAuthorizeDenyRedirectsAccessDenied(t *testing.T) {
	s, c, q := consentFixture(t)
	rec := postDecision(s, c, q, "deny", nil)
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if loc.Query().Get("error") != "access_denied" || loc.Query().Get("code") != "" {
		t.Fatalf("deny Location = %s", loc)
	}
}

func TestAuthorizeRejectsCrossOriginApprove(t *testing.T) {
	s, c, q := consentFixture(t)
	rec := postDecision(s, c, q, "approve", map[string]string{"Origin": "https://evil.example"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin approve = %d, want 403", rec.Code)
	}
	rec = postDecision(s, c, q, "approve", map[string]string{"Sec-Fetch-Site": "cross-site"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site approve = %d, want 403", rec.Code)
	}
}

func TestAuthorizeApproveMintsCode(t *testing.T) {
	s, c, q := consentFixture(t)
	rec := postDecision(s, c, q, "approve", map[string]string{"Origin": "http://example.com"})
	loc, _ := url.Parse(rec.Header().Get("Location"))
	if rec.Code != http.StatusFound || loc.Query().Get("code") == "" || loc.Query().Get("state") != "st" {
		t.Fatalf("approve = %d Location=%s", rec.Code, loc)
	}
}

func TestRegisterNonLoopbackNeedsAdmin(t *testing.T) {
	s, m := newOAuthTestServer(t)
	body := `{"redirect_uris":["https://app.example/cb"],"client_name":"x"}`
	rec := httptest.NewRecorder()
	s.handleOAuthRegister(rec, httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(body)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous non-loopback register = %d, want 403", rec.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(body))
	req.AddCookie(loginCookie(t, m))
	rec = httptest.NewRecorder()
	s.handleOAuthRegister(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin non-loopback register = %d %s, want 201", rec.Code, rec.Body.String())
	}
}

func TestGrantedScopeCappedAtUserRole(t *testing.T) {
	s := newOIDCStoreServer(t)
	c, _ := oidcLogin(t, s, "olga", "olga@example.com") // viewer
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(c)
	if got := s.grantedOAuthScope(req, "write admin"); got != "read" {
		t.Errorf("viewer granted %q, want read", got)
	}
	admin := passwordLogin(t, s)
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(admin)
	if got := s.grantedOAuthScope(req, "write"); got != "write" {
		t.Errorf("admin asking write granted %q, want write", got)
	}
}
