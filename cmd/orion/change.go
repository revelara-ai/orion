package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/revelara-ai/orion/internal/conductor"
	"github.com/revelara-ai/orion/internal/llm"
)

// cmdChange runs the brownfield change-proof loop on the current repo: generate the
// change in a worktree, prove it preserves the existing tests (green→green), and commit
// it on a review branch. Usage:
//
//	orion change "<change intent>"
func cmdChange(args []string) int {
	intent := strings.TrimSpace(strings.Join(args, " "))
	if intent == "" {
		fmt.Fprintln(os.Stderr, `usage: orion change "<change intent>"`)
		return 2
	}
	key := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	if key == "" {
		fmt.Fprintln(os.Stderr, "orion change needs ANTHROPIC_API_KEY (it drives a model to write the change)")
		return 1
	}
	ctx := context.Background()
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion change:", err)
		return 1
	}
	root := conductor.GitRoot(ctx, cwd)
	if root == "" {
		fmt.Fprintln(os.Stderr, "orion change: not a git repository (the change is committed on a review branch)")
		return 1
	}

	provider := llm.NewAnthropic(key, os.Getenv("ORION_MODEL"))
	res, err := conductor.ChangeAndProve(ctx, root, nil, provider, intent)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion change:", err)
		return 1
	}

	fmt.Printf("change: %s\n  branch: %s\n", intent, res.Branch)
	if len(res.FilesChanged) > 0 {
		fmt.Printf("  files:  %s\n", strings.Join(res.FilesChanged, ", "))
	}
	fmt.Printf("  regression: green-before=%v green-after=%v held=%v\n", res.Regression.Before.Passed, res.Regression.After.Passed, res.Regression.Held)
	if res.Committed {
		fmt.Printf("  COMMITTED ✓ — review with: git diff main..%s\n", res.Branch)
		return 0
	}
	fmt.Printf("  NOT committed — %s\n", res.Reason)
	return 1
}
