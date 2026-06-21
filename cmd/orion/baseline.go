package main

import (
	"context"
	"fmt"
	"os"

	"github.com/revelara-ai/orion/internal/brownfield"
)

// cmdBaseline captures a target repo's regression baseline: it detects the repo's
// toolchain and runs its existing tests, reporting green/red. This is the first
// brownfield primitive — the invariant a proven change must preserve. Usage:
//
//	orion baseline [dir]   (dir defaults to .)
func cmdBaseline(args []string) int {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	abs, err := os.Getwd()
	if err == nil && dir == "." {
		dir = abs
	}

	res, err := brownfield.Baseline(context.Background(), dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion baseline:", err)
		return 1
	}
	if !res.Detected {
		fmt.Printf("baseline: no regression baseline for %s — %s\n", dir, res.Skipped)
		return 0 // not an error: the repo simply offers no test baseline
	}
	state := "RED (tests failing)"
	if res.Passed {
		state = "GREEN (tests passing)"
	}
	fmt.Printf("baseline: %s\n  toolchain: %s\n  command:   %s\n  state:     %s\n", dir, res.Toolchain, res.Command, state)
	if !res.Passed {
		fmt.Printf("\n%s\n", res.Output)
		return 1 // a red baseline means a change can't yet be regression-proven
	}
	return 0
}
