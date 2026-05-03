package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// @constraint RT-14 — golden path: echo returns the input verbatim.
func TestEchoToolGoldenPath(t *testing.T) {
	tool := EchoTool()
	got, err := tool.Call(context.Background(), json.RawMessage(`{"message":"hello"}`))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	res, ok := got.(echoArgs)
	if !ok {
		t.Fatalf("result type = %T; want echoArgs", got)
	}
	if res.Message != "hello" {
		t.Errorf("Message = %q; want hello", res.Message)
	}
}

// @constraint RT-14 — strict decode rejects unknown fields with
// ErrInvalidParams. Demonstrates the no-side-effect failure mode.
func TestEchoToolRejectsUnknownFields(t *testing.T) {
	tool := EchoTool()
	_, err := tool.Call(context.Background(), json.RawMessage(`{"message":"hi","extra":"x"}`))
	if !errors.Is(err, ErrInvalidParams) {
		t.Errorf("err = %v; want ErrInvalidParams", err)
	}
}

// @constraint RT-14 — missing required field returns ErrInvalidParams.
func TestEchoToolRejectsMissingMessage(t *testing.T) {
	tool := EchoTool()
	for _, body := range []string{`{}`, `{"message":""}`} {
		_, err := tool.Call(context.Background(), json.RawMessage(body))
		if !errors.Is(err, ErrInvalidParams) {
			t.Errorf("body=%q err = %v; want ErrInvalidParams", body, err)
		}
	}
}

func TestEchoToolRejectsEmptyOrInvalidJSON(t *testing.T) {
	tool := EchoTool()
	for _, body := range []string{``, `not json`, `[]`} {
		_, err := tool.Call(context.Background(), json.RawMessage(body))
		if !errors.Is(err, ErrInvalidParams) {
			t.Errorf("body=%q err = %v; want ErrInvalidParams", body, err)
		}
	}
}

// @constraint RT-14 — InputSchema is the published contract. Smoke
// test pins this exact bytes; if you change it, update the fixture.
func TestEchoToolInputSchemaIsStable(t *testing.T) {
	tool := EchoTool()
	schema := string(tool.InputSchema())
	for _, want := range []string{`"type": "object"`, `"message"`, `"required": ["message"]`, `"additionalProperties": false`} {
		if !strings.Contains(schema, want) {
			t.Errorf("schema missing %q; got\n%s", want, schema)
		}
	}
}

func TestEchoToolMetadata(t *testing.T) {
	tool := EchoTool()
	if tool.Name() != "echo" {
		t.Errorf("Name = %q; want echo", tool.Name())
	}
	if tool.Description() == "" {
		t.Error("Description should be non-empty (tools/list shows it)")
	}
}
