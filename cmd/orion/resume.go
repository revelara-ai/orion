package main

import (
	"context"
	"fmt"
	"os"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// cmdResume implements `orion resume` (or-v9f.16, folded in from or-v9f.6):
// the one verb for "continue where the run left off". It surfaces anything
// AWAITING A DECISION first (open escalations block their clusters until
// answered), then re-enters the idempotent run loop — unchanged clusters skip
// proof via the persisted memo, so resuming costs only the unfinished work.
func cmdResume(args []string) int {
	dir, err := resolveDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion resume:", err)
		return 1
	}
	store, err := contextstore.Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion resume:", err)
		return 1
	}
	ctx := context.Background()

	var open []contextstore.Escalation
	_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
		var e error
		open, e = tx.Escalations().ListOpen(ctx)
		return e
	})
	if len(open) > 0 {
		fmt.Printf("%d escalation(s) awaiting your decision — their clusters stay blocked until answered:\n", len(open))
		for _, esc := range open {
			fmt.Printf("  %s: %s (answer: orion escalations resolve %s)\n", esc.ID, esc.Reason, esc.ID)
		}
		fmt.Println()
	}
	// Show where the last run stopped, then re-enter the loop.
	if proj, _, perr := store.CurrentProjectSpec(ctx); perr == nil {
		if runID, ok, _ := store.LatestRunID(ctx, proj.ID); ok {
			if events, eerr := store.ListRunEventsAfter(ctx, runID, 0); eerr == nil && len(events) > 0 {
				last := events[len(events)-1]
				fmt.Printf("last run %s stopped at: %s %s %s\n", runID, last.Phase, last.Status, last.Detail)
			}
		}
	}
	_ = store.Close() // cmdRun reopens the store itself
	fmt.Println("resuming (unchanged clusters skip proof via the persisted memo)…")
	return cmdRun(args)
}
