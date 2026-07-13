package conductor

import (
	"strings"
	"testing"
)

// TestSplitAndTreeTools (or-045a.4 DONE-WHEN e): the agent ratifies a
// developer-confirmed split through ratify_split (children created, first one
// activated) and project_tree surfaces the parent/child statuses — including
// when the CURRENT project is a child.
func TestSplitAndTreeTools(t *testing.T) {
	_, run := specRegistry(t)

	// The guards speak through the tool: no resolved type yet.
	subs := `{"subs":[{"name":"world-sim","intent":"the PvE world simulation"},{"name":"mech-ai","intent":"RL-driven mech behavior"}]}`
	if _, err := run("ratify_split", subs); err == nil {
		t.Fatal("ratify_split before a resolved type must be refused")
	}
	if _, err := run("set_project_type", `{"project_type":"game"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := run("ratify_split", subs); err == nil || !strings.Contains(err.Error(), "goals") {
		t.Fatalf("ratify_split before ratified goals must be refused, got: %v", err)
	}
	if _, err := run("propose_goals", `{"goals":["pure PvE co-op extraction"],"non_goals":["PvP"],"success_criteria":["uncanny mech movement"]}`); err != nil {
		t.Fatal(err)
	}
	if _, err := run("ratify_goals", `{}`); err != nil {
		t.Fatal(err)
	}
	// A one-element split is refused at the boundary.
	if _, err := run("ratify_split", `{"subs":[{"name":"only","intent":"everything"}]}`); err == nil {
		t.Fatal("a single sub-spec must be refused")
	}
	out, err := run("ratify_split", subs)
	if err != nil {
		t.Fatalf("ratify_split: %v", err)
	}
	for _, want := range []string{"world-sim", "mech-ai", "activated"} {
		if !strings.Contains(out, want) {
			t.Fatalf("ratify_split output must report %q, got:\n%s", want, out)
		}
	}

	// The tree renders from the CHILD's seat (the active project is now child 1).
	tree, err := run("project_tree", `{}`)
	if err != nil {
		t.Fatalf("project_tree: %v", err)
	}
	for _, want := range []string{"world-sim", "mech-ai", "active", "queued"} {
		if !strings.Contains(tree, want) {
			t.Fatalf("project_tree must show %q, got:\n%s", want, tree)
		}
	}
}
