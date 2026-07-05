package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// cmdDeliver implements `orion deliver show [--json] [--runbook]`. It reports the
// latest delivery (with its operating envelope) or, if the bar was not met, the
// escalation.
func cmdDeliver(args []string) int {
	if len(args) == 0 || args[0] != "show" {
		fmt.Fprintln(os.Stderr, "orion deliver: expected 'show'")
		return 2
	}
	fs := flag.NewFlagSet("deliver show", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	_ = fs.Bool("runbook", false, "include the runbook (or-d82)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	dir, err := resolveDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion deliver show:", err)
		return 1
	}
	store, err := contextstore.Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion deliver show:", err)
		return 1
	}
	defer store.Close()
	ctx := context.Background()

	// A delivery record only exists once the project is delivered — by which point it
	// has left the active slot (or-v9f.1). Resolve active-or-last-delivered so `orion
	// deliver show` reports on the project that actually has a delivery.
	proj, _, err := store.CurrentOrLastDeliveredProjectSpec(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion deliver show: no current project")
		return 1
	}

	var del contextstore.Delivery
	var found bool
	_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
		del, found, err = tx.Deliveries().LatestForProject(ctx, proj.ID)
		return err
	})

	out := map[string]any{"operating_envelope": nil}
	if found {
		out["decision"] = "deliver"
		out["human_mergeable"] = true
		out["operating_envelope"] = json.RawMessage(del.OperatingEnvelope)
		// The runbook JSON is {"sections": {...}}; surface .sections at top level so
		// `deliver show --runbook` exposes them.
		var rb struct {
			Sections map[string]string `json:"sections"`
		}
		if json.Unmarshal([]byte(del.Runbook), &rb) == nil {
			out["sections"] = rb.Sections
		}
	} else {
		out["decision"] = "escalate"
		out["human_mergeable"] = false
	}

	if *asJSON {
		return emitJSON(out)
	}
	if found {
		fmt.Printf("delivery: human-mergeable\noperating envelope: %s\n", del.OperatingEnvelope)
	} else {
		fmt.Println("delivery: none (escalated or not yet run)")
	}
	return 0
}
