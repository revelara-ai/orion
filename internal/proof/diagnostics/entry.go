package diagnostics

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
)

// entrySignature is the CLI-family generation contract's entry shape (or-v9f.3
// §4.2): the behavioral corpus calls this symbol in-process.
const entrySignature = "func run(args []string, stdin io.Reader, stdout, stderr io.Writer, env map[string]string) int"

// CheckEntry verifies the artifact declares the exec entry symbol with the
// exact contract signature — the fast-tier guard that turns a missing/mis-signed
// run() into ONE targeted generator-facing diagnostic instead of a corpus
// compile failure cascading into all-obligations-Inconclusive spin.
func CheckEntry(artifactDir, entry string) Result {
	src, err := os.ReadFile(filepath.Join(artifactDir, "main.go"))
	if err != nil {
		return Result{OK: false, Output: "entry check: " + err.Error()}
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", src, 0)
	if err != nil {
		return Result{OK: false, Output: "entry check: main.go does not parse: " + err.Error()}
	}
	for _, d := range file.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != entry || fn.Recv != nil {
			continue
		}
		if entryConforms(fn) {
			return Result{OK: true}
		}
		return Result{OK: false, Output: fmt.Sprintf(
			"entry check: %s exists but does not match the generation contract — required: %s", entry, entrySignature)}
	}
	return Result{OK: false, Output: fmt.Sprintf(
		"entry check: no %s function — the CLI generation contract requires: %s (main must be a thin os.Exit(run(...)) wrapper)", entry, entrySignature)}
}

// entryConforms structurally checks run's signature: 5 params, int result.
// go/parser altitude (names/shapes), sufficient for the slice; go/types lands
// with the unit kind (Phase 2).
func entryConforms(fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil || fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
		return false
	}
	if ident, ok := fn.Type.Results.List[0].Type.(*ast.Ident); !ok || ident.Name != "int" {
		return false
	}
	n := 0
	for _, f := range fn.Type.Params.List {
		if len(f.Names) == 0 {
			n++
		} else {
			n += len(f.Names)
		}
	}
	return n == 5
}
