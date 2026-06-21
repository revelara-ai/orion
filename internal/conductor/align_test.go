package conductor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/revelara-ai/orion/internal/llm"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

func alignResp(aligned bool, sev, concern string) *llm.ChatResponse {
	in, _ := json.Marshal(map[string]any{"aligned": aligned, "severity": sev, "concern": concern})
	return &llm.ChatResponse{StopReason: llm.StopToolUse, Content: []llm.ContentBlock{
		{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{ID: "a1", Name: "report_alignment", Input: in}},
	}}
}

// TestNativeAlignerParsesVerdict: the LLM judge's report_alignment tool call is
// decoded into an AlignVerdict.
func TestNativeAlignerParsesVerdict(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc handleTime() {}\n"), 0o644)

	prov := &fakeLLM{resp: []*llm.ChatResponse{alignResp(false, "high", "returns a hardcoded timestamp, not the current time")}}
	v, err := NativeAligner(prov)(context.Background(), "return the CURRENT time", dir, nil)
	if err != nil {
		t.Fatalf("align: %v", err)
	}
	if v.Aligned || v.Severity != "high" || v.Concern == "" {
		t.Fatalf("verdict not parsed: %+v", v)
	}
}

// TestAlignmentGateIsLogOnly: a build that PASSES proof, with an aligner that
// reports MISALIGNMENT, must record the concern but leave the verdict untouched —
// proof.Accept stays the sole right-to-ship (Step 1 is log-only, not blocking).
func TestAlignmentGateIsLogOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a service + proof")
	}
	oc, ctx := ratifiedTimeService(t) // canonical spec; fixture build proves green

	// An aligner that always reports misalignment (the LLM judge is mocked here; the
	// point is that a MISALIGNED verdict does not change a PASSING build).
	misaligned := func(context.Context, string, string, []spec.BehavioralCase) (AlignVerdict, error) {
		return AlignVerdict{Aligned: false, Severity: "high", Concern: "returns a constant"}, nil
	}
	res, err := BuildAndProve(ctx, oc.Store(), nil, misaligned, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.Verdict != "Accept" || !res.Closed {
		t.Fatalf("log-only alignment must NOT change a passing verdict: %+v", res)
	}
	if !res.Alignment.Ran || res.Alignment.Aligned {
		t.Fatalf("misalignment was not recorded: %+v", res.Alignment)
	}
}
