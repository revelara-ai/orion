package empirical

import (
	"context"
	"path/filepath"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/proofexec"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
	"github.com/revelara-ai/orion/internal/proof/unitprobe"
)

// langTool is a language's empirical-proof surface (or-4y7.6): staging the
// buildable sources, producing the runnable binary, and building the sandboxed
// unit-case driver. The probing that follows — service run + HTTP/exec/file
// assertions (casecheck, probeContract, checkFileCase) — is language-neutral and
// stays shared. Resolved by Contract.Language; the Go tool is the default and
// wraps the V2.0 path verbatim. (An interpreted language's runnable form is an
// argv, not a compiled binary — that generalization lands with the first such
// impl, or-4y7.9; the seam keeps Go byte-identical meanwhile.)
type langTool interface {
	Language() string
	// Stage copies the buildable sources from the artifact into the
	// proof-controlled dir (never the generator's worktree).
	Stage(src, dst string) error
	// Build produces the runnable binary in binDir, returning its path plus the
	// build output and exit code (a non-zero exit is a proof failure, not err).
	Build(ctx context.Context, binDir string) (bin string, output string, code int, err error)
	// BuildUnitDriver builds the sandboxed unit-case driver (or-v9f.23):
	// driver path, whether it built, and the build detail when it did not.
	BuildUnitDriver(ctx context.Context, binDir string, unitCases []spec.BehavioralCase) (string, bool, string, error)
	// UnitRound executes one round of unit cases against the built driver in the
	// language's sandbox (or-4y7.9): per-case obligation statuses + failure detail.
	UnitRound(ctx context.Context, driver string, unitCases []spec.BehavioralCase) (map[string]truthalign.ObligationStatus, string)
}

var langTools = map[string]langTool{}

func registerLangTool(t langTool) { langTools[t.Language()] = t }

// langToolFor resolves the empirical tool for a language ("" → go). An
// unregistered language returns nil — Prove refuses rather than go-building
// non-Go sources.
func langToolFor(language string) langTool {
	if language == "" {
		language = "go"
	}
	return langTools[language]
}

// goTool is the default: the V2.0 Go stage/build/unit-driver, verbatim.
type goTool struct{}

func (goTool) Language() string { return "go" }

func (goTool) Stage(src, dst string) error { return stageArtifact(src, dst) }

func (goTool) Build(ctx context.Context, binDir string) (string, string, int, error) {
	out, code, err := proofexec.GoToolchain(ctx, binDir, "build", "-o", "svc", ".")
	return filepath.Join(binDir, "svc"), out, code, err
}

func (goTool) BuildUnitDriver(ctx context.Context, binDir string, unitCases []spec.BehavioralCase) (string, bool, string, error) {
	return unitprobe.BuildDriver(ctx, binDir, unitCases)
}

func (goTool) UnitRound(ctx context.Context, driver string, unitCases []spec.BehavioralCase) (map[string]truthalign.ObligationStatus, string) {
	return unitprobe.RunRound(ctx, driver, unitCases)
}

func init() { registerLangTool(goTool{}) }
