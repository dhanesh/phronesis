package auth

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

// makeArgon2idPHC builds a PHC-formatted Argon2id hash for the given
// secret. Test-only helper; production hashes should use a higher
// memory cost than the params used here.
func makeArgon2idPHC(secret []byte) (string, error) {
	salt := []byte("0123456789ABCDEF")
	mem := uint32(64 * 1024)
	iter := uint32(2)
	para := uint8(1)
	keyLen := uint32(32)
	hash := argon2.IDKey(secret, salt, iter, mem, para, keyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		mem, iter, para,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// TestBreakGlassDisabledByDefault
//
// @constraint S4 — disabled state means the route does not exist (404, not 401).
// Satisfies RT-13 evidence E29.
//
// The contract: when env unset, BreakGlassHandler() returns nil and the
// caller-side server.go is expected NOT to mount the route at all. So
// any caller that hits /admin/break-glass with no env config sees the
// mux's default 404, not a handler-emitted 401.
func TestBreakGlassDisabledByDefault(t *testing.T) {
	t.Setenv(BreakGlassConfigEnv, "")
	if BreakGlassEnabled() {
		t.Fatal("BreakGlassEnabled should be false when env unset")
	}
	if h := BreakGlassHandler(); h != nil {
		t.Fatal("BreakGlassHandler should return nil when env unset (caller does not mount the route)")
	}
}

func TestBreakGlassMalformedEnvIsDisabled(t *testing.T) {
	t.Setenv(BreakGlassConfigEnv, "not-a-phc-string")
	if BreakGlassEnabled() {
		t.Fatal("malformed env var should not enable break-glass")
	}
	if h := BreakGlassHandler(); h != nil {
		t.Fatal("malformed env var should produce nil handler")
	}
}

func TestBreakGlassMalformedAlgIsDisabled(t *testing.T) {
	// Wrong algorithm name (argon2i instead of argon2id) — should fail parse.
	bad := "$argon2i$v=19$m=65536,t=2,p=1$MDEyMzQ1Njc4OUFCQ0RFRg$" + base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	t.Setenv(BreakGlassConfigEnv, bad)
	if BreakGlassEnabled() {
		t.Fatal("argon2i should be rejected (we require argon2id)")
	}
}

// TestBreakGlassAcceptsCorrectSecretRejectsWrong
//
// @constraint S4 — break-glass authenticates the configured secret and
// rejects any other input. The audit-on-use side is asserted in
// TestBreakGlassEmitsAuditOnSuccess.
func TestBreakGlassAcceptsCorrectSecretRejectsWrong(t *testing.T) {
	secret := []byte("correct-horse-battery-staple")
	phc, err := makeArgon2idPHC(secret)
	if err != nil {
		t.Fatalf("makePHC: %v", err)
	}
	t.Setenv(BreakGlassConfigEnv, phc)

	h := BreakGlassHandler()
	if h == nil {
		t.Fatal("handler nil despite valid env")
	}

	// Correct secret -> 200
	req := httptest.NewRequest(http.MethodPost, "/admin/break-glass", nil)
	req.Header.Set(BreakGlassHeader, string(secret))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("correct secret should return 200, got %d (%s)", w.Code, w.Body.String())
	}

	// Wrong secret -> 401, no leak of the candidate or the configured value
	req = httptest.NewRequest(http.MethodPost, "/admin/break-glass", nil)
	req.Header.Set(BreakGlassHeader, "wrong-guess")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret should return 401, got %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "correct-horse") {
		t.Fatalf("response leaked secret: %s", w.Body.String())
	}

	// Empty header -> 401
	req = httptest.NewRequest(http.MethodPost, "/admin/break-glass", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("empty header should return 401, got %d", w.Code)
	}
}

// TestBreakGlassEmitsAuditOnSuccess
//
// @constraint S4 — every successful use emits a severity=high audit signal.
// Satisfies RT-13 evidence E30.
func TestBreakGlassEmitsAuditOnSuccess(t *testing.T) {
	secret := []byte("audit-me-secret")
	phc, _ := makeArgon2idPHC(secret)
	t.Setenv(BreakGlassConfigEnv, phc)

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	h := BreakGlassHandler()
	req := httptest.NewRequest(http.MethodPost, "/admin/break-glass", nil)
	req.Header.Set(BreakGlassHeader, string(secret))
	h.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	if !strings.Contains(out, "severity=high") {
		t.Fatalf("expected severity=high in slog output:\n%s", out)
	}
	if !strings.Contains(out, "event=breakglass.use") {
		t.Fatalf("expected event=breakglass.use:\n%s", out)
	}
}

func TestBreakGlassRejectsNonPost(t *testing.T) {
	secret := []byte("any-secret")
	phc, _ := makeArgon2idPHC(secret)
	t.Setenv(BreakGlassConfigEnv, phc)

	h := BreakGlassHandler()
	req := httptest.NewRequest(http.MethodGet, "/admin/break-glass", nil)
	req.Header.Set(BreakGlassHeader, string(secret))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET should return 405, got %d", w.Code)
	}
	if !strings.Contains(w.Header().Get("Allow"), http.MethodPost) {
		t.Fatalf("405 should advertise Allow: POST, got %q", w.Header().Get("Allow"))
	}
}
