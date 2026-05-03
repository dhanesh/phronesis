package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ResponseCeiling is the maximum size of a tools/call response body
// after JSON marshalling. Tools that would emit more than this fail
// closed with a structured error rather than truncating; oversized
// payloads are expected to use a blob-reference pattern instead
// (out-of-band download via /media/<sha>).
//
// Satisfies: T5 (server-enforced 10 MB ceiling on MCP tool responses).
const ResponseCeiling = 10 * 1024 * 1024

// Tool is the registration surface for a single MCP tool. Each tool
// owns:
//
//   - Name, Description — what tools/list publishes to clients.
//   - InputSchema — the JSON schema clients render in their UI and
//     validate against locally; phronesis ALSO validates server-side
//     before dispatch via Call's params decoding.
//   - Call — the dispatcher. Receives raw JSON params, returns the
//     marshallable result. Implementations are responsible for
//     decoding params with strict (DisallowUnknownFields) semantics
//     so unknown fields surface as ErrInvalidParams.
//
// Satisfies: RT-14 (per-tool JSON schema validation before dispatch).
type Tool interface {
	Name() string
	Description() string
	InputSchema() json.RawMessage
	Call(ctx context.Context, params json.RawMessage) (any, error)
}

// ToolInfo is the public per-tool descriptor served by tools/list.
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// ErrInvalidParams is the umbrella error tools return when their
// input fails schema validation. The MCP server translates this to
// a JSON-RPC -32602 envelope with no audit row, no partial mutation,
// and no downstream call (T5 / RT-14 fail-closed contract).
var ErrInvalidParams = errors.New("mcp: invalid params")

// ErrResponseTooLarge is returned when a tool's marshalled result
// exceeds ResponseCeiling. Surfaced to the MCP client as a structured
// JSON-RPC error rather than truncated payload.
var ErrResponseTooLarge = errors.New("mcp: response exceeds 10 MB ceiling")

// ErrToolUnknown is returned when tools/call references a name that
// is not registered. Translates to JSON-RPC method-not-found.
var ErrToolUnknown = errors.New("mcp: unknown tool")

// Registry is the in-memory tool catalogue. Concurrency-safe.
//
// Nil receivers are safe for read-only lookups (Get/List); Register
// on a nil receiver returns an error so a misconfigured server fails
// loudly at startup rather than silently dropping registrations.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]Tool)}
}

// Register adds t to the registry. Returns an error on nil receiver,
// nil tool, empty name, or duplicate name (registration is idempotent
// only when the same instance is re-registered — different instances
// with the same name are rejected so unrelated callers can't shadow
// each other).
func (r *Registry) Register(t Tool) error {
	if r == nil {
		return errors.New("mcp: cannot Register on nil Registry")
	}
	if t == nil {
		return errors.New("mcp: cannot Register nil Tool")
	}
	name := t.Name()
	if name == "" {
		return errors.New("mcp: Tool.Name returned empty string")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, dup := r.tools[name]; dup && existing != t {
		return fmt.Errorf("mcp: tool %q already registered", name)
	}
	r.tools[name] = t
	return nil
}

// Get returns the tool registered under name, if any.
func (r *Registry) Get(name string) (Tool, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns all registered tools sorted by name. The sorted order
// is stable so tools/list responses are deterministic — important for
// the T7 spec smoke test, which asserts against a fixture.
func (r *Registry) List() []ToolInfo {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]ToolInfo, 0, len(names))
	for _, name := range names {
		t := r.tools[name]
		out = append(out, ToolInfo{
			Name:        name,
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return out
}

// Dispatch is the central tools/call shim. It looks up name, calls
// the tool, marshals the result, enforces the response ceiling, and
// returns either the marshalled bytes (success) or a typed error.
//
// Any error returned by the tool's Call surfaces verbatim — MCP
// server callers translate to JSON-RPC envelopes by inspecting the
// error type.
func (r *Registry) Dispatch(ctx context.Context, name string, params json.RawMessage) (json.RawMessage, error) {
	if r == nil {
		return nil, ErrToolUnknown
	}
	t, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrToolUnknown, name)
	}
	result, err := t.Call(ctx, params)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal tool %s result: %w", name, err)
	}
	if len(out) > ResponseCeiling {
		return nil, ErrResponseTooLarge
	}
	return out, nil
}
