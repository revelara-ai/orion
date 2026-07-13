// Package proofexec runs the proof domain's go-toolchain execs (build/test of
// generated, untrusted code) inside internal/sandbox, so that code cannot read
// host secrets (the model API key) or reach the network during proof. It is the
// comprehensive successor to internal/proof/safeenv's env-scrub-only boundary
// (or-5ym, PRD Security Requirements / Manifesto: side-effect sandboxing).
package proofexec

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/revelara-ai/orion/internal/proof/prooflock"
	"github.com/revelara-ai/orion/internal/proof/safeenv"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// allowedTools is the verification-command allowlist for RunTool: argv[0] MUST be a key here.
// `make` is intentionally NOT allowed — it is a dynamically-linked C binary that won't load in
// the lib-less sandbox, and its $(shell ...) parse-time expansion (even under -n) is an exec
// vector; Makefile targets are proven by static inspection (a "file" assertion), not execution.
// The argv is constructed by Orion, never lifted from a generated file.
var allowedTools = map[string]bool{"go": true, "golangci-lint": true}

// goDeniedSubcommands are `go` subcommands that compile-and-RUN arbitrary code beyond the
// sandboxed build/test (which already executes generated tests under isolation). They are
// refused so a verify case can't smuggle arbitrary execution via the trusted go arm.
var goDeniedSubcommands = map[string]bool{
	"run": true, "generate": true, "get": true, "install": true, "tool": true,
}

// runToolTimeout bounds a single proof exec (build/test/lint of generated content).
const runToolTimeout = 4 * time.Minute

// isolation selects the sandbox backend. "auto" uses bwrap when available and
// falls back to the (unisolated, logged) "none" backend otherwise. Overridable
// via ORION_SANDBOX_ISOLATION for environments that must force a backend.
func isolation() string {
	if v := strings.TrimSpace(os.Getenv("ORION_SANDBOX_ISOLATION")); v != "" {
		return v
	}
	return "auto"
}

var (
	warnNoneOnce sync.Once
	goRootOnce   sync.Once
	goRootCached string
)

// goRoot resolves the toolchain root to read-only-bind into the sandbox. It asks
// the `go` on PATH (trusted host exec — no generated code runs here), falling
// back to the GOROOT env var. (`go env GOROOT` is the documented replacement for
// the deprecated runtime.GOROOT().)
func goRoot() string {
	goRootOnce.Do(func() {
		if out, err := exec.Command("go", "env", "GOROOT").Output(); err == nil {
			if r := strings.TrimSpace(string(out)); r != "" {
				goRootCached = r
				return
			}
		}
		goRootCached = os.Getenv("GOROOT")
	})
	return goRootCached
}

// toolEnv builds the hermetic, secret-scrubbed environment shared by every sandboxed tool
// exec. GOPROXY=off + GOTOOLCHAIN=local + the scrubbed allowlist mean no network and no host
// secrets; GOCACHE/GOPATH/HOME land inside the writable workdir.
func toolEnv(root, workdir string) map[string]string {
	env := safeenv.Map() // scrubbed allowlist (no secrets)
	env["PATH"] = filepath.Join(root, "bin") + ":/usr/bin:/bin"
	env["HOME"] = workdir
	env["GOROOT"] = root
	env["GOCACHE"] = filepath.Join(workdir, ".orion-gocache") // writable: inside the workdir
	env["GOPATH"] = filepath.Join(workdir, ".orion-gopath")
	env["GOTOOLCHAIN"] = "local" // never fetch a toolchain over the (denied) network
	env["GOPROXY"] = "off"       // no module downloads under proof
	env["GOENV"] = "off"         // never read/write the host go env file (no `go env -w` side effects)
	env["GOFLAGS"] = ""
	env["CGO_ENABLED"] = "0" // no C toolchain needed inside the sandbox
	return env
}

