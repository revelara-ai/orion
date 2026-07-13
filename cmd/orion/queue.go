package main

import (
	"context"
	"fmt"
	"os"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// cmdQueue implements the intent-queue verbs (or-v9f.1). Submit enqueues behind
// the active intent instead of superseding it; these verbs let the developer see
// and drive the queue:
//
//	orion queue list             active + queued intents (FIFO)
//	orion queue next             promote the FIFO head when nothing is active
//	orion queue abandon <id>     close out a queued/active intent
//	orion queue activate <id>    jump the queue: demote the active intent back to
//	                             queued and activate the named one
func cmdQueue(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "orion queue: usage: orion queue list|next|abandon <project-id>|activate <project-id>")
		return 2
	}
	dir, err := resolveDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion queue:", err)
		return 1
	}
	store, err := contextstore.Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion queue:", err)
		return 1
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	switch args[0] {
	case "list":
		return queueList(ctx, store)
	case "next":
		next, promoted, err := store.ActivateNextQueued(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "orion queue next: nothing active and nothing queued")
			return 1
		}
		if !promoted {
			fmt.Printf("already active: %s  %q — finish or abandon it first\n", next.ID, next.Intent)
			return 0
		}
		fmt.Printf("activated: %s  %q\n", next.ID, next.Intent)
		return 0
	case "abandon":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "orion queue abandon: usage: orion queue abandon <project-id>")
			return 2
		}
		if err := setProjectStatus(ctx, store, args[1], "abandoned"); err != nil {
			fmt.Fprintln(os.Stderr, "orion queue abandon:", err)
			return 1
		}
		fmt.Printf("abandoned: %s\n", args[1])
		return 0
	case "activate":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "orion queue activate: usage: orion queue activate <project-id>")
			return 2
		}
		err := store.WithTx(ctx, func(tx *contextstore.Tx) error {
			target, e := tx.Projects().Get(ctx, args[1])
			if e != nil {
				return fmt.Errorf("project %s: %w", args[1], e)
			}
			if target.Status == "delivered" || target.Status == "abandoned" {
				return fmt.Errorf("project %s is %s (terminal)", target.ID, target.Status)
			}
			if active, e := tx.Projects().Active(ctx); e == nil && active.ID != target.ID {
				if e := tx.Projects().SetStatus(ctx, active.ID, "queued"); e != nil {
					return e
				}
			}
			return tx.Projects().SetStatus(ctx, target.ID, "active")
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "orion queue activate:", err)
			return 1
		}
		fmt.Printf("activated: %s\n", args[1])
		return 0
	default:
		fmt.Fprintln(os.Stderr, "orion queue: unknown subcommand", args[0])
		return 2
	}
}

func queueList(ctx context.Context, store *contextstore.Store) int {
	if p, _, err := store.CurrentProjectSpec(ctx); err == nil {
		fmt.Printf("active:  %s  %q\n", p.ID, p.Intent)
	} else {
		fmt.Println("active:  (none)")
	}
	queued, err := store.QueuedProjects(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion queue list:", err)
		return 1
	}
	if len(queued) == 0 {
		fmt.Println("queued:  (empty)")
		return 0
	}
	for i, p := range queued {
		fmt.Printf("queued:  %d. %s  %q\n", i+1, p.ID, p.Intent)
	}
	return 0
}

func setProjectStatus(ctx context.Context, store *contextstore.Store, id, status string) error {
	return store.WithTx(ctx, func(tx *contextstore.Tx) error {
		return tx.Projects().SetStatus(ctx, id, status)
	})
}
