package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dhanesh/phronesis/internal/audit"
	"github.com/dhanesh/phronesis/internal/auth/oidc"
)

// newStubOIDCProvider is a tiny helper wrapping oidc.NewStubProvider so the
// OIDC integration tests read cleanly.
func newStubOIDCProvider(issuer, audience string, secret []byte) *stubOIDCProvider {
	return &stubOIDCProvider{p: oidc.NewStubProvider(issuer, audience, secret)}
}

type stubOIDCProvider struct {
	p *oidc.StubProvider
}

func (s *stubOIDCProvider) Issue(subject, name string) (string, error) {
	return s.p.Issue(oidc.Claims{
		Subject: subject,
		Name:    name,
		TTL:     time.Hour,
	})
}

func TestLoginAndPageLifecycle(t *testing.T) {
	cfg := Config{
		Addr:          ":0",
		PagesDir:      t.TempDir(),
		FrontendDist:  t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
	}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	loginBody := []byte(`{"username":"admin","password":"secret"}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(loginBody))
	loginRes := httptest.NewRecorder()
	server.HTTP().Handler.ServeHTTP(loginRes, loginReq)
	if loginRes.Code != http.StatusOK {
		t.Fatalf("login status = %d body=%s", loginRes.Code, loginRes.Body.String())
	}
	cookie := loginRes.Result().Cookies()[0]

	updateBody := []byte(`{"content":"# Home\n\n- [ ] Ship docs\nSee [[Roadmap]] #planning","baseVersion":0}`)
	updateReq := httptest.NewRequest(http.MethodPost, "/api/pages/home", bytes.NewReader(updateBody))
	updateReq.AddCookie(cookie)
	updateRes := httptest.NewRecorder()
	server.HTTP().Handler.ServeHTTP(updateRes, updateReq)
	if updateRes.Code != http.StatusOK {
		t.Fatalf("update status = %d body=%s", updateRes.Code, updateRes.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/pages/home", nil)
	getReq.AddCookie(cookie)
	getRes := httptest.NewRecorder()
	server.HTTP().Handler.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", getRes.Code, getRes.Body.String())
	}

	var payload struct {
		Page struct {
			Content string `json:"content"`
			Render  struct {
				Links []string `json:"links"`
				Tags  []string `json:"tags"`
			} `json:"render"`
		} `json:"page"`
	}
	if err := json.Unmarshal(getRes.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload.Page.Content == "" || len(payload.Page.Render.Links) != 1 || len(payload.Page.Render.Tags) != 1 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

// @constraint RT-5 RT-6.2 S1 S6 INT-2 INT-3
// Integration test: cookie-authenticated upload to /media succeeds, GET round-
// trips the bytes, and API-KEY auth (service account principal) also works.
// This proves INT-2 (Principal middleware) composes correctly with INT-3
// (media handler mount) and INT-4 (blob store construct). Before INT-2, media
// endpoints returned 401; this test now expects 201 on upload + 200 on fetch.
func TestMediaEndpointEndToEnd(t *testing.T) {
	cfg := Config{
		Addr:          ":0",
		PagesDir:      t.TempDir(),
		FrontendDist:  t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
		APIKey:        "test-api-key",
	}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// --- Path A: cookie-authenticated user upload -> GET round-trip ---
	loginBody := []byte(`{"username":"admin","password":"secret"}`)
	loginReq := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(loginBody))
	loginRes := httptest.NewRecorder()
	server.HTTP().Handler.ServeHTTP(loginRes, loginReq)
	if loginRes.Code != http.StatusOK {
		t.Fatalf("login: %d %s", loginRes.Code, loginRes.Body.String())
	}
	cookie := loginRes.Result().Cookies()[0]

	// Upload a PNG via cookie auth.
	pngBytes := []byte("\x89PNG\r\n\x1a\ncookie-authenticated-upload")
	upReq := httptest.NewRequest(http.MethodPost, "/media", bytes.NewReader(pngBytes))
	upReq.AddCookie(cookie)
	upReq.Header.Set("Content-Type", "image/png")
	upRes := httptest.NewRecorder()
	server.HTTP().Handler.ServeHTTP(upRes, upReq)
	if upRes.Code != http.StatusCreated {
		t.Fatalf("cookie upload: got %d, want 201; body=%s", upRes.Code, upRes.Body.String())
	}
	var upResp struct {
		URL  string `json:"url"`
		Hash string `json:"hash"`
	}
	if err := json.Unmarshal(upRes.Body.Bytes(), &upResp); err != nil {
		t.Fatalf("unmarshal upload resp: %v", err)
	}
	if upResp.URL == "" || upResp.Hash == "" {
		t.Fatalf("empty URL/Hash in upload response")
	}

	// GET the same blob via cookie auth.
	getReq := httptest.NewRequest(http.MethodGet, upResp.URL, nil)
	getReq.AddCookie(cookie)
	getRes := httptest.NewRecorder()
	server.HTTP().Handler.ServeHTTP(getRes, getReq)
	if getRes.Code != http.StatusOK {
		t.Fatalf("cookie GET /media: got %d, want 200; body=%s", getRes.Code, getRes.Body.String())
	}
	if !bytes.Equal(getRes.Body.Bytes(), pngBytes) {
		t.Errorf("cookie GET body mismatch")
	}

	// --- Path B: API-KEY (service account) upload ---
	apiPng := []byte("\x89PNG\r\n\x1a\napi-key-uploaded")
	apiReq := httptest.NewRequest(http.MethodPost, "/media", bytes.NewReader(apiPng))
	apiReq.Header.Set("Content-Type", "image/png")
	apiReq.Header.Set("API-KEY", "test-api-key")
	apiRes := httptest.NewRecorder()
	server.HTTP().Handler.ServeHTTP(apiRes, apiReq)
	if apiRes.Code != http.StatusCreated {
		t.Fatalf("api-key upload: got %d, want 201; body=%s", apiRes.Code, apiRes.Body.String())
	}

	// --- Path C: unauthenticated upload must 401 ---
	anonReq := httptest.NewRequest(http.MethodPost, "/media", bytes.NewReader(pngBytes))
	anonReq.Header.Set("Content-Type", "image/png")
	anonRes := httptest.NewRecorder()
	server.HTTP().Handler.ServeHTTP(anonRes, anonReq)
	if anonRes.Code != http.StatusUnauthorized {
		t.Errorf("anon upload: got %d, want 401", anonRes.Code)
	}

	// --- Path D: wrong API-KEY must 401 ---
	badReq := httptest.NewRequest(http.MethodPost, "/media", bytes.NewReader(pngBytes))
	badReq.Header.Set("Content-Type", "image/png")
	badReq.Header.Set("API-KEY", "nope")
	badRes := httptest.NewRecorder()
	server.HTTP().Handler.ServeHTTP(badRes, badReq)
	if badRes.Code != http.StatusUnauthorized {
		t.Errorf("bad api-key upload: got %d, want 401", badRes.Code)
	}
}

// @constraint S2 S9 RT-4.4 INT-5
// Integration test: auth + page edit/view actions produce durable audit log
// entries. Exercises the full hot-path-Enqueue -> async-drainer -> FileSink
// pipeline. After Close(ctx), the audit.log file on disk contains JSONL
// entries with principal_id + principal_type + action per event.
func TestAuditLogRecordsEvents(t *testing.T) {
	tmp := t.TempDir()
	auditPath := filepath.Join(tmp, "audit.log")
	cfg := Config{
		Addr:          ":0",
		PagesDir:      t.TempDir(),
		FrontendDist:  t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
		AuditLog:      auditPath,
	}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// 1. Failed login (should emit auth.login_failed).
	bad := []byte(`{"username":"admin","password":"wrong"}`)
	badReq := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(bad))
	badRes := httptest.NewRecorder()
	server.HTTP().Handler.ServeHTTP(badRes, badReq)
	if badRes.Code != http.StatusUnauthorized {
		t.Fatalf("bad login: got %d, want 401", badRes.Code)
	}

	// 2. Successful login (auth.login).
	good := []byte(`{"username":"admin","password":"secret"}`)
	goodReq := httptest.NewRequest(http.MethodPost, "/api/login", bytes.NewReader(good))
	goodRes := httptest.NewRecorder()
	server.HTTP().Handler.ServeHTTP(goodRes, goodReq)
	if goodRes.Code != http.StatusOK {
		t.Fatalf("good login: got %d", goodRes.Code)
	}
	cookie := goodRes.Result().Cookies()[0]

	// 3. Page edit (doc.edit).
	editBody := []byte(`{"content":"# Home\n","baseVersion":0}`)
	editReq := httptest.NewRequest(http.MethodPost, "/api/pages/home", bytes.NewReader(editBody))
	editReq.AddCookie(cookie)
	editRes := httptest.NewRecorder()
	server.HTTP().Handler.ServeHTTP(editRes, editReq)
	if editRes.Code != http.StatusOK {
		t.Fatalf("edit: got %d", editRes.Code)
	}

	// 4. Page view (doc.view).
	viewReq := httptest.NewRequest(http.MethodGet, "/api/pages/home", nil)
	viewReq.AddCookie(cookie)
	viewRes := httptest.NewRecorder()
	server.HTTP().Handler.ServeHTTP(viewRes, viewReq)
	if viewRes.Code != http.StatusOK {
		t.Fatalf("view: got %d", viewRes.Code)
	}

	// 5. Close drains the buffer to disk. This is the S9 flush point; without
	// it the async drainer might not have written events yet.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 6. Read the audit log and verify we got all four events.
	f, err := os.Open(auditPath)
	if err != nil {
		t.Fatalf("open audit log: %v", err)
	}
	defer f.Close()

	sawFailed, sawLogin, sawEdit, sawView := false, false, false, false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e struct {
			Action        string `json:"Action"`
			PrincipalID   string `json:"PrincipalID"`
			PrincipalType string `json:"PrincipalType"`
			ResourceID    string `json:"ResourceID"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Errorf("malformed audit line: %v (%s)", err, scanner.Text())
			continue
		}
		switch e.Action {
		case "auth.login_failed":
			sawFailed = true
		case "auth.login":
			sawLogin = true
			if e.PrincipalID != "admin" || e.PrincipalType != "user" {
				t.Errorf("auth.login: unexpected principal %q/%q", e.PrincipalID, e.PrincipalType)
			}
		case "doc.edit":
			sawEdit = true
			if e.ResourceID != "home" {
				t.Errorf("doc.edit: ResourceID got %q, want home", e.ResourceID)
			}
			if e.PrincipalType != "user" {
				t.Errorf("doc.edit: PrincipalType got %q, want user (TN8)", e.PrincipalType)
			}
		case "doc.view":
			sawView = true
		}
	}
	if !sawFailed {
		t.Error("auth.login_failed event missing from audit log")
	}
	if !sawLogin {
		t.Error("auth.login event missing from audit log")
	}
	if !sawEdit {
		t.Error("doc.edit event missing from audit log")
	}
	if !sawView {
		t.Error("doc.view event missing from audit log")
	}
}

