// Package mcp implements the Model Context Protocol HTTP transport.
//
// Stage 3b scope: JSON-RPC 2.0 framing, Mcp-Session-Id session
// management, isolated panic-recover boundary (RT-11 / T6), and the
// minimum method set MCP clients exercise during connection
// establishment: initialize, ping, tools/list, tools/call.
//
// Tool dispatch is a stub at this stage — tools/list returns []
// and tools/call returns method-not-found until Stage 3c installs
// the per-tool JSON-schema registry (RT-14 / T5 / T7).
//
// Satisfies: RT-2 (MCP transport half), RT-11 (sub-handler isolation),
//
//	T6 (panic isolated from main HTTP server).
package mcp

import (
	"encoding/json"
	"errors"
	"io"
)

// Standard JSON-RPC 2.0 error codes (https://www.jsonrpc.org/specification).
const (
	ErrCodeParseError     = -32700
	ErrCodeInvalidRequest = -32600
	ErrCodeMethodNotFound = -32601
	ErrCodeInvalidParams  = -32602
	ErrCodeInternalError  = -32603
)

// Request is a JSON-RPC 2.0 request envelope. ID may be string,
// number, or null (RFC 7231-ish — we keep it as raw JSON to preserve
// fidelity in the response). A request with absent ID is a
// notification (no response expected).
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// IsNotification reports whether the request lacks an ID, meaning
// the server MUST NOT emit a response.
func (r *Request) IsNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

// Response is the JSON-RPC 2.0 response envelope. Exactly one of
// Result or Error is populated.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is the standard JSON-RPC 2.0 error body.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error implements error so handlers can return an *RPCError directly.
func (e *RPCError) Error() string { return e.Message }

// ErrParseFailed is returned by DecodeRequest when the request body
// is not valid JSON-RPC 2.0.
var ErrParseFailed = errors.New("mcp: parse error")

// DecodeRequest reads a JSON-RPC request from r. Caps the body at
// maxBytes to defend against unbounded input.
func DecodeRequest(r io.Reader, maxBytes int64) (*Request, error) {
	if maxBytes <= 0 {
		maxBytes = 1024 * 1024 // 1 MiB default
	}
	body, err := io.ReadAll(io.LimitReader(r, maxBytes))
	if err != nil {
		return nil, err
	}
	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, ErrParseFailed
	}
	if req.JSONRPC != "2.0" {
		return nil, ErrParseFailed
	}
	if req.Method == "" {
		return nil, ErrParseFailed
	}
	return &req, nil
}

// SuccessResponse builds a {"jsonrpc":"2.0","id":<id>,"result":<r>}
// envelope.
func SuccessResponse(id json.RawMessage, result any) *Response {
	return &Response{JSONRPC: "2.0", ID: id, Result: result}
}

// ErrorResponse builds a {"jsonrpc":"2.0","id":<id>,"error":{...}}
// envelope.
func ErrorResponse(id json.RawMessage, code int, message string, data any) *Response {
	return &Response{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: message, Data: data},
	}
}
