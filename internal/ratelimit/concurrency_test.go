package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dhanesh/phronesis/internal/principal"
)

// @constraint S6 — Acquire returns false at the cap; Release frees a
// slot.
func TestSemaphoreAcquireAndRelease(t *testing.T) {
	s := NewSemaphore(2)
	if !s.Acquire("a") {
		t.Fatal("first Acquire should succeed")
	}
	if !s.Acquire("a") {
		t.Fatal("second Acquire should succeed at cap=2")
	}
	if s.Acquire("a") {
		t.Error("third Acquire for a at cap=2 should fail")
	}
	if s.InFlight("a") != 2 {
		t.Errorf("InFlight = %d; want 2", s.InFlight("a"))
	}
	s.Release("a")
	if !s.Acquire("a") {
		t.Error("Acquire after Release should succeed")
	}
}

// @constraint S6 — keys are isolated; one rogue key cannot starve
// another.
func TestSemaphoreIsolatesKeys(t *testing.T) {
	s := NewSemaphore(1)
	if !s.Acquire("a") {
		t.Fatal("Acquire a should succeed")
	}
	if !s.Acquire("b") {
		t.Error("Acquire b should succeed despite a being at cap")
	}
}

// @constraint S6 — over-release is a no-op (defensive against double
// release in handler chains).
func TestSemaphoreOverReleaseIsNoop(t *testing.T) {
	s := NewSemaphore(2)
	_ = s.Acquire("a")
	s.Release("a")
	s.Release("a") // already at zero — must not panic or go negative
	if s.InFlight("a") != 0 {
		t.Errorf("InFlight = %d; want 0", s.InFlight("a"))
	}
}

// @constraint S6 — disabled semaphore (max <= 0) always permits.
// Matches the convention from the sliding-window Limiter.
func TestSemaphoreDisabledAlwaysAllows(t *testing.T) {
	s := NewSemaphore(0)
	for i := 0; i < 100; i++ {
		if !s.Acquire("a") {
			t.Fatalf("iter %d: disabled semaphore should never reject", i)
		}
	}
}

func TestSemaphoreNilReceiverIsSafe(t *testing.T) {
	var s *Semaphore
	if !s.Acquire("anything") {
		t.Error("nil semaphore should always permit")
	}
	s.Release("anything") // must not panic
	if s.InFlight("x") != 0 {
		t.Error("nil InFlight should return 0")
	}
}

// @constraint S6 — concurrent Acquire/Release is race-free under
// `go test -race`.
func TestSemaphoreConcurrentAccessIsRaceFree(t *testing.T) {
	s := NewSemaphore(10)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.Acquire("hot") {
				time.Sleep(time.Microsecond)
				s.Release("hot")
			}
		}()
	}
	wg.Wait()
	if got := s.InFlight("hot"); got != 0 {
		t.Errorf("after wg.Wait InFlight = %d; want 0", got)
	}
}

func newOKHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// @constraint S6 — middleware skips unauthenticated requests
// (per-IP floor handles those).
func TestPerKeyConcurrencyMiddlewareSkipsUnauthenticated(t *testing.T) {
	sem := NewSemaphore(1)
	h := PerKeyConcurrencyMiddleware(PerKeyConcurrencyConfig{Semaphore: sem}, newOKHandler())

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("iter %d: expected 200, got %d", i, rec.Code)
		}
	}
}

// @constraint S6 — middleware skips user-cookie principals; only
// service-account requests are gated.
func TestPerKeyConcurrencyMiddlewareSkipsUserPrincipals(t *testing.T) {
	sem := NewSemaphore(1)
	h := PerKeyConcurrencyMiddleware(PerKeyConcurrencyConfig{Semaphore: sem}, newOKHandler())

	user := principal.Principal{Type: principal.TypeUser, ID: "alice", WorkspaceID: "default", Role: principal.RoleAdmin}
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
		req = req.WithContext(principal.WithPrincipal(req.Context(), user))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("iter %d: expected 200 for user, got %d", i, rec.Code)
		}
	}
}

