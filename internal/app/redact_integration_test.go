package app

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dhanesh/phronesis/internal/redact"
)

// captureSlog replaces slog.Default() with a redact-wrapped text
// handler writing to a bytes.Buffer, returning a cleanup hook.
//
// Mirrors the boot setup in cmd/phronesis/main.go so this test
// exercises the same redact pipeline production runs through.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(redact.NewSlogHandler(slog.NewTextHandler(&buf, nil))))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// TestRecoverMiddlewareDoesNotLeakBearerInPanicStack
//
// @constraint RT-6 — BINDING cross-cutting redaction at the
// panic-recover boundary.
// Satisfies: G7 closure (binding-cross-cutting wiring),
//
//	S2 (bearer tokens never appear in panic stacks).
func TestRecoverMiddlewareDoesNotLeakBearerInPanicStack(t *testing.T) {
	logBuf := captureSlog(t)

	// Handler that captures the bearer header into the panic value
	// — this is the exact failure mode the cross-cutting wiring
	// must defeat. If recoverMiddleware's redact pass works, the
	// captured value gets scrubbed before reaching the log buffer.
	panicker := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		panic("handler crashed; saw " + auth)
	})

	srv := httptest.NewServer(recoverMiddleware(panicker))
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/?code=secret_oauth_code_value", nil)
	req.Header.Set("Authorization", "Bearer phr_live_supersecrettoken123456")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 from recover, got %d", res.StatusCode)
	}

	logs := logBuf.String()
	for _, secret := range []string{
		"phr_live_supersecrettoken123456",
		"secret_oauth_code_value",
	} {
		if strings.Contains(logs, secret) {
			t.Fatalf("panic log leaked %q:\n%s", secret, logs)
		}
	}
	// Sanity: the redaction marker should be present, proving the
	// scrub path executed.
	if !strings.Contains(logs, redact.Redacted) {
		t.Fatalf("expected redaction marker in panic log:\n%s", logs)
	}
	// Context preservation: panic + path markers should survive.
	if !strings.Contains(logs, "panic recovered") {
		t.Fatalf("expected 'panic recovered' marker in log:\n%s", logs)
	}
}

// TestWriteErrorRedactsErrErrorString
//
// @constraint RT-6 — writeError is the cross-cutting funnel for
// JSON error responses; pushing redaction into it covers all ~30
// call sites in the package.
// Satisfies: G7 closure, S2.
func TestWriteErrorRedactsErrErrorString(t *testing.T) {
	w := httptest.NewRecorder()
	// Simulated err whose message embeds a bearer-shaped token.
	// The real-world equivalent is e.g. an upstream HTTP client
	// surfacing a request URL with ?code=... in its error.
	err := errors.New("upstream call failed for code=secret_value_123 (cause: network)")
	writeError(w, http.StatusBadGateway, err.Error())

	body := w.Body.String()
	if strings.Contains(body, "secret_value_123") {
		t.Fatalf("writeError leaked secret: %s", body)
	}
	// JSON encoder escapes the angle brackets to < / >
	// (HTML-safe default). The inner "redacted" word survives
	// untouched and is the simplest reliable evidence the marker
	// landed.
	if !strings.Contains(body, "redacted") {
		t.Fatalf("expected redaction marker in error body: %s", body)
	}
	if !strings.Contains(body, `"error":`) {
		t.Fatalf("error envelope corrupted: %s", body)
	}
}

// TestWriteErrorRedactsBearerInMessage
//
// Defense-in-depth: even direct calls to writeError that pass a
// raw Authorization header (which a future careless handler might
// do) get scrubbed.
func TestWriteErrorRedactsBearerInMessage(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusUnauthorized,
		"validation failed: Authorization: Bearer phr_live_xxxxxxxxxxxxxxxxxxxx")

	body := w.Body.String()
	if strings.Contains(body, "phr_live_xxxxxxxxxxxxxxxxxxxx") {
		t.Fatalf("writeError leaked bearer token: %s", body)
	}
}

// TestWriteErrorPlainMessageRoundTripsCleanly
//
// Static-string callers (~95% of writeError sites) shouldn't be
// affected by the regex pass.
func TestWriteErrorPlainMessageRoundTripsCleanly(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, "invalid request body")
	body := w.Body.String()
	if !strings.Contains(body, `"invalid request body"`) {
		t.Fatalf("plain message mutated: %s", body)
	}
	if strings.Contains(body, "<redacted>") {
		t.Fatalf("plain message should not produce redaction marker: %s", body)
	}
}