// RunTool runs an ALLOWLISTED verification tool (go | golangci-lint | make -n) over workdir
// under the same sandbox posture as the go toolchain: a scrubbed/hermetic env (toolEnv), a
// read-only bind of GOROOT, caches inside the writable workdir, and default-deny network +
// filesystem isolation. For a non-go tool it resolves the binary from the TRUSTED host
// (exec.LookPath) and read-only-binds it — never trusting a generated file — and FAILS CLOSED
// under the unisolated "none" backend (running an external tool over generated content without
// namespace isolation is refused). A non-zero exit is returned via exitCode; err is reserved
// for policy violations (non-allowlisted tool, non-dry-run make, missing binary/sandbox) and
// launch failures.
func RunTool(ctx context.Context, workdir, tool string, args ...string) (stdout, stderr string, exitCode int, err error) {
	if !allowedTools[tool] {
		return "", "", -1, fmt.Errorf("proofexec: tool %q is not on the verification allowlist", tool)
	}
	if tool == "go" && len(args) > 0 && goDeniedSubcommands[args[0]] {
		return "", "", -1, fmt.Errorf("proofexec: `go %s` is not allowed (it runs arbitrary code)", args[0])
	}
	// or-7y68: ONE toolchain exec per machine — the same flock the brownfield
	// regression gates hold (or-6wbl), so concurrent proof builds/tests (and a
	// gate + a proof) queue instead of stampeding the CPU into false reds. The
	// wait honors the CALLER's ctx (taken before the per-exec timeout below, so
	// queueing never eats the exec's own budget). Fail-open: no lock, no queue,
	// the proof still runs.
	release, lerr := prooflock.Acquire(ctx)
	if lerr != nil {
		return "", "", -1, lerr
	}
	defer release()
	// Bound every proof exec so generated code (an init() spin-loop, a hung tool) can't wedge.
	ctx, cancel := context.WithTimeout(ctx, runToolTimeout)
	defer cancel()
	be, err := sandbox.New(isolation())
	if err != nil {
		return "", "", -1, err
	}

	root := goRoot()
	roBinds := []string{root}
	// or-tf8 H3 enabler: the HOST module cache binds read-only so a
	// dependency-bearing repo's synth_test resolves from cache with the
	// network still denied (GOPROXY=off).
	modCache := hostModCache()
	if modCache != "" {
		roBinds = append(roBinds, modCache)
	}
	var argv []string
	if tool == "go" {
		if be.Name() == "none" {
			// or-tf8 H1: `go test` executes GENERATED code — without a
			// namespace sandbox it can read host files and reach the network
			// even with a scrubbed env. FAIL CLOSED; the operator override is
			// explicit and visible, never a silent warn.
			if os.Getenv("ORION_ALLOW_UNSAFE_GO_ARM") != "1" {
				return "", "", -1, fmt.Errorf("proofexec: refusing to run generated code without a namespace sandbox — install bwrap, or set ORION_ALLOW_UNSAFE_GO_ARM=1 to explicitly accept unisolated proof execs")
			}
			warnNoneOnce.Do(func() {
				slog.Warn("proofexec: ORION_ALLOW_UNSAFE_GO_ARM=1 — proof execs run with a scrubbed env but WITHOUT network/filesystem isolation",
					"backend", be.Name())
			})
		}
		argv = append([]string{filepath.Join(root, "bin", "go")}, args...)
	} else {
		// A non-go tool runs over GENERATED, untrusted content: require a real namespace
		// sandbox, and bind the binary resolved on the trusted host (never from the worktree).
		if be.Name() == "none" {
			return "", "", -1, fmt.Errorf("proofexec: refusing to run %q without a namespace sandbox (install bwrap or set ORION_SANDBOX_ISOLATION=bwrap)", tool)
		}
		bin, lerr := exec.LookPath(tool)
		if lerr != nil {
			return "", "", -1, fmt.Errorf("proofexec: %q not found on host: %w", tool, lerr)
		}
		if resolved, serr := filepath.EvalSymlinks(bin); serr == nil {
			bin = resolved
		}
		roBinds = append(roBinds, bin)
		argv = append([]string{bin}, args...)
	}

	env := toolEnv(root, workdir)
	if modCache != "" {
		env["GOMODCACHE"] = modCache // read-only bind above; cache-only resolution
	}
	res, runErr := be.Run(ctx, sandbox.Spec{
		Workdir:  workdir,
		Argv:     argv,
		Env:      env,
		ROBinds:  roBinds,
		AllowNet: false, // default-deny egress — never true on this path
	})
	return res.Stdout, res.Stderr, res.ExitCode, runErr
}

// GoToolchain runs `go <goArgs...>` for proof with workdir as the cwd (and the only writable
// path under isolation), returning combined stdout+stderr and the process exit code. It is a
// thin wrapper over RunTool's "go" arm (sandboxed, scrubbed, network-denied). A non-zero exit
// is returned via exitCode, not err; err is reserved for failures to launch the toolchain.
func GoToolchain(ctx context.Context, workdir string, goArgs ...string) (output string, exitCode int, err error) {
	stdout, stderr, code, err := RunTool(ctx, workdir, "go", goArgs...)
	return stdout + stderr, code, err
}

// hostModCache resolves the host GOMODCACHE once (best-effort; "" = none).
func hostModCache() string {
	modCacheOnce.Do(func() {
		out, err := exec.Command("go", "env", "GOMODCACHE").Output()
		if err == nil {
			if p := strings.TrimSpace(string(out)); p != "" && p != "off" {
				if _, serr := os.Stat(p); serr == nil {
					modCacheVal = p
				}
			}
		}
	})
	return modCacheVal
}

var (
	modCacheOnce sync.Once
	modCacheVal  string
)
