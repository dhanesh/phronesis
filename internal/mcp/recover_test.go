package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// @constraint RT-11 / T6 — a deliberate panic inside the MCP handler
// is caught and returned as a JSON-RPC internal-error envelope; the
// server stays responsive afterwards.
func TestRecoverMiddlewareCatchesPanic(t *testing.T) {
	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("deliberate panic in MCP tool dispatcher")
	})
	wrapped := RecoverMiddleware(panicHandler)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200 (JSON-RPC envelope, not HTTP error)", rec.Code)
	}
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("body should be JSON-RPC envelope: %v; body=%s", err, rec.Body.String())
	}
	if resp.Error == nil {
		t.Fatal("expected error envelope, got success")
	}
	if resp.Error.Code != ErrCodeInternalError {
		t.Errorf("error.code = %d; want %d", resp.Error.Code, ErrCodeInternalError)
	}
	if resp.Error.Message != "internal error" {
		t.Errorf("error.message = %q; want internal error", resp.Error.Message)
	}
}

// @constraint RT-11 / T6 — non-panicking handlers pass through
// unchanged.
func TestRecoverMiddlewareNoOpOnSuccess(t *testing.T) {
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	})
	wrapped := RecoverMiddleware(okHandler)

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200", rec.Code)
	}
	if rec.Body.String() != `{"jsonrpc":"2.0","id":1,"result":{}}` {
		t.Errorf("body = %q; pass-through expected", rec.Body.String())
	}
}

// @constraint RT-11 / T6 — multiple sequential panics are each caught
// independently. A handler that always panics MUST NOT poison the
// middleware (defer pattern correctness).
func TestRecoverMiddlewareIsRepeatable(t *testing.T) {
	panicHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("again")
	})
	wrapped := RecoverMiddleware(panicHandler)

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("iter %d: status = %d; want 200", i, rec.Code)
		}
	}
}
