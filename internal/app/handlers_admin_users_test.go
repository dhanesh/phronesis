package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/dhanesh/phronesis/internal/audit"
	"github.com/dhanesh/phronesis/internal/store/sqlite"
)

// newAdminTestServer constructs a *Server with the bare-minimum
// dependencies for the admin user/key handlers — a SQLite store and a
// no-op audit drainer. This bypasses the full NewServer wiring (which
// requires PagesDir and other unrelated state) so the tests stay
// focused on handler behaviour.
func newAdminTestServer(t *testing.T) *Server {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	auditSink, err := audit.NewFileSink(filepath.Join(t.TempDir(), "audit.log"))
	if err != nil {
		t.Fatalf("audit.NewFileSink: %v", err)
	}
	drainer := audit.NewBufferedDrainer(auditSink, audit.DrainerConfig{Capacity: 64, Batch: 16})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = drainer.Close(ctx)
		_ = auditSink.Close(context.Background())
	})

	return &Server{
		store:        store,
		auditSink:    auditSink,
		auditDrainer: drainer,
	}
}

// seedUser inserts a row directly into the users table so tests don't
// depend on the OIDC projection layer.
func seedUser(t *testing.T, s *Server, sub, email, name, role, status string) int64 {
	t.Helper()
	res, err := s.store.DB().Exec(
		`INSERT INTO users (oidc_sub, email, display_name, role, status) VALUES (?, ?, ?, ?, ?)`,
		sub, email, name, role, status)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// @constraint U2 — admin Users page lists projected users.
// Satisfies RT-9 evidence E22 (server-side coverage).
func TestAdminUsersList(t *testing.T) {
	s := newAdminTestServer(t)
	seedUser(t, s, "sub-1", "alice@example.com", "Alice", "user", "active")
	seedUser(t, s, "sub-2", "admin@example.com", "Admin", "admin", "active")

	req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	w := httptest.NewRecorder()
	s.handleAdminUsers(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
	}
	var resp struct {
		Users []adminUserRow `json:"users"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(resp.Users))
	}
	// Counts are aggregated even with no keys / no requests; they must
	// be 0, not omitted.
	for _, u := range resp.Users {
		if u.ActiveKeyCount != 0 {
			t.Errorf("expected ActiveKeyCount=0, got %d", u.ActiveKeyCount)
		}
		if u.PendingRequestCount != 0 {
			t.Errorf("expected PendingRequestCount=0, got %d", u.PendingRequestCount)
		}
	}
}

func TestAdminUsersListReturnsEmptyArray(t *testing.T) {
	s := newAdminTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	w := httptest.NewRecorder()
	s.handleAdminUsers(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	// Per the project's API JSON-arrays-always-present convention, the
	// users field must be present as an array (not omitted, not null)
	// even when empty. The frontend reads .length on it.
	var resp struct {
		Users []adminUserRow `json:"users"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Users == nil {
		t.Fatal("users field should be [] not null")
	}
	if len(resp.Users) != 0 {
		t.Fatalf("expected 0 users, got %d", len(resp.Users))
	}
}

// @constraint S5 — suspending updates status atomically.
func TestAdminUserSuspendAndReactivate(t *testing.T) {
	s := newAdminTestServer(t)
	id := seedUser(t, s, "sub-x", "x@example.com", "X", "user", "active")

	// Suspend
	req := httptest.NewRequest(http.MethodPost, "/api/admin/users/"+itoa(id)+"/suspend", nil)
	w := httptest.NewRecorder()
	s.handleAdminUsers(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("suspend: expected 204, got %d (%s)", w.Code, w.Body.String())
	}
	if got := readUserStatus(t, s, id); got != "suspended" {
		t.Fatalf("expected status=suspended, got %q", got)
	}

	// Reactivate
	req = httptest.NewRequest(http.MethodPost, "/api/admin/users/"+itoa(id)+"/reactivate", nil)
	w = httptest.NewRecorder()
	s.handleAdminUsers(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("reactivate: expected 204, got %d (%s)", w.Code, w.Body.String())
	}
	if got := readUserStatus(t, s, id); got != "active" {
		t.Fatalf("expected status=active, got %q", got)
	}
}

func TestAdminUserSuspendNotFound(t *testing.T) {
	s := newAdminTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/users/9999/suspend", nil)
	w := httptest.NewRecorder()
	s.handleAdminUsers(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestAdminUserDelete(t *testing.T) {
	s := newAdminTestServer(t)
	id := seedUser(t, s, "sub-d", "d@example.com", "D", "user", "active")

	req := httptest.NewRequest(http.MethodDelete, "/api/admin/users/"+itoa(id), nil)
	w := httptest.NewRecorder()
	s.handleAdminUsers(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: expected 204, got %d (%s)", w.Code, w.Body.String())
	}
	var n int
	_ = s.store.DB().QueryRow(`SELECT COUNT(*) FROM users WHERE id=?`, id).Scan(&n)
	if n != 0 {
		t.Fatalf("user still present after delete: count=%d", n)
	}
}

func TestAdminUsersUnknownAction(t *testing.T) {
	s := newAdminTestServer(t)
	id := seedUser(t, s, "sub-u", "u@example.com", "U", "user", "active")

	req := httptest.NewRequest(http.MethodPost, "/api/admin/users/"+itoa(id)+"/sudo", nil)
	w := httptest.NewRecorder()
	s.handleAdminUsers(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown action, got %d", w.Code)
	}
}

// TestAdminUsersWithoutStoreReturns503
//
// @constraint RT-8 — admin endpoints depend on the SQLite store; when
// StorePath is unset, the route returns 503 rather than 500/panic.
func TestAdminUsersWithoutStoreReturns503(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	w := httptest.NewRecorder()
	s.handleAdminUsers(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when store is nil, got %d (%s)", w.Code, w.Body.String())
	}
}

// helpers

func itoa(n int64) string {
	b := []byte{}
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

func readUserStatus(t *testing.T, s *Server, id int64) string {
	t.Helper()
	var status string
	err := s.store.DB().QueryRow(`SELECT status FROM users WHERE id=?`, id).Scan(&status)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	return status
}
