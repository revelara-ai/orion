package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/memory"
)

// cmdBackup implements `orion backup [dest-dir]` (or-hy4, A17 self-operability):
// a consistent point-in-time snapshot of BOTH data stores — the Context Store
// (orion.db) and the memory store (memory.db) — via SQLite VACUUM INTO
// (WAL-safe; Orion may keep running). Default destination:
// <dataDir>/backups/<UTC timestamp>/.
func cmdBackup(args []string) int {
	dataDir, err := resolveDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion backup:", err)
		return 1
	}
	dest := filepath.Join(dataDir, "backups", time.Now().UTC().Format("20060102-150405"))
	if len(args) > 0 && args[0] != "" {
		dest = args[0]
	}
	ctx := context.Background()

	cs, err := contextstore.Open(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion backup: context store:", err)
		return 1
	}
	defer func() { _ = cs.Close() }()
	if err := cs.SnapshotTo(ctx, filepath.Join(dest, contextstore.DBFile)); err != nil {
		fmt.Fprintln(os.Stderr, "orion backup:", err)
		return 1
	}
	ms, err := memory.Open(dataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion backup: memory store:", err)
		return 1
	}
	defer func() { _ = ms.Close() }()
	if err := ms.SnapshotTo(ctx, filepath.Join(dest, "memory.db")); err != nil {
		fmt.Fprintln(os.Stderr, "orion backup:", err)
		return 1
	}
	fmt.Printf("backup written: %s\n  %s\n  %s\nrestore with: orion restore %s\n",
		dest, contextstore.DBFile, "memory.db", dest)
	return 0
}

// cmdRestore implements `orion restore <backup-dir>`: copies the snapshot DBs
// back over the live stores. Stale -wal/-shm siblings of the live DBs are
// removed (the snapshot is self-contained; a leftover WAL would resurrect
// post-backup writes). Run it while Orion is NOT running.
func cmdRestore(args []string) int {
	if len(args) == 0 || args[0] == "" {
		fmt.Fprintln(os.Stderr, "usage: orion restore <backup-dir>   (a directory produced by `orion backup`)")
		return 1
	}
	src := args[0]
	dataDir, err := resolveDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion restore:", err)
		return 1
	}
	// The Context Store snapshot is the restore's anchor — refuse a dir that
	// doesn't hold one (catches typos before any file is touched).
	if _, err := os.Stat(filepath.Join(src, contextstore.DBFile)); err != nil {
		fmt.Fprintf(os.Stderr, "orion restore: %s does not look like an orion backup (no %s): %v\n", src, contextstore.DBFile, err)
		return 1
	}
	restored := 0
	for _, name := range []string{contextstore.DBFile, "memory.db"} {
		snap := filepath.Join(src, name)
		if _, err := os.Stat(snap); err != nil {
			continue // a backup may predate one of the stores
		}
		live := filepath.Join(dataDir, name)
		for _, sib := range []string{live, live + "-wal", live + "-shm"} {
			if err := os.Remove(sib); err != nil && !os.IsNotExist(err) {
				fmt.Fprintln(os.Stderr, "orion restore:", err)
				return 1
			}
		}
		if err := copyFile(snap, live); err != nil {
			fmt.Fprintln(os.Stderr, "orion restore:", err)
			return 1
		}
		restored++
		fmt.Printf("restored %s\n", name)
	}
	if restored == 0 {
		fmt.Fprintln(os.Stderr, "orion restore: nothing restored")
		return 1
	}
	return 0
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- developer-provided backup path
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- data-dir path
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
