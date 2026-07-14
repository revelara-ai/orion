package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/conductor"
)

// cmdSessions implements `orion sessions` (or-8my7 S3): shell-side discovery and
// recall of conductor conversations, read from the on-disk message logs without
// starting the TUI.
//
//	orion sessions              list resumable sessions (newest first)
//	orion sessions show <id>    print a session's transcript (stamp or id fragment)
//	orion sessions prune        compress old logs (default gzip after 30d); --delete-after=<days> to remove
//
// To CONTINUE a session, start the conductor and use /resume <id> in the TUI.
func cmdSessions(args []string) int {
	dir, err := resolveDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion sessions:", err)
		return 1
	}
	sdir := filepath.Join(dir, "sessions")

	if len(args) > 0 && args[0] == "prune" {
		return sessionsPrune(sdir, args[1:])
	}
	if len(args) > 0 && args[0] == "show" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "orion sessions show: name a session (stamp or id fragment) — `orion sessions` lists them")
			return 1
		}
		stamp, err := conductor.ResolveSession(sdir, args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, "orion sessions show:", err)
			return 1
		}
		out, err := conductor.RenderSessionTranscript(sdir, stamp)
		if err != nil {
			fmt.Fprintln(os.Stderr, "orion sessions show:", err)
			return 1
		}
		fmt.Print(out)
		return 0
	}

	infos, err := conductor.ListSessions(sdir)
	if err != nil || len(infos) == 0 {
		fmt.Println("No resumable sessions yet.")
		return 0
	}
	fmt.Printf("%d resumable session(s) — `orion sessions show <id>` to read, /resume <id> in the conductor to continue:\n", len(infos))
	for _, s := range infos {
		fmt.Printf("  %s  %s  %d msg  %s\n",
			s.Stamp, s.Modified.Format("2006-01-02 15:04"), s.Messages, sessionExcerpt(s.Intent))
	}
	return 0
}

// sessionsPrune runs the retention sweep: compress-only by default (never
// deletes), with an opt-in --delete-after horizon. Enumerates what it changed.
func sessionsPrune(sdir string, args []string) int {
	gzipDays := 30
	deleteDays := 0 // opt-in
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--gzip-after="):
			gzipDays, _ = strconv.Atoi(strings.TrimPrefix(a, "--gzip-after="))
		case strings.HasPrefix(a, "--delete-after="):
			deleteDays, _ = strconv.Atoi(strings.TrimPrefix(a, "--delete-after="))
		default:
			fmt.Fprintln(os.Stderr, "orion sessions prune: unknown flag", a)
			return 1
		}
	}
	day := 24 * time.Hour
	rep, err := conductor.PruneSessions(sdir, conductor.PruneOptions{
		GzipOlderThan:   time.Duration(gzipDays) * day,
		DeleteOlderThan: time.Duration(deleteDays) * day,
		Now:             time.Now(),
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion sessions prune:", err)
		return 1
	}
	fmt.Println(rep.String())
	for _, s := range rep.Compressed {
		fmt.Println("  compressed", s)
	}
	for _, s := range rep.Deleted {
		fmt.Println("  deleted", s)
	}
	return 0
}

// sessionExcerpt trims an intent to one short line for the listing.
func sessionExcerpt(s string) string {
	const n = 60
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
