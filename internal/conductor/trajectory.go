package conductor

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// buildTrajectory (or-gb1.4) accumulates the harness-derived story of a task's
// refinement loop: how many attempts it took, WHAT each failing attempt's
// causal analysis said, and what the passing attempt changed relative to the
// last failing artifact. Everything here is harness-derived (proof reports,
// artifact bytes) — never an agent self-report — so it may ride proof-tier
// memory without crossing the trust wall.
type buildTrajectory struct {
	Attempts       int
	Overcame       []string // each failed attempt's causal failureAnalysis (clipped)
	lastFailingSrc string   // the last failing artifact's entrypoint source
	ChangeSummary  string   // last failing vs passing artifact, computed by finish
}

// trajectoryClip bounds each persisted analysis — memory is evidence, not a transcript.
const trajectoryClip = 700

// recordFailure captures a failed attempt: its causal analysis and the failing
// artifact's entrypoint source (for the eventual inter-attempt diff).
func (tr *buildTrajectory) recordFailure(analysis, buildDir string) {
	tr.Attempts++
	if a := strings.TrimSpace(analysis); a != "" {
		if len(a) > trajectoryClip {
			a = a[:trajectoryClip] + "…"
		}
		tr.Overcame = append(tr.Overcame, a)
	}
	if b, err := os.ReadFile(filepath.Join(buildDir, "main.go")); err == nil {
		tr.lastFailingSrc = string(b)
	}
}

// finish records the passing attempt and computes the inter-attempt change
// summary between the last failing artifact and the passing one.
func (tr *buildTrajectory) finish(buildDir string) {
	tr.Attempts++
	if tr.lastFailingSrc == "" {
		return // first-attempt pass: nothing was overcome
	}
	b, err := os.ReadFile(filepath.Join(buildDir, "main.go"))
	if err != nil {
		return
	}
	tr.ChangeSummary = changeSummary(tr.lastFailingSrc, string(b))
}

// overcame reports whether the task converged after at least one failure —
// only then is there a trajectory worth remembering.
func (tr *buildTrajectory) overcame() bool {
	return tr != nil && len(tr.Overcame) > 0
}

// changeSummary describes what changed between a failing and a passing
// artifact: which top-level declarations were added, removed, or modified,
// plus a line-count delta. Deterministic and harness-derived. Falls back to
// the line stat alone if either side fails to parse.
func changeSummary(before, after string) string {
	stat := fmt.Sprintf("%+d lines", strings.Count(after, "\n")-strings.Count(before, "\n"))
	bd, berr := topDecls(before)
	ad, aerr := topDecls(after)
	if berr != nil || aerr != nil {
		return stat
	}
	var added, removed, changed []string
	for name, body := range ad {
		prev, ok := bd[name]
		switch {
		case !ok:
			added = append(added, name)
		case prev != body:
			changed = append(changed, name)
		}
	}
	for name := range bd {
		if _, ok := ad[name]; !ok {
			removed = append(removed, name)
		}
	}
	var parts []string
	if len(changed) > 0 {
		parts = append(parts, "modified "+strings.Join(sortedStrings(changed), ", "))
	}
	if len(added) > 0 {
		parts = append(parts, "added "+strings.Join(sortedStrings(added), ", "))
	}
	if len(removed) > 0 {
		parts = append(parts, "removed "+strings.Join(sortedStrings(removed), ", "))
	}
	if len(parts) == 0 {
		parts = append(parts, "no top-level declaration changes")
	}
	return strings.Join(parts, "; ") + " (" + stat + ")"
}

// topDecls maps each top-level declaration name to its source text.
func topDecls(src string) (map[string]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", src, 0)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, d := range f.Decls {
		start, end := fset.Position(d.Pos()).Offset, fset.Position(d.End()).Offset
		if start < 0 || end > len(src) || start >= end {
			continue
		}
		out[declName(d)] = src[start:end]
	}
	return out, nil
}

// declName renders a stable identifier for a top-level declaration.
func declName(d ast.Decl) string {
	switch v := d.(type) {
	case *ast.FuncDecl:
		name := "func " + v.Name.Name
		if v.Recv != nil && len(v.Recv.List) == 1 {
			name = "method " + v.Name.Name
		}
		return name
	case *ast.GenDecl:
		var names []string
		for _, sp := range v.Specs {
			switch s := sp.(type) {
			case *ast.TypeSpec:
				names = append(names, "type "+s.Name.Name)
			case *ast.ValueSpec:
				for _, n := range s.Names {
					names = append(names, n.Name)
				}
			case *ast.ImportSpec:
				return "imports"
			}
		}
		if len(names) == 0 {
			return v.Tok.String()
		}
		return strings.Join(names, ",")
	default:
		return "decl"
	}
}

func sortedStrings(s []string) []string {
	sort.Strings(s)
	return s
}
