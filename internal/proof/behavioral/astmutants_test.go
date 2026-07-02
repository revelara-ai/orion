package behavioral

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

const mutSrc = `package main

func decide(a, b int, flag bool) int {
	if a == b {
		return a + b
	}
	if flag {
		return a - b
	}
	verbose := true
	_ = verbose
	msg := "x" + "y"
	_ = msg
	return 0
}
`

// TestASTMutantsGenerateRealVariants: comparison flips, boolean flips, and
// arithmetic swaps are produced — each parseable and different from the original.
func TestASTMutantsGenerateRealVariants(t *testing.T) {
	ms := astMutants(mutSrc, 8)
	if len(ms) < 3 {
		t.Fatalf("expected comparison+bool+arith mutants, got %d: %+v", len(ms), ms)
	}
	kinds := map[string]bool{}
	for _, m := range ms {
		if m.source == mutSrc {
			t.Errorf("mutant %s is identical to the original", m.name)
		}
		if _, err := parser.ParseFile(token.NewFileSet(), "m.go", m.source, 0); err != nil {
			t.Errorf("mutant %s does not parse: %v", m.name, err)
		}
		switch {
		case strings.HasPrefix(m.name, "ast-flip"):
			kinds["flip"] = true
		case strings.HasPrefix(m.name, "ast-bool"):
			kinds["bool"] = true
		case strings.HasPrefix(m.name, "ast-arith"):
			kinds["arith"] = true
		}
	}
	for _, k := range []string{"flip", "bool", "arith"} {
		if !kinds[k] {
			t.Errorf("missing %s mutants in %+v", k, ms)
		}
	}
	// String concat must not be arithmetic-swapped (it would just be a compile
	// error discarded later — but the operator must not even try).
	for _, m := range ms {
		if strings.Contains(m.source, `"x" - "y"`) {
			t.Errorf("string concat was arithmetic-swapped: %s", m.name)
		}
	}
}

// TestASTMutantsRespectCap: the cap bounds proof cost deterministically.
func TestASTMutantsRespectCap(t *testing.T) {
	if got := len(astMutants(mutSrc, 2)); got != 2 {
		t.Fatalf("cap 2 must yield 2 mutants, got %d", got)
	}
	if got := len(astMutants("package main\nfunc f() {}\n", 8)); got != 0 {
		t.Fatalf("no mutable sites must yield 0 mutants, got %d", got)
	}
}

// TestMutationGateMatrix: zero applicable mutants is UNMEASURED — inconclusive,
// never a silent pass (or-v9f.11); the threshold is the caller's (tier) bar.
func TestMutationGateMatrix(t *testing.T) {
	cases := []struct {
		name             string
		killed, total    int
		threshold        float64
		wantPass, wantInc bool
	}{
		{"zero mutants is inconclusive", 0, 0, 0.6, false, true},
		{"below threshold fails", 1, 4, 0.6, false, false},
		{"at threshold passes", 3, 4, 0.6, true, false},
		{"critical bar fails what standard passes", 3, 4, 0.9, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pass, inc, _ := mutationGate(true, tc.killed, tc.total, tc.threshold)
			if pass != tc.wantPass || inc != tc.wantInc {
				t.Errorf("got pass=%v inc=%v, want %v/%v", pass, inc, tc.wantPass, tc.wantInc)
			}
		})
	}
}
