// Package sandbox isolates untrusted agent execution (or-1zj, PRD Security
// Requirements). It is a PLUGGABLE backend with a stable contract: the agent runs
// with a scoped writable workdir (its worktree), default-deny network egress, a
// scrubbed environment, and no visibility of the host filesystem beyond explicit
// binds — so the Context Store and held-out corpus are unreachable from inside
// (Trust-Domain invariant 3).
//
// Backends are selectable via sandbox.isolation. gVisor/runsc is the intended
// production default; this build ships a **bubblewrap** (namespace) backend,
// which meets the same contract on a typical Linux/WSL2 laptop without nested
// virtualization, plus a "none" passthrough fallback (weaker; logged).
//
// Manifesto: side-effect sandboxing; trust-domain isolation.
package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// Spec describes a sandboxed execution.
type Spec struct {
	Workdir  string            // the only writable directory (the worktree)
	Argv     []string          // command to run (argv[0] is the program)
	Stdin    string            // fed to the process's stdin (or-v9f.3 exec cases); empty = no input
	Env      map[string]string // scrubbed environment (no ambient creds)
	ROBinds  []string          // host paths to expose read-only (e.g. a static helper)
	AllowNet bool              // default false → egress denied
}

// Result is the outcome of a sandboxed run.
type Result struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

// Backend is a sandbox implementation.
type Backend interface {
	Name() string
	Available() bool
	Run(ctx context.Context, s Spec) (Result, error)
}

// New selects a backend by isolation name: "bwrap", "none", or "auto" (bwrap if
// available, else none).
func New(isolation string) (Backend, error) {
	switch isolation {
	case "bwrap":
		b := bwrapBackend{}
		if !b.Available() {
			return nil, errors.New("sandbox: bwrap backend requested but bwrap not found")
		}
		return b, nil
	case "none":
		return noneBackend{}, nil
	case "", "auto":
		if (bwrapBackend{}).Available() {
			return bwrapBackend{}, nil
		}
		return noneBackend{}, nil
	default:
		return nil, fmt.Errorf("sandbox: unknown isolation %q (want bwrap|none|auto)", isolation)
	}
}

// ── bubblewrap backend ───────────────────────────────────────────────────────

type bwrapBackend struct{}

func (bwrapBackend) Name() string { return "bwrap" }

func (bwrapBackend) Available() bool {
	_, err := exec.LookPath("bwrap")
	return err == nil
}

func (bwrapBackend) Run(ctx context.Context, s Spec) (Result, error) {
	if s.Workdir == "" {
		return Result{}, errors.New("sandbox: empty workdir")
	}
	if len(s.Argv) == 0 {
		return Result{}, errors.New("sandbox: empty argv")
	}
	args := []string{
		"--unshare-user", "--unshare-ipc", "--unshare-pid", "--unshare-uts", "--unshare-cgroup",
		"--die-with-parent", "--clearenv",
		"--proc", "/proc", "--tmpfs", "/tmp",
		// Minimal fresh devtmpfs (/dev/null, /dev/urandom, …): no host devices, but
		// required by real toolchains — `go build`/`go test` open /dev/null for the
		// build cache and telemetry. (A static binary needs none, which is why the
		// original backend omitted it.)
		"--dev", "/dev",
	}
	if !s.AllowNet {
		args = append(args, "--unshare-net") // default-deny egress
	}
	// Scoped writable workdir; nothing else of the host FS is visible beyond the
	// explicit read-only binds below.
	args = append(args, "--bind", s.Workdir, s.Workdir, "--chdir", s.Workdir)
	for _, b := range s.ROBinds {
		args = append(args, "--ro-bind", b, b)
	}
	// Deterministic env order.
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		args = append(args, "--setenv", k, s.Env[k])
	}
	args = append(args, "--")
	args = append(args, s.Argv...)

	cmd := exec.CommandContext(ctx, "bwrap", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if s.Stdin != "" {
		cmd.Stdin = strings.NewReader(s.Stdin)
	}
	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitCode()
			return res, nil // a non-zero exit is a result, not an error
		}
		return res, fmt.Errorf("sandbox run: %w", err)
	}
	return res, nil
}

// ── none backend (fallback) ──────────────────────────────────────────────────

// noneBackend runs the command directly with a scrubbed env and the workdir as
// cwd. It provides NO isolation (no egress denial, no FS scoping) — used only
// when no namespace backend is available; callers should treat its results as
// untrusted-environment and log the downgrade.
type noneBackend struct{}

func (noneBackend) Name() string    { return "none" }
func (noneBackend) Available() bool { return true }

func (noneBackend) Run(ctx context.Context, s Spec) (Result, error) {
	if len(s.Argv) == 0 {
		return Result{}, errors.New("sandbox: empty argv")
	}
	cmd := exec.CommandContext(ctx, s.Argv[0], s.Argv[1:]...)
	cmd.Dir = s.Workdir
	env := []string{}
	for k, v := range s.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if s.Stdin != "" {
		cmd.Stdin = strings.NewReader(s.Stdin)
	}
	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			res.ExitCode = ee.ExitCode()
			return res, nil
		}
		return res, fmt.Errorf("sandbox run: %w", err)
	}
	return res, nil
}