// @constraint O5 RT-12.1 INT-10 S2
// Integration test for INT-10: Serve runs until ctx is cancelled, then
// gracefully shuts down HTTP and drains the audit buffer. After Serve
// returns, queued audit events MUST be present on disk -- proves the
// shutdown chain (HTTP.Shutdown -> s.Close -> audit.Drainer.Close) runs
// in the correct order and actually flushes.
func TestServeGracefulShutdownDrainsAudit(t *testing.T) {
	tmp := t.TempDir()
	auditPath := filepath.Join(tmp, "audit.log")
	cfg := Config{
		Addr:          "127.0.0.1:0", // OS-assigned port
		PagesDir:      t.TempDir(),
		FrontendDist:  t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
		AuditLog:      auditPath,
	}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// Pre-queue an audit event so we can later verify it made it to disk.
	// This simulates an event that arrived via the hot path just before
	// shutdown was initiated.
	server.auditDrainer.Enqueue(audit.Event{
		At:          time.Now().UTC(),
		Action:      "test.preshutdown",
		PrincipalID: "test-runner",
	})

	// Bind the HTTP listener manually so we don't actually accept connections
	// in the test (ListenAndServe would succeed but we don't need a real port
	// here -- we only want to exercise the ctx-cancel -> shutdown path).
	// We cancel ctx immediately so Serve goes straight to graceful shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before Serve starts; Serve should still drain cleanly

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(ctx, 3*time.Second)
	}()

	select {
	case err := <-serveErr:
		// http.ErrServerClosed OR a port-bind race is expected when the
		// listener never actually opened or closed immediately. The
		// important property is that Serve RETURNS -- it must not hang.
		_ = err
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return within 5s of cancelled ctx")
	}

	// Audit log should now contain the pre-queued event, proving Close()
	// ran as part of the shutdown chain.
	if _, err := os.Stat(auditPath); err != nil {
		t.Fatalf("audit log not created: %v", err)
	}
	f, err := os.Open(auditPath)
	if err != nil {
		t.Fatalf("open audit log: %v", err)
	}
	defer f.Close()

	found := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e struct {
			Action      string `json:"Action"`
			PrincipalID string `json:"PrincipalID"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			continue
		}
		if e.Action == "test.preshutdown" && e.PrincipalID == "test-runner" {
			found = true
			break
		}
	}
	if !found {
		t.Error("pre-shutdown audit event not persisted; Close() did not drain the buffer")
	}
}

// @constraint INT-10
// Calling Close() a second time must be a no-op. Tests the defensive contract
// so repeated Close during error handling does not panic.
func TestCloseIsIdempotent(t *testing.T) {
	cfg := Config{
		PagesDir:      t.TempDir(),
		FrontendDist:  t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
		AuditLog:      filepath.Join(t.TempDir(), "audit.log"),
	}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	ctx := context.Background()
	if err := server.Close(ctx); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// Second Close must not panic or error.
	if err := server.Close(ctx); err != nil {
		t.Errorf("second Close: got %v, want nil (idempotent)", err)
	}
}

// @constraint RT-11 S4 TN5 INT-8
// End-to-end OIDC login flow: stub provider issues HMAC id_token, server
// validates + issues session cookie, subsequent /api/pages request uses
// the cookie as if it were a password login.
func TestOIDCLoginIssuesSessionCookie(t *testing.T) {
	secret := []byte("stub-secret-at-least-32-bytes-long!!")
	cfg := Config{
		Addr:          ":0",
		PagesDir:      t.TempDir(),
		FrontendDist:  t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
		AuditLog:      filepath.Join(t.TempDir(), "audit.log"),
		OIDCEnabled:   true,
		OIDCIssuer:    "https://idp.test",
		OIDCAudience:  "phronesis",
		OIDCSecret:    string(secret),
	}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close(context.Background())

	// Issue a stub id_token with the OIDC stub provider.
	provider := newStubOIDCProvider("https://idp.test", "phronesis", secret)
	token, err := provider.Issue("alice", "Alice")
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}

	body, _ := json.Marshal(map[string]string{"id_token": token})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/oidc/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	server.HTTP().Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("oidc login: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	cookies := rr.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no session cookie set")
	}
	cookie := cookies[0]

	// Use the cookie to fetch pages (proves session persisted through sessions.Store).
	listReq := httptest.NewRequest(http.MethodGet, "/api/pages", nil)
	listReq.AddCookie(cookie)
	listRes := httptest.NewRecorder()
	server.HTTP().Handler.ServeHTTP(listRes, listReq)

	if listRes.Code != http.StatusOK {
		t.Errorf("authenticated /api/pages: got %d, want 200", listRes.Code)
	}
}

// @constraint RT-5 I1-fix
// Regression guard: cookie-authenticated OIDC session must produce a
// Principal whose Claims["auth_method"] reflects the OIDC origin, NOT
// the default "password". Before the I1 review fix, principalFromRequest
// would stamp every cookie session as auth_method=password, dropping the
// metadata stored by issueOIDCSession.
func TestOIDCCookieSessionCarriesOIDCAuthMethod(t *testing.T) {
	secret := []byte("stub-secret-at-least-32-bytes-long!!")
	cfg := Config{
		Addr:          ":0",
		PagesDir:      t.TempDir(),
		FrontendDist:  t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
		AuditLog:      filepath.Join(t.TempDir(), "audit.log"),
		OIDCEnabled:   true,
		OIDCIssuer:    "https://idp.test",
		OIDCAudience:  "phronesis",
		OIDCSecret:    string(secret),
	}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close(context.Background())

	provider := newStubOIDCProvider("https://idp.test", "phronesis", secret)
	token, _ := provider.Issue("alice", "Alice")

	// Perform OIDC login to get a session cookie.
	body, _ := json.Marshal(map[string]string{"id_token": token})
	loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/oidc/login", bytes.NewReader(body))
	loginRes := httptest.NewRecorder()
	server.HTTP().Handler.ServeHTTP(loginRes, loginReq)
	if loginRes.Code != http.StatusOK {
		t.Fatalf("oidc login: %d", loginRes.Code)
	}
	cookie := loginRes.Result().Cookies()[0]

	// Now craft a follow-up request bearing the cookie and resolve the
	// principal directly via the server's resolver.
	followReq := httptest.NewRequest(http.MethodGet, "/api/pages", nil)
	followReq.AddCookie(cookie)
	p, ok := server.principalFromRequest(followReq)
	if !ok {
		t.Fatal("principalFromRequest: want ok=true, got false")
	}
	if got := p.Claims["auth_method"]; got != "oidc" {
		t.Errorf("auth_method: got %q, want \"oidc\" (I1 regression — metadata dropped)", got)
	}
	if p.ID != "alice" {
		t.Errorf("Principal.ID: got %q, want alice", p.ID)
	}
	if p.WorkspaceID != defaultWorkspaceID {
		t.Errorf("Principal.WorkspaceID: got %q, want %q", p.WorkspaceID, defaultWorkspaceID)
	}
}

// @constraint RT-11 INT-8
// OIDC route returns 404 when OIDC is not enabled (default).
func TestOIDCLoginNotFoundWhenDisabled(t *testing.T) {
	cfg := Config{
		Addr:          ":0",
		PagesDir:      t.TempDir(),
		FrontendDist:  t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
		AuditLog:      filepath.Join(t.TempDir(), "audit.log"),
		// OIDCEnabled omitted -> false
	}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close(context.Background())

	body, _ := json.Marshal(map[string]string{"id_token": "anything"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/oidc/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	server.HTTP().Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("disabled OIDC: got %d, want 404", rr.Code)
	}
}

// @constraint RT-11 S4
// Tampered id_token is rejected with 401 (signature check fails).
func TestOIDCLoginRejectsTamperedToken(t *testing.T) {
	secret := []byte("stub-secret-at-least-32-bytes-long!!")
	cfg := Config{
		Addr:          ":0",
		PagesDir:      t.TempDir(),
		FrontendDist:  t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
		AuditLog:      filepath.Join(t.TempDir(), "audit.log"),
		OIDCEnabled:   true,
		OIDCIssuer:    "https://idp.test",
		OIDCAudience:  "phronesis",
		OIDCSecret:    string(secret),
	}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close(context.Background())

	// Deliberately wrong secret -> signature won't match.
	provider := newStubOIDCProvider("https://idp.test", "phronesis", []byte("different-secret-still-32-bytes!"))
	token, _ := provider.Issue("alice", "Alice")

	body, _ := json.Marshal(map[string]string{"id_token": token})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/oidc/login", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	server.HTTP().Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("tampered token: got %d, want 401", rr.Code)
	}
}

func TestUnauthorizedAPI(t *testing.T) {
	cfg := Config{
		Addr:          ":0",
		PagesDir:      t.TempDir(),
		FrontendDist:  t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
	}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/pages", nil)
	res := httptest.NewRecorder()
	server.HTTP().Handler.ServeHTTP(res, req)
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body=%s", res.Code, res.Body.String())
	}
}
