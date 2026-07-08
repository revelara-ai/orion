package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/revelara-ai/orion/internal/conductor"
	"github.com/revelara-ai/orion/internal/proof/newbehavior"
	"github.com/revelara-ai/orion/pkg/llm"
)

// cmdChange runs the brownfield change-proof loop on the current repo: generate the
// change in a worktree, prove it preserves the existing tests (green→green), optionally
// prove the asked-for NEW behavior against ratified --cases, and commit it on a review
// branch. Usage:
//
//	orion change [--cases <file.json>] "<change intent>"
func cmdChange(args []string) int {
	args, cases, err := extractCases(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion change:", err)
		return 2
	}
	intent := strings.TrimSpace(strings.Join(args, " "))
	if intent == "" {
		fmt.Fprintln(os.Stderr, `usage: orion change [--cases <file.json>] "<change intent>"`)
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
	res, err := conductor.ChangeAndProve(ctx, root, nil, provider, intent, cases, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion change:", err)
		return 1
	}

	fmt.Printf("change: %s\n  branch: %s\n", intent, res.Branch)
	if len(res.FilesChanged) > 0 {
		fmt.Printf("  files:  %s\n", strings.Join(res.FilesChanged, ", "))
	}
	fmt.Printf("  regression: green-before=%v green-after=%v held=%v\n", res.Regression.Before.Passed, res.Regression.After.Passed, res.Regression.Held)
	if res.NewBehavior != nil {
		fmt.Printf("  new-behavior: pass=%v (%d case(s))\n", res.NewBehavior.Pass, len(res.NewBehavior.Obligations))
	}
	if res.Committed {
		fmt.Printf("  COMMITTED ✓ — review with: git diff main..%s\n", res.Branch)
		if res.Tier != "" {
			fmt.Printf("  tier: %s\n", res.Tier)
		}
		if res.PR.Opened { // or-v9f.15
			fmt.Printf("  PR opened: %s\n", res.PR.URL)
		} else if res.PR.ArtifactPath != "" {
			fmt.Printf("  PR-ready: %s\n", res.PR.ArtifactPath)
		}
		return 0
	}
	fmt.Printf("  NOT committed — %s\n", res.Reason)
	if res.EscalationID != "" { // or-v9f.15: actionable via the unified inbox
		fmt.Printf("  escalation: %s — resolve with: orion escalations resolve %s\n", res.EscalationID, res.EscalationID)
	}
	return 1
}

// extractCases pulls "--cases <file.json>" out of args (a ratified new-behavior case
// list) and returns the remaining args (the intent) plus the parsed cases.
func extractCases(args []string) ([]string, []newbehavior.Case, error) {
	var rest []string
	var cases []newbehavior.Case
	for i := 0; i < len(args); i++ {
		if args[i] == "--cases" {
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("--cases needs a file path")
			}
			data, err := os.ReadFile(args[i+1])
			if err != nil {
				return nil, nil, fmt.Errorf("read --cases: %w", err)
			}
			if err := json.Unmarshal(data, &cases); err != nil {
				return nil, nil, fmt.Errorf("parse --cases: %w", err)
			}
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	return rest, cases, nil
}
