package conductor

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/revelara-ai/orion/pkg/llm"
)

// phaseRecorder collects PhaseEvents; ChangeAndProve syncSink-wraps the sink,
// but the recorder locks anyway so the test never depends on that detail.
type phaseRecorder struct {
	mu     sync.Mutex
	events []PhaseEvent
}

func (r *phaseRecorder) sink(e PhaseEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *phaseRecorder) find(phase string, status PhaseStatus) *PhaseEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.events {
		if r.events[i].Phase == phase && r.events[i].Status == status {
			return &r.events[i]
		}
	}
	return nil
}

func (r *phaseRecorder) anyDetailContains(phase, substr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.events {
		if e.Phase == phase && strings.Contains(e.Detail, substr) {
			return true
		}
	}
	return false
}

// TestChangeAndProveEmitsPhases: the change pipeline must narrate itself
// through the sink — worktree, regression gate (with the brownfield heartbeat
// riding Detail), and commit — so the TUI activity panel and CLI can show a
// live pipeline instead of a silent spinner (or-m45w).
func TestChangeAndProveEmitsPhases(t *testing.T) {
	if testing.Short() {
		t.Skip("git worktree + go test + LLM loop")
	}
	repo := gitInitGreenRepo(t)
	prov := &fakeLLM{resp: []*llm.ChatResponse{
		tuResp("1", "write_file", `{"path":"extra.go","content":"package t\n\nfunc Mul(a, b int) int { return a * b }\n"}`),
		endTurn("added Mul"),
	}}
	rec := &phaseRecorder{}

	res, err := ChangeAndProve(context.Background(), repo, nil, prov, "add a Mul helper", nil, nil, rec.sink)
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if !res.Committed {
		t.Fatalf("happy path must commit: %+v", res)
	}

	for _, want := range []struct {
		phase  string
		status PhaseStatus
	}{
		{"change worktree", PhaseDone},
		{"regression gate", PhaseRunning},
		{"regression gate", PhaseDone},
		{"commit", PhaseDone},
	} {
		if rec.find(want.phase, want.status) == nil {
			t.Errorf("missing phase event %s/%s; got %+v", want.phase, want.status, rec.events)
		}
	}
	// The brownfield heartbeat must ride the regression-gate phase: step labels
	// and at least one per-package completion in Detail.
	for _, step := range []string{"green-before", "green-after"} {
		if !rec.anyDetailContains("regression gate", step) {
			t.Errorf("regression-gate events carry no %q heartbeat; got %+v", step, rec.events)
		}
	}
	if !rec.anyDetailContains("regression gate", "example.com/t") {
		t.Errorf("no per-package completion reached the sink; got %+v", rec.events)
	}
	// A failed pipeline is out of scope here; warn/fail paths are covered by the
	// existing reject tests running with a nil sink (nil-safety is the contract).
}
