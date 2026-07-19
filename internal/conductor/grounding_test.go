package conductor

import (
	"context"
	"encoding/json"
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
		func(acp.PermissionRequest) (acp.PermissionResult, error) { return acp.PermissionResult{Outcome: "granted"}, nil }); err != nil {
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

	// Citation trail: routed pre-submit (or-3p5.10), the facts are recorded
	// on the reserved brownfield project.
	var recorded string
	_ = oc.Store().WithTx(context.Background(), func(tx *contextstore.Tx) error {
		pid, e := tx.Projects().GetOrCreateReserved(context.Background(), contextstore.BrownfieldProjectName, "brownfield")
		if e != nil {
			return e
		}
		if e, ok, _ := tx.PolarisContext().Get(context.Background(), pid, codeGroundingKind); ok {
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

// groundTools builds a store-backed conductor + its spec-tool registry over a
// FRESH store (no pre-submitted project), so submit_intent/read_codebase can be
// exercised directly. Returns the conductor and a run(name,input) helper.
func groundTools(t *testing.T) (*orchestrator.Conductor, func(name, input string) (string, error)) {
	t.Helper()
	c := orchestrator.NewWithStore(openStore(t))
	r := specTools(c, nil, &changeSession{}, nil)
	run := func(name, input string) (string, error) {
		tool, ok := r.Get(name)
		if !ok {
			t.Fatalf("tool %s not registered", name)
		}
		return tool.Run(context.Background(), json.RawMessage(input))
	}
	return c, run
}

// TestForceGreenfieldSubmitNoGrounding (or-hn15.1 DONE-WHEN a): a force_greenfield
// submit run from inside a brownfield cwd appends NO codebase grounding and
// persists NO code_grounding row on the greenfield project — the cwd repo is
// not the build target (the exact dogfood leak: the game intent got Orion's own
// Go map with "cite these packages in the spec").
func TestForceGreenfieldSubmitNoGrounding(t *testing.T) {
	brownfieldFixtureRepo(t)
	c, run := groundTools(t)

	out, err := run("submit_intent", `{"intent":"Build a PvE game like Arc Raiders with RL-driven mechs.","force_greenfield":true}`)
	if err != nil {
		t.Fatalf("submit_intent: %v", err)
	}
	if strings.Contains(out, "CODEBASE GROUNDING") {
		t.Fatalf("a force_greenfield submit must NOT inject the cwd repo's map:\n%s", out)
	}
	if strings.Contains(out, "widgetstore") || strings.Contains(out, "PutWidget") {
		t.Fatalf("the cwd repo's packages leaked into the greenfield submit result:\n%s", out)
	}
	// And no code_grounding audit row on the greenfield project.
	ctx := context.Background()
	proj, _, err := c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	if err := c.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
		_, ok, e := tx.PolarisContext().Get(ctx, proj.ID, codeGroundingKind)
		found = ok
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("a greenfield project must not record the harness cwd as its code_grounding")
	}
}

// TestRoutedChangeKeepsGrounding (or-hn15.1 DONE-WHEN b): the brownfield ROUTED
// redirect (the CHANGE flow) still carries the cwd grounding — that is exactly
// where citing the real repo belongs.
func TestRoutedChangeKeepsGrounding(t *testing.T) {
	brownfieldFixtureRepo(t)
	_, run := groundTools(t)

	out, err := run("submit_intent", `{"intent":"Add a retry to the outbound HTTP client."}`)
	if err != nil {
		t.Fatalf("submit_intent: %v", err)
	}
	if !strings.Contains(out, "ROUTED") {
		t.Fatalf("a non-forced brownfield submit must route to the change flow:\n%s", out)
	}
	if !strings.Contains(out, "CODEBASE GROUNDING") {
		t.Fatalf("the change-flow redirect must keep its cwd grounding:\n%s", out)
	}
}

// TestReadCodebaseGreenfieldNeutral (or-hn15.1 DONE-WHEN c): read_codebase on a
// greenfield intake — even from inside a brownfield cwd — returns a neutral
// "unrelated codebase" note instead of dumping the host repo's map. Before any
// project it still surfaces the cwd map (the change flow wants it).
func TestReadCodebaseGreenfieldNeutral(t *testing.T) {
	brownfieldFixtureRepo(t)
	_, run := groundTools(t)

	pre, err := run("read_codebase", `{}`)
	if err != nil {
		t.Fatalf("read_codebase: %v", err)
	}
	if !strings.Contains(pre, "widgetstore") {
		t.Fatalf("with no active project read_codebase must still show the cwd map:\n%s", pre)
	}

	if _, err := run("submit_intent", `{"intent":"Build a PvE game like Arc Raiders.","force_greenfield":true}`); err != nil {
		t.Fatal(err)
	}
	out, err := run("read_codebase", `{}`)
	if err != nil {
		t.Fatalf("read_codebase: %v", err)
	}
	if strings.Contains(out, "widgetstore") || strings.Contains(out, "PutWidget") {
		t.Fatalf("read_codebase must NOT dump the unrelated cwd repo on a greenfield intake:\n%s", out)
	}
	if !strings.Contains(strings.ToUpper(out), "UNRELATED") {
		t.Fatalf("read_codebase must return the neutral unrelated-codebase note, got:\n%s", out)
	}
}

// TestForceGreenfieldWordingNeutral (or-hn15.1 DONE-WHEN d): the force_greenfield
// surfaces describe a NEW standalone PROJECT (any type), not a "service".
func TestForceGreenfieldWordingNeutral(t *testing.T) {
	c := orchestrator.NewWithStore(openStore(t))
	r := specTools(c, nil, &changeSession{}, nil)
	tool, ok := r.Get("submit_intent")
	if !ok {
		t.Fatal("submit_intent not registered")
	}
	schema := string(tool.InputSchema)
	if strings.Contains(schema, "standalone service") {
		t.Fatalf("force_greenfield must not be described as a 'standalone service' (project-type bias): %s", schema)
	}
	if !strings.Contains(schema, "standalone project") {
		t.Fatalf("force_greenfield should describe a NEW standalone project: %s", schema)
	}

	brownfieldFixtureRepo(t)
	run := func(name, input string) (string, error) {
		tl, _ := r.Get(name)
		return tl.Run(context.Background(), json.RawMessage(input))
	}
	out, _ := run("submit_intent", `{"intent":"Add a retry to the outbound HTTP client."}`)
	if strings.Contains(out, "standalone service") {
		t.Fatalf("the ROUTED redirect must not say 'standalone service': %s", out)
	}
}
