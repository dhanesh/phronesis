package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// @constraint RT-8 O4 TN9
// On a freshly constructed server with no pending journal, /readyz returns 200
// + JSON with ready=true and every check green.
func TestReadyzGreenOnHealthyServer(t *testing.T) {
	cfg := Config{
		PagesDir:      t.TempDir(),
		FrontendDist:  t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
		AuditLog:      filepath.Join(t.TempDir(), "audit.log"),
		// JournalPath deliberately empty -> journal check is a no-op.
	}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close(context.Background())

	req := httptest.NewRequest(http.MethodGet, "/api/readyz", nil)
	rr := httptest.NewRecorder()
	server.handleReadyz(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp readyzResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Ready {
		t.Errorf("ready: got false, want true; checks: %+v", resp.Checks)
	}
	if len(resp.Checks) == 0 {
		t.Error("no checks returned")
	}
	for _, c := range resp.Checks {
		if !c.OK {
			t.Errorf("check %q failed: %s", c.Name, c.Error)
		}
	}
}

// @constraint RT-8 RT-7.2
// When the journal file has unreplayed entries, /readyz returns 503 with the
// journal_replayed check failing. This is the RT-7.2 gating contract.
func TestReadyzFailsWhenJournalHasEntries(t *testing.T) {
	dir := t.TempDir()
	journalPath := filepath.Join(dir, "journal.log")

	// Seed the journal file with a line so Exists returns true.
	if err := os.WriteFile(journalPath, []byte(`{"id":"pending","at":"2026-04-19T00:00:00Z","kind":"git.push","payload":null}`+"\n"), 0o640); err != nil {
		t.Fatalf("seed journal: %v", err)
	}

	cfg := Config{
		PagesDir:      t.TempDir(),
		FrontendDist:  t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "secret",
		AuditLog:      filepath.Join(t.TempDir(), "audit.log"),
		JournalPath:   journalPath,
	}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close(context.Background())

	req := httptest.NewRequest(http.MethodGet, "/api/readyz", nil)
	rr := httptest.NewRecorder()
	server.handleReadyz(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d, want 503; body=%s", rr.Code, rr.Body.String())
	}
	var resp readyzResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.Ready {
		t.Error("ready: got true, want false")
	}

	found := false
	for _, c := range resp.Checks {
		if c.Name == "journal_replayed" && !c.OK {
			found = true
			if c.Error == "" {
				t.Error("journal_replayed failure has empty Error")
			}
		}
	}
	if !found {
		t.Errorf("expected failing journal_replayed check; got %+v", resp.Checks)
	}
}

// @constraint RT-8
// Server.Ready exposes the same checks as /readyz for programmatic probes.
func TestReadyMethodMirrorsReadyzHandler(t *testing.T) {
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
	defer server.Close(context.Background())

	if err := server.Ready(context.Background()); err != nil {
		t.Errorf("Ready() on fresh server: got %v, want nil", err)
	}
}

// @constraint RT-8
// /readyz only accepts GET. POST should return 405.
func TestReadyzRejectsNonGet(t *testing.T) {
	cfg := Config{
		PagesDir: t.TempDir(), FrontendDist: t.TempDir(),
		AdminUser: "admin", AdminPassword: "secret",
		AuditLog: filepath.Join(t.TempDir(), "audit.log"),
	}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close(context.Background())

	req := httptest.NewRequest(http.MethodPost, "/api/readyz", nil)
	rr := httptest.NewRecorder()
	server.handleReadyz(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want 405", rr.Code)
	}
}

// @constraint RT-8 TN9
// /readyz does NOT call out to git remote or external URLs; a network-isolated
// test should still return 200. This is the TN9 contract: readiness reflects
// "can I serve traffic right now", not "is the backup channel working".
func TestReadyzDoesNotDependOnGitRemote(t *testing.T) {
	// The test is implicit: TestReadyzGreenOnHealthyServer runs with no
	// configured git remote and returns 200. This test documents the
	// intent with an explicit name so grep-for-TN9 finds it.
	cfg := Config{
		PagesDir: t.TempDir(), FrontendDist: t.TempDir(),
		AdminUser: "admin", AdminPassword: "secret",
		AuditLog: filepath.Join(t.TempDir(), "audit.log"),
		// No git remote, no journal, no OIDC.
	}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	defer server.Close(context.Background())

	if err := server.Ready(context.Background()); err != nil {
		t.Errorf("Ready() without git remote: got %v, want nil", err)
	}
}
