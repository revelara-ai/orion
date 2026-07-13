package conductor

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// Run/phase survivability (or-v9f.16): BuildDAG tees every phase event into
// the context store as it happens, so a multi-hour run's progress outlives
// the terminal. Best-effort: a persist miss never fails a build — the tee
// swallows errors (the terminal sink still gets the event).

// newRunID mints a unique run identifier.
func newRunID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "run-" + hex.EncodeToString(b)
}

// teeRunSink wraps a PhaseSink so every event ALSO persists as a run event
// attributed to taskID ("" = run-level).
func teeRunSink(base PhaseSink, store *contextstore.Store, projectID, runID, taskID string) PhaseSink {
	if store == nil {
		return base
	}
	return func(ev PhaseEvent) {
		_ = store.AppendRunEvent(context.Background(), contextstore.RunEvent{
			ProjectID: projectID, RunID: runID, TaskID: taskID,
			Phase: ev.Phase, Status: string(ev.Status), Detail: ev.Detail,
		})
		if base != nil {
			base(ev)
		}
	}
}
