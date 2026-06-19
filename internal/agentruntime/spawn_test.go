package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/proc"
)

// shellPreset builds a native-ACP preset whose "agent" is a /bin/sh script, so
// spawn can be exercised without a real vendor binary.
func shellPreset(script string, envAllow ...string) Preset {
	return Preset{
		Name:     "fake",
		Command:  "/bin/sh",
		Args:     []string{"-c", script},
		ACPMode:  ACPNative,
		EnvAllow: envAllow,
	}
}

// TestSpawnVendorAgentInjectsEnvAndRole: a spawned agent receives the preset's
// allowlisted env and a readable role-template file — and nothing off-allowlist.
func TestSpawnVendorAgentInjectsEnvAndRole(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	// The fake agent records the injected key, the role text, and whether a
	// non-allowlisted secret leaked.
	script := "printf 'KEY=%s\\nROLE=%s\\nLEAK=%s\\n' \"$MYKEY\" \"$(cat \"$ORION_ROLE_FILE\")\" \"$SECRET_OFF_LIST\" > " + out

	cfg := SpawnConfig{
		Preset:    shellPreset(script, "MYKEY"),
		EnvSource: map[string]string{"MYKEY": "injected-value", "SECRET_OFF_LIST": "should-not-leak"},
		Role:      "ORION-ROLE-TEMPLATE-7",
		Dir:       dir,
	}
	a, err := Spawn(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if err := a.Wait(); err != nil {
		t.Fatalf("agent wait: %v", err)
	}

	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read agent output: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, "KEY=injected-value") {
		t.Fatalf("allowlisted env not injected into agent:\n%s", got)
	}
	if !strings.Contains(got, "ROLE=ORION-ROLE-TEMPLATE-7") {
		t.Fatalf("role template not injected/readable:\n%s", got)
	}
	if !strings.Contains(got, "LEAK=\n") && !strings.HasSuffix(strings.TrimRight(got, "\n"), "LEAK=") {
		t.Fatalf("non-allowlisted secret leaked into agent env:\n%s", got)
	}
	// Role + pid files were written under the working dir.
	if _, err := os.Stat(a.RoleFile); err != nil {
		t.Fatalf("role file missing: %v", err)
	}
}

// TestSpawnedAgentDeadlineReaped: a long-running agent is killed when its
// deadline elapses (Cancel/deadline-enforced), and the whole group is reaped.
func TestSpawnedAgentDeadlineReaped(t *testing.T) {
	cfg := SpawnConfig{
		Preset:   shellPreset("sleep 60"),
		Dir:      t.TempDir(),
		Deadline: 300 * time.Millisecond,
	}
	start := time.Now()
	a, err := Spawn(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	pid := a.Pid()
	_ = a.Wait() // returns when the deadline kills it
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("deadline not enforced: agent ran %s", elapsed)
	}
	// Process must be gone.
	if pid > 0 && processAlive(pid) {
		t.Fatalf("agent pid %d still alive after deadline", pid)
	}
}

// TestSpawnedAgentReapedByReaper: a tracked agent is killed by the reaper on
// shutdown (the SIGINT/SIGTERM cleanup path).
func TestSpawnedAgentReapedByReaper(t *testing.T) {
	r := proc.NewReaper()
	cfg := SpawnConfig{Preset: shellPreset("sleep 60"), Dir: t.TempDir()}
	a, err := Spawn(context.Background(), cfg, r)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	pid := a.Pid()
	r.Shutdown()
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("reaper did not reap the agent")
	}
	if pid > 0 && processAlive(pid) {
		t.Fatalf("agent pid %d alive after reaper shutdown", pid)
	}
}

// processAlive reports whether pid exists (signal 0 probe).
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
