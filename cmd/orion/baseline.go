package main

import (
	"context"
	"fmt"
	"os"
	"strings"

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

	// Step-2 fork: greenfield (create new structure) vs brownfield (integrate with
	// existing code). This decides the create-vs-edit path for everything downstream.
	prof := brownfield.Classify(dir)
	fmt.Printf("repo: %s\n  mode: %s", dir, prof.Mode)
	if len(prof.Languages) > 0 {
		fmt.Printf(" (%s)", strings.Join(prof.Languages, ", "))
	}
	fmt.Printf("\n  git: %v (commits: %v) · source files: %d · tests: %v\n\n", prof.HasGit, prof.HasCommits, prof.SourceFiles, prof.HasTests)

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
