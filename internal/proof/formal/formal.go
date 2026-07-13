// Package formal is the design-proof model-checker runner (or-56c): it
// executes a HUMAN-RATIFIED formal model (FizzBee by default; TLA+/Apalache is
// the documented escape hatch for hard liveness/deadlock cases) and returns a
// deterministic verdict. This is the SECOND proof of the Manifesto: the triad
// proves the product implements the design; this proves the DESIGN itself has
// no race, deadlock, or reachable unsafe state — before code exists.
//
// Trust posture: the model is drafted by an LLM but RATIFIED by the human
// (STPA posture), so the checker runs in the trusted control plane — the
// sandbox here (bwrap: read-only binds, no network, CLEARED environment) is
// hygiene against a compromised toolchain, not the generation⊥proof wall.
package formal

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Result is a deterministic model-check verdict.
type Result struct {
	Passed    bool
	Invariant string // the violated invariant's name, when the checker names one
	Deadlock  bool   // a reachable non-terminal state with no enabled action (the liveness failure class)
	Output    string // combined checker output (the counterexample trace lives here)
	Skipped   string // non-empty when the checker could not run (toolchain absent)
}

// Runner checks one model file.
type Runner interface {
	Check(ctx context.Context, modelPath string) (Result, error)
}

// ── FizzBee ──────────────────────────────────────────────────────────────────

// FizzBee runs the fizzbee checker distribution (the `fizz` wrapper: a Python
// parser stage feeding the Go checker). Dir is the dist root; empty resolves
// ORION_FIZZBEE_DIR, then ~/.orion/tools/fizzbee.
type FizzBee struct {
	Dir string
	// noSandbox disables the bwrap profile (tests exercising the fallback).
	noSandbox bool
}

// ResolveFizzBeeDir finds the checker dist, or "" when not installed.
func ResolveFizzBeeDir() string {
	if d := os.Getenv("ORION_FIZZBEE_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	d := filepath.Join(home, ".orion", "tools", "fizzbee")
	if _, err := os.Stat(filepath.Join(d, "fizz")); err == nil {
		return d
	}
	return ""
}

var invariantRe = regexp.MustCompile(`(?i)invariant:\s*(\S+)`)

// Check runs the model. The checker executes inside a bwrap sandbox when
// available — read-only system/toolchain binds, a scratch workdir (the model
// is COPIED there so checker run-artifacts never land next to the source),
// --unshare-net and --clearenv (the proof-exec env-scrub posture; see the
// safeenv rule) — and degrades to a plain exec with a scrubbed environment
// when bwrap is absent (mirrors internal/sandbox's pluggable backends).
func (f *FizzBee) Check(ctx context.Context, modelPath string) (Result, error) {
	dir := f.Dir
	if dir == "" {
		dir = ResolveFizzBeeDir()
	}
	if dir == "" {
		return Result{Skipped: "fizzbee not installed (set ORION_FIZZBEE_DIR or install to ~/.orion/tools/fizzbee)"}, nil
	}
	model, err := os.ReadFile(modelPath)
	if err != nil {
		return Result{}, fmt.Errorf("read model: %w", err)
	}
	// Scratch workdir: the checker writes run outputs (graphs, html) next to
	// the model — never pollute the caller's tree.
	work, err := os.MkdirTemp("", "orion-formal-*")
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = os.RemoveAll(work) }()
	workModel := filepath.Join(work, "model.fizz")
	if err := os.WriteFile(workModel, model, 0o600); err != nil { // #nosec G703 -- workModel is a harness-built path in the proof workdir
		return Result{}, err
	}

	var cmd *exec.Cmd
	if bw, berr := exec.LookPath("bwrap"); berr == nil && !f.noSandbox {
		cmd = exec.CommandContext(ctx, bw, bwrapArgs(dir, work)...) // #nosec G204 -- resolved bwrap binary; harness-built args
	} else {
		cmd = exec.CommandContext(ctx, filepath.Join(dir, "fizz"), workModel) // #nosec G204 -- pinned fizzbee binary in the harness toolchain dir
		cmd.Env = scrubbedEnv()
	}
	cmd.Dir = work
	out, runErr := cmd.CombinedOutput()
	res := Result{Output: string(out)}
	switch {
	case strings.Contains(res.Output, "PASSED: Model checker completed successfully"):
		res.Passed = true
	case strings.Contains(res.Output, "FAILED"):
		res.Deadlock = strings.Contains(res.Output, "DEADLOCK detected")
		if m := invariantRe.FindStringSubmatch(res.Output); m != nil {
			res.Invariant = m[1]
		}
	case runErr != nil:
		return res, fmt.Errorf("fizzbee: %w: %s", runErr, firstLine(res.Output))
	}
	return res, nil
}

