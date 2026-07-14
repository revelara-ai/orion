package conductor

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Session log space management (or-8my7 S4). Conversation logs are cold, bulk,
// append-only data, so they are managed at the file level — never bloating the
// proof-state DB. gzipOlderThan compresses stale hot logs (transparently read
// back by openSessionLog); deleteOlderThan is an OPT-IN hard prune (default off —
// session history is the developer's; we compress by default, delete only on
// request). PruneReport says exactly what was compressed/removed, so nothing is
// silently dropped.

// PruneOptions controls the retention sweep. A zero duration disables that leg.
type PruneOptions struct {
	GzipOlderThan   time.Duration // compress hot .jsonl logs older than this
	DeleteOlderThan time.Duration // delete logs (any form) older than this (opt-in)
	Now             time.Time      // reference time (age is Now - modtime)
}

// PruneReport is the outcome of a sweep — enumerated, never silent.
type PruneReport struct {
	Compressed []string // stamps compressed .jsonl → .jsonl.gz
	Deleted    []string // stamps removed entirely
	FreedBytes int64    // bytes reclaimed (pre-compression size of compressed + deleted)
}

// PruneSessions applies the retention policy to the session logs under dir.
func PruneSessions(dir string, opts PruneOptions) (PruneReport, error) {
	var rep PruneReport
	entries, err := os.ReadDir(dir)
	if err != nil {
		return rep, err
	}
	// Sort for deterministic report ordering.
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		isHot := strings.HasSuffix(name, ".jsonl")
		isCold := strings.HasSuffix(name, ".jsonl.gz")
		if !isHot && !isCold {
			continue // .md, .tmp, index, etc. are not our concern
		}
		path := filepath.Join(dir, name)
		fi, err := os.Stat(path)
		if err != nil {
			continue
		}
		age := opts.Now.Sub(fi.ModTime())
		stamp := strings.TrimSuffix(strings.TrimSuffix(name, ".gz"), ".jsonl")

		// Delete leg (opt-in) takes precedence over compression.
		if opts.DeleteOlderThan > 0 && age > opts.DeleteOlderThan {
			if err := os.Remove(path); err == nil {
				rep.Deleted = append(rep.Deleted, stamp)
				rep.FreedBytes += fi.Size()
			}
			continue
		}
		// Compress leg: only hot logs, only when a threshold is set.
		if isHot && opts.GzipOlderThan > 0 && age > opts.GzipOlderThan {
			pre, err := gzipFile(path)
			if err == nil {
				rep.Compressed = append(rep.Compressed, stamp)
				rep.FreedBytes += pre
			}
		}
	}
	return rep, nil
}

// gzipFile compresses path to path+".gz" (preserving the modtime so age stays
// meaningful) and removes the original on success. Returns the original size.
func gzipFile(path string) (int64, error) {
	in, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = in.Close() }()
	fi, err := in.Stat()
	if err != nil {
		return 0, err
	}
	tmp := path + ".gz.tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	gz := gzip.NewWriter(out)
	if _, err := io.Copy(gz, in); err != nil {
		_ = gz.Close()
		_ = out.Close()
		_ = os.Remove(tmp)
		return 0, err
	}
	if err := gz.Close(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return 0, err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	final := path + ".gz"
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return 0, err
	}
	_ = os.Chtimes(final, fi.ModTime(), fi.ModTime()) // keep age meaningful
	if err := os.Remove(path); err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// String renders a one-line human summary of a sweep.
func (r PruneReport) String() string {
	if len(r.Compressed) == 0 && len(r.Deleted) == 0 {
		return "nothing to prune"
	}
	return fmt.Sprintf("compressed %d, deleted %d, freed ~%dKB", len(r.Compressed), len(r.Deleted), r.FreedBytes/1024)
}
