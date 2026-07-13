package behavioral

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
)

// astMutantCap bounds how many mutants a proof runs — mutation cost is one
// sandboxed compile+test per mutant, so the cap keeps proof latency sane while
// still defeating tautological corpora. Sites are visited in source order, so
// the selection is deterministic.
const astMutantCap = 8

// comparisonFlips are behavior-inverting operator substitutions.
var comparisonFlips = map[token.Token]token.Token{
	token.EQL: token.NEQ, token.NEQ: token.EQL,
	token.LSS: token.GEQ, token.GTR: token.LEQ,
	token.LEQ: token.GTR, token.GEQ: token.LSS,
}

// arithmeticSwaps alter computed values the corpus should pin.
var arithmeticSwaps = map[token.Token]token.Token{
	token.ADD: token.SUB, token.SUB: token.ADD,
	token.MUL: token.QUO,
}

// astMutants generates AST-level mutants of src (a Go source file): comparison
// flips, boolean literal flips, and arithmetic swaps — one mutation per site.
// String-concat '+' is skipped when a string literal is visible on either side;
// anything that still fails to compile is discarded by the caller's build check
// (a broken mutant must never count as killed). Returns at most cap mutants.
func astMutants(src string, limit int) []mutant {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", src, parser.ParseComments)
	if err != nil {
		return nil
	}

	var out []mutant
	render := func(name string) {
		var b bytes.Buffer
		if err := printer.Fprint(&b, fset, file); err == nil {
			out = append(out, mutant{name: name, source: b.String()})
		}
	}

	ast.Inspect(file, func(n ast.Node) bool {
		if len(out) >= limit {
			return false
		}
		switch node := n.(type) {
		case *ast.BinaryExpr:
			if flipped, ok := comparisonFlips[node.Op]; ok {
				orig := node.Op
				node.Op = flipped
				render("ast-flip-" + orig.String())
				node.Op = orig
				return true
			}
			if swapped, ok := arithmeticSwaps[node.Op]; ok && !hasStringOperand(node) {
				orig := node.Op
				node.Op = swapped
				render("ast-arith-" + orig.String())
				node.Op = orig
			}
		case *ast.Ident:
			if node.Name == "true" || node.Name == "false" {
				orig := node.Name
				if orig == "true" {
					node.Name = "false"
				} else {
					node.Name = "true"
				}
				render("ast-bool-flip")
				node.Name = orig
			}
		}
		return true
	})
	return out
}

// hasStringOperand reports whether either side of the expression is (visibly) a
// string literal — a heuristic to avoid mutating string concatenation; deeper
// string-typed expressions that slip through are discarded by the build check.
func hasStringOperand(e *ast.BinaryExpr) bool {
	for _, side := range []ast.Expr{e.X, e.Y} {
		if lit, ok := side.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			return true
		}
	}
	return false
}
