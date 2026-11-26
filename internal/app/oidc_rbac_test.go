package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/dhanesh/phronesis/internal/auth/oidc"
)

// Decision 2026-11-27 (docs/decisions.md): OIDC users are projected into
// users on login; a new user is role viewer.
// An admin-set role survives later logins. Roles come from the row on
// every request, so suspension takes effect on existing sessions.

const rbacSecret = "stub-secret-at-least-32-bytes-long!!"

func newOIDCStoreServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	srv, err := NewServer(Config{
		Addr:          ":0",
		PagesDir:      filepath.Join(dir, "pages"),
		FrontendDist:  t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
		AuditLog:      filepath.Join(dir, "audit.log"),
		StorePath:     filepath.Join(dir, "phronesis.db"),
		OIDCEnabled:   true,
		OIDCIssuer:    "https://idp.test",
		OIDCAudience:  "phronesis",
		OIDCSecret:    rbacSecret,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close(context.Background()) })
	return srv
}

func oidcLogin(t *testing.T, s *Server, sub, email string) (*http.Cookie, int) {
	t.Helper()
	p := oidc.NewStubProvider("https://idp.test", "phronesis", []byte(rbacSecret))
	tok, err := p.Issue(oidc.Claims{Subject: sub, Email: email, Name: sub, TTL: time.Hour})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	body, _ := json.Marshal(map[string]string{"id_token": tok})
	rr := httptest.NewRecorder()
	s.HTTP().Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/auth/oidc/login", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		return nil, rr.Code
	}
	return rr.Result().Cookies()[0], rr.Code
}

func passwordLogin(t *testing.T, s *Server) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "secret"})
	rr := httptest.NewRecorder()
	s.HTTP().Handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("password login: %d %s", rr.Code, rr.Body.String())
	}
	return rr.Result().Cookies()[0]
}

