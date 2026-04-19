//go:build !prod

package webfs

import (
	"io/fs"
	"strings"
	"testing"
)

// @constraint T3 — embedded frontend FS is available at runtime
// @constraint T5 — tests run without a prior `npm run build`
// @constraint RT-9 — dev stub behavior
func TestStubFSServesPlaceholderIndex(t *testing.T) {
	data, err := fs.ReadFile(FS(), "index.html")
	if err != nil {
		t.Fatalf("read index.html from stub: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "dev stub") && !strings.Contains(body, "Dev stub") {
		t.Errorf("stub index.html must self-identify as dev stub; body had: %.200s", body)
	}
}

// @constraint RT-9 — IsStub correctly reports dev build
func TestIsStubIsTrueInDevBuild(t *testing.T) {
	if !IsStub() {
		t.Error("IsStub() must return true in dev build (no -tags=prod)")
	}
}

// @constraint RT-9 — FS is callable multiple times without panic
func TestFSIsIdempotent(t *testing.T) {
	a := FS()
	b := FS()
	if a == nil || b == nil {
		t.Fatal("FS() must not return nil")
	}
	// Both handles must find the same file.
	if _, err := fs.ReadFile(a, "index.html"); err != nil {
		t.Fatalf("first FS handle: %v", err)
	}
	if _, err := fs.ReadFile(b, "index.html"); err != nil {
		t.Fatalf("second FS handle: %v", err)
	}
}
