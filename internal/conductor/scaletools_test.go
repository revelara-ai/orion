package conductor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
)

// TestSetScaleTool (or-hn15.2 DONE-WHEN c): the set_scale tool is the agent's
// recovery path for a misclassified scale — it upgrades a standard project to
// large and persists it; an unknown scale is refused.
func TestSetScaleTool(t *testing.T) {
	ctx := context.Background()
	c := orchestrator.NewWithStore(openStore(t))
	if _, err := c.Submit(ctx, "Build a plain time service."); err != nil {
		t.Fatal(err)
	}
	r := specTools(c, nil, &changeSession{}, nil)
	run := func(name, input string) (string, error) {
		tool, ok := r.Get(name)
		if !ok {
			t.Fatalf("tool %s not registered", name)
		}
		return tool.Run(ctx, json.RawMessage(input))
	}

	out, err := run("set_scale", `{"scale":"large"}`)
	if err != nil {
		t.Fatalf("set_scale: %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "large") {
		t.Fatalf("set_scale should confirm the new scale, got %q", out)
	}
	proj, _, err := c.Store().CurrentProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if proj.Scale != "large" {
		t.Fatalf("set_scale must persist the scale, got %q", proj.Scale)
	}
	// Negative: an unknown scale is refused at the tool boundary.
	if _, err := run("set_scale", `{"scale":"enormous"}`); err == nil {
		t.Fatal("set_scale must refuse an unknown scale")
	}
}

// TestSubmitIntentThreadsVerbatim (or-hn15.2 DONE-WHEN b): specTools threads the
// developer's verbatim turn into submit_intent, so a paraphrase that drops the
// "large project" signal still classifies large. Without the verbatim turn the
// paraphrase alone governs (standard).
func TestSubmitIntentThreadsVerbatim(t *testing.T) {
	t.Chdir(t.TempDir()) // greenfield cwd: no routing, no grounding
	ctx := context.Background()

	// With the verbatim turn: large survives the paraphrase.
	c := orchestrator.NewWithStore(openStore(t))
	rv := specTools(c, nil, &changeSession{}, nil,
		"I'd like to build a game like Arc Raiders, pure PvE. I expect this to be a large project.")
	tool, _ := rv.Get("submit_intent")
	out, err := tool.Run(ctx, json.RawMessage(`{"intent":"Build a PvE game like Arc Raiders."}`))
	if err != nil {
		t.Fatalf("submit_intent: %v", err)
	}
	if !strings.Contains(out, `"scale":"large"`) {
		t.Fatalf("the verbatim large signal must reach classification, got %q", out)
	}

	// Without it: the terse paraphrase alone → standard (no invented signal).
	c2 := orchestrator.NewWithStore(openStore(t))
	tool2, _ := specTools(c2, nil, &changeSession{}, nil).Get("submit_intent")
	out2, err := tool2.Run(ctx, json.RawMessage(`{"intent":"Build a PvE game like Arc Raiders."}`))
	if err != nil {
		t.Fatalf("submit_intent: %v", err)
	}
	if !strings.Contains(out2, `"scale":"standard"`) {
		t.Fatalf("without a verbatim signal the paraphrase governs (standard), got %q", out2)
	}
}

// TestSubmitIntentSchemaDemandsVerbatim (or-hn15.2 DONE-WHEN a): the intent
// parameter is documented as the developer's VERBATIM words, since the
// deterministic classifier reads it.
func TestSubmitIntentSchemaDemandsVerbatim(t *testing.T) {
	c := orchestrator.NewWithStore(openStore(t))
	r := specTools(c, nil, &changeSession{}, nil)
	tool, ok := r.Get("submit_intent")
	if !ok {
		t.Fatal("submit_intent not registered")
	}
	if !strings.Contains(strings.ToLower(string(tool.InputSchema)), "verbatim") {
		t.Fatalf("the intent parameter must demand the developer's VERBATIM words: %s", tool.InputSchema)
	}
}