// bwrapArgs is the checker's sandbox profile: RO system + toolchain binds
// (the parser stage is Python — it needs the interpreter and /dev/urandom),
// the dist RO, the scratch workdir RW, no network, CLEARED environment.
func bwrapArgs(dist, work string) []string {
	args := []string{
		"--ro-bind", "/usr", "/usr",
		"--ro-bind", "/bin", "/bin",
		"--ro-bind", "/lib", "/lib",
		"--dev", "/dev",
		"--ro-bind", dist, "/fizzdist",
		"--bind", work, "/work",
		"--tmpfs", "/tmp", // the fizz wrapper mktemp's; isolated, never the host's
		"--unshare-net",
		"--clearenv",
		"--setenv", "HOME", "/work",
		"--setenv", "PATH", sandboxPath(),
		"--chdir", "/work",
	}
	// Optional binds: present on some hosts, required when they are.
	for _, p := range []string{"/lib64", "/home/linuxbrew"} {
		if _, err := os.Stat(p); err == nil {
			args = append(args[:0:0], append([]string{"--ro-bind", p, p}, args...)...)
		}
	}
	return append(args, "/fizzdist/fizz", "/work/model.fizz")
}

// sandboxPath lets the wrapper find bash/python3 inside the sandbox, including
// a linuxbrew python when that is what the host has.
func sandboxPath() string {
	p := "/usr/bin:/bin"
	if _, err := os.Stat("/home/linuxbrew/.linuxbrew/bin"); err == nil {
		p += ":/home/linuxbrew/.linuxbrew/bin"
	}
	return p
}

// scrubbedEnv is the no-bwrap fallback: PATH+HOME only — never os.Environ().
func scrubbedEnv() []string {
	return []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.TempDir()}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// ── TLA+/Apalache escape hatch ───────────────────────────────────────────────

// Apalache is the documented escape hatch for hard liveness/fairness and
// deadlock cases FizzBee's checker does not yet cover (or-56c design). It
// resolves the `apalache-mc` binary from PATH; absent = Skipped, so the
// design-proof gate degrades exactly like the FizzBee runner.
type Apalache struct{}

func (Apalache) Check(ctx context.Context, modelPath string) (Result, error) {
	bin, err := exec.LookPath("apalache-mc")
	if err != nil {
		return Result{Skipped: "apalache-mc not installed (TLA+ escape hatch; used for liveness/deadlock cases FizzBee does not cover)"}, nil
	}
	cmd := exec.CommandContext(ctx, bin, "check", modelPath) // #nosec G204 -- pinned checker binary; harness-built model path
	cmd.Env = scrubbedEnv()
	out, runErr := cmd.CombinedOutput()
	res := Result{Output: string(out)}
	switch {
	case strings.Contains(res.Output, "Checker reports no error"):
		res.Passed = true
	case runErr != nil && !strings.Contains(res.Output, "Checker has found an error"):
		return res, fmt.Errorf("apalache: %w: %s", runErr, firstLine(res.Output))
	}
	return res, nil
}
