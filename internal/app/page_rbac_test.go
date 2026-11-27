package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Decision 2026-11-27: RBAC is enforced on pages, and API keys work on the
// HTTP API like sessions do, bounded by scope, owner role and workspace.

func doBearer(t *testing.T, s *Server, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewReader(b)
	} else {
		buf = bytes.NewReader(nil)
	}
	r := httptest.NewRequest(method, path, buf)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	s.HTTP().Handler.ServeHTTP(rr, r)
	return rr
}

func setRole(t *testing.T, s *Server, admin *http.Cookie, sub, role string) {
	t.Helper()
	var id int64
	if err := s.store.DB().QueryRow(`SELECT id FROM users WHERE oidc_sub=?`, sub).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if rr := do(t, s, admin, http.MethodPost, "/api/admin/users/"+itoa(id)+"/role", map[string]string{"role": role}); rr.Code != http.StatusOK {
		t.Fatalf("set role %s: %d %s", role, rr.Code, rr.Body.String())
	}
}

// mintKeyFor runs the request -> approve flow and returns the plaintext.
func mintKeyFor(t *testing.T, s *Server, user, admin *http.Cookie, scope string) string {
	t.Helper()
	rr := do(t, s, user, http.MethodPost, "/api/keys/requests", map[string]string{"scope": scope, "label": "k-" + scope})
	if rr.Code != http.StatusCreated {
		t.Fatalf("request: %d %s", rr.Code, rr.Body.String())
	}
	var created struct {
		Request adminKeyRequestRow `json:"request"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	appr := do(t, s, admin, http.MethodPost, "/api/admin/keys/requests/"+itoa(created.Request.ID)+"/approve", nil)
	if appr.Code != http.StatusCreated {
		t.Fatalf("approve: %d %s", appr.Code, appr.Body.String())
	}
	var out struct {
		Key struct {
			Plaintext string `json:"plaintext"`
		} `json:"key"`
	}
	_ = json.Unmarshal(appr.Body.Bytes(), &out)
	return out.Key.Plaintext
}

var editBody = map[string]any{"content": "# hi\n", "baseVersion": 0}

func TestViewerCanReadButNotWritePages(t *testing.T) {
	s := newOIDCStoreServer(t)
	c, _ := oidcLogin(t, s, "vera", "vera@example.com")
	if rr := do(t, s, c, http.MethodGet, "/api/pages", nil); rr.Code != http.StatusOK {
		t.Errorf("viewer list = %d", rr.Code)
	}
	if rr := do(t, s, c, http.MethodGet, "/api/pages/home", nil); rr.Code != http.StatusOK {
		t.Errorf("viewer read = %d", rr.Code)
	}
	if rr := do(t, s, c, http.MethodPost, "/api/pages/home", editBody); rr.Code != http.StatusForbidden {
		t.Errorf("viewer write = %d, want 403", rr.Code)
	}
	if rr := do(t, s, c, http.MethodPost, "/api/workspaces/default/pages/home", editBody); rr.Code != http.StatusForbidden {
		t.Errorf("viewer write via workspace route = %d, want 403", rr.Code)
	}
}

func TestPromotedEditorCanWriteAtOnce(t *testing.T) {
	s := newOIDCStoreServer(t)
	admin := passwordLogin(t, s)
	c, _ := oidcLogin(t, s, "ed", "ed@example.com")
	setRole(t, s, admin, "ed", "editor")
	if got := sessionRole(t, s, c); got != "editor" {
		t.Errorf("session role = %q, want editor", got)
	}
	if rr := do(t, s, c, http.MethodPost, "/api/pages/home", editBody); rr.Code != http.StatusOK {
		t.Errorf("editor write = %d %s", rr.Code, rr.Body.String())
	}
	if rr := do(t, s, c, http.MethodGet, "/api/admin/users", nil); rr.Code != http.StatusForbidden {
		t.Errorf("editor admin api = %d, want 403", rr.Code)
	}
	if rr := do(t, s, admin, http.MethodPost, "/api/admin/users/1/role", map[string]string{"role": "owner"}); rr.Code != http.StatusBadRequest {
		t.Errorf("bad role = %d, want 400", rr.Code)
	}
}

func TestAPIKeysWorkOnHTTPAPIWithinScopeAndWorkspace(t *testing.T) {
	s := newOIDCStoreServer(t)
	admin := passwordLogin(t, s)
	user, _ := oidcLogin(t, s, "kim", "kim@example.com")
	setRole(t, s, admin, "kim", "editor")

	read := mintKeyFor(t, s, user, admin, "read")
	write := mintKeyFor(t, s, user, admin, "write")

	if rr := doBearer(t, s, read, http.MethodGet, "/api/pages", nil); rr.Code != http.StatusOK {
		t.Errorf("read key list = %d %s", rr.Code, rr.Body.String())
	}
	if rr := doBearer(t, s, read, http.MethodPost, "/api/pages/home", editBody); rr.Code != http.StatusForbidden {
		t.Errorf("read key write = %d, want 403", rr.Code)
	}
	if rr := doBearer(t, s, write, http.MethodPost, "/api/pages/home", editBody); rr.Code != http.StatusOK {
		t.Errorf("write key write = %d %s", rr.Code, rr.Body.String())
	}
	if rr := doBearer(t, s, "phr_live_bogus_bogus", http.MethodGet, "/api/pages", nil); rr.Code != http.StatusUnauthorized {
		t.Errorf("bogus key = %d, want 401", rr.Code)
	}

	// Workspace pin: a default-workspace key is refused on another workspace.
	if rr := do(t, s, admin, http.MethodPost, "/api/admin/workspaces", map[string]string{"slug": "research", "name": "Research"}); rr.Code/100 != 2 {
		t.Fatalf("create workspace: %d %s", rr.Code, rr.Body.String())
	}
	if rr := doBearer(t, s, write, http.MethodGet, "/api/workspaces/research/pages", nil); rr.Code != http.StatusForbidden {
		t.Errorf("key on other workspace = %d, want 403", rr.Code)
	}
	var ws struct {
		Workspaces []struct {
			Slug string `json:"slug"`
		} `json:"workspaces"`
	}
	_ = json.Unmarshal(doBearer(t, s, write, http.MethodGet, "/api/workspaces", nil).Body.Bytes(), &ws)
	if len(ws.Workspaces) != 1 || ws.Workspaces[0].Slug != "default" {
		t.Errorf("key workspace list = %+v, want only default", ws.Workspaces)
	}

	// Demoting the owner caps the write key at viewer immediately.
	setRole(t, s, admin, "kim", "viewer")
	if rr := doBearer(t, s, write, http.MethodPost, "/api/pages/home", editBody); rr.Code != http.StatusForbidden {
		t.Errorf("write key after owner demotion = %d, want 403", rr.Code)
	}
}
