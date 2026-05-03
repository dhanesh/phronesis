package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dhanesh/phronesis/internal/principal"
)

func newAuthedRequest(t *testing.T, method, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	p := principal.Principal{
		Type:        principal.TypeServiceAccount,
		ID:          "phr_oauth_client_x",
		WorkspaceID: "default",
		Role:        principal.RoleEditor,
	}
	_ = method // kept for symmetry; current tests don't switch
	return req.WithContext(principal.WithPrincipal(req.Context(), p))
}

// @constraint RT-2 — POST /mcp with method=initialize returns server
// info + capabilities AND an Mcp-Session-Id response header.
func TestServeInitializeReturnsSessionHeader(t *testing.T) {
	srv := NewServer()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`
	req := newAuthedRequest(t, "initialize", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200; body=%s", rec.Code, rec.Body.String())
	}
	sid := rec.Header().Get("Mcp-Session-Id")
	if sid == "" {
		t.Error("Mcp-Session-Id header missing")
	}
	if !strings.HasPrefix(sid, "mcp_") {
		t.Errorf("session id = %q; want mcp_ prefix", sid)
	}

	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.JSONRPC != "2.0" {
		t.Errorf("jsonrpc = %q; want 2.0", resp.JSONRPC)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not an object: %v", resp.Result)
	}
	if got := result["protocolVersion"]; got != ProtocolVersion {
		t.Errorf("protocolVersion = %v; want %s", got, ProtocolVersion)
	}
}

// @constraint RT-2 / RT-14 — tools/list returns a JSON-RPC success
// with the registered tools' descriptors.
func TestServeToolsListReturnsRegisteredTools(t *testing.T) {
	srv := NewServer()
	if err := srv.Tools.Register(EchoTool()); err != nil {
		t.Fatalf("Register: %v", err)
	}

	body := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	req := newAuthedRequest(t, "tools/list", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Error != nil {
		t.Errorf("error = %+v; want nil", resp.Error)
	}
	result, _ := resp.Result.(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools count = %d; want 1", len(tools))
	}
	first, _ := tools[0].(map[string]any)
	if first["name"] != "echo" {
		t.Errorf("first tool name = %v; want echo", first["name"])
	}
}

// @constraint RT-2 — empty registry serves an empty tools/list.
func TestServeToolsListEmptyWhenNoToolsRegistered(t *testing.T) {
	srv := NewServer()
	body := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	req := newAuthedRequest(t, "tools/list", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	result, _ := resp.Result.(map[string]any)
	tools, _ := result["tools"].([]any)
	if len(tools) != 0 {
		t.Errorf("tools = %v; want empty array", tools)
	}
}

func TestServePingReturnsEmptyResult(t *testing.T) {
	srv := NewServer()
	body := `{"jsonrpc":"2.0","id":3,"method":"ping"}`
	req := newAuthedRequest(t, "ping", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
}

// @constraint RT-14 — tools/call dispatches through the registry;
// echo round-trips the message verbatim.
func TestServeToolsCallEchoRoundTrips(t *testing.T) {
	srv := NewServer()
	_ = srv.Tools.Register(EchoTool())
	body := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"message":"hi"}}}`
	req := newAuthedRequest(t, "tools/call", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("error = %+v; want nil", resp.Error)
	}
	result, _ := resp.Result.(map[string]any)
	content, _ := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content length = %d; want 1", len(content))
	}
	first, _ := content[0].(map[string]any)
	if first["type"] != "text" {
		t.Errorf("content[0].type = %v; want text", first["type"])
	}
	raw, _ := first["text"].(string)
	if !strings.Contains(raw, `"message":"hi"`) {
		t.Errorf("content text = %q; want contains message:hi", raw)
	}
}

// @constraint RT-14 — tools/call with bad args returns
// -32602 invalid_params with NO side effect.
func TestServeToolsCallRejectsBadParams(t *testing.T) {
	srv := NewServer()
	_ = srv.Tools.Register(EchoTool())
	body := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"unknown":"x"}}}`
	req := newAuthedRequest(t, "tools/call", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != ErrCodeInvalidParams {
		t.Errorf("error code = %v; want -32602", resp.Error)
	}
}

// @constraint RT-14 — tools/call with unknown name returns
// -32601 method-not-found.
func TestServeToolsCallUnknownToolReturnsMethodNotFound(t *testing.T) {
	srv := NewServer()
	body := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"does-not-exist"}}`
	req := newAuthedRequest(t, "tools/call", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != ErrCodeMethodNotFound {
		t.Errorf("error code = %v; want -32601", resp.Error)
	}
}

// @constraint RT-2 — the auth gate rejects requests without a
// service-account principal in context. User principals MUST also
// be rejected — MCP is machine-to-machine.
func TestServeRejectsUnauthenticated(t *testing.T) {
	srv := NewServer()
	body := `{"jsonrpc":"2.0","id":1,"method":"ping"}`

	// No principal in context.
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no-principal status = %d; want 401", rec.Code)
	}

	// User principal present — still rejected (MCP is for service accounts).
	user := principal.Principal{Type: principal.TypeUser, ID: "alice", WorkspaceID: "default", Role: principal.RoleAdmin}
	req2 := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req2 = req2.WithContext(principal.WithPrincipal(req2.Context(), user))
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnauthorized {
		t.Errorf("user-principal status = %d; want 401", rec2.Code)
	}
}

// @constraint RT-2 — non-POST returns 405.
func TestServeRejectsNonPOST(t *testing.T) {
	srv := NewServer()
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d; want 405", rec.Code)
	}
}

func TestServeRejectsMalformedJSON(t *testing.T) {
	srv := NewServer()
	req := newAuthedRequest(t, "x", "not json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != ErrCodeParseError {
		t.Errorf("error code = %v; want -32700", resp.Error)
	}
}

// @constraint RT-2 — unknown method returns method-not-found.
func TestServeUnknownMethod(t *testing.T) {
	srv := NewServer()
	body := `{"jsonrpc":"2.0","id":5,"method":"resources/list"}`
	req := newAuthedRequest(t, "resources/list", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != ErrCodeMethodNotFound {
		t.Errorf("error code = %v; want -32601", resp.Error)
	}
}

// @constraint RT-2 — notifications (no id) get an empty 200 response,
// not a JSON-RPC envelope (per JSON-RPC 2.0 §4.1).
func TestServeNotificationsGetNoBody(t *testing.T) {
	srv := NewServer()
	body := `{"jsonrpc":"2.0","method":"ping"}`
	req := newAuthedRequest(t, "ping", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q; want empty (notifications get no response)", rec.Body.String())
	}
}
