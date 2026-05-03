package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// fakeTool is a minimal Tool implementation used by registry tests.
type fakeTool struct {
	name     string
	desc     string
	schema   json.RawMessage
	callFunc func(context.Context, json.RawMessage) (any, error)
}

func (f *fakeTool) Name() string                 { return f.name }
func (f *fakeTool) Description() string          { return f.desc }
func (f *fakeTool) InputSchema() json.RawMessage { return f.schema }
func (f *fakeTool) Call(ctx context.Context, params json.RawMessage) (any, error) {
	return f.callFunc(ctx, params)
}

// @constraint RT-14 — Register + Get round-trips; List sorts by name.
func TestRegistryRegisterAndList(t *testing.T) {
	r := NewRegistry()
	zebra := &fakeTool{name: "zebra", desc: "z", schema: json.RawMessage(`{}`)}
	apple := &fakeTool{name: "apple", desc: "a", schema: json.RawMessage(`{}`)}

	if err := r.Register(zebra); err != nil {
		t.Fatalf("Register zebra: %v", err)
	}
	if err := r.Register(apple); err != nil {
		t.Fatalf("Register apple: %v", err)
	}

	got, ok := r.Get("apple")
	if !ok || got.Name() != "apple" {
		t.Errorf("Get apple = %v, ok=%v", got, ok)
	}
	list := r.List()
	if len(list) != 2 || list[0].Name != "apple" || list[1].Name != "zebra" {
		t.Errorf("List = %v; want sorted [apple zebra]", list)
	}
}

// @constraint RT-14 — duplicate name with a DIFFERENT instance is
// rejected (no shadowing). Re-registering the same instance is OK
// (idempotent for callers who don't track whether they've called
// Register yet).
func TestRegistryRejectsDuplicateName(t *testing.T) {
	r := NewRegistry()
	a := &fakeTool{name: "echo", schema: json.RawMessage(`{}`)}
	b := &fakeTool{name: "echo", schema: json.RawMessage(`{}`)}

	if err := r.Register(a); err != nil {
		t.Fatalf("Register a: %v", err)
	}
	// Same instance — idempotent OK.
	if err := r.Register(a); err != nil {
		t.Errorf("Register a twice (same instance) should be idempotent, got %v", err)
	}
	// Different instance — rejected.
	if err := r.Register(b); err == nil {
		t.Error("Register b with same name should error")
	}
}

func TestRegistryRejectsZeroValues(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Error("Register(nil) should error")
	}
	if err := r.Register(&fakeTool{name: "", schema: json.RawMessage(`{}`)}); err == nil {
		t.Error("Register with empty name should error")
	}
	var nilR *Registry
	if err := nilR.Register(&fakeTool{name: "x"}); err == nil {
		t.Error("Register on nil receiver should error")
	}
}

// @constraint RT-14 — Dispatch invokes Call and returns marshalled
// result; unknown tool returns ErrToolUnknown.
func TestRegistryDispatchGoldenAndUnknown(t *testing.T) {
	r := NewRegistry()
	ok := &fakeTool{
		name:   "ok",
		schema: json.RawMessage(`{}`),
		callFunc: func(_ context.Context, _ json.RawMessage) (any, error) {
			return map[string]string{"hello": "world"}, nil
		},
	}
	_ = r.Register(ok)

	out, err := r.Dispatch(context.Background(), "ok", nil)
	if err != nil {
		t.Fatalf("Dispatch ok: %v", err)
	}
	if !strings.Contains(string(out), `"hello":"world"`) {
		t.Errorf("Dispatch result = %s; want hello:world", string(out))
	}

	if _, err := r.Dispatch(context.Background(), "nope", nil); !errors.Is(err, ErrToolUnknown) {
		t.Errorf("Dispatch unknown err = %v; want ErrToolUnknown", err)
	}
}

// @constraint T5 — response over the 10MB ceiling fails closed with
// ErrResponseTooLarge.
func TestRegistryDispatchEnforcesResponseCeiling(t *testing.T) {
	r := NewRegistry()
	huge := &fakeTool{
		name:   "huge",
		schema: json.RawMessage(`{}`),
		callFunc: func(_ context.Context, _ json.RawMessage) (any, error) {
			// 11 MiB string — comfortably over the ceiling once
			// JSON-marshalled (adds quotes).
			return strings.Repeat("a", 11*1024*1024), nil
		},
	}
	_ = r.Register(huge)

	_, err := r.Dispatch(context.Background(), "huge", nil)
	if !errors.Is(err, ErrResponseTooLarge) {
		t.Errorf("err = %v; want ErrResponseTooLarge", err)
	}
}

// @constraint RT-14 — tool errors propagate through Dispatch verbatim
// so the server can map them to the right JSON-RPC code.
func TestRegistryDispatchPropagatesToolError(t *testing.T) {
	r := NewRegistry()
	bad := &fakeTool{
		name:   "bad",
		schema: json.RawMessage(`{}`),
		callFunc: func(_ context.Context, _ json.RawMessage) (any, error) {
			return nil, ErrInvalidParams
		},
	}
	_ = r.Register(bad)

	_, err := r.Dispatch(context.Background(), "bad", nil)
	if !errors.Is(err, ErrInvalidParams) {
		t.Errorf("err = %v; want ErrInvalidParams", err)
	}
}

// @constraint RT-14 — concurrent register/lookup is race-free.
func TestRegistryConcurrentAccessIsRaceFree(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			t := &fakeTool{
				name:   fmt.Sprintf("t%d", i),
				schema: json.RawMessage(`{}`),
				callFunc: func(_ context.Context, _ json.RawMessage) (any, error) {
					return i, nil
				},
			}
			_ = r.Register(t)
		}(i)
		go func(i int) {
			defer wg.Done()
			r.Get(fmt.Sprintf("t%d", i))
			_ = r.List()
		}(i)
	}
	wg.Wait()
}
