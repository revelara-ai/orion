package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func chainReg(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry()
	r.Register(Tool{
		Name: "lookup", Safety: Safety{ReadOnly: true},
		Run: func(_ context.Context, in json.RawMessage) (string, error) {
			var p struct{ Key string }
			_ = json.Unmarshal(in, &p)
			return "INTERMEDIATE-" + p.Key, nil
		},
	})
	r.Register(Tool{
		Name: "transform", Safety: Safety{ReadOnly: true},
		Run: func(_ context.Context, in json.RawMessage) (string, error) {
			var p struct{ Value string }
			_ = json.Unmarshal(in, &p)
			return "MIDDLE(" + p.Value + ")", nil
		},
	})
	r.Register(Tool{
		Name: "finalize", Safety: Safety{ReadOnly: true},
		Run: func(_ context.Context, in json.RawMessage) (string, error) {
			var p struct{ Value string }
			_ = json.Unmarshal(in, &p)
			return "FINAL:" + p.Value, nil
		},
	})
	r.Register(Tool{
		Name: "approve_me", Safety: Safety{RequiresApproval: true},
		Run: func(context.Context, json.RawMessage) (string, error) { return "acted", nil },
	})
	r.Register(Tool{
		Name: "boom", Safety: Safety{ReadOnly: true},
		Run: func(context.Context, json.RawMessage) (string, error) { return "", fmt.Errorf("kaput") },
	})
	RegisterChain(r)
	return r
}

func runChain(t *testing.T, r *Registry, input string) (string, bool) {
	t.Helper()
	out, isErr := r.Dispatch(context.Background(), "run_tool_chain", json.RawMessage(input))
	return out, isErr
}

// TestChainRunsMultiStepSequenceInOneCall (or-ykz.14 Done-when, half 1): a
// three-step deterministic sequence with output piping runs as ONE tool call.
func TestChainRunsMultiStepSequenceInOneCall(t *testing.T) {
	out, isErr := runChain(t, chainReg(t), `{"steps":[
		{"tool":"lookup","input":{"key":"k1"},"save_as":"a"},
		{"tool":"transform","input":{"value":"${a}"},"save_as":"b"},
		{"tool":"finalize","input":{"value":"${b}"}}
	]}`)
	if isErr {
		t.Fatalf("chain errored: %s", out)
	}
	if !strings.Contains(out, "FINAL:MIDDLE(INTERMEDIATE-k1)") {
		t.Fatalf("final output must reflect the piped chain, got:\n%s", out)
	}
}

// TestChainKeepsIntermediatesOutOfTheResult (or-ykz.14 Done-when, half 2 — the
// token assertion): intermediate step outputs are buffered inside the executor
// and NEVER returned to the model; only the final output + a compact digest
// (sizes, not bodies) reach the context window.
func TestChainKeepsIntermediatesOutOfTheResult(t *testing.T) {
	out, _ := runChain(t, chainReg(t), `{"steps":[
		{"tool":"lookup","input":{"key":"secret-blob"},"save_as":"a"},
		{"tool":"transform","input":{"value":"${a}"},"save_as":"b"},
		{"tool":"finalize","input":{"value":"ok"}}
	]}`)
	if strings.Contains(out, "INTERMEDIATE-secret-blob") || strings.Contains(out, "MIDDLE(") {
		t.Fatalf("intermediate outputs leaked into the model-visible result:\n%s", out)
	}
	if !strings.Contains(out, "FINAL:ok") {
		t.Fatalf("final output missing:\n%s", out)
	}
	// The digest names the steps without carrying their bodies.
	for _, want := range []string{"lookup", "transform", "finalize"} {
		if !strings.Contains(out, want) {
			t.Fatalf("digest must name step %q:\n%s", want, out)
		}
	}
}

// TestChainRefusesApprovalRequiringTools: a chain must never launder the
// per-call human approval a tool demands.
func TestChainRefusesApprovalRequiringTools(t *testing.T) {
	out, isErr := runChain(t, chainReg(t), `{"steps":[
		{"tool":"approve_me","input":{}}
	]}`)
	if !isErr || !strings.Contains(out, "approval") {
		t.Fatalf("approval-requiring tool must be refused with the reason, got isErr=%v:\n%s", isErr, out)
	}
}

// TestChainRefusesRecursion: a chain calling run_tool_chain would evade the
// step ceiling and complicate the safety story — refuse it flat.
func TestChainRefusesRecursion(t *testing.T) {
	out, isErr := runChain(t, chainReg(t), `{"steps":[
		{"tool":"run_tool_chain","input":{"steps":[]}}
	]}`)
	if !isErr || !strings.Contains(out, "recursive") {
		t.Fatalf("recursion must be refused, got isErr=%v:\n%s", isErr, out)
	}
}

// TestChainStopsAtFirstError: a failing step halts the chain; the digest names
// the failed step and the steps that never ran.
func TestChainStopsAtFirstError(t *testing.T) {
	out, isErr := runChain(t, chainReg(t), `{"steps":[
		{"tool":"lookup","input":{"key":"k"},"save_as":"a"},
		{"tool":"boom","input":{}},
		{"tool":"finalize","input":{"value":"${a}"}}
	]}`)
	if !isErr {
		t.Fatalf("a failed step must fail the chain:\n%s", out)
	}
	if !strings.Contains(out, "kaput") || !strings.Contains(out, "not run") {
		t.Fatalf("digest must carry the failure and the unrun steps, got:\n%s", out)
	}
}

// TestChainRefusesUnknownToolAndBadRef: unknown tool names and dangling
// ${refs} are deterministic input errors, named before anything runs.
func TestChainRefusesUnknownToolAndBadRef(t *testing.T) {
	if out, isErr := runChain(t, chainReg(t), `{"steps":[{"tool":"nope","input":{}}]}`); !isErr || !strings.Contains(out, "nope") {
		t.Fatalf("unknown tool must refuse by name: %v %s", isErr, out)
	}
	if out, isErr := runChain(t, chainReg(t), `{"steps":[{"tool":"finalize","input":{"value":"${ghost}"}}]}`); !isErr || !strings.Contains(out, "ghost") {
		t.Fatalf("dangling ref must refuse by name: %v %s", isErr, out)
	}
}

// TestChainCapsSteps: a runaway plan is refused, not executed.
func TestChainCapsSteps(t *testing.T) {
	var steps []string
	for i := 0; i < maxChainSteps+1; i++ {
		steps = append(steps, `{"tool":"lookup","input":{"key":"k"}}`)
	}
	out, isErr := runChain(t, chainReg(t), `{"steps":[`+strings.Join(steps, ",")+`]}`)
	if !isErr || !strings.Contains(out, "steps") {
		t.Fatalf("over-cap chain must refuse: %v %s", isErr, out)
	}
}
