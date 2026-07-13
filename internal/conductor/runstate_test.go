package conductor

import (
	"context"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// TestTeeRunSinkPersistsAndForwards (or-v9f.16): the tee writes the event to
// the store WITH task attribution and still forwards to the terminal sink;
// a nil store is a passthrough.
func TestTeeRunSinkPersistsAndForwards(t *testing.T) {
	ctx := context.Background()
	s, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	var got []PhaseEvent
	base := PhaseSink(func(ev PhaseEvent) { got = append(got, ev) })

	sink := teeRunSink(base, s, "p1", "run-x", "T7")
	sink.emit("Prove", PhaseDone, "Accept")

	if len(got) != 1 || got[0].Phase != "Prove" {
		t.Fatalf("the terminal sink must still receive the event: %+v", got)
	}
	events, err := s.ListRunEventsAfter(ctx, "run-x", 0)
	if err != nil || len(events) != 1 {
		t.Fatalf("want 1 persisted event, got %d (%v)", len(events), err)
	}
	e := events[0]
	if e.TaskID != "T7" || e.Phase != "Prove" || e.Status != "done" || e.Detail != "Accept" || e.ProjectID != "p1" {
		t.Fatalf("persisted event lost fidelity: %+v", e)
	}

	// nil store: pure passthrough, never a panic.
	teeRunSink(base, nil, "p1", "run-x", "").emit("Generate", PhaseRunning, "")
	if len(got) != 2 {
		t.Fatal("nil-store tee must still forward")
	}
}
