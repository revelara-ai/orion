package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/revelara-ai/orion/internal/checkpoint"
)

// cmdCheckpoint implements `orion checkpoint save|rollback <id>` (or-ykz.15):
// a shadow-git snapshot of the current worktree, restorable exactly. The
// shadow object store lives under the data dir (outside the worktree), so a
// rollback's cleanup never touches the checkpoint store. This is the manual
// surface; the per-turn auto-checkpoint (invisible to the generator) is the
// harness integration tracked separately.
func cmdCheckpoint(args []string) int {
	if len(args) < 2 || (args[0] != "save" && args[0] != "rollback") {
		fmt.Fprintln(os.Stderr, "usage: orion checkpoint save|rollback <id>")
		return 2
	}
	action, id := args[0], args[1]

	wt, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion checkpoint:", err)
		return 1
	}
	dataDir, err := resolveDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion checkpoint:", err)
		return 1
	}
	shadow := filepath.Join(dataDir, "checkpoints", "shadow.git")
	store, err := checkpoint.New(wt, shadow)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion checkpoint:", err)
		return 1
	}
	ctx := context.Background()

	switch action {
	case "save":
		if err := store.Checkpoint(ctx, id); err != nil {
			fmt.Fprintln(os.Stderr, "orion checkpoint save:", err)
			return 1
		}
		fmt.Printf("checkpoint %q saved (worktree %s)\n", id, wt)
	case "rollback":
		if err := store.Rollback(ctx, id); err != nil {
			fmt.Fprintln(os.Stderr, "orion checkpoint rollback:", err)
			return 1
		}
		fmt.Printf("worktree restored to checkpoint %q\n", id)
	}
	return 0
}
