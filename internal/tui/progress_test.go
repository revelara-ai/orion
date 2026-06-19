package tui

import (
	"strings"
	"testing"
	"time"
)

// TestLivenessHeartbeat: when no event arrives within the heartbeat interval, a
// heartbeat tick is emitted so no pane goes silent past the threshold.
func TestLivenessHeartbeat(t *testing.T) {
	base := time.Unix(1000, 0)
	clk := base
	bus := NewProgressBus(time.Second)
	bus.now = func() time.Time { return clk }
	bus.last = clk

	bus.Emit("behavioral", "synthesizing corpus")
	// Within the window → no heartbeat due.
	if bus.HeartbeatDue(clk.Add(500 * time.Millisecond)) {
		t.Fatal("heartbeat should not be due within the interval")
	}
	// Past the window → due; Tick emits a heartbeat.
	if !bus.HeartbeatDue(clk.Add(2 * time.Second)) {
		t.Fatal("heartbeat should be due after the interval")
	}
	if !bus.Tick(clk.Add(2 * time.Second)) {
		t.Fatal("Tick should emit a heartbeat when due")
	}
	last := bus.Events()[len(bus.Events())-1]
	if !last.Heartbeat {
		t.Fatal("last event should be a heartbeat")
	}
	// After the tick, silence is bounded by the heartbeat interval.
	if ms := bus.MaxSilence(clk.Add(2 * time.Second)); ms > 2*time.Second {
		t.Fatalf("max silence %s exceeds heartbeat-bounded window", ms)
	}
}

// TestProofProgressEventsRendered: per-mode proof progress events are recorded and
// rendered in the pane.
func TestProofProgressEventsRendered(t *testing.T) {
	bus := NewProgressBus(5 * time.Second)
	for _, mode := range []string{"behavioral", "empirical", "hazard"} {
		bus.Emit(mode, "start")
		bus.Emit(mode, "finish: pass")
	}
	view := RenderProgress(bus.Events())
	for _, mode := range []string{"behavioral", "empirical", "hazard"} {
		if !strings.Contains(view, mode) {
			t.Fatalf("progress pane missing %s mode:\n%s", mode, view)
		}
	}
	if strings.Count(view, "start") != 3 || strings.Count(view, "finish") != 3 {
		t.Fatalf("expected start/finish per mode:\n%s", view)
	}
}
