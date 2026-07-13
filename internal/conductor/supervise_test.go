package conductor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func superviseMgr(t *testing.T) *LifecycleManager {
	t.Helper()
	return &LifecycleManager{Dir: t.TempDir(), Command: []string{"/bin/sh", "-c", "sleep 300"}}
}

// TestSuperviseRestartsCrashedConductor (or-v9f.25a acceptance): killing the
// conductor produces conductor.crashed then conductor.restarted, and the
// agent file holds a NEW live pid.
func TestSuperviseRestartsCrashedConductor(t *testing.T) {
	m := superviseMgr(t)
	var events []string
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- m.Supervise(ctx, SuperviseOpts{MaxRestarts: 5, Window: time.Minute, Backoff: 20 * time.Millisecond, Poll: 30 * time.Millisecond},
			func(kind, detail string) {
				events = append(events, kind)
				if kind == "conductor.restarted" {
					cancel() // observed the full crash→restart cycle
				}
			})
	}()

	// Wait for the initial start, then murder the conductor.
	var pid int
	for i := 0; i < 100; i++ {
		if running, p := m.Status(); running {
			pid = p
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("supervisor never started the conductor")
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("supervisor did not complete the crash→restart cycle in time")
	}
	joined := strings.Join(events, ",")
	if !strings.Contains(joined, "conductor.crashed") || !strings.Contains(joined, "conductor.restarted") {
		t.Fatalf("want crashed then restarted, got %v", events)
	}
	if running, newPid := m.Status(); !running || newPid == pid {
		t.Fatalf("the agent file must hold a NEW live pid, got running=%v pid=%d (old %d)", running, newPid, pid)
	}
	_ = m.Stop()
}

// TestSuperviseExhaustsRestartBudget (or-v9f.25a acceptance): a kill loop
// beyond the cap stops retrying with an escalation-kind event.
func TestSuperviseExhaustsRestartBudget(t *testing.T) {
	m := &LifecycleManager{Dir: t.TempDir(), Command: []string{"/bin/sh", "-c", "exit 1"}} // dies instantly, forever
	var events []string
	err := m.Supervise(context.Background(),
		SuperviseOpts{MaxRestarts: 2, Window: time.Minute, Backoff: 10 * time.Millisecond, Poll: 20 * time.Millisecond},
		func(kind, _ string) { events = append(events, kind) })
	if err == nil || !strings.Contains(err.Error(), "supervision stopped") {
		t.Fatalf("an exhausted budget must stop with a named error: %v", err)
	}
	joined := strings.Join(events, ",")
	if !strings.Contains(joined, "conductor.supervision.exhausted") {
		t.Fatalf("the escalation event must fire: %v", events)
	}
	if strings.Count(joined, "conductor.restarted") > 2 {
		t.Fatalf("restarts must respect the cap: %v", events)
	}
}

// TestStatusKindThreeStates (or-v9f.25a acceptance): stopped / running /
// wedged (live pid, stale heartbeat) are distinct.
func TestStatusKindThreeStates(t *testing.T) {
	m := superviseMgr(t)
	if got := m.StatusKind(); got != "stopped" {
		t.Fatalf("no pid → stopped, got %s", got)
	}
	if err := m.Start(time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop() }()

	// Live pid + fresh heartbeat → running.
	TouchHeartbeat(m.Dir)
	if got := m.StatusKind(); got != "running" {
		t.Fatalf("fresh heartbeat → running, got %s", got)
	}
	// Live pid + STALE heartbeat → wedged.
	old := time.Now().Add(-2 * WedgeThreshold)
	if err := os.Chtimes(filepath.Join(m.Dir, heartbeatFile), old, old); err != nil {
		t.Fatal(err)
	}
	if got := m.StatusKind(); got != "wedged" {
		t.Fatalf("stale heartbeat + live pid → wedged, got %s", got)
	}
	// Live pid + NO heartbeat at all → wedged too (never silently 'running').
	_ = os.Remove(filepath.Join(m.Dir, heartbeatFile))
	if got := m.StatusKind(); got != "wedged" {
		t.Fatalf("absent heartbeat + live pid → wedged, got %s", got)
	}
}
