package conductor

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
)

// TestChangeRepoToolRegistered (slice 4): the change_repo tool is exposed to the brain with a
// valid schema declaring the verify-command oracle (incl. curate_golangci) and marked Destructive.
func TestChangeRepoToolRegistered(t *testing.T) {
	r := specTools(orchestrator.NewWithStore(openStore(t)), nil)
	tool, ok := r.Get("change_repo")
	if !ok {
		t.Fatal("change_repo tool is not registered")
	}
	if !tool.Safety.Destructive {
		t.Error("change_repo should be marked Destructive (it commits a change)")
	}
	var schema map[string]any
	if err := json.Unmarshal(tool.InputSchema, &schema); err != nil {
		t.Fatalf("change_repo InputSchema is not valid JSON: %v", err)
	}
	for _, want := range []string{"intent", "verify", "curate_golangci", "must_exit_zero"} {
		if !strings.Contains(string(tool.InputSchema), want) {
			t.Errorf("change_repo schema should declare %q", want)
		}
	}
}

// TestSystemPromptHasBrownfieldChange: the brain is told to drive a tooling/config change via
// change_repo with verify commands — and NOT to fabricate a service.
func TestSystemPromptHasBrownfieldChange(t *testing.T) {
	a := &OrionAgent{role: RoleTemplate{Project: "demo"}}
	p := a.systemPrompt()
	for _, want := range []string{"change_repo", "TOOLING", "do-no-harm", "golangci-lint", "go vet", "file", "ff-only", "land"} {
		if !strings.Contains(p, want) {
			t.Errorf("systemPrompt missing brownfield-change guidance %q", want)
		}
	}
}
