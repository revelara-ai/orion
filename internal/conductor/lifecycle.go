package conductor

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/revelara-ai/orion/internal/proc"
)

// AgentFile records the running Conductor control process (maps Gastown's mayor
// pid/agent file). Its presence + a live pid is the cleanup veto.
type AgentFile struct {
	PID       int    `json:"pid"`
	Transport string `json:"transport"`
	Command   string `json:"command"`
	StartedAt string `json:"started_at,omitempty"`
}

// LifecycleManager is the thin manager that spawns/observes/tears down the
// Conductor control process (SPEC §3). It owns no reasoning — only process
// lifecycle + the cleanup veto that blocks workspace teardown while alive.
type LifecycleManager struct {
	Dir     string   // where the agent file lives (the data dir)
	Command []string // how to launch the conductor (e.g. ["orion","conductor","acp"])
}

func (m *LifecycleManager) agentFilePath() string {
	return filepath.Join(m.Dir, "conductor.json")
}

// Start spawns the Conductor control process in its own process group and writes
// the agent file. It is an error to start while one is already running.
func (m *LifecycleManager) Start(startedAt string) error {
	if running, _ := m.Status(); running {
		return fmt.Errorf("conductor already running")
	}
	if len(m.Command) == 0 {
		return fmt.Errorf("conductor: no launch command")
	}
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return err
	}
	cmd := exec.Command(m.Command[0], m.Command[1:]...)
	cmd.SysProcAttr = proc.SandboxSysProcAttr()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("conductor: spawn: %w", err)
	}
	// Reap the child if it exits while we live; the control process is otherwise
	// a detached daemon (it survives the launching CLI invocation).
	go func() { _ = cmd.Wait() }()

	return m.writeAgentFile(AgentFile{
		PID: cmd.Process.Pid, Transport: "acp",
		Command: strings.Join(m.Command, " "), StartedAt: startedAt,
	})
}

// Status reports whether the Conductor control process is alive and its pid.
func (m *LifecycleManager) Status() (bool, int) {
	af, err := m.readAgentFile()
	if err != nil {
		return false, 0
	}
	if af.PID > 0 && processAlive(af.PID) {
		return true, af.PID
	}
	return false, af.PID
}

// Stop terminates the Conductor process group and removes the agent file.
func (m *LifecycleManager) Stop() error {
	af, err := m.readAgentFile()
	if err != nil {
		return nil // nothing to stop
	}
	if af.PID > 0 && processAlive(af.PID) {
		_ = syscall.Kill(-af.PID, syscall.SIGTERM)
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if !processAlive(af.PID) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if processAlive(af.PID) {
			_ = syscall.Kill(-af.PID, syscall.SIGKILL)
		}
	}
	return os.Remove(m.agentFilePath())
}

// CleanupVeto reports whether workspace/worktree teardown must be blocked because
// the Conductor control process is alive (maps Gastown cleanup.go).
func (m *LifecycleManager) CleanupVeto() bool {
	running, _ := m.Status()
	return running
}

func (m *LifecycleManager) writeAgentFile(af AgentFile) error {
	b, err := json.MarshalIndent(af, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.agentFilePath(), b, 0o600)
}

func (m *LifecycleManager) readAgentFile() (AgentFile, error) {
	b, err := os.ReadFile(m.agentFilePath())
	if err != nil {
		return AgentFile{}, err
	}
	var af AgentFile
	if err := json.Unmarshal(b, &af); err != nil {
		return AgentFile{}, err
	}
	return af, nil
}

// processAlive reports whether pid exists (signal 0 probe).
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