func do(t *testing.T, s *Server, c *http.Cookie, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != nil {
		b, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if c != nil {
		r.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	s.HTTP().Handler.ServeHTTP(rr, r)
	return rr
}

func sessionRole(t *testing.T, s *Server, c *http.Cookie) string {
	t.Helper()
	var out struct {
		Role string `json:"role"`
	}
	_ = json.Unmarshal(do(t, s, c, http.MethodGet, "/api/session", nil).Body.Bytes(), &out)
	return out.Role
}

func TestOIDCLoginProjectsUserAsViewer(t *testing.T) {
	s := newOIDCStoreServer(t)
	c, code := oidcLogin(t, s, "alice", "alice@example.com")
	if code != http.StatusOK {
		t.Fatalf("login: %d", code)
	}
	var role, status, email string
	if err := s.store.DB().QueryRow(`SELECT role, status, COALESCE(email,'') FROM users WHERE oidc_sub='alice'`).
		Scan(&role, &status, &email); err != nil {
		t.Fatalf("user not projected: %v", err)
	}
	if role != "viewer" || status != "active" || email != "alice@example.com" {
		t.Errorf("row = %s/%s/%s, want user/active/alice@example.com", role, status, email)
	}
	if got := sessionRole(t, s, c); got != "viewer" {
		t.Errorf("session role = %q, want viewer", got)
	}
	if rr := do(t, s, c, http.MethodPost, "/api/admin/workspaces", map[string]string{"slug": "x", "name": "x"}); rr.Code != http.StatusForbidden {
		t.Errorf("viewer admin POST = %d, want 403", rr.Code)
	}
}

func TestOIDCPromotedRoleSurvivesRelogin(t *testing.T) {
	s := newOIDCStoreServer(t)
	if _, code := oidcLogin(t, s, "bob", "bob@example.com"); code != http.StatusOK {
		t.Fatalf("login: %d", code)
	}
	if _, err := s.store.DB().Exec(`UPDATE users SET role='admin' WHERE oidc_sub='bob'`); err != nil {
		t.Fatal(err)
	}
	c, _ := oidcLogin(t, s, "bob", "bob@example.com")
	if got := sessionRole(t, s, c); got != "admin" {
		t.Errorf("promoted session role = %q, want admin", got)
	}
}

func TestOIDCSuspendedUserLosesAccess(t *testing.T) {
	s := newOIDCStoreServer(t)
	c, _ := oidcLogin(t, s, "carol", "carol@example.com")
	if _, err := s.store.DB().Exec(`UPDATE users SET status='suspended' WHERE oidc_sub='carol'`); err != nil {
		t.Fatal(err)
	}
	if rr := do(t, s, c, http.MethodGet, "/api/pages", nil); rr.Code != http.StatusUnauthorized {
		t.Errorf("suspended existing session /api/pages = %d, want 401", rr.Code)
	}
	if _, code := oidcLogin(t, s, "carol", "carol@example.com"); code != http.StatusForbidden {
		t.Errorf("suspended re-login = %d, want 403", code)
	}
}

func TestPasswordAdminStaysAdmin(t *testing.T) {
	s := newOIDCStoreServer(t)
	if got := sessionRole(t, s, passwordLogin(t, s)); got != "admin" {
		t.Errorf("password admin role = %q, want admin", got)
	}
}

func TestKeyRequestCreateListApprove(t *testing.T) {
	s := newOIDCStoreServer(t)
	user, _ := oidcLogin(t, s, "dave", "dave@example.com")
	admin := passwordLogin(t, s)

	if rr := do(t, s, user, http.MethodPost, "/api/keys/requests", map[string]string{"scope": "superuser", "label": "x"}); rr.Code != http.StatusBadRequest {
		t.Errorf("bad scope = %d, want 400", rr.Code)
	}
	if rr := do(t, s, user, http.MethodPost, "/api/keys/requests", map[string]string{"scope": "read", "label": ""}); rr.Code != http.StatusBadRequest {
		t.Errorf("empty label = %d, want 400", rr.Code)
	}
	if rr := do(t, s, admin, http.MethodPost, "/api/keys/requests", map[string]string{"scope": "read", "label": "x"}); rr.Code != http.StatusForbidden {
		t.Errorf("password admin (no users row) = %d, want 403", rr.Code)
	}
	if rr := do(t, s, nil, http.MethodPost, "/api/keys/requests", map[string]string{"scope": "read", "label": "x"}); rr.Code != http.StatusUnauthorized {
		t.Errorf("anonymous = %d, want 401", rr.Code)
	}

	rr := do(t, s, user, http.MethodPost, "/api/keys/requests", map[string]string{"scope": "write", "label": "laptop cli"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create = %d %s, want 201", rr.Code, rr.Body.String())
	}
	var created struct {
		Request adminKeyRequestRow `json:"request"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if created.Request.ID == 0 || created.Request.RequestedScope != "write" || created.Request.WorkspaceSlug != "default" {
		t.Fatalf("created = %+v", created.Request)
	}

	var mine struct {
		Requests []adminKeyRequestRow `json:"requests"`
	}
	_ = json.Unmarshal(do(t, s, user, http.MethodGet, "/api/keys/requests", nil).Body.Bytes(), &mine)
	if len(mine.Requests) != 1 || mine.Requests[0].ID != created.Request.ID {
		t.Errorf("own list = %+v", mine.Requests)
	}

	var pending struct {
		Requests []adminKeyRequestRow `json:"requests"`
	}
	_ = json.Unmarshal(do(t, s, admin, http.MethodGet, "/api/admin/keys/requests", nil).Body.Bytes(), &pending)
	if len(pending.Requests) != 1 {
		t.Fatalf("admin pending = %+v", pending.Requests)
	}
	appr := do(t, s, admin, http.MethodPost, "/api/admin/keys/requests/"+itoa(created.Request.ID)+"/approve", nil)
	if appr.Code != http.StatusCreated {
		t.Fatalf("approve = %d %s", appr.Code, appr.Body.String())
	}
}
