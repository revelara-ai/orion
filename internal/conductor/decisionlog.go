package conductor

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/memory"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

// rememberDecidedConstraints is the persistent decision log (or-v9f.8): once a
// module PROVES, its structural decisions — module path, exported symbols,
// served routes — are extracted from the artifact itself and written as one
// proof-trust decision item. The context engine re-injects proof-trust items
// into every later task's generation context, so module N+1 REUSES module N's
// choices instead of re-deciding them (the manifesto's durable decision log).
// Trust wall: everything here derives from the artifact the proof accepted,
// never from the agent's self-report.
func rememberDecidedConstraints(ctx context.Context, mem *memory.Store, taskID, artifactDir string, report proof.Report) error {
	if mem == nil || report.Outcome.Verdict != truthalign.Accept {
		return nil
	}
	src, err := os.ReadFile(filepath.Join(artifactDir, "main.go"))
	if err != nil {
		return nil // nothing extractable; not an error worth failing memory upkeep
	}
	decisions := extractDecisions(string(src))
	if module := modulePath(artifactDir); module != "" {
		decisions = append([]string{"module " + module}, decisions...)
	}
	if len(decisions) == 0 {
		return nil
	}
	_, err = mem.Write(ctx, memory.Item{
		Tier:      memory.MTM,
		Kind:      memory.KindDecision,
		TrustTier: memory.TrustProof,
		Heat:      1.0,
		Content: fmt.Sprintf("Decided constraints from proven task %s — dependent modules REUSE these, never re-decide: %s",
			taskID, strings.Join(decisions, "; ")),
	})
	return err
}

// extractDecisions pulls the cross-module constraints out of a proven source
// file: exported func/type names (the API surface later modules call) and the
// route string literals registered on the mux (the wire surface they target).
func extractDecisions(src string) []string {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", src, 0)
	if err != nil {
		return nil
	}
	var exports, routes []string
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			if node.Name.IsExported() && node.Recv == nil {
				exports = append(exports, "func "+node.Name.Name)
			}
		case *ast.TypeSpec:
			if node.Name.IsExported() {
				exports = append(exports, "type "+node.Name.Name)
			}
		case *ast.CallExpr:
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok &&
				(sel.Sel.Name == "HandleFunc" || sel.Sel.Name == "Handle") && len(node.Args) > 0 {
				if lit, ok := node.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING && strings.HasPrefix(lit.Value, `"/`) {
					routes = append(routes, strings.Trim(lit.Value, `"`))
				}
			}
		}
		return true
	})
	sort.Strings(exports)
	sort.Strings(routes)
	var out []string
	if len(exports) > 0 {
		out = append(out, "exports: "+strings.Join(exports, ", "))
	}
	if len(routes) > 0 {
		out = append(out, "routes: "+strings.Join(routes, ", "))
	}
	return out
}

// modulePath reads the module directive from the artifact's go.mod.
func modulePath(artifactDir string) string {
	b, err := os.ReadFile(filepath.Join(artifactDir, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if m, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(m)
		}
	}
	return ""
}
