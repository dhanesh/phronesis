package app

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dhanesh/phronesis/internal/auth/oidc"
)

// testIdP is a minimal OIDC provider: discovery, JWKS, token. The token
// endpoint signs an id_token carrying the nonce of the last /auth request
// it was told about (badNonce flips it).
type testIdP struct {
	srv      *httptest.Server
	nonce    string
	badNonce bool
}

func newTestIdP(t *testing.T) *testIdP {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	signer, _ := oidc.NewRS256Signer(key, "k1")
	idp := &testIdP{}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer": idp.srv.URL, "authorization_endpoint": idp.srv.URL + "/auth",
			"token_endpoint": idp.srv.URL + "/token", "jwks_uri": idp.srv.URL + "/keys",
		})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, r *http.Request) {
		b, _ := signer.JWKSDocument()
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "phronesis" || p != "client-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		nonce := idp.nonce
		if idp.badNonce {
			nonce = "not-the-nonce"
		}
		now := time.Now().Unix()
		tok, _ := signer.Sign(map[string]any{
			"iss": idp.srv.URL, "aud": "phronesis", "sub": "Cg5hbm4tc3ViLW9wYXF1ZQ",
			"email": "ann@example.com", "name": "Ann Example", "nonce": nonce, "iat": now, "exp": now + 300,
		})
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": tok})
	})
	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	return idp
}

func newSSOServer(t *testing.T, idp *testIdP) *Server {
	t.Helper()
	dir := t.TempDir()
	srv, err := NewServer(Config{
		Addr: ":0", PagesDir: filepath.Join(dir, "pages"), FrontendDist: t.TempDir(),
		AdminUser: "admin", AdminPassword: "secret", AuditLog: filepath.Join(dir, "audit.log"),
		StorePath:   filepath.Join(dir, "phronesis.db"),
		OIDCEnabled: true, OIDCIssuer: idp.srv.URL, OIDCClientID: "phronesis",
		OIDCClientSecret: "client-secret", PublicURL: "http://wiki.test",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close(context.Background()) })
	return srv
}

func serve(s *Server, r *http.Request) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	s.HTTP().Handler.ServeHTTP(rr, r)
	return rr
}

// startSSO runs /start and returns the IdP auth URL and the state cookie.
func startSSO(t *testing.T, s *Server, idp *testIdP, next string) (*url.URL, *http.Cookie) {
	t.Helper()
	rr := serve(s, httptest.NewRequest(http.MethodGet, "/api/auth/oidc/start?next="+url.QueryEscape(next), nil))
	if rr.Code != http.StatusFound {
		t.Fatalf("start = %d %s", rr.Code, rr.Body.String())
	}
	loc, _ := url.Parse(rr.Header().Get("Location"))
	if !strings.HasPrefix(loc.String(), idp.srv.URL+"/auth?") {
		t.Fatalf("start redirected to %s", loc)
	}
	idp.nonce = loc.Query().Get("nonce")
	var c *http.Cookie
	for _, ck := range rr.Result().Cookies() {
		if ck.Name == ssoStateCookie {
			c = ck
		}
	}
	if c == nil || c.Value != loc.Query().Get("state") || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("state cookie = %+v", c)
	}
	return loc, c
}

func callback(s *Server, state string, c *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/api/auth/oidc/callback?code=abc&state="+url.QueryEscape(state), nil)
	if c != nil {
		r.AddCookie(c)
	}
	return serve(s, r)
}

func TestSSOHappyPathProjectsViewerAndRedirectsToNext(t *testing.T) {
	idp := newTestIdP(t)
	s := newSSOServer(t, idp)
	if rr := serve(s, httptest.NewRequest(http.MethodGet, "/api/session", nil)); !strings.Contains(rr.Body.String(), `"sso":true`) {
		t.Errorf("session does not advertise sso: %s", rr.Body.String())
	}
	loc, c := startSSO(t, s, idp, "/w/roadmap")
	rr := callback(s, loc.Query().Get("state"), c)
	if rr.Code != http.StatusFound || rr.Header().Get("Location") != "/w/roadmap" {
		t.Fatalf("callback = %d Location=%q", rr.Code, rr.Header().Get("Location"))
	}
	var session *http.Cookie
	for _, ck := range rr.Result().Cookies() {
		if ck.Name != ssoStateCookie && ck.Value != "" {
			session = ck
		}
	}
	if session == nil {
		t.Fatal("no session cookie")
	}
	var sess map[string]any
	_ = json.Unmarshal(do(t, s, session, http.MethodGet, "/api/session", nil).Body.Bytes(), &sess)
	if sess["authenticated"] != true || sess["role"] != "viewer" || sess["display_name"] != "Ann Example" {
		t.Errorf("session = %v", sess)
	}
	// Replaying the same state fails: flows are single-use.
	if rr := callback(s, loc.Query().Get("state"), c); !strings.Contains(rr.Header().Get("Location"), "sso_error") {
		t.Errorf("replayed state accepted: %s", rr.Header().Get("Location"))
	}
}

func TestSSOCallbackRejectsMissingStateCookie(t *testing.T) {
	idp := newTestIdP(t)
	s := newSSOServer(t, idp)
	loc, _ := startSSO(t, s, idp, "/")
	rr := callback(s, loc.Query().Get("state"), nil) // login CSRF: no browser binding
	if !strings.Contains(rr.Header().Get("Location"), "sso_error") || len(rr.Result().Cookies()) > 1 {
		t.Fatalf("callback without state cookie = %d %s", rr.Code, rr.Header().Get("Location"))
	}
}

func TestSSOCallbackRejectsNonceMismatch(t *testing.T) {
	idp := newTestIdP(t)
	s := newSSOServer(t, idp)
	loc, c := startSSO(t, s, idp, "/")
	idp.badNonce = true
	rr := callback(s, loc.Query().Get("state"), c)
	if !strings.Contains(rr.Header().Get("Location"), "sso_error") {
		t.Fatalf("nonce mismatch accepted: %s", rr.Header().Get("Location"))
	}
}

func TestSSOStartIgnoresOffsiteNext(t *testing.T) {
	idp := newTestIdP(t)
	s := newSSOServer(t, idp)
	for _, next := range []string{"//evil.example/x", "https://evil.example", "/\\evil.example"} {
		loc, c := startSSO(t, s, idp, next)
		rr := callback(s, loc.Query().Get("state"), c)
		if got := rr.Header().Get("Location"); got != "/" {
			t.Errorf("next %q -> %q, want /", next, got)
		}
	}
}
