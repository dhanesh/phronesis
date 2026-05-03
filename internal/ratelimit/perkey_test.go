package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dhanesh/phronesis/internal/principal"
)

func newOK() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// @constraint RT-7 / S6 — no principal in context = pass-through. The
// middleware does not punish unauthenticated requests; per-IP floor
// (RT-10) handles those.
func TestPerKeyMiddlewarePassesThroughWithoutPrincipal(t *testing.T) {
	lim := NewLimiter(time.Minute, 1)
	h := PerKeyMiddleware(PerKeyConfig{Limiter: lim}, newOK())

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/anything", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("iter %d: expected 200, got %d", i, rec.Code)
		}
	}
}

// @constraint RT-7 / S6 — user-cookie principals are not rate-limited
// per-key. Their request volume is bounded by what one human can issue.
func TestPerKeyMiddlewareSkipsUserPrincipals(t *testing.T) {
	lim := NewLimiter(time.Minute, 1)
	h := PerKeyMiddleware(PerKeyConfig{Limiter: lim}, newOK())

	user := principal.Principal{
		Type:        principal.TypeUser,
		ID:          "alice",
		WorkspaceID: "default",
		Role:        principal.RoleAdmin,
	}
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/anything", nil)
		req = req.WithContext(principal.WithPrincipal(req.Context(), user))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("iter %d: expected 200 for user, got %d", i, rec.Code)
		}
	}
}

// @constraint RT-7 / S6 — service-account principal IS rate-limited
// keyed on its prefix (Principal.ID).
func TestPerKeyMiddlewareLimitsServiceAccount(t *testing.T) {
	lim := NewLimiter(time.Minute, 2)
	now := time.Date(2026, 5, 3, 12, 0, 0, 0, time.UTC)
	h := PerKeyMiddleware(PerKeyConfig{
		Limiter: lim,
		Now:     func() time.Time { return now },
	}, newOK())

	sa := principal.Principal{
		Type:        principal.TypeServiceAccount,
		ID:          "phr_live_aaaa1111bbbb",
		WorkspaceID: "default",
		Role:        principal.RoleEditor,
	}

	// First 2 requests fit within max=2.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
		req = req.WithContext(principal.WithPrincipal(req.Context(), sa))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("iter %d: expected 200, got %d", i, rec.Code)
		}
	}
	// Third request crosses the budget.
	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req = req.WithContext(principal.WithPrincipal(req.Context(), sa))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "60" {
		t.Errorf("expected Retry-After=60, got %q", got)
	}
}

// @constraint RT-7 / S6 — buckets are independent across distinct
// service-account prefixes; one rogue key cannot starve another.
func TestPerKeyMiddlewareIsolatesPrefixes(t *testing.T) {
	lim := NewLimiter(time.Minute, 1)
	h := PerKeyMiddleware(PerKeyConfig{Limiter: lim}, newOK())

	a := principal.Principal{Type: principal.TypeServiceAccount, ID: "phr_live_aaaaaaaaaaaa", WorkspaceID: "default", Role: principal.RoleEditor}
	b := principal.Principal{Type: principal.TypeServiceAccount, ID: "phr_live_bbbbbbbbbbbb", WorkspaceID: "default", Role: principal.RoleEditor}

	// Burn A's budget.
	doReq := func(p principal.Principal) int {
		req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
		req = req.WithContext(principal.WithPrincipal(req.Context(), p))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := doReq(a); got != http.StatusOK {
		t.Fatalf("a-1: expected 200, got %d", got)
	}
	if got := doReq(a); got != http.StatusTooManyRequests {
		t.Fatalf("a-2: expected 429, got %d", got)
	}
	// B should still have its full budget.
	if got := doReq(b); got != http.StatusOK {
		t.Fatalf("b-1: expected 200, got %d", got)
	}
}

// @constraint RT-7 / S6 — OnDeny callback fires on rejection, carrying
// the prefix and path for audit/metrics.
func TestPerKeyMiddlewareOnDenyFires(t *testing.T) {
	lim := NewLimiter(time.Minute, 1)
	var seenKey, seenPath string
	h := PerKeyMiddleware(PerKeyConfig{
		Limiter: lim,
		OnDeny:  func(k, p string) { seenKey, seenPath = k, p },
	}, newOK())

	sa := principal.Principal{Type: principal.TypeServiceAccount, ID: "phr_live_xxxxxxxx", WorkspaceID: "default", Role: principal.RoleEditor}

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/path-of-deny", nil)
		req = req.WithContext(principal.WithPrincipal(req.Context(), sa))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		_ = rec
	}

	if seenKey != "phr_live_xxxxxxxx" {
		t.Errorf("expected OnDeny key=phr_live_xxxxxxxx, got %q", seenKey)
	}
	if seenPath != "/api/path-of-deny" {
		t.Errorf("expected OnDeny path=/api/path-of-deny, got %q", seenPath)
	}
}

// @constraint RT-7 — disabled limiter (window=0 / max=0) never denies.
// Keeps test/CI configs that don't care about per-key floors permissive.
func TestPerKeyMiddlewareDisabledLimiterAlwaysAllows(t *testing.T) {
	lim := NewLimiter(0, 0)
	h := PerKeyMiddleware(PerKeyConfig{Limiter: lim}, newOK())

	sa := principal.Principal{Type: principal.TypeServiceAccount, ID: "phr_live_zzzzzzzz", WorkspaceID: "default", Role: principal.RoleEditor}
	for i := 0; i < 1000; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
		req = req.WithContext(principal.WithPrincipal(req.Context(), sa))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("iter %d: expected 200 from disabled limiter, got %d", i, rec.Code)
		}
	}
}

func TestPerKeyMiddlewarePanicsWithoutLimiter(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic when Limiter is nil")
		}
	}()
	PerKeyMiddleware(PerKeyConfig{}, newOK())
}
