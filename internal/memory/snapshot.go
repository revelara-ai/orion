package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// SnapshotTo writes a CONSISTENT point-in-time copy of the memory store to
// path via SQLite's VACUUM INTO (WAL-safe, single self-contained file) — the
// backup primitive for `orion backup` (or-hy4). Refuses an existing target so
// a prior snapshot is never silently clobbered.
func (s *Store) SnapshotTo(ctx context.Context, path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("memory snapshot: target %s already exists — refusing to clobber a prior snapshot", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("memory snapshot: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("memory snapshot: %w", err)
	}
	return nil
}
