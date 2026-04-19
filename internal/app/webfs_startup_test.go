package app

import (
	"bytes"
	"context"
	"log"
	"strings"
	"testing"
)

// TestNewServerLogsFrontendMode verifies the frontend-mode signal fires
// at server construction time, so operators don't have to wait for a
// first HTTP request to learn whether the binary is running the dev
// stub or the embedded production assets.
//
// Review response I5/M5: the sync.Once in internal/webfs was removed so
// this check is now a simple log-capture test.
//
// @constraint RT-9 — loud startup signal validation criterion
func TestNewServerLogsFrontendMode(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })

	cfg := Config{
		PagesDir:      t.TempDir(),
		AdminUser:     "admin",
		AdminPassword: "admin123",
	}
	srv, err := NewServer(cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close(context.Background()) })

	out := buf.String()
	// Under `go test` (no -tags=prod) we always hit the stub branch.
	if !strings.Contains(out, "dev-stub frontend active") {
		t.Errorf("expected dev-stub startup warning in log output, got: %q", out)
	}
	if !strings.Contains(out, "make build") {
		t.Errorf("warning should mention `make build` remediation, got: %q", out)
	}
}
