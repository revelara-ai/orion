package proc

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestSignalCleanupReapsSandboxes: a tracked subprocess (in its own process
// group) is killed and reaped by Shutdown — no orphan survives.
func TestSignalCleanupReapsSandboxes(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = SandboxSysProcAttr()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid

	r := NewReaper()
	r.Track(cmd)
	r.Shutdown()

	// After reaping, signal 0 to the pid must fail (process gone).
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			return // ESRCH: process no longer exists — reaped
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %d still alive after Shutdown", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestShutdownIdempotent: calling Shutdown twice (and with nothing tracked) is safe.
func TestShutdownIdempotent(t *testing.T) {
	r := NewReaper()
	r.Shutdown()
	r.Shutdown()
}
