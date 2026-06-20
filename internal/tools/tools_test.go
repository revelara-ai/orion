package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestRegistrySpecsAndDispatch: Specs default a missing schema to an object, and
// Dispatch runs a tool, surfaces tool errors, and reports unknown tools as a
// (handled) error rather than panicking.
func TestRegistrySpecsAndDispatch(t *testing.T) {
	r := NewRegistry()
	r.Register(Tool{Name: "ok", Run: func(_ context.Context, in json.RawMessage) (string, error) { return "ran:" + string(in), nil }})
	r.Register(Tool{Name: "boom", Run: func(context.Context, json.RawMessage) (string, error) { return "", errors.New("kaboom") }})

	specs := r.Specs()
	if len(specs) != 2 {
		t.Fatalf("specs = %d, want 2", len(specs))
	}
	if string(specs[0].InputSchema) != `{"type":"object"}` {
		t.Fatalf("missing schema not defaulted: %s", specs[0].InputSchema)
	}

	if out, isErr := r.Dispatch(context.Background(), "ok", json.RawMessage(`{"x":1}`)); isErr || !strings.Contains(out, `{"x":1}`) {
		t.Fatalf("ok dispatch: out=%q isErr=%v", out, isErr)
	}
	if out, isErr := r.Dispatch(context.Background(), "boom", nil); !isErr || !strings.Contains(out, "kaboom") {
		t.Fatalf("tool error not surfaced: out=%q isErr=%v", out, isErr)
	}
	if out, isErr := r.Dispatch(context.Background(), "nope", nil); !isErr || !strings.Contains(out, "unknown tool") {
		t.Fatalf("unknown tool not handled: out=%q isErr=%v", out, isErr)
	}
}
