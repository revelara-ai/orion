package harnessconfig

import "testing"

// or-csmy: tool-turn caps are tunable without rebuild — per-role env wins,
// then the global env, then the compiled default; junk degrades to the
// default (harnessconfig posture: bad config never bricks a run).
func TestToolTurns(t *testing.T) {
	if got := ToolTurns("MAIN", 200); got != 200 {
		t.Fatalf("no env → compiled default, got %d", got)
	}
	t.Setenv("ORION_TOOL_TURNS", "500")
	if got := ToolTurns("MAIN", 200); got != 500 {
		t.Fatalf("global env applies to every role, got %d", got)
	}
	if got := ToolTurns("SUBAGENT", 60); got != 500 {
		t.Fatalf("global env applies to sub-loops too, got %d", got)
	}
	t.Setenv("ORION_TOOL_TURNS_SUBAGENT", "80")
	if got := ToolTurns("SUBAGENT", 60); got != 80 {
		t.Fatalf("per-role env wins over global, got %d", got)
	}
	t.Setenv("ORION_TOOL_TURNS", "not-a-number")
	t.Setenv("ORION_TOOL_TURNS_SUBAGENT", "")
	if got := ToolTurns("SUBAGENT", 60); got != 60 {
		t.Fatalf("junk global degrades to the compiled default, got %d", got)
	}
	t.Setenv("ORION_TOOL_TURNS", "0")
	if got := ToolTurns("MAIN", 200); got != 200 {
		t.Fatalf("non-positive degrades to the compiled default, got %d", got)
	}
}
