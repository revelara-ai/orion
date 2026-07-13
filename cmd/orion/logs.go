package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// logsTailLines is how many trailing events the one-shot `orion logs` shows.
const logsTailLines = 20

// cmdLogs implements `orion logs [run-id] [--follow|-f]` (or-hy4, A17): the
// tail of Orion's self-instrumentation — the persisted run_events stream
// (or-kzf.3) for the latest (or given) run. One-shot prints the last N events;
// --follow keeps polling the ListRunEventsAfter cursor and switches to a newer
// run when one starts. `orion trace` stays the retrospective full-run report;
// this is the live tail.
func cmdLogs(args []string) int {
	follow := false
	runID := ""
	for _, a := range args {
		switch a {
		case "--follow", "-f":
			follow = true
		default:
			runID = a
		}
	}
	dir, err := resolveDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion logs:", err)
		return 1
	}
	store, err := contextstore.Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion logs:", err)
		return 1
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	projID := ""
	if proj, _, err := store.CurrentOrLastDeliveredProjectSpec(ctx); err == nil {
		projID = proj.ID
	}
	if runID == "" {
		if projID == "" {
			fmt.Println("no project yet — nothing to tail (start one with `orion run`)")
			return 0
		}
		runs, err := store.ListRunIDs(ctx, projID, 1)
		if err != nil || len(runs) == 0 {
			fmt.Println("no persisted runs yet — start one with `orion run`")
			return 0
		}
		runID = runs[0]
	}

	out, lastID, err := logsTail(ctx, store, runID, logsTailLines)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion logs:", err)
		return 1
	}
	fmt.Print(out)
	if !follow {
		return 0
	}
	for { // live tail: poll the cursor; hop onto a newer run when one starts
		time.Sleep(time.Second)
		events, err := store.ListRunEventsAfter(ctx, runID, lastID)
		if err != nil {
			fmt.Fprintln(os.Stderr, "orion logs:", err)
			return 1
		}
		for _, e := range events {
			fmt.Print(formatRunEvent(e))
			lastID = e.ID
		}
		if projID != "" {
			if runs, err := store.ListRunIDs(ctx, projID, 1); err == nil && len(runs) > 0 && runs[0] != runID {
				runID, lastID = runs[0], 0
				fmt.Printf("── newer run %s started — following it ──\n", runID)
			}
		}
	}
}

// logsTail renders the LAST n events of a run and returns the rendered text
// plus the highest event id (the follow cursor).
func logsTail(ctx context.Context, store *contextstore.Store, runID string, n int) (string, int64, error) {
	events, err := store.ListRunEventsAfter(ctx, runID, 0)
	if err != nil {
		return "", 0, err
	}
	if len(events) == 0 {
		return fmt.Sprintf("run %s: no events\n", runID), 0, nil
	}
	start := 0
	if len(events) > n {
		start = len(events) - n
	}
	var b strings.Builder
	fmt.Fprintf(&b, "run %s — %d event(s), showing last %d (orion trace for the full report)\n", runID, len(events), len(events)-start)
	for _, e := range events[start:] {
		b.WriteString(formatRunEvent(e))
	}
	return b.String(), events[len(events)-1].ID, nil
}

// formatRunEvent renders one run event as a single tail line.
func formatRunEvent(e contextstore.RunEvent) string {
	task := ""
	if e.TaskID != "" {
		task = " [" + e.TaskID + "]"
	}
	return fmt.Sprintf("%s  %-10s%s %s: %s\n", e.CreatedAt, e.Phase, task, e.Status, e.Detail)
}
