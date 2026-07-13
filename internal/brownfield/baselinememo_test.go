package brownfield

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestBaselineMemoKeySoundness (or-u595): the key exists only for a CLEAN
// tree, differs by scope/skip, and disappears under ORION_BASELINE_MEMO=off.
func TestBaselineMemoKeySoundness(t *testing.T) {
	ctx := context.Background()
	repo := newScopeRepo(t)
	t.Setenv("ORION_BASELINE_MEMO", "")

	k1, ok := baselineMemoKey(ctx, repo, []string{"./a/..."}, nil)
	if !ok || k1 == "" {
		t.Fatal("a clean tree must yield a key")
	}
	k2, _ := baselineMemoKey(ctx, repo, []string{"./b/..."}, nil)
	if k1 == k2 {
		t.Fatal("different scopes must key differently")
	}
	k3, _ := baselineMemoKey(ctx, repo, []string{"./a/..."}, []string{"TestX"})
	if k1 == k3 {
		t.Fatal("different skip sets must key differently")
	}

	// Dirty tree → no key (never memoize what isn't the HEAD tree).
	if err := os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := baselineMemoKey(ctx, repo, []string{"./a/..."}, nil); ok {
		t.Fatal("a dirty tree must never yield a memo key")
	}
	_ = os.Remove(filepath.Join(repo, "dirty.txt"))

	t.Setenv("ORION_BASELINE_MEMO", "off")
	if _, ok := baselineMemoKey(ctx, repo, []string{"./a/..."}, nil); ok {
		t.Fatal("off must disable the memo")
	}
}

// TestBaselineMemoGreenOnlyAndTTL (or-u595): green round-trips; red is never
// stored; expired entries miss.
func TestBaselineMemoGreenOnlyAndTTL(t *testing.T) {
	ctx := context.Background()
	repo := newScopeRepo(t)
	t.Setenv("ORION_BASELINE_MEMO", "")
	key, ok := baselineMemoKey(ctx, repo, []string{"./a/..."}, nil)
	if !ok {
		t.Fatal("key")
	}

	saveBaselineMemo(ctx, repo, key, TestResult{Passed: false, Toolchain: "go"})
	if _, hit := loadBaselineMemo(ctx, repo, key); hit {
		t.Fatal("RED baselines must never be cached (environmental false-reds)")
	}
	saveBaselineMemo(ctx, repo, key, TestResult{Passed: true, Toolchain: "go", Command: "go test ./a/..."})
	e, hit := loadBaselineMemo(ctx, repo, key)
	if !hit || e.Command != "go test ./a/..." {
		t.Fatalf("a green baseline must round-trip: %+v hit=%v", e, hit)
	}
	if got := cachedBaselineResult(e); !got.Passed || !strings.Contains(got.Output, "baseline: cached green from a prior run at "+e.At) {
		t.Fatalf("the audit stamp must name the source run time: %q", got.Output)
	}

	// Expired entry misses.
	stale := e
	stale.At = time.Now().Add(-2 * baselineMemoTTL).UTC().Format(time.RFC3339)
	saveGreen := func(en baselineMemoEntry) {
		path, _ := baselineMemoPath(ctx, repo)
		raw, _ := os.ReadFile(path)
		_ = raw
		_ = os.WriteFile(path, []byte(`{"`+key+`":{"key":"`+key+`","at":"`+en.At+`","toolchain":"go","command":"go test"}}`), 0o600)
	}
	saveGreen(stale)
	if _, hit := loadBaselineMemo(ctx, repo, key); hit {
		t.Fatal("an expired entry must miss")
	}
}

// TestScopedGateReusesCachedBaseline (or-u595 e2e): the second run of the
// same scoped gate on the same tree reuses the cached green baseline and the
// evidence carries the audit stamp.
func TestScopedGateReusesCachedBaseline(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test")
	}
	t.Setenv("ORION_BASELINE_MEMO", "")
	repo := newScopeRepo(t)
	m := ScanRepoMap(repo)
	apply := func() error {
		return os.WriteFile(filepath.Join(repo, "a", "a.go"), []byte("package a\n\nfunc A() int { return 1 }\n\nfunc A2() int { return 2 }\n"), 0o644)
	}
	revert := func() {
		out, _ := gitOutput(context.Background(), repo, "checkout", "--", ".")
		_ = out
		_, _ = gitOutput(context.Background(), repo, "clean", "-fd")
	}

	r1, err := RegressionGateScoped(context.Background(), repo, m, nil, apply, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !r1.Held {
		t.Fatalf("first run must hold: %+v", r1)
	}
	if strings.Contains(r1.Before.Output, "cached green") {
		t.Fatal("the FIRST baseline must be fresh")
	}
	revert()

	r2, err := RegressionGateScoped(context.Background(), repo, m, nil, apply, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !r2.Held {
		t.Fatalf("second run must hold: %+v", r2)
	}
	if !strings.Contains(r2.Before.Output, "baseline: cached green from a prior run") {
		t.Fatalf("the second baseline must be the stamped cache hit, got: %q", r2.Before.Output)
	}
}
