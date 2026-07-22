package brownfield

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// a green Go module: Add(2,3)==5 holds.
func greenRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	must(t, filepath.Join(dir, "go.mod"), "module example.com/t\n\ngo 1.25\n")
	must(t, filepath.Join(dir, "lib.go"), "package t\n\nfunc Add(a, b int) int { return a + b }\n")
	must(t, filepath.Join(dir, "lib_test.go"), "package t\nimport \"testing\"\nfunc TestAdd(t *testing.T){ if Add(2,3)!=5 { t.Fatal(\"math\") } }\n")
	return dir
}

// TestRegressionGateHeldOnSafeChange: a change that keeps the suite green → Held.
func TestRegressionGateHeldOnSafeChange(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := greenRepo(t)
	// safe change: add an unrelated function.
	apply := func() error {
		return os.WriteFile(filepath.Join(dir, "extra.go"), []byte("package t\n\nfunc Mul(a, b int) int { return a * b }\n"), 0o644)
	}
	r, err := RegressionGate(context.Background(), dir, nil, apply, nil)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !r.Held {
		t.Fatalf("a behavior-preserving change should hold the regression gate: %+v", r)
	}
}

// TestRegressionGateCatchesRegression: a change that breaks an existing test → not Held.
func TestRegressionGateCatchesRegression(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := greenRepo(t)
	// breaking change: Add now returns the wrong thing → TestAdd fails.
	apply := func() error {
		return os.WriteFile(filepath.Join(dir, "lib.go"), []byte("package t\n\nfunc Add(a, b int) int { return a - b }\n"), 0o644)
	}
	r, err := RegressionGate(context.Background(), dir, nil, apply, nil)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if r.Held {
		t.Fatal("a change that breaks an existing test must NOT hold the regression gate")
	}
	if r.Reason == "" {
		t.Fatal("a failed gate should explain why")
	}
}

// TestRegressionGateNeedsGreenBaseline: a red-before baseline can't be regression-proven.
func TestRegressionGateNeedsGreenBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	// or-cp90: green-before-required is now the STRICT mode; the default (delta)
	// holds a harmless change against a red baseline (see regression_delta_test).
	t.Setenv("ORION_REGRESSION_BASELINE", "strict")
	dir := t.TempDir()
	must(t, filepath.Join(dir, "go.mod"), "module example.com/t\n\ngo 1.25\n")
	must(t, filepath.Join(dir, "lib.go"), "package t\n\nfunc Add(a, b int) int { return a + b }\n")
	must(t, filepath.Join(dir, "lib_test.go"), "package t\nimport \"testing\"\nfunc TestAdd(t *testing.T){ if Add(2,3)!=99 { t.Fatal(\"red baseline\") } }\n")
	r, err := RegressionGate(context.Background(), dir, nil, nil, nil)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if r.Held || r.Reason == "" {
		t.Fatalf("a red baseline cannot be regression-proven: %+v", r)
	}
}
