package conductor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/revelara-ai/orion/pkg/llm"
)

// Session discovery + resume (or-8my7 S2). /sessions lists the on-disk message
// logs; /resume <stamp-or-id> reloads one into the active session so the
// conversation continues after a process restart. Both read the append-only
// JSONL logs written by persistSession — no separate index to keep in sync
// (discovery is the filesystem itself).

// ListSessions returns the resumable session logs under dir, newest first — the
// CLI-facing wrapper over the discovery used by /sessions (or-8my7 S3).
func ListSessions(dir string) ([]SessionInfo, error) { return listSessions(dir) }

// ResolveSession maps a stamp or id fragment to a concrete session stamp.
func ResolveSession(dir, target string) (string, error) { return resolveSessionStamp(dir, target) }

// RenderSessionTranscript renders a session's markdown transcript from its
// faithful log — for `orion sessions show`, recall without starting the TUI.
func RenderSessionTranscript(dir, stamp string) (string, error) {
	msgs, err := loadSessionHistory(dir, stamp)
	if err != nil {
		return "", err
	}
	return renderTranscript(msgs), nil
}

// sessionsDir is the on-disk home of the session logs, or "" when store-less.
func (a *OrionAgent) sessionsDir() string {
	if a.conductor == nil {
		return ""
	}
	st := a.conductor.Store()
	if st == nil {
		return ""
	}
	return filepath.Join(st.Dir(), "sessions")
}

// SessionInfo is a listing row for a resumable session.
type SessionInfo struct {
	Stamp    string
	Modified time.Time
	Messages int
	Intent   string // first developer message (excerpt)
}

// listSessions returns the resumable sessions under dir, newest activity first.
func listSessions(dir string) ([]SessionInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []SessionInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		stamp := strings.TrimSuffix(e.Name(), ".jsonl")
		si := SessionInfo{Stamp: stamp}
		if fi, err := e.Info(); err == nil {
			si.Modified = fi.ModTime()
		}
		if msgs, err := loadSessionHistory(dir, stamp); err == nil {
			si.Messages = len(msgs)
			si.Intent = firstUserText(msgs)
		}
		out = append(out, si)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}

// firstUserText returns the first developer message's text (for the listing).
func firstUserText(msgs []llm.Message) string {
	for _, m := range msgs {
		if m.Role != llm.RoleUser {
			continue
		}
		for _, b := range m.Content {
			if b.Type == llm.BlockText && strings.TrimSpace(b.Text) != "" {
				return strings.TrimSpace(b.Text)
			}
		}
	}
	return ""
}

// resolveSessionStamp maps a developer-supplied target (an exact stamp, or a
// substring of one — an id fragment) to a stamp, preferring the most recent match.
func resolveSessionStamp(dir, target string) (string, error) {
	if _, err := os.Stat(sessionLogPath(dir, target)); err == nil {
		return target, nil
	}
	infos, err := listSessions(dir)
	if err != nil {
		return "", err
	}
	for _, s := range infos { // newest first
		if strings.Contains(s.Stamp, target) {
			return s.Stamp, nil
		}
	}
	return "", fmt.Errorf("resume: no session matching %q — /sessions lists them", target)
}

// sessionsView renders the /sessions listing.
func (a *OrionAgent) sessionsView() (string, error) {
	dir := a.sessionsDir()
	if dir == "" {
		return "No session store (offline).", nil
	}
	infos, err := listSessions(dir)
	if err != nil || len(infos) == 0 {
		return "No resumable sessions yet.", nil
	}
	var b strings.Builder
	b.WriteString("Resumable sessions (newest first) — /resume <stamp-or-id>:\n")
	for _, s := range infos {
		fmt.Fprintf(&b, "  %s · %d msg · %s\n", s.Stamp, s.Messages, excerpt(s.Intent, 64))
	}
	return b.String(), nil
}

// resumeSession reloads a prior session's history into the CURRENT session so the
// conversation continues. The loaded history persists into this session's own log
// on the next turn (the original log is left untouched — resume never mutates the
// source).
func (a *OrionAgent) resumeSession(currentSessionID, arg string) (string, error) {
	target := strings.TrimSpace(arg)
	if target == "" {
		return "resume: name a session — /sessions lists them", nil
	}
	dir := a.sessionsDir()
	if dir == "" {
		return "", fmt.Errorf("resume: no session store")
	}
	stamp, err := resolveSessionStamp(dir, target)
	if err != nil {
		return "", err
	}
	loaded, err := loadSessionHistory(dir, stamp)
	if err != nil {
		return "", fmt.Errorf("resume %s: %w", stamp, err)
	}
	if len(loaded) == 0 {
		return "", fmt.Errorf("resume: session %s has no messages", stamp)
	}
	a.mu.Lock()
	a.sessions[currentSessionID] = append([]llm.Message(nil), loaded...)
	a.persisted[currentSessionID] = 0 // continuation writes the full history into this session's log
	a.mu.Unlock()
	return fmt.Sprintf("Resumed %s (%d messages) — continue where it left off.", stamp, len(loaded)), nil
}

// excerpt trims s to n runes with an ellipsis (single-line).
func excerpt(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
