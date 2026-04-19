package app

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestShutdownClosesActiveSSEConnectionPromptly verifies that
// http.Server.Shutdown doesn't block on long-lived SSE handlers.
//
// Bug this guards against: the SSE handler at handleEvents() previously
// only listened to r.Context().Done(), which fires on client disconnect
// but NOT on server-initiated shutdown. An active /api/pages/<name>/events
// stream held http.Shutdown for the full drainTimeout budget (observed
// 30s on Ctrl-C with a browser open).
//
// Fix: RegisterOnShutdown callback closes s.shutdownCh, which the SSE
// select now listens to.
//
// @constraint O5 — graceful drain stays bounded under long-lived requests
// @constraint INT-10 — full shutdown path
func TestShutdownClosesActiveSSEConnectionPromptly(t *testing.T) {
	cfg := Config{
		PagesDir:      t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "admin123",
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// Wrap the server's handler in an httptest listener so we can make
	// real HTTP requests without racing the real Server.Serve loop.
	ts := httptest.NewServer(srv.http.Handler)
	t.Cleanup(ts.Close)

	// Log in so the SSE endpoint accepts the request. Cookie jar carries
	// the session cookie to subsequent calls on the same client.
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	loginResp, err := client.Post(ts.URL+"/api/login", "application/json",
		strings.NewReader(`{"username":"admin","password":"admin123"}`))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	_ = loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", loginResp.StatusCode)
	}

	// Open an SSE connection. Don't drain it — just leave it open so the
	// SSE handler is blocked in its select loop.
	sseCtx, sseCancel := context.WithCancel(context.Background())
	defer sseCancel()
	req, err := http.NewRequestWithContext(sseCtx, http.MethodGet, ts.URL+"/api/pages/home/events", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	// Use a client without the overall timeout for the SSE request itself.
	sseClient := &http.Client{Jar: jar}
	resp, err := sseClient.Do(req)
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("SSE content-type = %q, want text/event-stream", ct)
	}
	// Give the SSE handler a moment to actually enter its select loop.
	// The handler emits nothing before the 15s heartbeat fires, so a
	// short sleep is the simplest guarantee the goroutine is parked.
	time.Sleep(20 * time.Millisecond)

	// Now: close srv.http with a generous budget. With the fix, this
	// returns in <100ms because RegisterOnShutdown closes shutdownCh and
	// the SSE handler's select wakes. Without the fix, this waits the
	// full 2s and fails.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	err = srv.http.Shutdown(shutdownCtx)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Shutdown error: %v (took %v)", err, elapsed)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("Shutdown took %v, expected <500ms — SSE handler likely did not respect server-shutdown signal", elapsed)
	}

	// Drain background goroutines to satisfy the test's cleanup invariants.
	closeCtx, ccancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer ccancel()
	if err := srv.Close(closeCtx); err != nil {
		t.Logf("Close returned (expected OK): %v", err)
	}
}
