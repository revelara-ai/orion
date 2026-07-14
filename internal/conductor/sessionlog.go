package conductor

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"

	"github.com/revelara-ai/orion/pkg/llm"
)

// Session message log (or-8my7): the faithful, resumable record of a conductor
// conversation, kept as an append-only JSONL of llm.Message beside the
// human-readable markdown transcript. Unlike the markdown, it preserves tool_use
// IDs, the tool_result→tool_use linkage, and provider replay signatures, so a
// session can be reloaded and continued after the process exits. It lives on disk
// (one file per session under ~/.orion/sessions), not in the proof-state DB —
// conversation history is cold, bulk, append-only data and is space-managed at
// the file level (retention/gzip), never bloating the transactional store.

// persistSessionLog writes the turn's history to the session's JSONL log. In the
// common case (history grew) it APPENDS only the new messages — O(1) per turn and
// tolerant of a torn final line on crash. When compaction has REPLACED the
// history with a shorter summary, it rewrites the whole log atomically so the log
// reflects the current (compacted) state rather than stale pre-compaction turns.
func (a *OrionAgent) persistSessionLog(sessionID, path string, convo []llm.Message) error {
	a.mu.Lock()
	prev := a.persisted[sessionID]
	a.mu.Unlock()

	switch {
	case prev > len(convo):
		if err := rewriteSessionLog(path, convo); err != nil {
			return err
		}
	case len(convo) > prev:
		if err := appendSessionLog(path, convo[prev:]); err != nil {
			return err
		}
	default:
		return nil // unchanged — nothing to write
	}

	a.mu.Lock()
	a.persisted[sessionID] = len(convo)
	a.mu.Unlock()
	return nil
}

// appendSessionLog appends messages as JSONL (one object per line).
func appendSessionLog(path string, msgs []llm.Message) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	for i := range msgs {
		if err := enc.Encode(msgs[i]); err != nil {
			return err
		}
	}
	return nil
}

// rewriteSessionLog atomically replaces the log with the given history (used only
// when compaction shrinks it — the append path handles every normal turn).
func rewriteSessionLog(path string, convo []llm.Message) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	for i := range convo {
		if err := enc.Encode(convo[i]); err != nil {
			_ = f.Close()
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// loadSessionHistory reads a session's JSONL log back into its message history.
// A torn or unparseable line (e.g. a crash mid-append) is skipped rather than
// failing the load — the log is a recovery aid, not a transactional record.
// Returns os.ErrNotExist (wrapped) when no log exists for the stamp.
func loadSessionHistory(dir, stamp string) ([]llm.Message, error) {
	rc, err := openSessionLog(dir, stamp)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()

	var out []llm.Message
	r := bufio.NewReader(rc)
	for {
		line, rerr := r.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var m llm.Message
			if json.Unmarshal(line, &m) == nil {
				out = append(out, m)
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			return out, rerr
		}
	}
	return out, nil
}

// sessionLogPath is the on-disk path of a session's ACTIVE (uncompressed) log.
func sessionLogPath(dir, stamp string) string {
	return dir + string(os.PathSeparator) + stamp + ".jsonl"
}

// openSessionLog opens a session's log for reading, transparently handling a
// gzip-compressed (archived) log (or-8my7 S4): the hot .jsonl if present, else
// the compressed .jsonl.gz. The returned ReadCloser closes the underlying file.
func openSessionLog(dir, stamp string) (io.ReadCloser, error) {
	if f, err := os.Open(sessionLogPath(dir, stamp)); err == nil {
		return f, nil
	}
	f, err := os.Open(sessionLogPath(dir, stamp) + ".gz")
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return gzReadCloser{gz: gz, f: f}, nil
}

// gzReadCloser closes both the gzip reader and its backing file.
type gzReadCloser struct {
	gz *gzip.Reader
	f  *os.File
}

func (g gzReadCloser) Read(p []byte) (int, error) { return g.gz.Read(p) }
func (g gzReadCloser) Close() error {
	err := g.gz.Close()
	if cerr := g.f.Close(); err == nil {
		err = cerr
	}
	return err
}
