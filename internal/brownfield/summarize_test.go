package brownfield

import (
	"strings"
	"testing"
)

// TestSummarizeCountsAndExpandsOnlyFailures (or-rbc): the baseline summary
// counts packages by verdict (green / failed / no-tests) and captures the
// output block for FAILING packages only — a green or no-tests package is never
// expanded, so the report can show "N green / M failing" with failures alone.
func TestSummarizeCountsAndExpandsOnlyFailures(t *testing.T) {
	out := strings.Join([]string{
		"ok  \texample.com/m/alpha\t0.20s",
		"--- FAIL: TestBeta (0.00s)",
		"    beta_test.go:9: want 4 got 5",
		"FAIL",
		"FAIL\texample.com/m/beta\t0.01s",
		"?   \texample.com/m/gamma\t[no test files]",
		"ok  \texample.com/m/delta\t(cached)",
		"# example.com/m/epsilon",
		"./e.go:3:2: undefined: X",
		"FAIL\texample.com/m/epsilon [build failed]",
		"FAIL",
	}, "\n")

	s := Summarize(out)

	// Counts: alpha+delta green, beta+epsilon failed, gamma no-tests.
	if len(s.Green) != 2 {
		t.Fatalf("green=%v, want 2 (alpha, delta)", s.Green)
	}
	if len(s.Failed) != 2 {
		t.Fatalf("failed=%v, want 2 (beta, epsilon)", s.Failed)
	}
	if len(s.NoTests) != 1 {
		t.Fatalf("noTests=%v, want 1 (gamma)", s.NoTests)
	}

	// Only failures are expanded — with the failing detail intact.
	if !strings.Contains(s.Blocks["example.com/m/beta"], "want 4 got 5") {
		t.Fatalf("beta's failure detail must be captured, got %q", s.Blocks["example.com/m/beta"])
	}
	if !strings.Contains(s.Blocks["example.com/m/epsilon"], "undefined: X") {
		t.Fatalf("epsilon's build error must be captured, got %q", s.Blocks["example.com/m/epsilon"])
	}

	// Negative: green and no-tests packages must NOT be expanded (failure-only),
	// or the report would dump the full output it exists to replace.
	if _, ok := s.Blocks["example.com/m/alpha"]; ok {
		t.Fatal("a green package must not be in the expanded blocks")
	}
	if _, ok := s.Blocks["example.com/m/gamma"]; ok {
		t.Fatal("a no-tests package must not be in the expanded blocks")
	}
	// Every failed package has a block; the block count equals the failure count.
	if len(s.Blocks) != len(s.Failed) {
		t.Fatalf("expanded %d blocks for %d failures — must be one per failure", len(s.Blocks), len(s.Failed))
	}
}
