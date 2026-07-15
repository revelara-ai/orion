package proofexec

import (
	"fmt"
	"os/exec"
	"path/filepath"
)

// Toolchain is a language's proof-execution surface — everything RunTool's
// behavior varies by language on: the verification-command allowlist + denied
// subcommands, binary resolution, the read-only sandbox binds, and the
// hermetic env. It is resolved by language (or-4y7.2); the Go toolchain is the
// default and returns the V2.0 literals verbatim, so the Go path is
// byte-identical. It is version-aware by construction — ResolveBin picks the
// binary for the resolved runtime, so a Python/Node adapter selects the
// developer-pinned interpreter (or-4y7.10); Go ignores version (GOTOOLCHAIN=local).
type Toolchain interface {
	Language() string
	// Allow validates a tool + args against the language's allowlist and
	// denied-subcommand policy; a non-nil error refuses the exec.
	Allow(tool string, args []string) error
	// IsPrimary reports whether a tool is the language's PRIMARY toolchain binary
	// (resident under Roots, and eligible for the operator's unisolated-backend
	// override) vs an AUXILIARY tool (bound separately, sandbox strictly
	// required). Cheap — no host lookup — so the sandbox policy is decided BEFORE
	// resolving a binary (an auxiliary tool refuses the none backend before any
	// host LookPath).
	IsPrimary(tool string) bool
	// ResolveBin returns the absolute host path to a tool's binary (never the
	// worktree). It is called only after the sandbox policy admitted the exec.
	ResolveBin(tool string) (path string, err error)
	// Roots are directories bound read-only into the sandbox (toolchain root + caches).
	Roots() []string
	// Env is the hermetic, secret-scrubbed environment for a run in workdir.
	Env(workdir string) map[string]string
	// UnsafeNoneOverride reports whether the language permits the operator's
	// explicit unisolated-backend override for its primary binary.
	UnsafeNoneOverride() bool
}

// toolchains is the per-language registry; "" resolves to Go.
var toolchains = map[string]Toolchain{}

// registerToolchain adds a language's proof toolchain (called from init).
func registerToolchain(tc Toolchain) { toolchains[tc.Language()] = tc }

// toolchainFor resolves the proof toolchain for a language ("" → go). An
// unregistered language returns nil — RunTool refuses rather than guessing.
func toolchainFor(language string) Toolchain {
	if language == "" {
		language = "go"
	}
	return toolchains[language]
}

// goToolchain is the default: the V2.0 go/golangci-lint proof execs, verbatim.
type goToolchain struct{}

func (goToolchain) Language() string { return "go" }

func (goToolchain) Allow(tool string, args []string) error {
	if !allowedTools[tool] {
		return fmt.Errorf("proofexec: tool %q is not on the verification allowlist", tool)
	}
	if tool == "go" && len(args) > 0 && goDeniedSubcommands[args[0]] {
		return fmt.Errorf("proofexec: `go %s` is not allowed (it runs arbitrary code)", args[0])
	}
	return nil
}

func (goToolchain) IsPrimary(tool string) bool { return tool == "go" }

func (goToolchain) ResolveBin(tool string) (string, error) {
	if tool == "go" {
		return filepath.Join(goRoot(), "bin", "go"), nil
	}
	// A non-go tool (golangci-lint) is resolved from the TRUSTED host and bound.
	bin, err := exec.LookPath(tool)
	if err != nil {
		return "", fmt.Errorf("proofexec: %q not found on host: %w", tool, err)
	}
	if resolved, serr := filepath.EvalSymlinks(bin); serr == nil {
		bin = resolved
	}
	return bin, nil
}

func (goToolchain) Roots() []string {
	roots := []string{goRoot()}
	if mc := hostModCache(); mc != "" {
		roots = append(roots, mc)
	}
	return roots
}

func (goToolchain) Env(workdir string) map[string]string {
	env := toolEnv(goRoot(), workdir)
	if mc := hostModCache(); mc != "" {
		env["GOMODCACHE"] = mc // read-only bind (via Roots); cache-only resolution
	}
	return env
}

func (goToolchain) UnsafeNoneOverride() bool { return true }

func init() { registerToolchain(goToolchain{}) }
