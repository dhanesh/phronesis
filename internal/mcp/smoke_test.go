package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dhanesh/phronesis/internal/principal"
)

// SmokeProtocolVersion is the protocol version this server pins to
// publicly. The smoke test asserts initialize returns this exact
// value — when the MCP spec rev advances the constant, this fixture
// MUST be bumped together (T7's tripwire-vs-spec-churn semantics).
const SmokeProtocolVersion = "2025-06-18"

// @constraint T7 — full MCP handshake + tools/list + tools/call
// round-trip via an in-process client. Acts as the spec-pinned
// tripwire: a Claude-Code-equivalent client establishes a session,
// discovers echo, calls it, and receives the verbatim response.
//
// What this catches:
//
//   - protocol version drift (initialize returns a different version)
//   - response envelope drift (tools/call result shape changes)
//   - tool schema drift (echo's published schema mutates by accident)
//   - method dispatch drift (initialize/ping/tools/list/tools/call
//     stop returning the expected JSON-RPC shapes)
func TestMCPSpecSmoke(t *testing.T) {
	srv := NewServer()
	if err := srv.Tools.Register(EchoTool()); err != nil {
		t.Fatalf("Register: %v", err)
	}
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Stamp a service-account principal as the global
		// attachPrincipal middleware would in production. This
		// smoke test exercises /mcp directly so no real cookie /
		// JWT round-trip is involved.
		p := principal.Principal{
			Type:        principal.TypeServiceAccount,
			ID:          "phr_oauth_client_smoke",
			WorkspaceID: "default",
			Role:        principal.RoleEditor,
		}
		srv.ServeHTTP(w, r.WithContext(principal.WithPrincipal(r.Context(), p)))
	})
	ts := httptest.NewServer(RecoverMiddleware(wrapped))
	t.Cleanup(ts.Close)

	post := func(t *testing.T, body string) (*http.Response, []byte) {
		t.Helper()
		resp, err := http.Post(ts.URL, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 1024)
		for {
			n, err := resp.Body.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
			}
			if err != nil {
				break
			}
		}
		return resp, buf
	}

	// 1. initialize — assert protocolVersion + Mcp-Session-Id.
	resp, body := post(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize status = %d; want 200; body=%s", resp.StatusCode, body)
	}
	if resp.Header.Get("Mcp-Session-Id") == "" {
		t.Error("Mcp-Session-Id missing on initialize response")
	}
	var initResp Response
	if err := json.Unmarshal(body, &initResp); err != nil {
		t.Fatalf("parse initialize: %v", err)
	}
	result, _ := initResp.Result.(map[string]any)
	if result["protocolVersion"] != SmokeProtocolVersion {
		t.Errorf("protocolVersion = %v; want %s", result["protocolVersion"], SmokeProtocolVersion)
	}

	// 2. tools/list — assert echo is present with the fixture schema.
	_, body = post(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	var listResp Response
	if err := json.Unmarshal(body, &listResp); err != nil {
		t.Fatalf("parse tools/list: %v", err)
	}
	listResult, _ := listResp.Result.(map[string]any)
	tools, _ := listResult["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools count = %d; want 1", len(tools))
	}
	first, _ := tools[0].(map[string]any)
	if first["name"] != "echo" {
		t.Errorf("first tool name = %v; want echo", first["name"])
	}
	// Schema fixture: assert structure on the parsed shape rather
	// than byte-for-byte (different JSON encoders produce different
	// whitespace; what matters is the schema's contractual content).
	schema, _ := first["inputSchema"].(map[string]any)
	if schema["type"] != "object" {
		t.Errorf("inputSchema.type = %v; want object", schema["type"])
	}
	required, _ := schema["required"].([]any)
	if len(required) != 1 || required[0] != "message" {
		t.Errorf("inputSchema.required = %v; want [message]", required)
	}
	if schema["additionalProperties"] != false {
		t.Errorf("inputSchema.additionalProperties = %v; want false", schema["additionalProperties"])
	}

	// 3. tools/call — golden round-trip.
	_, body = post(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"message":"hello"}}}`)
	var callResp Response
	if err := json.Unmarshal(body, &callResp); err != nil {
		t.Fatalf("parse tools/call: %v", err)
	}
	if callResp.Error != nil {
		t.Fatalf("tools/call error = %+v", callResp.Error)
	}
	cr, _ := callResp.Result.(map[string]any)
	content, _ := cr["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content length = %d", len(content))
	}
	c0, _ := content[0].(map[string]any)
	text, _ := c0["text"].(string)
	if !strings.Contains(text, `"message":"hello"`) {
		t.Errorf("tools/call text = %q; want contains message:hello", text)
	}

	// 4. tools/call invalid arguments → -32602.
	_, body = post(t, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"echo","arguments":{"unknown":"x"}}}`)
	var errResp Response
	if err := json.Unmarshal(body, &errResp); err != nil {
		t.Fatalf("parse tools/call err: %v", err)
	}
	if errResp.Error == nil || errResp.Error.Code != ErrCodeInvalidParams {
		t.Errorf("invalid args code = %v; want -32602", errResp.Error)
	}

	// 5. tools/call unknown tool → -32601.
	_, body = post(t, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"does-not-exist"}}`)
	var unkResp Response
	if err := json.Unmarshal(body, &unkResp); err != nil {
		t.Fatalf("parse tools/call unknown: %v", err)
	}
	if unkResp.Error == nil || unkResp.Error.Code != ErrCodeMethodNotFound {
		t.Errorf("unknown tool code = %v; want -32601", unkResp.Error)
	}

	// 6. ping — liveness probe returns empty result.
	_, body = post(t, `{"jsonrpc":"2.0","id":6,"method":"ping"}`)
	var pingResp Response
	if err := json.Unmarshal(body, &pingResp); err != nil {
		t.Fatalf("parse ping: %v", err)
	}
	if pingResp.Error != nil {
		t.Errorf("ping error = %+v; want nil", pingResp.Error)
	}
}
