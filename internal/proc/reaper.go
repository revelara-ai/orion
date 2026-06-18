// Package proc provides crash-safe subprocess lifecycle management for Orion's
// sandboxes (or-n8j, PRD Harness Reliability). Lookout/sandbox subprocesses run
// in their own process groups so that on SIGINT/SIGTERM (or a crash) the whole
// group is reaped — no orphaned children survive the harness.
package proc

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
)

// SandboxSysProcAttr returns the SysProcAttr a sandboxed subprocess must be
// started with so it leads its own process group (enabling group-kill on reap).
func SandboxSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// Reaper tracks subprocesses and kills their process groups on shutdown.
type Reaper struct {
	mu   sync.Mutex
	cmds []*exec.Cmd
}

// NewReaper returns an empty reaper.
func NewReaper() *Reaper { return &Reaper{} }

// Track registers a started subprocess (launched with SandboxSysProcAttr) for
// reaping.
func (r *Reaper) Track(cmd *exec.Cmd) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cmds = append(r.cmds, cmd)
}

// Shutdown kills the process group of every tracked subprocess and reaps it.
// Idempotent and safe to call once during cleanup.
func (r *Reaper) Shutdown() {
	r.mu.Lock()
	cmds := r.cmds
	r.cmds = nil
	r.mu.Unlock()

	for _, cmd := range cmds {
		if cmd.Process == nil {
			continue
		}
		pid := cmd.Process.Pid
		// Negative pid signals the whole process group (the leader's pgid == pid
		// because it was started with Setpgid).
		if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
			_ = cmd.Process.Kill() // fall back to killing just the leader
		}
		_, _ = cmd.Process.Wait()
	}
}

// Install wires SIGINT/SIGTERM to cancel a context and reap all tracked
// subprocesses. It returns the cancellable context and a stop func that
// uninstalls the handler. The Conductor's interrupt path triggers the same
// cleanup.
func Install(parent context.Context, r *Reaper) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			cancel()
			r.Shutdown()
		case <-ctx.Done():
		}
	}()
	stop := func() {
		signal.Stop(sigCh)
		cancel()
	}
	return ctx, stop
}
