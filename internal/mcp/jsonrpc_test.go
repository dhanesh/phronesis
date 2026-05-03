package mcp

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// @constraint RT-2 — JSON-RPC 2.0 envelope with method + params is
// accepted; jsonrpc field MUST be "2.0".
func TestDecodeRequestGolden(t *testing.T) {
	body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	req, err := DecodeRequest(body, 0)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}
	if req.Method != "tools/list" {
		t.Errorf("Method = %q; want tools/list", req.Method)
	}
	if string(req.ID) != "1" {
		t.Errorf("ID = %q; want 1", string(req.ID))
	}
}

// @constraint RT-2 — wrong version string MUST be rejected (parse error).
func TestDecodeRequestRejectsWrongVersion(t *testing.T) {
	body := strings.NewReader(`{"jsonrpc":"1.0","id":1,"method":"x"}`)
	if _, err := DecodeRequest(body, 0); !errors.Is(err, ErrParseFailed) {
		t.Errorf("err = %v; want ErrParseFailed", err)
	}
}

func TestDecodeRequestRejectsMissingMethod(t *testing.T) {
	body := strings.NewReader(`{"jsonrpc":"2.0","id":1}`)
	if _, err := DecodeRequest(body, 0); !errors.Is(err, ErrParseFailed) {
		t.Errorf("err = %v; want ErrParseFailed", err)
	}
}

func TestDecodeRequestRejectsMalformedJSON(t *testing.T) {
	body := strings.NewReader(`not json`)
	if _, err := DecodeRequest(body, 0); !errors.Is(err, ErrParseFailed) {
		t.Errorf("err = %v; want ErrParseFailed", err)
	}
}

// @constraint RT-2 — request with absent ID is a notification.
// Notifications MUST NOT receive a response.
func TestRequestIsNotification(t *testing.T) {
	for _, tc := range []struct {
		body string
		want bool
	}{
		{`{"jsonrpc":"2.0","method":"x"}`, true},
		{`{"jsonrpc":"2.0","id":null,"method":"x"}`, true},
		{`{"jsonrpc":"2.0","id":1,"method":"x"}`, false},
		{`{"jsonrpc":"2.0","id":"abc","method":"x"}`, false},
	} {
		req, err := DecodeRequest(strings.NewReader(tc.body), 0)
		if err != nil {
			t.Fatalf("DecodeRequest %q: %v", tc.body, err)
		}
		if got := req.IsNotification(); got != tc.want {
			t.Errorf("body=%q IsNotification = %v; want %v", tc.body, got, tc.want)
		}
	}
}

func TestSuccessAndErrorResponseShape(t *testing.T) {
	id := json.RawMessage("42")
	ok := SuccessResponse(id, map[string]any{"k": "v"})
	if ok.Error != nil {
		t.Error("Error should be nil on success")
	}
	if ok.JSONRPC != "2.0" {
		t.Errorf("JSONRPC = %q; want 2.0", ok.JSONRPC)
	}

	bad := ErrorResponse(id, ErrCodeMethodNotFound, "method not found", nil)
	if bad.Result != nil {
		t.Error("Result should be nil on error")
	}
	if bad.Error.Code != ErrCodeMethodNotFound {
		t.Errorf("Error.Code = %d; want %d", bad.Error.Code, ErrCodeMethodNotFound)
	}
}

// @constraint RT-2 — body cap defends against unbounded input.
func TestDecodeRequestEnforcesMaxBytes(t *testing.T) {
	huge := strings.Repeat("a", 1024)
	body := `{"jsonrpc":"2.0","id":1,"method":"` + huge + `"}`
	if _, err := DecodeRequest(strings.NewReader(body), 100); err == nil {
		// Truncated body becomes invalid JSON, which surfaces as
		// ErrParseFailed — that's the expected closed-fail mode.
		t.Error("expected error for body > maxBytes")
	}
}
