package conductor

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/pkg/llm"
)

// TestSpecArtifactCapturesProvenance (or-tcs.5): the artifact records the FULL provenance — the
// initial intent, the grilling Q&A, and the functional/testing/non-functional contract.
func TestSpecArtifactCapturesProvenance(t *testing.T) {
	es := spec.ExecutableSpec{
		Intent:           "Build an HTTP service that returns the current time",
		Hash:             "abc123def",
		ResponseContract: spec.ResponseContract{Route: "/time", ContentType: "application/json"},
		Requirements: []spec.Requirement{
			{Text: "the response includes a non-empty zone field", Cases: []spec.BehavioralCase{
				{Request: spec.RequestShape{Method: "GET", Path: "/time"}, Expect: spec.ExpectShape{Status: 200, ContentType: "application/json"}},
			}},
		},
	}
	dialogue := []specQA{
		{Role: "Developer", Text: "build a time service"},
		{Role: "Orion", Text: "Which timezone should it use?"},
		{Role: "Developer", Text: "UTC"},
	}
	doc := SpecArtifact(es, dialogue, false)
	for _, want := range []string{
		"Design Document", "Intent (the initial request)", "Build an HTTP service",
		"How we got here — the grilling", "Which timezone should it use?", "UTC",
		"### Functional", "### Testing", "Non-functional — security & reliability", "GET /time",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("artifact missing %q", want)
		}
	}
	if strings.Contains(doc, "Out of scope") {
		t.Error("a LIGHT artifact should not carry PRD-only sections")
	}
}

// TestSpecArtifactHeavyIsPRD: a non-trivial contract is classified heavy and rendered as a PRD.
func TestSpecArtifactHeavyIsPRD(t *testing.T) {
	var cases []spec.BehavioralCase
	for i := 0; i < 4; i++ {
		cases = append(cases, spec.BehavioralCase{Request: spec.RequestShape{Method: "GET", Path: "/x"}, Expect: spec.ExpectShape{Status: 200}})
	}
	es := spec.ExecutableSpec{Intent: "build a platform", Requirements: []spec.Requirement{{Text: "x", Cases: cases}}}
	if !specWeight(es, nil) {
		t.Fatal("a 4-case contract should classify heavy")
	}
	doc := SpecArtifact(es, nil, true)
	if !strings.Contains(doc, "Product Requirements (PRD)") || !strings.Contains(doc, "Out of scope") {
		t.Errorf("a heavy artifact should be a PRD with an Out-of-scope section")
	}
}

// TestExtractDialogueDropsToolNoise: only developer-facing text (questions + answers) is kept;
// tool calls and tool results are dropped.
func TestExtractDialogueDropsToolNoise(t *testing.T) {
	convo := []llm.Message{
		llm.TextMessage(llm.RoleUser, "build a time service"),
		{Role: llm.RoleAssistant, Content: []llm.ContentBlock{
			{Type: llm.BlockText, Text: "Which timezone?"},
			{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{Name: "submit_intent"}},
		}},
		{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: llm.BlockToolResult, ToolResult: &llm.ToolResult{Content: "ok"}}}},
		llm.TextMessage(llm.RoleUser, "UTC"),
	}
	d := extractDialogue(convo)
	if len(d) != 3 {
		t.Fatalf("want 3 text turns, got %d: %+v", len(d), d)
	}
	if d[0].Role != "Developer" || d[1].Role != "Orion" || d[1].Text != "Which timezone?" || d[2].Text != "UTC" {
		t.Errorf("dialogue mis-extracted: %+v", d)
	}
}
