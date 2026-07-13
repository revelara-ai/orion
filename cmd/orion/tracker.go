package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/tracker"
)

// cmdTracker implements `orion tracker project`: a one-way store→tracker
// projection of the current project's epic + tasks to the configured backend
// (beads JSONL by default at <data-dir>/tracker.jsonl). It never mutates task
// state in the store — the Context Store stays the source of truth (or-y0z).
func cmdTracker(args []string) int {
	if len(args) == 0 || args[0] != "project" {
		fmt.Fprintln(os.Stderr, "orion tracker: usage: orion tracker project")
		return 2
	}
	dir, err := resolveDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion tracker project:", err)
		return 1
	}
	store, err := contextstore.Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion tracker project:", err)
		return 1
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	proj, _, err := store.CurrentProjectSpec(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion tracker project: no current project (submit + plan first)")
		return 1
	}

	tp, err := tracker.New(os.Getenv("ORION_TRACKER_BACKEND"), store, dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion tracker project:", err)
		return 1
	}
	n, err := tp.Project(ctx, proj.ID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion tracker project:", err)
		return 1
	}
	fmt.Printf("projected %d task(s) to %s\n", n, filepath.Join(dir, "tracker.jsonl"))
	return 0
}
