package app

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func newMCPServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	srv, err := NewServer(Config{
		Addr: ":0", PagesDir: filepath.Join(dir, "pages"), FrontendDist: t.TempDir(),
		AdminUser: "admin", AdminPassword: "secret", AuditLog: filepath.Join(dir, "audit.log"),
		StorePath:   filepath.Join(dir, "phronesis.db"),
		OIDCEnabled: true, OIDCIssuer: "https://idp.test", OIDCAudience: "phronesis", OIDCSecret: rbacSecret,
		OAuthEnabled: true, OAuthIssuer: "http://wiki.test", OAuthKeyPath: filepath.Join(dir, "oauth.pem"),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close(context.Background()) })
	return srv
}

// mcpCall runs tools/call and returns the decoded tool result, or the
// JSON-RPC error message.
func mcpCall(t *testing.T, s *Server, key, tool string, args map[string]any) (map[string]any, string) {
	t.Helper()
	rr := doBearer(t, s, key, http.MethodPost, "/mcp", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args},
	})
	var env struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("%s: %d %s", tool, rr.Code, rr.Body.String())
	}
	if env.Error != nil {
		return nil, env.Error.Message
	}
	var out map[string]any
	_ = json.Unmarshal([]byte(env.Result.Content[0].Text), &out)
	return out, ""
}

func TestWikiMCPToolsHonourScopeAndWorkspace(t *testing.T) {
	s := newMCPServer(t)
	admin := passwordLogin(t, s)
	user, _ := oidcLogin(t, s, "mia", "mia@example.com")
	setRole(t, s, admin, "mia", "editor")
	read := mintKeyFor(t, s, user, admin, "read")
	write := mintKeyFor(t, s, user, admin, "write")

	if _, e := mcpCall(t, s, write, "write_page", map[string]any{"name": "Agent Notes", "content": "# Notes\nThe launch codename is BLUEBIRD. [[roadmap]] #agents"}); e != "" {
		t.Fatalf("write_page: %s", e)
	}
	if _, e := mcpCall(t, s, read, "write_page", map[string]any{"name": "x", "content": "y"}); !strings.Contains(e, "forbidden") {
		t.Errorf("read key write_page error = %q, want forbidden", e)
	}
	list, e := mcpCall(t, s, read, "list_pages", nil)
	if e != "" || !strings.Contains(toJSON(list), "agent-notes") {
		t.Errorf("list_pages = %v %s", list, e)
	}
	page, e := mcpCall(t, s, read, "read_page", map[string]any{"name": "agent-notes"})
	if e != "" || !strings.Contains(page["content"].(string), "BLUEBIRD") || page["version"].(float64) < 1 {
		t.Errorf("read_page = %v %s", page, e)
	}
	// write with a stale base_version merges rather than clobbering.
	if out, e := mcpCall(t, s, write, "write_page", map[string]any{"name": "agent-notes", "content": page["content"].(string) + "\nmore", "base_version": page["version"]}); e != "" || out["merged"] != false {
		t.Errorf("write_page with base_version = %v %s", out, e)
	}
	hits, e := mcpCall(t, s, read, "search_pages", map[string]any{"query": "bluebird"})
	if e != "" || !strings.Contains(toJSON(hits), "agent-notes") || !strings.Contains(toJSON(hits), "BLUEBIRD") {
		t.Errorf("search_pages = %v %s", hits, e)
	}
	if _, e := mcpCall(t, s, read, "list_pages", map[string]any{"workspace": "elsewhere"}); !strings.Contains(e, "forbidden") {
		t.Errorf("other workspace error = %q, want forbidden", e)
	}
	if _, e := mcpCall(t, s, read, "read_page", map[string]any{"name": "x", "bogus": 1}); e == "" {
		t.Error("unknown argument accepted")
	}
	// The written file is on disk, authored through MCP.
	var tools struct {
		Result struct {
			Tools []struct{ Name string } `json:"tools"`
		} `json:"result"`
	}
	_ = json.Unmarshal(doBearer(t, s, read, http.MethodPost, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"}).Body.Bytes(), &tools)
	names := []string{}
	for _, x := range tools.Result.Tools {
		names = append(names, x.Name)
	}
	for _, want := range []string{"list_pages", "read_page", "write_page", "search_pages"} {
		if !strings.Contains(strings.Join(names, ","), want) {
			t.Errorf("tools/list missing %s: %v", want, names)
		}
	}
}

func toJSON(v any) string { b, _ := json.Marshal(v); return string(b) }
