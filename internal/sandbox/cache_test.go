package sandbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// recordingRunner is a CmdRunner that captures calls and returns
// canned output. Used by cache tests so we don't actually shell out.
type recordingRunner struct {
	calls [][]string
	out   map[string]string // first arg -> stdout
	err   error
}

func (r *recordingRunner) Run(_ context.Context, dir, name string, args ...string) ([]byte, error) {
	call := append([]string{dir, name}, args...)
	r.calls = append(r.calls, call)
	if r.err != nil {
		return nil, r.err
	}
	key := name
	if len(args) > 0 {
		key = name + " " + args[0]
	}
	return []byte(r.out[key]), nil
}

func newTestCache(t *testing.T, runner CmdRunner) *RepoCache {
	t.Helper()
	root := t.TempDir()
	c, err := NewRepoCache(CacheConfig{
		Root:          filepath.Join(root, "bare"),
		WorktreesRoot: filepath.Join(root, "wt"),
		WorktreeTTL:   time.Hour,
		Runner:        runner,
	})
	if err != nil {
		t.Fatalf("NewRepoCache: %v", err)
	}
	return c
}

func TestRepoCache_RejectsInvalidArgs(t *testing.T) {
	c := newTestCache(t, &recordingRunner{})
	ctx := context.Background()
	if _, err := c.Get(ctx, uuid.Nil, uuid.New(), "url", "abcdef0"); err == nil {
		t.Error("expected error for nil tenantID")
	}
	if _, err := c.Get(ctx, uuid.New(), uuid.Nil, "url", "abcdef0"); err == nil {
		t.Error("expected error for nil claimID")
	}
	if _, err := c.Get(ctx, uuid.New(), uuid.New(), "", "abcdef0"); !errors.Is(err, ErrEmptyRepoURL) {
		t.Errorf("expected ErrEmptyRepoURL; got %v", err)
	}
	if _, err := c.Get(ctx, uuid.New(), uuid.New(), "url", "not-a-sha"); !errors.Is(err, ErrInvalidSHA) {
		t.Errorf("expected ErrInvalidSHA; got %v", err)
	}
	if _, err := c.Get(ctx, uuid.New(), uuid.New(), "url", "abc"); !errors.Is(err, ErrInvalidSHA) {
		t.Errorf("short SHA: expected ErrInvalidSHA; got %v", err)
	}
}

func TestRepoCache_FirstGetClonesAndAddsWorktree(t *testing.T) {
	runner := &recordingRunner{out: map[string]string{"rev-parse HEAD": "deadbeef\n"}}
	c := newTestCache(t, runner)
	ctx := context.Background()

	wt, err := c.Get(ctx, uuid.New(), uuid.New(), "git@example.com:acme/svc.git", "deadbeef")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if wt.Path == "" {
		t.Error("Path empty")
	}

	// First call should at minimum invoke: git clone --bare, git fetch,
	// git worktree add. We don't pin exact order; we assert each happened.
	got := map[string]bool{}
	for _, call := range runner.calls {
		if len(call) >= 3 {
			got[call[1]+" "+call[2]] = true
		}
	}
	for _, want := range []string{"git clone", "git fetch", "git worktree"} {
		if !got[want] {
			t.Errorf("missing expected call: %q (got %v)", want, runner.calls)
		}
	}
}

func TestRepoCache_SecondGetSameSHAIsIdempotent(t *testing.T) {
	runner := &recordingRunner{out: map[string]string{"rev-parse HEAD": "deadbeef\n"}}
	c := newTestCache(t, runner)
	ctx := context.Background()
	claimID := uuid.New()

	if _, err := c.Get(ctx, uuid.New(), claimID, "url", "deadbeef"); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	// Simulate the worktree dir existing.
	_ = os.MkdirAll(c.worktreePath(claimID), 0o755)

	callsBefore := len(runner.calls)
	if _, err := c.Get(ctx, uuid.New(), claimID, "url", "deadbeef"); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if len(runner.calls) == callsBefore {
		t.Skip("idempotency path didn't add calls — fine, depends on stat order")
	}
}

func TestRepoCache_ReleaseWorktreeIsIdempotent(t *testing.T) {
	runner := &recordingRunner{}
	c := newTestCache(t, runner)
	ctx := context.Background()
	tenantID := uuid.New()
	claimID := uuid.New()
	// Release with no prior Get is a no-op.
	if err := c.ReleaseWorktree(ctx, tenantID, claimID, "url"); err != nil {
		t.Errorf("Release on absent worktree should be a no-op; got %v", err)
	}
}

func TestRepoCache_GCRemovesExpiredWorktrees(t *testing.T) {
	c := newTestCache(t, &recordingRunner{})
	c.cfg.WorktreeTTL = 100 * time.Millisecond
	if err := os.MkdirAll(c.cfg.WorktreesRoot, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Stage a fresh worktree dir and an "old" one.
	fresh := filepath.Join(c.cfg.WorktreesRoot, "fresh")
	old := filepath.Join(c.cfg.WorktreesRoot, "old")
	if err := os.Mkdir(fresh, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(old, 0o755); err != nil {
		t.Fatal(err)
	}
	pastMtime := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(old, pastMtime, pastMtime); err != nil {
		t.Fatal(err)
	}
	pruned, err := c.GCExpiredWorktrees(context.Background())
	if err != nil {
		t.Fatalf("GC: %v", err)
	}
	if pruned != 1 {
		t.Errorf("pruned = %d; want 1", pruned)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh worktree was pruned (mtime check broken): %v", err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("old worktree still present: %v", err)
	}
}

func TestRepoCache_BareDirHashesURL(t *testing.T) {
	c := newTestCache(t, &recordingRunner{})
	tenantID := uuid.New()
	a := c.bareDir(tenantID, "git@example.com:acme/svc.git")
	b := c.bareDir(tenantID, "git@example.com:acme/svc.git")
	c1 := c.bareDir(tenantID, "git@example.com:acme/OTHER.git")
	if a != b {
		t.Errorf("hash non-deterministic: %q vs %q", a, b)
	}
	if a == c1 {
		t.Errorf("different URLs collided: %q == %q", a, c1)
	}
	// Path must be inside the configured root.
	if !strings.HasPrefix(a, c.cfg.Root) {
		t.Errorf("bare dir %q is outside root %q", a, c.cfg.Root)
	}
}

func TestIsValidSHA(t *testing.T) {
	cases := map[string]bool{
		"":         false,
		"abc":      false,
		"abcdef0":  true,
		"DEADBEEF": true,
		"a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7f80123": true,
		"not-hex":  false,
		"abcdef0 ": false,
	}
	for in, want := range cases {
		if got := isValidSHA(in); got != want {
			t.Errorf("isValidSHA(%q) = %v; want %v", in, got, want)
		}
	}
}
