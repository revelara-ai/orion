package contextstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// SnapshotTo writes a CONSISTENT point-in-time copy of the store to path using
// SQLite's VACUUM INTO — safe under WAL (readers/writers keep going; the copy
// is a single self-contained file with no -wal/-shm siblings), so it is the
// backup primitive for `orion backup` (or-hy4). It refuses an existing target:
// a prior snapshot is never silently clobbered.
func (s *Store) SnapshotTo(ctx context.Context, path string) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("contextstore snapshot: target %s already exists — refusing to clobber a prior snapshot", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("contextstore snapshot: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return fmt.Errorf("contextstore snapshot: %w", err)
	}
	return nil
}
