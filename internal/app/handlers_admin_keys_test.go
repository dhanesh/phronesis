package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// seedKeyRequest inserts a row into key_requests so admin handlers
// have something to list/deny.
func seedKeyRequest(t *testing.T, s *Server, userID int64, workspace, scope, label string) int64 {
	t.Helper()
	res, err := s.store.DB().Exec(
		`INSERT INTO key_requests (user_id, workspace_slug, requested_scope, requested_label) VALUES (?, ?, ?, ?)`,
		userID, workspace, scope, label)
	if err != nil {
		t.Fatalf("seed key_request: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// @constraint U3 — admin Keys page lists keys (and serves [] when empty).
func TestAdminKeysListEmptyArray(t *testing.T) {
	s := newAdminTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/keys", nil)
	w := httptest.NewRecorder()
	s.handleAdminKeys(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	var resp struct {
		Keys []adminKeyRow `json:"keys"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Keys == nil {
		t.Fatal("keys field should be [] not null")
	}
	if len(resp.Keys) != 0 {
		t.Fatalf("expected 0 keys, got %d", len(resp.Keys))
	}
}

// @constraint U3 — admin lists pending key requests; empty state is [].
func TestAdminKeyRequestsListEmpty(t *testing.T) {
	s := newAdminTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/keys/requests", nil)
	w := httptest.NewRecorder()
	s.handleAdminKeys(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	var resp struct {
		Requests []adminKeyRequestRow `json:"requests"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Requests == nil {
		t.Fatal("requests field should be [] not null")
	}
}

// @constraint TN7 — request->approve flow: list pending and deny.
func TestAdminKeyRequestsListAndDeny(t *testing.T) {
	s := newAdminTestServer(t)
	uid := seedUser(t, s, "sub-r", "r@example.com", "R", "user", "active")
	rid := seedKeyRequest(t, s, uid, "default", "write", "claude-code")

	// List should surface the pending request.
	req := httptest.NewRequest(http.MethodGet, "/api/admin/keys/requests", nil)
	w := httptest.NewRecorder()
	s.handleAdminKeys(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	var resp struct {
		Requests []adminKeyRequestRow `json:"requests"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Requests) != 1 {
		t.Fatalf("expected 1 pending request, got %d", len(resp.Requests))
	}
	if resp.Requests[0].RequestedScope != "write" {
		t.Errorf("expected scope=write, got %q", resp.Requests[0].RequestedScope)
	}

	// Deny.
	req = httptest.NewRequest(http.MethodPost, "/api/admin/keys/requests/"+itoa(rid)+"/deny", nil)
	w = httptest.NewRecorder()
	s.handleAdminKeys(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("deny: expected 204, got %d (%s)", w.Code, w.Body.String())
	}

	// After deny, the pending list should be empty.
	req = httptest.NewRequest(http.MethodGet, "/api/admin/keys/requests", nil)
	w = httptest.NewRecorder()
	s.handleAdminKeys(w, req)
	resp.Requests = nil
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Requests) != 0 {
		t.Fatalf("expected 0 pending after deny, got %d", len(resp.Requests))
	}
}

func TestAdminKeyRequestsDenyAlreadyDecidedReturns404(t *testing.T) {
	s := newAdminTestServer(t)
	uid := seedUser(t, s, "sub-q", "q@example.com", "Q", "user", "active")
	rid := seedKeyRequest(t, s, uid, "default", "read", "test")

	// First deny: 204
	req := httptest.NewRequest(http.MethodPost, "/api/admin/keys/requests/"+itoa(rid)+"/deny", nil)
	w := httptest.NewRecorder()
	s.handleAdminKeys(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("first deny: expected 204, got %d", w.Code)
	}
	// Second deny: 404 (already decided)
	req = httptest.NewRequest(http.MethodPost, "/api/admin/keys/requests/"+itoa(rid)+"/deny", nil)
	w = httptest.NewRecorder()
	s.handleAdminKeys(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("second deny: expected 404, got %d", w.Code)
	}
}

// @constraint TN7 / S1 — approve returns 501 in Stage 1b. Stage 2 ships
// real Argon2id-hashed key minting.
func TestAdminKeyRequestApproveReturns501Stub(t *testing.T) {
	s := newAdminTestServer(t)
	uid := seedUser(t, s, "sub-a", "a@example.com", "A", "user", "active")
	rid := seedKeyRequest(t, s, uid, "default", "write", "test")

	req := httptest.NewRequest(http.MethodPost, "/api/admin/keys/requests/"+itoa(rid)+"/approve", nil)
	w := httptest.NewRecorder()
	s.handleAdminKeys(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d (%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Stage 2") {
		t.Errorf("error body should mention Stage 2: %s", w.Body.String())
	}
	// Approve must NOT have decided the request.
	var decision string
	_ = s.store.DB().QueryRow(`SELECT COALESCE(decision,'') FROM key_requests WHERE id=?`, rid).Scan(&decision)
	if decision != "" {
		t.Errorf("approve stub should not decide the request, but decision=%q", decision)
	}
}

func TestAdminKeysWithoutStoreReturns503(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/keys", nil)
	w := httptest.NewRecorder()
	s.handleAdminKeys(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when store is nil, got %d", w.Code)
	}
}
