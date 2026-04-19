package media

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dhanesh/phronesis/internal/blob"
	"github.com/dhanesh/phronesis/internal/principal"
)

func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	store, err := blob.NewLocalFSStore(t.TempDir(), blob.Config{QuotaBytes: 1024 * 1024})
	if err != nil {
		t.Fatalf("blob store: %v", err)
	}
	return NewHandler(store, 0 /* default 2MB */)
}

// Attaches a Principal to the request context (mimics auth middleware).
func withPrincipal(r *http.Request, p principal.Principal) *http.Request {
	return r.WithContext(principal.WithPrincipal(r.Context(), p))
}

// @constraint RT-6.2 S6
// Upload path: POST /media with allowed content-type returns 201 + /media/<hash> URL.
func TestUploadReturnsContentAddressedURL(t *testing.T) {
	h := newTestHandler(t)
	mux := http.NewServeMux()
	h.Routes(mux)

	body := []byte("\x89PNG\r\n\x1a\nhello-world-png")
	req := httptest.NewRequest("POST", "/media", bytes.NewReader(body))
	req.Header.Set("Content-Type", "image/png")
	req = withPrincipal(req, principal.Principal{
		Type: principal.TypeUser, ID: "alice", WorkspaceID: "ws", Role: principal.RoleEditor,
	})
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("status: got %d, want 201; body=%s", rr.Code, rr.Body.String())
	}
	var resp uploadResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(resp.URL, "/media/") {
		t.Errorf("URL: got %q, want /media/<hash>", resp.URL)
	}
	if resp.ContentType != "image/png" {
		t.Errorf("ContentType: got %q, want image/png", resp.ContentType)
	}
	if resp.Size != int64(len(body)) {
		t.Errorf("Size: got %d, want %d", resp.Size, len(body))
	}
}

// @constraint RT-6.2 S6
// Upload rejects disallowed content-types with HTTP 415.
func TestUploadRejectsBadContentType(t *testing.T) {
	h := newTestHandler(t)
	mux := http.NewServeMux()
	h.Routes(mux)

	req := httptest.NewRequest("POST", "/media", strings.NewReader("<html>"))
	req.Header.Set("Content-Type", "text/html")
	req = withPrincipal(req, principal.Principal{Type: principal.TypeUser, WorkspaceID: "ws", Role: principal.RoleEditor})
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status: got %d, want 415", rr.Code)
	}
}

// @constraint RT-6.2 S1 B2
// Upload without authentication returns 401.
func TestUploadUnauthenticated(t *testing.T) {
	h := newTestHandler(t)
	mux := http.NewServeMux()
	h.Routes(mux)

	req := httptest.NewRequest("POST", "/media", strings.NewReader("x"))
	req.Header.Set("Content-Type", "image/png")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rr.Code)
	}
}

// @constraint RT-6.2 B2
// Upload as viewer returns 403.
func TestUploadForbiddenForViewer(t *testing.T) {
	h := newTestHandler(t)
	mux := http.NewServeMux()
	h.Routes(mux)

	req := httptest.NewRequest("POST", "/media", strings.NewReader("x"))
	req.Header.Set("Content-Type", "image/png")
	req = withPrincipal(req, principal.Principal{Type: principal.TypeUser, WorkspaceID: "ws", Role: principal.RoleViewer})
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status: got %d, want 403", rr.Code)
	}
}

// @constraint RT-6.2 S5
// Uploads exceeding maxUploadBytes return 413.
func TestUploadRejectsOversizedBody(t *testing.T) {
	store, _ := blob.NewLocalFSStore(t.TempDir(), blob.Config{QuotaBytes: 1024 * 1024})
	h := NewHandler(store, 64) // tiny cap so test is fast
	mux := http.NewServeMux()
	h.Routes(mux)

	big := make([]byte, 256)
	req := httptest.NewRequest("POST", "/media", bytes.NewReader(big))
	req.Header.Set("Content-Type", "image/png")
	req = withPrincipal(req, principal.Principal{Type: principal.TypeUser, WorkspaceID: "ws", Role: principal.RoleEditor})
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status: got %d, want 413", rr.Code)
	}
}

// @constraint RT-6.2
// GET /media/<hash> round-trips the uploaded bytes for an authorized viewer.
func TestGetRoundTrip(t *testing.T) {
	h := newTestHandler(t)
	mux := http.NewServeMux()
	h.Routes(mux)

	data := []byte("\x89PNG\r\n\x1a\nhello")
	// Upload first
	up := httptest.NewRequest("POST", "/media", bytes.NewReader(data))
	up.Header.Set("Content-Type", "image/png")
	up = withPrincipal(up, principal.Principal{Type: principal.TypeUser, WorkspaceID: "ws", Role: principal.RoleEditor})
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, up)
	if rr.Code != http.StatusCreated {
		t.Fatalf("upload: %d %s", rr.Code, rr.Body.String())
	}
	var resp uploadResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)

	// Now GET as a viewer (different role, read-allowed)
	req := httptest.NewRequest("GET", resp.URL, nil)
	req = withPrincipal(req, principal.Principal{Type: principal.TypeUser, WorkspaceID: "ws", Role: principal.RoleViewer})
	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, req)

	if rr2.Code != http.StatusOK {
		t.Fatalf("get: got %d, want 200; body=%s", rr2.Code, rr2.Body.String())
	}
	got, _ := io.ReadAll(rr2.Body)
	if !bytes.Equal(got, data) {
		t.Errorf("round-trip body mismatch")
	}
	if ct := rr2.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type: got %q, want image/png", ct)
	}
	if cc := rr2.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control: got %q, want contains immutable", cc)
	}
}

// @constraint RT-6.2
// GET /media/<hash> with an unknown hash returns 404.
func TestGetNotFound(t *testing.T) {
	h := newTestHandler(t)
	mux := http.NewServeMux()
	h.Routes(mux)

	req := httptest.NewRequest("GET", "/media/deadbeef", nil)
	req = withPrincipal(req, principal.Principal{Type: principal.TypeUser, WorkspaceID: "ws", Role: principal.RoleViewer})
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d, want 404", rr.Code)
	}
}

// @constraint RT-6.2
// GET /media/<hash> with invalid (non-hex) hash returns 400.
func TestGetInvalidHash(t *testing.T) {
	h := newTestHandler(t)
	mux := http.NewServeMux()
	h.Routes(mux)

	req := httptest.NewRequest("GET", "/media/NOT-HEX!!", nil)
	req = withPrincipal(req, principal.Principal{Type: principal.TypeUser, WorkspaceID: "ws", Role: principal.RoleViewer})
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want 400", rr.Code)
	}
}

// silence unused import warnings if the test file shrinks
var _ = context.Background