// @constraint S6 — service-account requests beyond the cap return 429
// + Retry-After. Held in-flight requests block; released ones unblock.
func TestPerKeyConcurrencyMiddlewareEnforcesCap(t *testing.T) {
	sem := NewSemaphore(2)

	// Block the inner handler so we can observe the cap rejecting a
	// 3rd in-flight request.
	hold := make(chan struct{})
	release := make(chan struct{})
	var inFlight atomic.Int32

	blocking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inFlight.Add(1)
		<-hold
		inFlight.Add(-1)
		w.WriteHeader(http.StatusOK)
	})
	wrapped := PerKeyConcurrencyMiddleware(PerKeyConcurrencyConfig{Semaphore: sem}, blocking)

	sa := principal.Principal{Type: principal.TypeServiceAccount, ID: "phr_oauth_client_x", WorkspaceID: "default", Role: principal.RoleEditor}

	doReq := func(rec *httptest.ResponseRecorder) {
		req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
		req = req.WithContext(principal.WithPrincipal(req.Context(), sa))
		wrapped.ServeHTTP(rec, req)
	}

	// Two parallel acquires should pass.
	rec1 := httptest.NewRecorder()
	rec2 := httptest.NewRecorder()
	go doReq(rec1)
	go doReq(rec2)

	// Wait until both are in-flight.
	deadline := time.Now().Add(time.Second)
	for inFlight.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if inFlight.Load() != 2 {
		close(hold)
		t.Fatalf("expected 2 in-flight, got %d", inFlight.Load())
	}

	// Third — synchronous, should hit cap and return 429.
	rec3 := httptest.NewRecorder()
	doReq(rec3)
	if rec3.Code != http.StatusTooManyRequests {
		t.Errorf("third request status = %d; want 429", rec3.Code)
	}
	if got := rec3.Header().Get("Retry-After"); got != "1" {
		t.Errorf("Retry-After = %q; want 1", got)
	}

	// Release the held requests and confirm a subsequent request
	// succeeds (Release frees a slot).
	close(hold)
	go func() { close(release) }()
	<-release
	deadline = time.Now().Add(time.Second)
	for inFlight.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	rec4 := httptest.NewRecorder()
	hold = make(chan struct{})
	close(hold)
	doReq(rec4)
	if rec4.Code != http.StatusOK {
		t.Errorf("after release status = %d; want 200", rec4.Code)
	}
}

// @constraint S6 — OnDeny callback fires on rejection.
func TestPerKeyConcurrencyMiddlewareOnDenyFires(t *testing.T) {
	sem := NewSemaphore(1)
	var seenKey, seenPath string
	hold := make(chan struct{})
	defer close(hold)

	blocking := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-hold
	})
	wrapped := PerKeyConcurrencyMiddleware(PerKeyConcurrencyConfig{
		Semaphore: sem,
		OnDeny:    func(k, p string) { seenKey, seenPath = k, p },
	}, blocking)

	sa := principal.Principal{Type: principal.TypeServiceAccount, ID: "phr_oauth_x", WorkspaceID: "default", Role: principal.RoleEditor}
	doReq := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/y", nil)
		req = req.WithContext(principal.WithPrincipal(req.Context(), sa))
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		return rec
	}

	go doReq() // hold a slot
	time.Sleep(10 * time.Millisecond)
	doReq() // synchronous — must be denied

	if seenKey != "phr_oauth_x" {
		t.Errorf("OnDeny key = %q; want phr_oauth_x", seenKey)
	}
	if seenPath != "/api/y" {
		t.Errorf("OnDeny path = %q; want /api/y", seenPath)
	}
}

// @constraint S6 — a panic inside the inner handler MUST still release
// the slot (defer pattern correctness — otherwise a buggy tool poisons
// every subsequent request from that key).
func TestPerKeyConcurrencyMiddlewareReleasesOnPanic(t *testing.T) {
	sem := NewSemaphore(1)
	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	wrapped := PerKeyConcurrencyMiddleware(PerKeyConcurrencyConfig{Semaphore: sem}, panicHandler)

	sa := principal.Principal{Type: principal.TypeServiceAccount, ID: "phr_oauth_x", WorkspaceID: "default", Role: principal.RoleEditor}
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(), sa))

	func() {
		defer func() { _ = recover() }()
		wrapped.ServeHTTP(httptest.NewRecorder(), req)
	}()

	if got := sem.InFlight("phr_oauth_x"); got != 0 {
		t.Errorf("InFlight after panic = %d; want 0 (defer Release must run)", got)
	}
}

func TestPerKeyConcurrencyMiddlewarePanicsWithoutSemaphore(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when Semaphore is nil")
		}
	}()
	PerKeyConcurrencyMiddleware(PerKeyConcurrencyConfig{}, newOKHandler())
}
