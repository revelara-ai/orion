package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

// withStore opens the Context Store at the resolved data dir and runs fn with a
// store-backed Conductor. State persists across CLI invocations under
// ORION_DATA_DIR — the headless loop-control surface.
func withStore(fn func(context.Context, *orchestrator.Conductor) int) int {
	dir, err := resolveDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion:", err)
		return 1
	}
	store, err := contextstore.Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion: open context store:", err)
		return 1
	}
	defer store.Close()
	return fn(context.Background(), orchestrator.NewWithStore(store))
}

// cmdInit initializes the Orion data dir + Context Store.
func cmdInit(_ []string) int {
	dir, err := resolveDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion init:", err)
		return 1
	}
	store, err := contextstore.Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion init:", err)
		return 1
	}
	_ = store.Close()
	fmt.Printf("initialized orion data dir at %s\n", dir)
	return 0
}

// cmdSubmit implements `orion submit`. With --non-interactive it reads the intent
// from stdin, persists it (project + draft spec), runs the deterministic
// completeness gate, and prints the open decisions as JSON.
func cmdSubmit(args []string) int {
	fs := flag.NewFlagSet("submit", flag.ContinueOnError)
	nonInteractive := fs.Bool("non-interactive", false, "read intent from stdin and emit JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*nonInteractive {
		fmt.Fprintln(os.Stderr, "orion submit: interactive submit is the TUI; use --non-interactive for headless mode")
		return 2
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion submit: read stdin:", err)
		return 1
	}
	intent := strings.TrimSpace(string(raw))
	if intent == "" {
		fmt.Fprintln(os.Stderr, "orion submit: empty intent on stdin")
		return 1
	}
	return withStore(func(ctx context.Context, c *orchestrator.Conductor) int {
		conf, err := c.Submit(ctx, intent)
		if err != nil {
			fmt.Fprintln(os.Stderr, "orion submit:", err)
			return 1
		}
		out := struct {
			Intent        string                      `json:"intent"`
			Accepted      bool                        `json:"accepted"`
			OpenDecisions []completeness.OpenDecision `json:"open_decisions"`
		}{conf.Intent, conf.Accepted, conf.OpenDecisions}
		if out.OpenDecisions == nil {
			out.OpenDecisions = []completeness.OpenDecision{}
		}
		return emitJSON(out)
	})
}

// cmdAnswer implements `orion answer --key K --value V`.
func cmdAnswer(args []string) int {
	fs := flag.NewFlagSet("answer", flag.ContinueOnError)
	key := fs.String("key", "", "decision key")
	value := fs.String("value", "", "decision value")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *key == "" {
		fmt.Fprintln(os.Stderr, "orion answer: --key is required")
		return 2
	}
	return withStore(func(ctx context.Context, c *orchestrator.Conductor) int {
		if err := c.RecordAnswer(ctx, *key, *value); err != nil {
			fmt.Fprintln(os.Stderr, "orion answer:", err)
			return 1
		}
		fmt.Printf("recorded %s=%s\n", *key, *value)
		return 0
	})
}

// cmdSpec implements `orion spec <approve|approve-assumptions|show>`.
func cmdSpec(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "orion spec: expected 'approve', 'approve-assumptions', or 'show'")
		return 2
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "approve":
		return withStore(func(ctx context.Context, c *orchestrator.Conductor) int {
			es, err := c.ApproveSpec(ctx)
			if err != nil {
				fmt.Fprintln(os.Stderr, "orion spec approve:", err)
				return 1
			}
			fmt.Printf("spec accepted (hash %s)\n", es.Hash)
			return 0
		})
	case "approve-assumptions":
		// Records the developer's explicit confirmation of the open fallback
		// assumptions (or-v9f.19) — the audit record ratification requires. This is
		// the CLI form of the ACP approve_assumptions tool; `orion spec approve` fails
		// until these are approved.
		return withStore(func(ctx context.Context, c *orchestrator.Conductor) int {
			approved, err := c.ApproveAssumptions(ctx)
			if err != nil {
				fmt.Fprintln(os.Stderr, "orion spec approve-assumptions:", err)
				return 1
			}
			if len(approved) == 0 {
				fmt.Println("no open assumptions to approve")
				return 0
			}
			fmt.Printf("approved %d assumption(s): %s\n", len(approved), strings.Join(approved, ", "))
			return 0
		})
	case "show":
		fs := flag.NewFlagSet("spec show", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "emit JSON")
		if err := fs.Parse(rest); err != nil {
			return 2
		}
		return withStore(func(ctx context.Context, c *orchestrator.Conductor) int {
			v, err := c.SpecView(ctx)
			if err != nil {
				fmt.Fprintln(os.Stderr, "orion spec show:", err)
				return 1
			}
			if *asJSON {
				return emitJSON(v)
			}
			fmt.Printf("status: %s\nopen decisions: %d\nhash: %s\n", v.Status, len(v.OpenDecisions), v.Hash)
			return 0
		})
	default:
		fmt.Fprintf(os.Stderr, "orion spec: unknown subcommand %q\n", sub)
		return 2
	}
}

// cmdPlan implements `orion plan show [--json]`. It decomposes the accepted spec
// on demand and renders the Epic/Task plan.
func cmdPlan(args []string) int {
	if len(args) == 0 || args[0] != "show" {
		fmt.Fprintln(os.Stderr, "orion plan: expected 'show'")
		return 2
	}
	fs := flag.NewFlagSet("plan show", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	return withStore(func(ctx context.Context, c *orchestrator.Conductor) int {
		pv, err := c.PlanView(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "orion plan show:", err)
			return 1
		}
		if *asJSON {
			return emitJSON(pv)
		}
		fmt.Printf("epic: %s\ntasks: %d\n", pv.EpicTitle, len(pv.Tasks))
		for _, t := range pv.Tasks {
			fmt.Printf("  - %s [%s]\n", t.Title, t.FileScope)
		}
		return 0
	})
}

func emitJSON(v any) int {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintln(os.Stderr, "orion: encode:", err)
		return 1
	}
	return 0
}
