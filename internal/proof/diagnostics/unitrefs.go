package diagnostics

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// CheckUnitRefs verifies every unit case's package exists and parses in the
// artifact — the fast-tier guard that turns a missing package into ONE targeted
// generator-facing diagnostic (or-v9f.23). Deeper symbol resolution is the
// corpus/driver compile's job; their errors are case-attributable.
func CheckUnitRefs(artifactDir string, cases []spec.BehavioralCase) Result {
	for _, cs := range cases {
		if cs.Kind != spec.KindUnit || cs.Unit == nil {
			continue
		}
		dir := filepath.Join(artifactDir, cs.Unit.Pkg)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return Result{OK: false, Output: fmt.Sprintf(
				"unit refs: case %s targets package %q which does not exist in the artifact — create the package with the exported surface the case calls", cs.ID, cs.Unit.Pkg)}
		}
		parsed := false
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".go" {
				continue
			}
			if _, perr := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, parser.PackageClauseOnly); perr == nil {
				parsed = true
				break
			}
		}
		if !parsed {
			return Result{OK: false, Output: fmt.Sprintf(
				"unit refs: case %s targets package %q which has no parseable Go sources", cs.ID, cs.Unit.Pkg)}
		}
	}
	return Result{OK: true}
}
