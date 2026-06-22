// Package diagnostics is the proof harness's FAST-FEEDBACK tier: a cheap static
// check (compile + vet) run BEFORE the expensive behavioral/empirical/hazard modes.
// If generated code doesn't even compile, the full proof cannot run — fail fast with
// the compiler/vet diagnostics (seconds, not minutes), which the refinement loop feeds
// straight back to the generator. This is the inline-compile/LSP-style signal Orion
// lacked (harness-feature-comparison #A10).
//
// SECURITY: this compiles GENERATED (untrusted) code, so it runs with safeenv (no host
// secrets), exactly like every other proof exec.
package diagnostics

import (
	"context"
	"os/exec"
	"strings"

	"github.com/revelara-ai/orion/internal/proof/safeenv"
)

// Result is the static-check outcome.
type Result struct {
	OK     bool   // compiles + vet-clean
	Output string // compiler/vet diagnostics when not OK (trimmed)
}

// Check runs `go vet ./...` in dir. `go vet` builds first, so a single pass catches
// BOTH compile errors and vet findings (printf mismatches, unreachable code, shadowed
// returns, etc.) — the richest cheap signal. OK iff it exits clean.
func Check(ctx context.Context, dir string) Result {
	cmd := exec.CommandContext(ctx, "go", "vet", "./...")
	cmd.Dir = dir
	cmd.Env = safeenv.Build() // untrusted generated code never sees host secrets
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{OK: false, Output: clip(strings.TrimSpace(string(out)), 4000)}
	}
	return Result{OK: true}
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "\n… (truncated)"
	}
	return s
}
