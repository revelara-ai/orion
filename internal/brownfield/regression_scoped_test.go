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
	fr, err := RegressionGate(ctx, full, nil)
	if err != nil {
		t.Fatal(err)
	}
	if fr.Held {
		t.Fatalf("full gate must NOT hold (b is red): %+v", fr)
	}

	scoped := newScopeRepo(t)
	m := ScanRepoMap(scoped)
	sr, err := RegressionGateScoped(ctx, scoped, m, func() error {
		return os.WriteFile(filepath.Join(scoped, "a/a.go"),
			[]byte("package a\n\nfunc A() int { return 1 }\n\nfunc A2() int { return 2 }\n"), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sr.Held {
		t.Fatalf("scoped gate must hold (b is out of a's blast radius): %+v", sr)
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
	r, err := RegressionGateScoped(ctx, repo, m, func() error {
		return os.WriteFile(filepath.Join(repo, "a/a.go"),
			[]byte("package a\n\nfunc A() int { return 9 }\n"), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	if r.Held {
		t.Fatalf("scoped gate must catch the in-scope regression: %+v", r)
	}
}
