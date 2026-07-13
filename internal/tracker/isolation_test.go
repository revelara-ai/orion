package tracker

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// failingBackend simulates a tracker outage: every projection attempt errors.
type failingBackend struct{}

func (failingBackend) Name() string { return "dead" }
func (failingBackend) Project(context.Context, string, []Task) error {
	return errors.New("tracker outage")
}

// TestProjectorSurfacesBackendOutageBounded (or-mvr.4, C8 / inc-3ik): the
// tracker kill test. A dead backend returns an error promptly — no hang — and
// leaves the store untouched (no tracker_sync record, no task mutation): the
// store is truth, the tracker is a view.
func TestProjectorSurfacesBackendOutageBounded(t *testing.T) {
	s, pid := seed(t)
	p := &Projector{store: s, backend: failingBackend{}}
	start := time.Now()
	_, err := p.Project(context.Background(), pid)
	if time.Since(start) > 5*time.Second {
		t.Fatalf("tracker outage wedged projection for %v", time.Since(start))
	}
	if err == nil || !strings.Contains(err.Error(), "tracker outage") {
		t.Fatalf("a dead tracker must surface its error, got %v", err)
	}
	if n := storeTaskCount(t, s, pid); n != 3 {
		t.Fatalf("a failed projection must not mutate the store: %d tasks", n)
	}
	var synced bool
	_ = s.WithTx(context.Background(), func(tx *contextstore.Tx) error {
		_, ok, err := tx.PolarisContext().Get(context.Background(), pid, "tracker_sync")
		synced = err == nil && ok
		return nil
	})
	if synced {
		t.Fatal("a failed projection must not record a tracker_sync entry")
	}
}

// TestLoopPathDoesNotImportTracker (or-mvr.4): the isolation guard. The
// tracker is a CLI-invoked projection (cmd/orion tracker), NOT a loop
// dependency — the conductor's import graph must never grow an edge to it,
// or a tracker outage lands on the loop's critical path.
func TestLoopPathDoesNotImportTracker(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go binary unavailable")
	}
	cmd := exec.Command(gobin, "list", "-deps", "github.com/revelara-ai/orion/internal/conductor")
	cmd.Dir = "../.."
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	if strings.Contains(string(out), "internal/tracker") {
		t.Fatal("the conductor (loop path) must not depend on internal/tracker — the tracker is an off-path projection (or-mvr.4)")
	}
}
