package tracker

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// seed creates a project + epic + 3 tasks; returns projectID and the store.
func seed(t *testing.T) (*contextstore.Store, string) {
	t.Helper()
	s, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()
	var pid string
	if err := s.WithTx(ctx, func(tx *contextstore.Tx) error {
		pid, _ = tx.Projects().Create(ctx, "demo", "build a time service", "http-service")
		sid, _ := tx.Specs().CreateDraft(ctx, pid)
		eid, _ := tx.Epics().Create(ctx, pid, sid, "Deliver: time service")
		for _, title := range []string{"scaffold", "handler", "capacity"} {
			if _, e := tx.Tasks().Create(ctx, eid, title, "cmd/"); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return s, pid
}

func storeTaskCount(t *testing.T, s *contextstore.Store, projectID string) int {
	t.Helper()
	ctx := context.Background()
	n := 0
	_ = s.WithTx(ctx, func(tx *contextstore.Tx) error {
		epic, err := tx.Epics().LatestForProject(ctx, projectID)
		if err != nil {
			return err
		}
		ts, err := tx.Tasks().ListByEpic(ctx, epic.ID)
		n = len(ts)
		return err
	})
	return n
}

// TestProjectEpicTasksToBeads: projecting writes the task subset to the beads
// JSONL view; the tracker is one-way — editing it out-of-band does not mutate the
// store.
func TestProjectEpicTasksToBeads(t *testing.T) {
	ctx := context.Background()
	s, pid := seed(t)
	dir := t.TempDir()

	p, err := New("beads", s, dir)
	if err != nil {
		t.Fatalf("new projector: %v", err)
	}
	if p.Backend().Name() != "beads" {
		t.Fatalf("backend = %q, want beads", p.Backend().Name())
	}
	n, err := p.Project(ctx, pid)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if n != 3 {
		t.Fatalf("projected %d tasks, want 3", n)
	}

	path := p.Backend().(JSONLBackend).Path
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read projection: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 3 {
		t.Fatalf("projection has %d lines, want 3", len(lines))
	}
	for _, want := range []string{"scaffold", "handler", "capacity"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("projection missing task %q", want)
		}
	}

	// Out-of-band tracker edit must NOT mutate the store (one-way; store is truth).
	before := storeTaskCount(t, s, pid)
	if err := os.WriteFile(path, append(data, []byte(`{"id":"rogue","title":"injected","status":"done"}`+"\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if after := storeTaskCount(t, s, pid); after != before {
		t.Fatalf("store task count changed after tracker edit: %d → %d (tracker must be a view)", before, after)
	}

	// tracker_sync recorded.
	var hasSync bool
	_ = s.WithTx(ctx, func(tx *contextstore.Tx) error {
		_, ok, _ := tx.PolarisContext().Get(ctx, pid, "tracker_sync")
		hasSync = ok
		return nil
	})
	if !hasSync {
		t.Fatal("tracker_sync record not written")
	}
}

// TestUnknownBackendRejected.
func TestUnknownBackendRejected(t *testing.T) {
	s, _ := seed(t)
	if _, err := New("jira", s, t.TempDir()); err == nil {
		t.Fatal("unknown backend should error in V2.0 (beads only)")
	}
}
