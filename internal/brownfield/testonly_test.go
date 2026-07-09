package brownfield

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTestOnlyChange(t *testing.T) {
	cases := []struct {
		name  string
		paths []string
		want  bool
	}{
		{"all test files", []string{"pkg/llm/config/config_test.go", "pkg/llm/gemini_test.go"}, true},
		{"mixed prod and test", []string{"pkg/llm/gemini.go", "pkg/llm/gemini_test.go"}, false},
		{"prod only", []string{"pkg/llm/gemini.go"}, false},
		{"empty", nil, false},
		{"testdata fixture is not test-only", []string{"pkg/llm/testdata/fixture.json"}, false},
		{"suffix must be _test.go, not test.go", []string{"pkg/llm/contest.go"}, false},
		// A rename foo.go -> foo_test.go surfaces as BOTH paths once changedPaths
		// keeps both sides — the deleted prod file breaks the classification, as
		// it must (the package's non-test code changed).
		{"rename prod to test lists both sides", []string{"a/foo.go", "a/foo_test.go"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := testOnlyChange(c.paths); got != c.want {
				t.Fatalf("testOnlyChange(%v) = %v, want %v", c.paths, got, c.want)
			}
		})
	}
}

// TestChangedPathsKeepsBothSidesOfRename: a staged rename's porcelain line is
// "R  old -> new"; the scope decision needs BOTH sides — the source package
// loses a file (its non-test code changes) even though only the destination
// path "exists" afterward.
func TestChangedPathsKeepsBothSidesOfRename(t *testing.T) {
	repo := newScopeRepo(t)
	gitX(t, repo, "mv", "a/a.go", "a/renamed_test.go")
	paths := changedPaths(context.Background(), repo)
	joined := strings.Join(paths, "\n")
	if !strings.Contains(joined, "a/a.go") || !strings.Contains(joined, "a/renamed_test.go") {
		t.Fatalf("rename must list both sides, got %v", paths)
	}
	if testOnlyChange(paths) {
		t.Fatal("a prod->test rename must never classify as test-only")
	}
}

// newFastPathRepo builds a committed module with pkg a (green) and pkg c that
// IMPORTS a and carries an intentionally RED test. Blast-radius scope for a
// change in a includes c (gate fails on c's red test); the test-only fast path
// runs only a's own tests (gate holds) — the compiler guarantees c cannot be
// affected by a's _test.go files.
func newFastPathRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeRepoFile(t, dir, "go.mod", "module fastmod\n\ngo 1.21\n")
	writeRepoFile(t, dir, "a/a.go", "package a\n\nfunc A() int { return 1 }\n")
	writeRepoFile(t, dir, "a/a_test.go", `package a

import "testing"

func TestA(t *testing.T) {
	if A() != 1 {
		t.Fatal("A changed")
	}
}
`)
	writeRepoFile(t, dir, "c/c.go", "package c\n\nimport \"fastmod/a\"\n\nfunc C() int { return a.A() + 1 }\n")
	writeRepoFile(t, dir, "c/c_test.go", `package c

import "testing"

func TestC(t *testing.T) { t.Fatal("c intentionally red") }
`)
	gitX(t, dir, "init", "-b", "main")
	gitX(t, dir, "add", ".")
	gitX(t, dir, "commit", "-m", "init")
	return dir
}

// TestRegressionGateScopedTestOnlyFastPath is the discriminating pair proving
// the fast path both activates and stays sound:
//   - a test-only diff in a holds WITHOUT running dependent c's red suite, and
//     the scope stamp says why;
//   - the same repo with a prod-code diff in a takes the blast-radius path,
//     which pulls in c and correctly fails on its red baseline.
func TestRegressionGateScopedTestOnlyFastPath(t *testing.T) {
	t.Run("test-only diff skips dependents", func(t *testing.T) {
		repo := newFastPathRepo(t)
		m := ScanRepoMap(repo)
		r, err := RegressionGateScoped(context.Background(), repo, m, nil, func() error {
			return os.WriteFile(filepath.Join(repo, "a/extra_test.go"),
				[]byte("package a\n\nimport \"testing\"\n\nfunc TestAExtra(t *testing.T) {\n\tif A() != 1 {\n\t\tt.Fatal(\"A changed\")\n\t}\n}\n"), 0o644)
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !r.Held {
			t.Fatalf("test-only diff must hold without dependents: %+v", r)
		}
		if !strings.Contains(r.Scope, "test-only") {
			t.Fatalf("scope stamp must record the fast-path justification, got %q", r.Scope)
		}
		if strings.Contains(r.After.Command, "./c") {
			t.Fatalf("fast path must not run dependent c, ran: %s", r.After.Command)
		}
	})

	t.Run("prod diff takes blast path and fails on dependent", func(t *testing.T) {
		repo := newFastPathRepo(t)
		m := ScanRepoMap(repo)
		r, err := RegressionGateScoped(context.Background(), repo, m, nil, func() error {
			return os.WriteFile(filepath.Join(repo, "a/a.go"),
				[]byte("package a\n\n// touched\nfunc A() int { return 1 }\n"), 0o644)
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if r.Held {
			t.Fatalf("prod diff must gate on dependent c's red baseline: %+v", r)
		}
		if strings.Contains(r.Scope, "test-only") {
			t.Fatalf("prod diff must not fast-path, scope: %q", r.Scope)
		}
	})
}

// TestGateCommandCarriesTimeout: the gate's go test invocations carry the
// explicit -timeout raise (lever 5: a loaded machine must not convert a slow
// suite into a false-red; a longer timeout never weakens the proof).
func TestGateCommandCarriesTimeout(t *testing.T) {
	repo := newScopeRepo(t)
	m := ScanRepoMap(repo)
	r, err := RegressionGateScoped(context.Background(), repo, m, nil, func() error {
		return os.WriteFile(filepath.Join(repo, "a/a.go"),
			[]byte("package a\n\n// touched\nfunc A() int { return 1 }\n"), 0o644)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.After.Command, "-timeout") {
		t.Fatalf("gate command must carry -timeout, got: %s", r.After.Command)
	}
}

// TestRegressionResultScopeStamped: every scope decision leaves an audit
// stamp — proof strength includes knowing WHICH argument produced the verdict.
func TestRegressionResultScopeStamped(t *testing.T) {
	repo := newScopeRepo(t)
	m := ScanRepoMap(repo)

	t.Run("vacuous", func(t *testing.T) {
		r, err := RegressionGateScoped(context.Background(), repo, m, nil, func() error {
			return os.WriteFile(filepath.Join(repo, "Makefile"), []byte("x:\n\t@true\n"), 0o644)
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if r.Scope == "" {
			t.Fatal("vacuous hold must still stamp its scope")
		}
	})

	t.Run("forced full on go.mod", func(t *testing.T) {
		repo := newScopeRepo(t)
		r, err := RegressionGateScoped(context.Background(), repo, ScanRepoMap(repo), nil, func() error {
			return os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module testmod\n\ngo 1.22\n"), 0o644)
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(r.Scope, "full") {
			t.Fatalf("go.mod change must stamp forced-full scope, got %q", r.Scope)
		}
	})
}
