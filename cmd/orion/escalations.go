package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// cmdEscalations implements the escalation inbox verbs (or-v9f.4) — the queue of
// decisions the loop routed to a human:
//
//	orion escalations list              open escalations, oldest first
//	orion escalations show <id>         one escalation with its full payload
//	orion escalations resolve <id> [note...]   close it out with a decision note
func cmdEscalations(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "orion escalations: usage: orion escalations list|show <id>|resolve <id> [note...]")
		return 2
	}
	dir, err := resolveDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion escalations:", err)
		return 1
	}
	store, err := contextstore.Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion escalations:", err)
		return 1
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()

	switch args[0] {
	case "list":
		var open []contextstore.Escalation
		err := store.WithTx(ctx, func(tx *contextstore.Tx) error {
			var e error
			open, e = tx.Escalations().ListOpen(ctx)
			return e
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "orion escalations list:", err)
			return 1
		}
		if len(open) == 0 {
			fmt.Println("no open escalations")
			return 0
		}
		for _, e := range open {
			task := e.TaskID
			if task == "" {
				task = "(project-level)"
			}
			fmt.Printf("%s  %s  task=%s  %s\n", e.ID, e.CreatedAt, task, e.Reason)
		}
		return 0
	case "show":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "orion escalations show: usage: orion escalations show <id>")
			return 2
		}
		var esc contextstore.Escalation
		err := store.WithTx(ctx, func(tx *contextstore.Tx) error {
			var e error
			esc, e = tx.Escalations().Get(ctx, args[1])
			return e
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "orion escalations show:", err)
			return 1
		}
		fmt.Printf("id:         %s\nproject:    %s\ntask:       %s\ncreated:    %s\nreason:     %s\n", esc.ID, esc.ProjectID, esc.TaskID, esc.CreatedAt, esc.Reason)
		if esc.Detail != "" {
			fmt.Printf("detail:\n  %s\n", strings.ReplaceAll(esc.Detail, "\n", "\n  "))
		}
		if esc.Resolved {
			fmt.Printf("resolved:   %s\nresolution: %s\n", esc.ResolvedAt, esc.Resolution)
		} else {
			fmt.Println("status:     OPEN")
		}
		return 0
	case "resolve":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "orion escalations resolve: usage: orion escalations resolve <id> [note...]")
			return 2
		}
		note := strings.Join(args[2:], " ")
		if note == "" {
			note = "resolved by developer"
		}
		err := store.WithTx(ctx, func(tx *contextstore.Tx) error {
			// or-gb1.8: the resolution is a Gold label, captured atomically.
			// The CLI is the human's own hand — no model produced this act.
			return tx.ResolveEscalationGold(ctx, args[1], note, "human/cli", "")
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "orion escalations resolve:", err)
			return 1
		}
		fmt.Printf("resolved: %s\n", args[1])
		return 0
	default:
		fmt.Fprintln(os.Stderr, "orion escalations: unknown subcommand", args[0])
		return 2
	}
}
