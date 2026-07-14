package conductor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/orchestrator"
)

// TestPruneGzipStillResumable (or-8my7 S4): a session compressed by the retention
// sweep is still discoverable AND still loads faithfully (transparent gunzip) —
// space is reclaimed without losing resumability.
func TestPruneGzipStillResumable(t *testing.T) {
	st := openStore(t)
	a := NewOrionAgent(nil, orchestrator.NewWithStore(st), RoleTemplate{})
	msgs := convo("an old conversation")
	a.sessions["old"] = msgs
	a.persistSession("old", msgs)
	dir := a.sessionsDir()
	stamp := a.sessionStamp("old")

	// Age the hot log past the gzip threshold.
	old := time.Now().Add(-30 * 24 * time.Hour)
	_ = os.Chtimes(sessionLogPath(dir, stamp), old, old)

	rep, err := PruneSessions(dir, PruneOptions{GzipOlderThan: 7 * 24 * time.Hour, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Compressed) != 1 || rep.Compressed[0] != stamp {
		t.Fatalf("the stale log must be compressed: %+v", rep)
	}
	// The hot file is gone; the archive exists.
	if _, err := os.Stat(sessionLogPath(dir, stamp)); !os.IsNotExist(err) {
		t.Fatal("the hot .jsonl must be removed after compression")
	}
	if _, err := os.Stat(sessionLogPath(dir, stamp) + ".gz"); err != nil {
		t.Fatalf("the .jsonl.gz archive must exist: %v", err)
	}
	// Still listed, and still loads faithfully from the archive.
	infos, _ := listSessions(dir)
	if len(infos) != 1 || infos[0].Stamp != stamp {
		t.Fatalf("an archived session must still be listed: %+v", infos)
	}
	loaded, err := loadSessionHistory(dir, stamp)
	if err != nil {
		t.Fatalf("an archived session must still load: %v", err)
	}
	if len(loaded) != len(msgs) || loaded[1].Content[1].ToolUse.ID != "tu_1" {
		t.Fatalf("the archived history must round-trip faithfully: %+v", loaded)
	}
}

// TestPruneRetentionLegs (or-8my7 S4): compression is default-safe (never
// deletes); delete is opt-in and enumerated; a fresh session is untouched.
func TestPruneRetentionLegs(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	write := func(name string, age time.Duration) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(`{"role":"user","content":[]}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		mt := now.Add(-age)
		_ = os.Chtimes(p, mt, mt)
	}
	write("20260101T000000_a.jsonl", 40*24*time.Hour) // ancient hot
	write("20260601T000000_b.jsonl.gz", 40*24*time.Hour) // ancient cold
	write("20260713T000000_c.jsonl", 1*time.Hour)      // fresh

	// Compress-only (default): the ancient hot log compresses; nothing deleted.
	rep, err := PruneSessions(dir, PruneOptions{GzipOlderThan: 7 * 24 * time.Hour, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Deleted) != 0 {
		t.Fatalf("compress-only must never delete, got %+v", rep.Deleted)
	}
	if len(rep.Compressed) != 1 || rep.Compressed[0] != "20260101T000000_a" {
		t.Fatalf("only the stale hot log should compress: %+v", rep.Compressed)
	}
	// The fresh log is untouched.
	if _, err := os.Stat(filepath.Join(dir, "20260713T000000_c.jsonl")); err != nil {
		t.Fatal("a fresh session must not be touched")
	}

	// Opt-in delete leg removes anything past the delete horizon (hot or cold).
	rep2, err := PruneSessions(dir, PruneOptions{DeleteOlderThan: 30 * 24 * time.Hour, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep2.Deleted) != 2 {
		t.Fatalf("delete leg must remove both ancient sessions (hot-now-gz + cold): %+v", rep2.Deleted)
	}
	if _, err := os.Stat(filepath.Join(dir, "20260713T000000_c.jsonl")); err != nil {
		t.Fatal("the fresh session must survive the delete sweep")
	}
}
