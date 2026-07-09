package brownfield

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeRepoFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitX(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=T"}, args...)
	if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newScopeRepo builds a committed Go module with two INDEPENDENT packages: a (green)
// and b (intentionally RED). A blast-radius-scoped gate on a must exclude b.
func newScopeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeRepoFile(t, dir, "go.mod", "module testmod\n\ngo 1.21\n")
	writeRepoFile(t, dir, "a/a.go", "package a\n\nfunc A() int { return 1 }\n")
	writeRepoFile(t, dir, "a/a_test.go", `package a

import "testing"

func TestA(t *testing.T) {
	if A() != 1 {
		t.Fatal("A changed")
	}
}
`)
	writeRepoFile(t, dir, "b/b.go", "package b\n\nfunc B() int { return 2 }\n")
	writeRepoFile(t, dir, "b/b_test.go", `package b

import "testing"

func TestB(t *testing.T) { t.Fatal("b intentionally red") }
`)
	gitX(t, dir, "init", "-b", "main")
	gitX(t, dir, "add", ".")
	gitX(t, dir, "commit", "-m", "init")
	return dir
}

// TestRegressionGateScopedExcludesOutOfScopePackage: the FULL gate is blocked by b's
// red baseline; a change confined to a HOLDS under the scoped gate because b is outside
// a's blast radius.
func TestRegressionGateScopedExcludesOutOfScopePackage(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	ctx := context.Background()

	full := newScopeRepo(t)
	fr, err := RegressionGate(ctx, full, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fr.Held {
		t.Fatalf("full gate must NOT hold (b is red): %+v", fr)
	}

	scoped := newScopeRepo(t)
	m := ScanRepoMap(scoped)
	sr, err := RegressionGateScoped(ctx, scoped, m, nil, func() error {
		return os.WriteFile(filepath.Join(scoped, "a/a.go"),
			[]byte("package a\n\nfunc A() int { return 1 }\n\nfunc A2() int { return 2 }\n"), 0o644)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !sr.Held {
		t.Fatalf("scoped gate must hold (b is out of a's blast radius): %+v", sr)
	}
}

// TestRegressionForcedFull: a module/dependency change forces the full suite; ordinary
// source/config changes do not.
func TestRegressionForcedFull(t *testing.T) {
	for _, c := range []struct {
		paths []string
		want  bool
	}{
		{[]string{"go.mod"}, true},
		{[]string{"go.sum"}, true},
		{[]string{"go.work"}, true},
		{[]string{"internal/foo/go.mod"}, true},
		{[]string{"internal/foo/x.go", ".golangci.yml"}, false},
		{[]string{"Makefile"}, false},
	} {
		if got := regressionForcedFull(c.paths); got != c.want {
			t.Errorf("regressionForcedFull(%v) = %v, want %v", c.paths, got, c.want)
		}
	}
}

// TestScopeDirsForChange: .go files always scope to their dir (new or modified package);
// non-.go files scope only when they live in an existing package (embed/testdata); files
// outside any package (root config/docs) contribute nothing.
func TestScopeDirsForChange(t *testing.T) {
	m := RepoMap{Packages: []GoPackage{{Dir: "internal/foo"}, {Dir: "internal/bar"}}}
	eq := func(got []string, want ...string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}
	if d := scopeDirsForChange([]string{".golangci.yml", "Makefile"}, m); len(d) != 0 {
		t.Errorf("root config change should yield empty scope, got %v", d)
	}
	if d := scopeDirsForChange([]string{"internal/foo/x.go"}, m); !eq(d, "internal/foo") {
		t.Errorf("go change scope = %v, want [internal/foo]", d)
	}
	if d := scopeDirsForChange([]string{"internal/foo/data.json"}, m); !eq(d, "internal/foo") {
		t.Errorf("embedded-asset change scope = %v, want [internal/foo]", d)
	}
	if d := scopeDirsForChange([]string{"docs/readme.md"}, m); len(d) != 0 {
		t.Errorf("non-package non-go change should be empty, got %v", d)
	}
	if d := scopeDirsForChange([]string{"internal/new/y.go"}, m); !eq(d, "internal/new") {
		t.Errorf("new (unmapped) package scope = %v, want [internal/new]", d)
	}
}

// TestRegressionGateScopedSkipsToolingChange: a change touching NO Go package holds vacuously
// with zero tests run — even though the repo has a RED package (which the full suite would trip
// on). This is the fix for a tooling/config change that used to run the whole suite.
func TestRegressionGateScopedSkipsToolingChange(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	repo := newScopeRepo(t) // b is intentionally RED
	m := ScanRepoMap(repo)
	r, err := RegressionGateScoped(context.Background(), repo, m, nil, func() error {
		return os.WriteFile(filepath.Join(repo, "Makefile"), []byte("lint:\n\t@echo lint\n"), 0o644)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Held {
		t.Fatalf("a no-Go-package change must hold vacuously (not run the red package): %+v", r)
	}
	if r.Before.Skipped == "" || r.Before.Command != "" {
		t.Errorf("skip case must run NO tests (Skipped set, Command empty): %+v", r.Before)
	}
}

// TestRegressionGateScopedEscalatesOnGoMod: a go.mod change escalates to the FULL ./... suite
// (the import graph can't capture a dependency change's runtime impact).
func TestRegressionGateScopedEscalatesOnGoMod(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	repo := newScopeRepo(t)
	m := ScanRepoMap(repo)
	r, err := RegressionGateScoped(context.Background(), repo, m, nil, func() error {
		return os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module testmod\n\ngo 1.21\n// dep bump\n"), 0o644)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Before.Command != "go test ./..." {
		t.Errorf("a go.mod change must escalate to the full suite, got command %q (%+v)", r.Before.Command, r)
	}
}

// TestRegressionGateScopedCatchesInScopeRegression: a change that breaks a test WITHIN
// the scope is still rejected.
func TestRegressionGateScopedCatchesInScopeRegression(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	ctx := context.Background()
	repo := newScopeRepo(t)
	m := ScanRepoMap(repo)
	r, err := RegressionGateScoped(ctx, repo, m, nil, func() error {
		return os.WriteFile(filepath.Join(repo, "a/a.go"),
			[]byte("package a\n\nfunc A() int { return 9 }\n"), 0o644)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Held {
		t.Fatalf("scoped gate must catch the in-scope regression: %+v", r)
	}
}
