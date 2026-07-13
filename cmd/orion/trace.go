package main

import (
	"context"
	"fmt"
	"os"

	"github.com/revelara-ai/orion/internal/conductor"
	"github.com/revelara-ai/orion/internal/contextstore"
)

// cmdTrace implements `orion trace` (or-kzf.3): the full persisted trace of a
// build run — every phase event with per-task attribution and timing — plus
// the derived run summary and the cross-run DRIFT indicator (rising
// refinement attempts, falling first-pass proof rate vs the previous run).
func cmdTrace(args []string) int {
	dir, err := resolveDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion trace:", err)
		return 1
	}
	store, err := contextstore.Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion trace:", err)
		return 1
	}
	defer store.Close()
	ctx := context.Background()

	proj, _, err := store.CurrentOrLastDeliveredProjectSpec(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion trace: no project:", err)
		return 1
	}
	runs, err := store.ListRunIDs(ctx, proj.ID, 10)
	if err != nil || len(runs) == 0 {
		fmt.Println("no persisted runs for this project yet — start one with `orion run`")
		return 0
	}
	runID := runs[0]
	if len(args) > 0 && args[0] != "--drift" {
		runID = args[0]
	}

	events, err := store.ListRunEventsAfter(ctx, runID, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion trace:", err)
		return 1
	}
	fmt.Printf("trace %s (project %s) — %d event(s)\n", runID, proj.Name, len(events))
	for _, e := range events {
		task := ""
		if e.TaskID != "" {
			task = " [" + e.TaskID + "]"
		}
		fmt.Printf("%s  %-10s%s %s: %s\n", e.CreatedAt, e.Phase, task, e.Status, e.Detail)
	}

	sum, err := conductor.SummarizeRun(ctx, store, runID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion trace: summarize:", err)
		return 1
	}
	fmt.Printf("\nsummary: %d task(s), %d attempt(s) (%.2f/task), first-pass proof %.0f%%, proven %d, wall %.1fs, failed=%v\n",
		sum.Tasks, sum.Attempts, sum.AttemptsPerTask(), sum.FirstPassRate()*100, sum.ProvenTasks, sum.WallSeconds, sum.Failed)

	// Drift vs the previous run of this project, when one exists.
	for _, prior := range runs {
		if prior == runID {
			continue
		}
		prev, perr := conductor.SummarizeRun(ctx, store, prior)
		if perr != nil {
			continue
		}
		if sig := conductor.DriftSignal(prev, sum); sig != "" {
			fmt.Println(sig)
		} else {
			fmt.Printf("no drift vs previous run %s\n", prior)
		}
		break
	}
	return 0
}
