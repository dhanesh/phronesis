package ratelimit

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

// passthroughCounter is a test handler that counts invocations.
type passthroughCounter struct{ n int }

func (p *passthroughCounter) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	p.n++
	w.WriteHeader(http.StatusOK)
}

// @constraint RT-10 RT-10.1
// Middleware allows requests under the limit and denies 429 above it.
func TestMiddlewareAllowsThenDenies(t *testing.T) {
	l := NewLimiter(time.Minute, 2)
	h := &passthroughCounter{}
	m := Middleware(Config{
		Limiter:      l,
		PathPrefixes: []string{"/api/login"},
		Now:          func() time.Time { return time.Unix(1_700_000_000, 0) },
	}, h)

	// 2 requests from same IP pass.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/login", nil)
		req.RemoteAddr = "10.0.0.5:1234"
		rr := httptest.NewRecorder()
		m.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("req %d: got %d, want 200", i+1, rr.Code)
		}
	}

	// 3rd request is denied.
	req := httptest.NewRequest(http.MethodPost, "/api/login", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	rr := httptest.NewRecorder()
	m.ServeHTTP(rr, req)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("denied req: got %d, want 429", rr.Code)
	}
	if got := rr.Header().Get("Retry-After"); got == "" {
		t.Error("429 response should include Retry-After header")
	}
	if h.n != 2 {
		t.Errorf("next handler invoked %d times, want 2", h.n)
	}
}

// @constraint RT-10
// Middleware passes through requests whose path is NOT in PathPrefixes.
func TestMiddlewareIgnoresUngatedPaths(t *testing.T) {
	l := NewLimiter(time.Minute, 1) // aggressive
	h := &passthroughCounter{}
	m := Middleware(Config{
		Limiter:      l,
		PathPrefixes: []string{"/api/login"},
	}, h)

	// 10 requests to an ungated path all pass.
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
		req.RemoteAddr = "10.0.0.5:1234"
		rr := httptest.NewRecorder()
		m.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("/api/health req %d: got %d, want 200", i, rr.Code)
		}
	}
	if h.n != 10 {
		t.Errorf("next invocation count: got %d, want 10", h.n)
	}
}

// @constraint RT-10.2
// ResolveKey honors X-Forwarded-For only when the peer is in TrustedProxies.
func TestResolveKeyTrustedProxy(t *testing.T) {
	trusted := MustParsePrefixes("10.0.0.0/8")

	// Peer in trusted range -> XFF leftmost wins.
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.1.2.3:9999"
	req.Header.Set("X-Forwarded-For", "203.0.113.45, 10.1.2.3")
	if got := ResolveKey(req, trusted); got != "203.0.113.45" {
		t.Errorf("trusted peer: got %q, want 203.0.113.45", got)
	}

	// Peer NOT in trusted range -> XFF ignored, direct peer used.
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.RemoteAddr = "192.168.1.5:9999"
	req2.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := ResolveKey(req2, trusted); got != "192.168.1.5" {
		t.Errorf("untrusted peer: got %q, want 192.168.1.5 (XFF must be ignored)", got)
	}
}

// @constraint RT-10.2
// No trusted proxies configured -> direct peer always wins.
func TestResolveKeyNoTrustedProxies(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "203.0.113.45:12345"
	req.Header.Set("X-Forwarded-For", "1.2.3.4") // must be ignored
	if got := ResolveKey(req, nil); got != "203.0.113.45" {
		t.Errorf("no trusted: got %q, want 203.0.113.45", got)
	}
}

// @constraint RT-10.2
// Empty XFF falls back to direct peer even when peer is trusted.
func TestResolveKeyEmptyXFFFallsBack(t *testing.T) {
	trusted := MustParsePrefixes("10.0.0.0/8")
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.5.5.5:12345"
	// No XFF header.
	if got := ResolveKey(req, trusted); got != "10.5.5.5" {
		t.Errorf("empty XFF: got %q, want 10.5.5.5", got)
	}
}

// @constraint RT-10.1
// Denied requests trigger the OnDeny callback (observability hook).
func TestMiddlewareOnDenyCallback(t *testing.T) {
	l := NewLimiter(time.Minute, 1)
	var denyKey, denyPath string

	m := Middleware(Config{
		Limiter:      l,
		PathPrefixes: []string{"/api/login"},
		OnDeny:       func(key, path string) { denyKey, denyPath = key, path },
	}, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }))

	// First request allowed.
	first := httptest.NewRequest("POST", "/api/login", nil)
	first.RemoteAddr = "10.0.0.5:1234"
	m.ServeHTTP(httptest.NewRecorder(), first)

	// Second request denied.
	second := httptest.NewRequest("POST", "/api/login", nil)
	second.RemoteAddr = "10.0.0.5:1234"
	rr := httptest.NewRecorder()
	m.ServeHTTP(rr, second)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second: got %d, want 429", rr.Code)
	}
	if denyKey != "10.0.0.5" {
		t.Errorf("OnDeny key: got %q, want 10.0.0.5", denyKey)
	}
	if denyPath != "/api/login" {
		t.Errorf("OnDeny path: got %q, want /api/login", denyPath)
	}
}

// @constraint RT-10
// Zero PathPrefixes gates everything (useful for global rate-limits).
func TestMiddlewareGatesAllPathsWhenPrefixesEmpty(t *testing.T) {
	l := NewLimiter(time.Minute, 1)
	h := &passthroughCounter{}
	m := Middleware(Config{Limiter: l}, h)

	first := httptest.NewRequest("GET", "/anything", nil)
	first.RemoteAddr = "10.0.0.5:1234"
	m.ServeHTTP(httptest.NewRecorder(), first)

	second := httptest.NewRequest("GET", "/anything/else", nil)
	second.RemoteAddr = "10.0.0.5:1234"
	rr := httptest.NewRecorder()
	m.ServeHTTP(rr, second)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("second: got %d, want 429 (empty prefixes = gate all)", rr.Code)
	}
}

// Helper: netip sanity checks so MustParsePrefixes can't silently drop values.
func TestMustParsePrefixesSanity(t *testing.T) {
	ps := MustParsePrefixes("10.0.0.0/8", "192.168.0.0/16")
	if len(ps) != 2 {
		t.Fatalf("parsed: got %d, want 2", len(ps))
	}
	if !ps[0].Contains(netip.MustParseAddr("10.5.5.5")) {
		t.Error("10.0.0.0/8 should contain 10.5.5.5")
	}
	if !ps[1].Contains(netip.MustParseAddr("192.168.1.1")) {
		t.Error("192.168.0.0/16 should contain 192.168.1.1")
	}
}
