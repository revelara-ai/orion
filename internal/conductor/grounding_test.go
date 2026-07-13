package conductor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/pkg/llm"
)

// brownfieldFixtureRepo drops a small real Go package tree and chdirs in.
func brownfieldFixtureRepo(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "widgetstore"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":                 "module fixture\n\ngo 1.24\n",
		"main.go":                "package main\n\nfunc main() {}\n",
		"widgetstore/widgets.go": "package widgetstore\n\n// PutWidget stores a widget.\nfunc PutWidget(id string) error { return nil }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
}

// or-tcs.5 clause 1 (acceptance): grilling in an existing repo cites
// code-derived facts — the submit_intent RESULT carries the repo digest
// naming REAL packages, without the model calling read_codebase, and the
// same facts land on the project record as the citation trail.
func TestSubmitIntentAutoGroundsInBrownfieldRepo(t *testing.T) {
	brownfieldFixtureRepo(t)
	oc := orchestrator.NewWithStore(openStore(t))
	prov := &fakeLLM{resp: []*llm.ChatResponse{
		tuResp("1", "submit_intent", `{"intent":"refactor the widget storage"}`),
		endTurn("ok"),
	}}
	agent := NewOrionAgent(prov, oc, RoleTemplate{Project: "demo"})
	if _, err := agent.Prompt(context.Background(), "s1", "refactor the widget storage",
		func(acp.Update) {},
		func(acp.PermissionRequest) (acp.PermissionResult, error) { return acp.PermissionResult{}, nil }); err != nil {
		t.Fatal(err)
	}

	// The tool RESULT (fed back to the model) must carry the grounding.
	var toolResult string
	for _, m := range prov.lastReq.Messages {
		for _, b := range m.Content {
			if b.Type == llm.BlockToolResult && b.ToolResult != nil && strings.Contains(b.ToolResult.Content, "CODEBASE GROUNDING") {
				toolResult = b.ToolResult.Content
			}
		}
	}
	if toolResult == "" {
		t.Fatal("submit_intent result carries no codebase grounding in a brownfield repo")
	}
	if !strings.Contains(toolResult, "widgetstore") {
		t.Fatalf("grounding must name the REAL package: %s", toolResult)
	}

	// Citation trail: the facts are recorded on the project.
	proj, _, err := oc.Store().CurrentProjectSpec(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var recorded string
	_ = oc.Store().WithTx(context.Background(), func(tx *contextstore.Tx) error {
		if e, ok, _ := tx.PolarisContext().Get(context.Background(), proj.ID, codeGroundingKind); ok {
			recorded = e.Payload
		}
		return nil
	})
	if !strings.Contains(recorded, "widgetstore") {
		t.Fatalf("grounding not recorded on the project: %q", recorded)
	}
}

// Greenfield: no source → no grounding block, no phantom citations.
func TestSubmitIntentNoGroundingInGreenfield(t *testing.T) {
	t.Chdir(t.TempDir())
	oc := orchestrator.NewWithStore(openStore(t))
	if _, err := oc.Submit(context.Background(), "build a fresh service"); err != nil {
		t.Fatal(err)
	}
	if g := codebaseGrounding(context.Background(), oc); g != "" {
		t.Fatalf("greenfield must yield no grounding: %q", g)
	}
}
