// Package proofexec runs the proof domain's go-toolchain execs (build/test of
// generated, untrusted code) inside internal/sandbox, so that code cannot read
// host secrets (the model API key) or reach the network during proof. It is the
// comprehensive successor to internal/proof/safeenv's env-scrub-only boundary
// (or-5ym, PRD Security Requirements / Manifesto: side-effect sandboxing).
package proofexec

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/revelara-ai/orion/internal/proof/safeenv"
	"github.com/revelara-ai/orion/internal/sandbox"
)

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

// GoToolchain runs `go <goArgs...>` for proof with workdir as the cwd (and the
// only writable path under isolation), returning combined stdout+stderr and the
// process exit code. A non-zero exit is returned via exitCode, not err; err is
// reserved for failures to launch the toolchain at all.
//
// Inside the sandbox the toolchain gets: a read-only bind of GOROOT (the `go`
// binary + stdlib + tools), GOCACHE/GOPATH redirected INTO the writable workdir,
// GOTOOLCHAIN=local + GOPROXY=off + CGO_ENABLED=0 (no network, no C toolchain),
// and a scrubbed env (no host secrets). Network egress is denied by default, so
// untrusted code under proof can neither phone home nor pull an unverified
// dependency.
func GoToolchain(ctx context.Context, workdir string, goArgs ...string) (output string, exitCode int, err error) {
	be, err := sandbox.New(isolation())
	if err != nil {
		return "", 0, err
	}
	if be.Name() == "none" {
		warnNoneOnce.Do(func() {
			slog.Warn("proofexec: no namespace sandbox available; proof execs run with a scrubbed env but WITHOUT network/filesystem isolation",
				"backend", be.Name())
		})
	}

	root := goRoot()
	env := safeenv.Map() // scrubbed allowlist (no secrets)
	// Toolchain must be self-contained and hermetic inside the sandbox.
	env["PATH"] = filepath.Join(root, "bin") + ":/usr/bin:/bin"
	env["HOME"] = workdir
	env["GOROOT"] = root
	env["GOCACHE"] = filepath.Join(workdir, ".orion-gocache") // writable: inside the workdir
	env["GOPATH"] = filepath.Join(workdir, ".orion-gopath")
	env["GOTOOLCHAIN"] = "local" // never fetch a toolchain over the (denied) network
	env["GOPROXY"] = "off"       // no module downloads under proof
	env["GOFLAGS"] = ""
	env["CGO_ENABLED"] = "0" // no C toolchain needed inside the sandbox

	res, runErr := be.Run(ctx, sandbox.Spec{
		Workdir:  workdir,
		Argv:     append([]string{filepath.Join(root, "bin", "go")}, goArgs...),
		Env:      env,
		ROBinds:  []string{root},
		AllowNet: false, // default-deny egress — the security property of this path
	})
	return res.Stdout + res.Stderr, res.ExitCode, runErr
}
