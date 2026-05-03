package mcp

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/dhanesh/phronesis/internal/principal"
)

// ProtocolVersion is the MCP protocol version this server advertises.
// Clients should treat any other value in the initialize response as a
// hard incompatibility (T7 — pinned-version semantics). Bumped only
// alongside the smoke test in Stage 3c.
const ProtocolVersion = "2025-06-18"

// Server is the MCP HTTP handler. Stage 3c completes the protocol
// surface MCP clients exercise on connect: initialize, ping,
// tools/list (registered tools), tools/call (dispatched through the
// Registry with per-tool JSON-schema validation).
//
// Auth: Server requires a TypeServiceAccount principal in the request
// context (set by the global attachPrincipal middleware via the
// Bearer-JWT path from Stage 3b). Cookie-session principals are
// rejected — the MCP transport is intentionally machine-to-machine.
type Server struct {
	Sessions *SessionStore
	Tools    *Registry
	MaxBytes int64 // body cap for /mcp; defaults to 1 MiB
}

// NewServer constructs a Server with sane defaults. Cleanup of the
// session store is the caller's job (Stage 3b doesn't run a janitor —
// the in-memory map naturally bounds growth via SessionTTL).
func NewServer() *Server {
	return &Server{
		Sessions: NewSessionStore(nil),
		Tools:    NewRegistry(),
		MaxBytes: 1024 * 1024,
	}
}

// ServeHTTP implements http.Handler. Routes a single JSON-RPC request
// per HTTP body — the simplest framing that MCP allows and the only
// one Claude Code currently uses for the HTTPS transport.
//
// Method dispatch:
//
//	initialize    — handshake; returns protocolVersion + serverInfo;
//	                emits Mcp-Session-Id response header.
//	ping          — returns {} (liveness probe, MCP standard).
//	tools/list    — returns {"tools": []} (Stage 3c populates).
//	tools/call    — returns method-not-found until tools register.
//
// Anything else returns ErrCodeMethodNotFound.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}

	p, err := principal.FromContext(r.Context())
	if err != nil || !p.IsServiceAccount() {
		http.Error(w, "service-account principal required", http.StatusUnauthorized)
		return
	}

	req, err := DecodeRequest(r.Body, s.MaxBytes)
	if err != nil {
		writeJSONRPCError(w, nil, ErrCodeParseError, "parse error")
		return
	}

	switch req.Method {
	case "initialize":
		s.handleInitialize(w, req, p)
	case "ping":
		s.respond(w, req, map[string]any{})
	case "tools/list":
		s.respond(w, req, map[string]any{"tools": s.Tools.List()})
	case "tools/call":
		s.handleToolsCall(w, r, req)
	default:
		s.respondError(w, req, ErrCodeMethodNotFound, "method not found: "+req.Method)
	}
}

// handleToolsCall dispatches a tools/call request through the
// registry. Params shape (per MCP spec):
//
//	{"name": "<tool>", "arguments": {<tool-specific>}}
//
// Validation order:
//
//  1. params decode error → -32602 invalid_params (NO side effect)
//  2. unknown tool name   → -32601 method not found
//  3. tool's Call returns ErrInvalidParams → -32602
//  4. tool's Call returns ErrResponseTooLarge → -32603 internal error
//     (T5 ceiling — fail closed instead of truncating silently)
//  5. any other error → -32603 internal error with the error message
//  6. success → result echoed verbatim
func (s *Server) handleToolsCall(w http.ResponseWriter, r *http.Request, req *Request) {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments,omitempty"`
	}
	if err := json.Unmarshal(req.Params, &call); err != nil {
		s.respondError(w, req, ErrCodeInvalidParams, "invalid tools/call params: "+err.Error())
		return
	}
	if call.Name == "" {
		s.respondError(w, req, ErrCodeInvalidParams, "tools/call name is required")
		return
	}

	out, err := s.Tools.Dispatch(r.Context(), call.Name, call.Arguments)
	switch {
	case errors.Is(err, ErrToolUnknown):
		s.respondError(w, req, ErrCodeMethodNotFound, "tool not registered: "+call.Name)
	case errors.Is(err, ErrInvalidParams):
		s.respondError(w, req, ErrCodeInvalidParams, "invalid arguments: "+err.Error())
	case errors.Is(err, ErrResponseTooLarge):
		s.respondError(w, req, ErrCodeInternalError, "response exceeds 10 MB ceiling")
	case err != nil:
		s.respondError(w, req, ErrCodeInternalError, err.Error())
	default:
		// Success — wrap the raw tool result so MCP clients see the
		// standard {"content": [{"type":"text","text":"<json>"}],
		// "isError": false} envelope. Stage 3c uses the text variant
		// (most portable across MCP client versions); a future stage
		// can add the structured-content variant if needed.
		s.respond(w, req, map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": string(out)},
			},
			"isError": false,
		})
	}
}

// handleInitialize creates a fresh session, emits the Mcp-Session-Id
// header, and returns the standard initialize result envelope.
//
// Result shape:
//
//	{
//	  "protocolVersion": "2025-06-18",
//	  "serverInfo": {"name": "phronesis", "version": "stage-3b"},
//	  "capabilities": {"tools": {}}
//	}
func (s *Server) handleInitialize(w http.ResponseWriter, req *Request, p principal.Principal) {
	id, err := s.Sessions.Create(p)
	if err != nil {
		s.respondError(w, req, ErrCodeInternalError, "session create: "+err.Error())
		return
	}
	w.Header().Set("Mcp-Session-Id", id)
	s.respond(w, req, map[string]any{
		"protocolVersion": ProtocolVersion,
		"serverInfo": map[string]string{
			"name":    "phronesis",
			"version": "stage-3b",
		},
		"capabilities": map[string]any{"tools": map[string]any{}},
	})
}

// respond writes a success envelope. Skipped for notifications
// (RFC: no response).
func (s *Server) respond(w http.ResponseWriter, req *Request, result any) {
	if req.IsNotification() {
		w.WriteHeader(http.StatusOK)
		return
	}
	resp := SuccessResponse(req.ID, result)
	writeJSONRPC(w, http.StatusOK, resp)
}

// respondError writes an error envelope. Notifications get an empty
// 200 — JSON-RPC forbids responses to notifications even on error.
func (s *Server) respondError(w http.ResponseWriter, req *Request, code int, message string) {
	if req.IsNotification() {
		w.WriteHeader(http.StatusOK)
		return
	}
	writeJSONRPCError(w, req.ID, code, message)
}

func writeJSONRPC(w http.ResponseWriter, status int, resp *Response) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

func writeJSONRPCError(w http.ResponseWriter, id json.RawMessage, code int, message string) {
	resp := ErrorResponse(id, code, message, nil)
	writeJSONRPC(w, http.StatusOK, resp)
}

// ErrUnauthenticated is exported so callers can match on it via
// errors.Is when integrating the MCP server into a larger handler
// chain. Stage 3b returns plain HTTP 401 for the auth gate; richer
// error semantics land in Stage 3c.
var ErrUnauthenticated = errors.New("mcp: service-account principal required")
