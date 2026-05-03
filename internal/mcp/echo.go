package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// echoSchema is the JSON schema published in tools/list and used to
// document the shape of arguments echo accepts. Pinned as a fixture
// to support the T7 spec smoke test (any change here breaks the
// fixture assertion deliberately — bump the fixture together).
const echoSchema = `{
  "type": "object",
  "properties": {
    "message": {
      "type": "string",
      "description": "The message to echo back."
    }
  },
  "required": ["message"],
  "additionalProperties": false
}`

// echoTool is the canonical first MCP tool — a no-side-effect mirror
// that returns its input message verbatim. Exists primarily so the
// MCP transport has SOMETHING in tools/list and the spec smoke test
// has SOMETHING to call. Real wiki-facing tools land in later stages.
//
// Satisfies: RT-14 (per-tool JSON schema validation: strict-decode
//
//	rejects unknown fields and missing required field).
type echoTool struct{}

// EchoTool returns the singleton instance. Callers register it on
// the MCP server's Registry at startup.
func EchoTool() Tool { return &echoTool{} }

func (*echoTool) Name() string        { return "echo" }
func (*echoTool) Description() string { return "Returns the input message verbatim." }
func (*echoTool) InputSchema() json.RawMessage {
	return json.RawMessage(echoSchema)
}

type echoArgs struct {
	Message string `json:"message"`
}

func (*echoTool) Call(_ context.Context, params json.RawMessage) (any, error) {
	if len(params) == 0 {
		return nil, fmt.Errorf("%w: params is empty", ErrInvalidParams)
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	var args echoArgs
	if err := dec.Decode(&args); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidParams, err)
	}
	if args.Message == "" {
		return nil, fmt.Errorf("%w: message is required", ErrInvalidParams)
	}
	return args, nil
}
