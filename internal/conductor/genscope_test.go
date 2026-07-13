package conductor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/decomposer"
)

// TestGenScopeWriteToolRefusesOutOfLease (or-tcs.11a): with enforcement on, a
// native write outside the lease gets a corrective error NAMING the lease;
// in-lease writes and go.mod always pass; observe mode never blocks.
func TestGenScopeWriteToolRefusesOutOfLease(t *testing.T) {
	t.Setenv("ORION_SCOPE_LEASE", "enforce")
	dir := t.TempDir()
	wt := leaseGuarded(writeFileTool(dir), []string{"pkga"})

	_, err := wt.Run(context.Background(), json.RawMessage(`{"path":"other/evil.go","content":"package other\n"}`))
	if err == nil || !strings.Contains(err.Error(), "pkga") || !strings.Contains(err.Error(), "file scope") {
		t.Fatalf("an out-of-lease write must be refused with the lease named: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "other", "evil.go")); err == nil {
		t.Fatal("the refused write must not land on disk")
	}
	if _, err := wt.Run(context.Background(), json.RawMessage(`{"path":"pkga/ok.go","content":"package pkga\n"}`)); err != nil {
		t.Fatalf("an in-lease write must pass: %v", err)
	}
	if _, err := wt.Run(context.Background(), json.RawMessage(`{"path":"go.mod","content":"module m\n"}`)); err != nil {
		t.Fatalf("go.mod is always allowed: %v", err)
	}

	// edit_file shares the guard.
	et := leaseGuarded(editFileTool(dir), []string{"pkga"})
	if _, err := et.Run(context.Background(), json.RawMessage(`{"path":"other/evil.go","old_string":"a","new_string":"b"}`)); err == nil || !strings.Contains(err.Error(), "pkga") {
		t.Fatalf("edit_file must share the lease guard: %v", err)
	}

	// Observe mode (default): the guard is inert.
	t.Setenv("ORION_SCOPE_LEASE", "")
	wo := leaseGuarded(writeFileTool(dir), []string{"pkga"})
	if _, err := wo.Run(context.Background(), json.RawMessage(`{"path":"other/fine.go","content":"package other\n"}`)); err != nil {
		t.Fatalf("observe mode must not block: %v", err)
	}
}

// TestGenScopeUndeclaredKeepsWholeWorktree (or-tcs.11c): an undeclared lease
// preserves current behavior even under enforcement.
func TestGenScopeUndeclaredKeepsWholeWorktree(t *testing.T) {
	t.Setenv("ORION_SCOPE_LEASE", "enforce")
	dir := t.TempDir()
	wt := leaseGuarded(writeFileTool(dir), nil)
	if _, err := wt.Run(context.Background(), json.RawMessage(`{"path":"anywhere/x.go","content":"package anywhere\n"}`)); err != nil {
		t.Fatalf("undeclared lease must keep whole-worktree behavior: %v", err)
	}
	if outOfLease(nil, []string{"anywhere/x.go"}) != nil {
		t.Fatal("the post-generate gate must pass everything when no lease is declared")
	}
}

// TestGenScopeOutOfLeaseDetection: the deterministic diff-vs-lease helpers —
// observed writes parsed from git status, violations named exactly.
func TestGenScopeOutOfLeaseDetection(t *testing.T) {
	ctx := context.Background()
	repo := initManagedRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "pkga"), 0o755); err != nil {
		t.Fatal(err)
	}
	dogWrite(t, filepath.Join(repo, "pkga", "in.go"), "package pkga\n")
	dogWrite(t, filepath.Join(repo, "rogue.go"), "package main\n")

	obs := observedWrites(ctx, repo)
	joined := strings.Join(obs, ";")
	for _, want := range []string{"pkga/in.go", "rogue.go"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("observed writes must include %s: %v", want, obs)
		}
	}
	bad := outOfLease([]string{"pkga"}, obs)
	if len(bad) != 1 || bad[0] != "rogue.go" {
		t.Fatalf("exactly the out-of-lease path must be named, got %v", bad)
	}
}

// TestGenScopeObservedPreferredForIntegration (or-tcs.11.4): a cluster whose
// members ALL recorded observed scopes leases what was actually written; any
// unrecorded member falls back to the declaration.
func TestGenScopeObservedPreferredForIntegration(t *testing.T) {
	ctx := context.Background()
	store, pid := surfaceStore(t)
	if err := store.SaveStringListKind(ctx, pid, contextstore.ObservedScopeKind+"t1", []string{"pkga/in.go", "go.mod"}); err != nil {
		t.Fatal(err)
	}

	resolver := observedScopeFor(ctx, store, pid)
	cl := decomposer.TaskCluster{Key: "clA", Members: []string{"t1"}}
	if got := resolver(cl); len(got) != 2 || got[0] != "pkga/in.go" {
		t.Fatalf("recorded observed scope must drive the integration lease, got %v", got)
	}
	mixed := decomposer.TaskCluster{Key: "clB", Members: []string{"t1", "t2-unrecorded"}}
	if got := resolver(mixed); got != nil {
		t.Fatalf("any unrecorded member must fall back to the declaration, got %v", got)
	}
}
