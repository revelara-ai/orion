package harnessconfig

import (
	"os"
	"strconv"
)

// ToolTurns resolves a harness loop's tool-turn cap (or-csmy): the per-role
// env (ORION_TOOL_TURNS_<ROLE>) wins, then the global ORION_TOOL_TURNS, then
// the compiled fallback. The cap is a runaway backstop, not the cost limit —
// the budget accountant's token/dollar/wall ceilings govern spend — so it
// should be sized for the largest legitimate task, not the average one
// (evidence: a 40-turn main loop died mid-reconnaissance on a large feature).
// Junk or non-positive values degrade to the fallback, per the package
// posture: bad config never bricks a run.
func ToolTurns(role string, fallback int) int {
	for _, key := range []string{"ORION_TOOL_TURNS_" + role, "ORION_TOOL_TURNS"} {
		if v := os.Getenv(key); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 1 {
				return n
			}
		}
	}
	return fallback
}
