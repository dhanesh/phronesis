package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dhanesh/phronesis/internal/audit"
	"github.com/dhanesh/phronesis/internal/principal"
	"github.com/dhanesh/phronesis/internal/store/sqlite"
)

// memSink is a tiny in-memory audit.Sink used by middleware tests
// so we can inspect emitted events without opening a real store.
type memSink struct {
	mu     sync.Mutex
	events []audit.Event
}

func (m *memSink) Write(_ context.Context, evts []audit.Event) error {
	m.mu.Lock()
	m.events = append(m.events, evts...)
	m.mu.Unlock()
	return nil
}
func (m *memSink) Close(_ context.Context) error { return nil }
func (m *memSink) snapshot() []audit.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]audit.Event, len(m.events))
	copy(out, m.events)
	return out
}

// newAuditMiddlewareTestServer constructs a *Server with the
// minimal dependencies needed to exercise auditMiddleware: a
// memSink-backed drainer + SQLite store (so admin endpoints don't
// 503 if a future test reaches them) + audit fields wired.
func newAuditMiddlewareTestServer(t *testing.T) (*Server, *memSink) {
	t.Helper()
	store, err := sqlite.Open(filepath.Join(t.TempDir(), "audit-mw.db"))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	sink := &memSink{}
	drainer := audit.NewBufferedDrainer(sink, audit.DrainerConfig{
		Capacity: 64, Batch: 16, Interval: 10 * time.Millisecond,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = drainer.Close(ctx)
	})

	return &Server{store: store, auditSink: sink, auditDrainer: drainer}, sink
}

// flushDrainer waits long enough for the drainer's tick interval
// (10ms in test fixture) plus jitter so events are written through
// to the sink before assertions run.
func flushDrainer() { time.Sleep(40 * time.Millisecond) }

// authenticatedReq returns an *http.Request whose context already
// carries a Principal — simulating attachPrincipal having run.
func authenticatedReq(method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	p := principal.Principal{
		Type:        principal.TypeUser,
		ID:          "alice",
		WorkspaceID: "default",
		Role:        principal.RoleEditor,
	}
	return r.WithContext(principal.WithPrincipal(r.Context(), p))
}

// @constraint B1 — every authenticated request without explicit
// handler-level audit produces a default `http.<method>` event.
// Satisfies RT-1 (universal middleware).
func TestAuditMiddlewareEmitsDefaultEventWhenHandlerDoesNot(t *testing.T) {
	s, sink := newAuditMiddlewareTestServer(t)

	noopHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	w := httptest.NewRecorder()
	s.auditMiddleware(noopHandler).ServeHTTP(w, authenticatedReq(http.MethodGet, "/api/pages/foo"))
	flushDrainer()

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 default audit event, got %d", len(events))
	}
	got := events[0]
	if got.Action != "http.get" {
		t.Errorf("expected action http.get, got %q", got.Action)
	}
	if got.PrincipalID != "alice" || got.WorkspaceID != "default" {
		t.Errorf("principal fields missing: %+v", got)
	}
	if got.Metadata["path"] != "/api/pages/foo" {
		t.Errorf("path metadata missing: %+v", got.Metadata)
	}
	if got.Metadata["status"] != "200" {
		t.Errorf("status metadata: got %q", got.Metadata["status"])
	}
}

// @constraint RT-1 — middleware does NOT emit when a handler already
// audited via auditEnqueue. Avoids duplicate rows for the same action.
func TestAuditMiddlewareSkipsWhenHandlerAuditedExplicitly(t *testing.T) {
	s, sink := newAuditMiddlewareTestServer(t)

	semantic := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate a handler emitting a semantic action.
		s.auditEnqueue("workspace.create", r, "research", map[string]string{"name": "Research"})
		w.WriteHeader(http.StatusCreated)
	})

	w := httptest.NewRecorder()
	s.auditMiddleware(semantic).ServeHTTP(w, authenticatedReq(http.MethodPost, "/api/admin/workspaces"))
	flushDrainer()

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected exactly 1 audit event (semantic, no default), got %d: %+v", len(events), events)
	}
	if events[0].Action != "workspace.create" {
		t.Errorf("expected semantic action, got %q", events[0].Action)
	}
}

// @constraint RT-1 — unauthenticated requests do NOT generate an
// audit row. Health checks, login pre-auth, etc. stay quiet.
func TestAuditMiddlewareDoesNotEmitForUnauthenticatedRequests(t *testing.T) {
	s, sink := newAuditMiddlewareTestServer(t)

	noopHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// No principal in context.
	r := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	s.auditMiddleware(noopHandler).ServeHTTP(w, r)
	flushDrainer()

	if events := sink.snapshot(); len(events) != 0 {
		t.Fatalf("expected 0 events for unauthenticated request, got %d: %+v", len(events), events)
	}
}

// @constraint RT-1 — service-account principal (bearer-key path)
// produces an audit row attributable to the key prefix.
func TestAuditMiddlewareEmitsForServiceAccountPrincipal(t *testing.T) {
	s, sink := newAuditMiddlewareTestServer(t)

	noopHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	r := httptest.NewRequest(http.MethodGet, "/api/pages/home", nil)
	r = r.WithContext(principal.WithPrincipal(r.Context(), principal.Principal{
		Type:        principal.TypeServiceAccount,
		ID:          "phr_live_abcd1234efgh",
		WorkspaceID: "default",
		Role:        principal.RoleViewer,
	}))

	w := httptest.NewRecorder()
	s.auditMiddleware(noopHandler).ServeHTTP(w, r)
	flushDrainer()

	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].PrincipalType != string(principal.TypeServiceAccount) {
		t.Errorf("expected service_account, got %q", events[0].PrincipalType)
	}
	if events[0].PrincipalID != "phr_live_abcd1234efgh" {
		t.Errorf("expected key-prefix as id, got %q", events[0].PrincipalID)
	}
}

// @constraint RT-6 — recovery from handler panic still produces an
// audit row (deferred emission runs even after recover catches the
// panic). Note: middleware order in routes.go is auditMiddleware
// OUTSIDE recoverMiddleware, so the audit defer runs after recover
// converts the panic to a 500.
func TestAuditMiddlewareEmitsAfterPanicRecovery(t *testing.T) {
	s, sink := newAuditMiddlewareTestServer(t)

	panicker := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		panic("synthetic")
	})
	// Wrap order matches routes.go: audit then recover then handler.
	chain := s.auditMiddleware(recoverMiddleware(panicker))

	w := httptest.NewRecorder()
	chain.ServeHTTP(w, authenticatedReq(http.MethodPost, "/api/admin/users/1/suspend"))
	flushDrainer()

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 from recover, got %d", w.Code)
	}
	events := sink.snapshot()
	if len(events) != 1 {
		t.Fatalf("expected audit row even after panic, got %d", len(events))
	}
	if events[0].Metadata["status"] != "500" {
		t.Errorf("expected status=500 in metadata, got %q", events[0].Metadata["status"])
	}
}

func TestMarkRequestAuditedNilCellIsNoop(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	// No marker in context — must not panic.
	markRequestAudited(r)
}
