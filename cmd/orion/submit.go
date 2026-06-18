package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

// cmdSubmit implements `orion submit`. With --non-interactive it reads the intent
// from stdin, runs the deterministic completeness gate, and prints the open
// decisions as JSON — the headless surface the acceptance criteria exercise.
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

	c := orchestrator.New()
	conf, err := c.Submit(context.Background(), intent)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion submit:", err)
		return 1
	}

	out := struct {
		Intent        string                      `json:"intent"`
		Accepted      bool                        `json:"accepted"`
		OpenDecisions []completeness.OpenDecision `json:"open_decisions"`
	}{
		Intent:        conf.Intent,
		Accepted:      conf.Accepted,
		OpenDecisions: conf.OpenDecisions,
	}
	if out.OpenDecisions == nil {
		out.OpenDecisions = []completeness.OpenDecision{}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, "orion submit: encode:", err)
		return 1
	}
	return 0
}
