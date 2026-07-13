package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/revelara-ai/orion/internal/conductor"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/llmsetup"
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
	const usage = `usage: orion change [--cases <file.json>] [--] "<change intent>"

Runs the brownfield change-proof loop on the current repo: generate the change
in a worktree, prove it preserves existing tests (green→green), optionally
prove NEW behavior against ratified --cases, and commit on a review branch.`
	// or-ux02: flag-shaped arguments must NEVER become intents — "--help" once
	// executed a live change loop. --help/-h prints usage; any other leading
	// "-" arg refuses unless the developer separates a deliberate dash-leading
	// intent with "--".
	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Println(usage)
		return 0
	}
	args, cases, err := extractCases(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion change:", err)
		return 2
	}
	if len(args) > 0 && args[0] == "--" {
		args = args[1:] // explicit separator: everything after is intent, verbatim
	} else if len(args) > 0 && strings.HasPrefix(args[0], "-") {
		fmt.Fprintf(os.Stderr, "orion change: unknown flag %q (an intent never starts with '-'; use -- to force one)\n%s\n", args[0], usage)
		return 2
	}
	intent := strings.TrimSpace(strings.Join(args, " "))
	if intent == "" {
		fmt.Fprintln(os.Stderr, usage)
		return 2
	}
	ctx := context.Background()
	brain := llmsetup.Select()
	if brain.Provider == nil {
		fmt.Fprintln(os.Stderr, "orion change needs a model provider (it drives a model to write the change) — "+brain.Reason)
		return 1
	}
	// Tool gate (spec: fail only tool flows): orion change is entirely
	// tool-call-driven, so a model that can't demonstrate tool calling fails
	// fast HERE, before any worktree or baseline work starts.
	if !llm.AdvertisesTools(ctx, brain.Provider, brain.Model) {
		ok, perr := llm.Probe(ctx, brain.Provider)
		if perr != nil {
			fmt.Fprintf(os.Stderr, "orion change: probing %s: %v\n", brain.Ref, perr)
			return 1
		}
		if !ok {
			fmt.Fprintf(os.Stderr, "orion change: model %s did not demonstrate tool calling — orion change requires a tool-capable model\n", brain.Ref)
			return 1
		}
	}

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

	// Open the context store so the change loop has the full control plane —
	// reliability-floor signals resolve Polaris credentials + cache through it
	// (or-uvw.9 dogfood: store=nil silently disabled the floor). Best-effort: a
	// store failure degrades to the storeless path (floor fails open) rather
	// than blocking the change.
	var store *contextstore.Store
	if dir, derr := resolveDataDir(); derr == nil {
		if s, serr := contextstore.Open(dir); serr == nil {
			store = s
			defer func() { _ = store.Close() }()
		} else {
			fmt.Fprintln(os.Stderr, "orion change: context store unavailable (continuing without reliability context):", serr)
		}
	}

	provider := brain.Provider
	res, err := conductor.ChangeAndProve(ctx, root, store, provider, intent, cases, nil, cliPhaseSink())
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion change:", err)
		return 1
	}

	fmt.Printf("change: %s\n  branch: %s\n", intent, res.Branch)
	if len(res.FilesChanged) > 0 {
		fmt.Printf("  files:  %s\n", strings.Join(res.FilesChanged, ", "))
	}
	fmt.Printf("  regression: green-before=%v green-after=%v held=%v\n", res.Regression.Before.Passed, res.Regression.After.Passed, res.Regression.Held)
	if res.Regression.Scope != "" {
		fmt.Printf("  scope: %s\n", res.Regression.Scope)
	}
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
	if d := res.FailureDigest(); d != "" {
		fmt.Printf("  do-no-harm transcript (digest):\n    %s\n", strings.ReplaceAll(d, "\n", "\n    "))
	}
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
			data, err := os.ReadFile(args[i+1]) // #nosec G304,G703 -- the developer's own --cases file path, by their own hand
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

// cliPhaseSink renders change-pipeline phase events as stderr progress lines,
// so a long regression gate heartbeats package-by-package instead of hanging
// silently (or-m45w).
func cliPhaseSink() conductor.PhaseSink {
	return func(e conductor.PhaseEvent) {
		if e.Detail != "" {
			fmt.Fprintf(os.Stderr, "  · %s [%s] %s\n", e.Phase, e.Status, e.Detail)
			return
		}
		fmt.Fprintf(os.Stderr, "  · %s [%s]\n", e.Phase, e.Status)
	}
}
