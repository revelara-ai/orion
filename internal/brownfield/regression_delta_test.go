package brownfield

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// or-cp90: parseTestFailures collects top-level failures, subtest failures,
// and package build failures — the identity set the delta verdict diffs.
func TestParseTestFailures(t *testing.T) {
	out := `=== RUN   TestAdd
--- FAIL: TestAdd (0.00s)
=== RUN   TestScope
--- FAIL: TestScope (0.09s)
    --- FAIL: TestScope/team_scope_allows_valid_team_access (0.00s)
ok  	example.com/t/ok	0.01s
FAIL	example.com/t	0.10s
FAIL	example.com/t/broken [build failed]
`
	got := parseTestFailures(out)
	want := []string{
		"TestAdd",
		"TestScope",
		"TestScope/team_scope_allows_valid_team_access",
		"example.com/t/broken [build failed]",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseTestFailures:\n got %v\nwant %v", got, want)
	}
	if parseTestFailures("ok  \texample.com/t\t0.01s\n") != nil {
		t.Fatal("a green run must parse to no failures")
	}
}

// a repo with one green test and one deliberately red test — the polaris shape:
// a baseline that is red for reasons unrelated to any change.
func preexistingRedRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	must(t, filepath.Join(dir, "go.mod"), "module example.com/t\n\ngo 1.25\n")
	must(t, filepath.Join(dir, "lib.go"), "package t\n\nfunc Add(a, b int) int { return a + b }\n")
	must(t, filepath.Join(dir, "lib_test.go"), "package t\nimport \"testing\"\nfunc TestAdd(t *testing.T){ if Add(2,3)!=5 { t.Fatal(\"math\") } }\nfunc TestPreexisting(t *testing.T){ t.Fatal(\"red before any change\") }\n")
	return dir
}

// or-cp90 core: a change that introduces NO new failures holds against a red
// baseline (delta is the default), with the pre-existing failures named.
func TestRegressionGateDeltaHoldsOnPreexistingRed(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := preexistingRedRepo(t)
	apply := func() error {
		return os.WriteFile(filepath.Join(dir, "extra.go"), []byte("package t\n\nfunc Mul(a, b int) int { return a * b }\n"), 0o644)
	}
	r, err := RegressionGate(context.Background(), dir, nil, apply, nil)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !r.Held {
		t.Fatalf("a harmless change against a red baseline must hold under delta semantics: %+v", r)
	}
	if len(r.PreExisting) != 1 || r.PreExisting[0] != "TestPreexisting" {
		t.Fatalf("the excluded pre-existing failures must be named: %+v", r.PreExisting)
	}
}

// or-cp90: a change that breaks a PREVIOUSLY-GREEN test still blocks, and the
// verdict names the new failure (not just held=false).
func TestRegressionGateDeltaBlocksNewFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := preexistingRedRepo(t)
	apply := func() error { // break Add → TestAdd (green before) regresses
		return os.WriteFile(filepath.Join(dir, "lib.go"), []byte("package t\n\nfunc Add(a, b int) int { return a - b }\n"), 0o644)
	}
	r, err := RegressionGate(context.Background(), dir, nil, apply, nil)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if r.Held {
		t.Fatal("a change that regresses a previously-green test must NOT hold")
	}
	if len(r.NewFailures) != 1 || r.NewFailures[0] != "TestAdd" {
		t.Fatalf("the NEW failure must be identified: %+v", r.NewFailures)
	}
	if !containsAll(r.Reason, "TestAdd") {
		t.Fatalf("the reason must name the blocking test: %q", r.Reason)
	}
}

// or-cp90: ORION_REGRESSION_BASELINE=strict preserves the old behavior — a red
// baseline refuses the gate outright, before any apply.
func TestRegressionGateStrictModeKeepsOldBehavior(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	t.Setenv("ORION_REGRESSION_BASELINE", "strict")
	dir := preexistingRedRepo(t)
	applied := false
	apply := func() error { applied = true; return nil }
	r, err := RegressionGate(context.Background(), dir, nil, apply, nil)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if r.Held {
		t.Fatal("strict mode must refuse a red baseline")
	}
	if applied {
		t.Fatal("strict mode must not apply the change against a red baseline")
	}
	if !containsAll(r.Reason, "baseline is RED") {
		t.Fatalf("strict mode keeps the old reason: %q", r.Reason)
	}
}

// or-cp90: the scoped (default) gate holds the same delta semantics.
func TestRegressionGateScopedDeltaHoldsOnPreexistingRed(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	dir := preexistingRedRepo(t)
	gitX(t, dir, "init")
	gitX(t, dir, "add", ".")
	gitX(t, dir, "commit", "-m", "baseline")
	m := ScanRepoMap(dir)
	apply := func() error {
		return os.WriteFile(filepath.Join(dir, "extra.go"), []byte("package t\n\nfunc Mul(a, b int) int { return a * b }\n"), 0o644)
	}
	r, err := RegressionGateScoped(context.Background(), dir, m, nil, apply, nil)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if !r.Held {
		t.Fatalf("scoped delta must hold a harmless change against a red baseline: %+v", r)
	}
	if len(r.PreExisting) != 1 || r.PreExisting[0] != "TestPreexisting" {
		t.Fatalf("scoped delta must name the excluded failures: %+v", r.PreExisting)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
