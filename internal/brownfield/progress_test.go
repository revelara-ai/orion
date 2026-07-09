package brownfield

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// progressRecorder collects Progress events; the gate may emit from a streaming
// goroutine, so recording is locked.
type progressRecorder struct {
	mu      sync.Mutex
	steps   []string
	details []string
}

func (r *progressRecorder) sink(step, detail string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps = append(r.steps, step)
	r.details = append(r.details, detail)
}

func (r *progressRecorder) hasStep(step string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.steps {
		if s == step {
			return true
		}
	}
	return false
}

func (r *progressRecorder) joinedDetails() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.details, "\n")
}

// TestRegressionGateScopedEmitsProgress: the DEFAULT gate must heartbeat while
// it runs — step transitions for both baseline runs plus at least one
// per-package completion event naming the package under test (or-m45w: a
// silent 10-minute gate is indistinguishable from a hang).
func TestRegressionGateScopedEmitsProgress(t *testing.T) {
	repo := newScopeRepo(t) // a green, b intentionally red (out of scope)
	m := ScanRepoMap(repo)
	rec := &progressRecorder{}
	r, err := RegressionGateScoped(context.Background(), repo, m, nil, func() error {
		return os.WriteFile(filepath.Join(repo, "a/a.go"),
			[]byte("package a\n\n// touched\nfunc A() int { return 1 }\n"), 0o644)
	}, rec.sink)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Held {
		t.Fatalf("gate must hold: %+v", r)
	}
	for _, step := range []string{"apply-change", "scope", "green-before", "green-after"} {
		if !rec.hasStep(step) {
			t.Errorf("missing %q step in progress events; got steps %v", step, rec.steps)
		}
	}
	// Per-package completion lines from the streamed `go test` output must reach
	// the sink, carrying the package path and a completion counter.
	details := rec.joinedDetails()
	if !strings.Contains(details, "testmod/a") {
		t.Errorf("no per-package completion event (want a detail naming testmod/a); details:\n%s", details)
	}
	if !strings.Contains(details, "(1/1)") {
		t.Errorf("per-package events must carry an n/total counter for a scoped run; details:\n%s", details)
	}
}

// TestRegressionGateFullEmitsProgress: the full-suite gate heartbeats too, with
// an open-ended counter (total unknown for ./...).
func TestRegressionGateFullEmitsProgress(t *testing.T) {
	dir := t.TempDir()
	writeRepoFile(t, dir, "go.mod", "module fullmod\n\ngo 1.21\n")
	writeRepoFile(t, dir, "a/a.go", "package a\n\nfunc A() int { return 1 }\n")
	writeRepoFile(t, dir, "a/a_test.go", `package a

import "testing"

func TestA(t *testing.T) {
	if A() != 1 {
		t.Fatal("A changed")
	}
}
`)
	rec := &progressRecorder{}
	r, err := RegressionGate(context.Background(), dir, nil, nil, rec.sink)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Held {
		t.Fatalf("gate must hold: %+v", r)
	}
	for _, step := range []string{"green-before", "green-after"} {
		if !rec.hasStep(step) {
			t.Errorf("missing %q step; got %v", step, rec.steps)
		}
	}
	if details := rec.joinedDetails(); !strings.Contains(details, "fullmod/a") {
		t.Errorf("no per-package completion event; details:\n%s", details)
	}
}
