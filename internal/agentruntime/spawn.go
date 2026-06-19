package agentruntime

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/revelara-ai/orion/internal/proc"
)

// RoleFileEnv is the env var Orion sets to point a spawned agent at its rendered
// role template (SPEC §0,§3: "injects env + the role template").
const RoleFileEnv = "ORION_ROLE_FILE"

// SpawnConfig parameterizes spawning a vendor coding-agent subprocess.
type SpawnConfig struct {
	Preset    Preset            // which vendor agent + its env allowlist / ACP mode
	EnvSource map[string]string // source for allowlisted env injection (nil → host env)
	Role      string            // role template text that primes the agent
	Dir       string            // working dir (role + pid files live here; "" → temp)
	Deadline  time.Duration     // per-agent deadline (0 → none)
}

// SpawnedAgent is a running vendor agent subprocess. Stdin/Stdout are the ACP
// JSON-RPC transport (driven by or-b7w). The process leads its own group so the
// whole tree is reaped on cancel/deadline/stop.
type SpawnedAgent struct {
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	Stdin    io.WriteCloser
	Stdout   io.ReadCloser
	Stderr   io.ReadCloser
	RoleFile string
	PidFile  string

	done     chan struct{}
	waitErr  error
	stopOnce sync.Once
}

// Spawn launches the agent described by cfg.Preset. Orion is a control plane, not
// an LLM client: it only sets the process env (a minimal base + the preset's
// allowlisted keys + the role-file pointer) and the agent authenticates itself.
func Spawn(parent context.Context, cfg SpawnConfig, reaper *proc.Reaper) (*SpawnedAgent, error) {
	if cfg.Preset.Command == "" {
		return nil, fmt.Errorf("spawn: preset has no command")
	}
	dir := cfg.Dir
	if dir == "" {
		var err error
		if dir, err = os.MkdirTemp("", "orion-agent-"); err != nil {
			return nil, fmt.Errorf("spawn: workdir: %w", err)
		}
	}
	rolePath := filepath.Join(dir, "orion-role.md")
	if err := os.WriteFile(rolePath, []byte(cfg.Role), 0o600); err != nil {
		return nil, fmt.Errorf("spawn: write role: %w", err)
	}

	source := cfg.EnvSource
	if source == nil {
		source = hostEnvMap()
	}
	env := baseEnv()
	env = append(env, cfg.Preset.Env(source)...)
	env = append(env, RoleFileEnv+"="+rolePath)

	ctx, cancel := context.WithCancel(parent)
	if cfg.Deadline > 0 {
		ctx, cancel = context.WithTimeout(parent, cfg.Deadline)
	}

	argv := cfg.Preset.LaunchArgs()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.SysProcAttr = proc.SandboxSysProcAttr()
	// On ctx cancel/deadline, kill the whole process group (not just the leader).
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("spawn: stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("spawn: stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("spawn: stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("spawn: start %s: %w", cfg.Preset.Name, err)
	}
	if reaper != nil {
		reaper.Track(cmd)
	}
	pidPath := filepath.Join(dir, "agent.pid")
	_ = os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)

	a := &SpawnedAgent{
		cmd: cmd, cancel: cancel,
		Stdin: stdin, Stdout: stdout, Stderr: stderr,
		RoleFile: rolePath, PidFile: pidPath,
		done: make(chan struct{}),
	}
	go func() {
		a.waitErr = cmd.Wait()
		close(a.done)
	}()
	return a, nil
}

// Pid returns the spawned process id.
func (a *SpawnedAgent) Pid() int {
	if a.cmd.Process == nil {
		return 0
	}
	return a.cmd.Process.Pid
}

// Wait blocks until the agent exits and returns its wait error.
func (a *SpawnedAgent) Wait() error { <-a.done; return a.waitErr }

// Done reports a channel closed when the agent exits.
func (a *SpawnedAgent) Done() <-chan struct{} { return a.done }

// Stop terminates the agent: SIGTERM to its process group, then SIGKILL after
// grace if it has not exited. Idempotent.
func (a *SpawnedAgent) Stop(grace time.Duration) {
	a.stopOnce.Do(func() {
		if a.cmd.Process != nil {
			pid := a.cmd.Process.Pid
			_ = syscall.Kill(-pid, syscall.SIGTERM)
			select {
			case <-a.done:
			case <-time.After(grace):
				_ = syscall.Kill(-pid, syscall.SIGKILL)
			}
		}
		a.cancel()
		_ = os.Remove(a.PidFile)
	})
}

// baseEnv is the minimal environment a CLI needs to run — deliberately NOT the
// full host environment, so ambient secrets are not handed to the agent.
func baseEnv() []string {
	var env []string
	for _, k := range []string{"PATH", "HOME", "LANG", "TERM"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func hostEnvMap() map[string]string {
	m := map[string]string{}
	for _, kv := range os.Environ() {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				m[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return m
}
