# Git Handling Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Orion build in an Orion-*managed* git repo (init for greenfield, clone for brownfield) off which all agent worktrees branch, instead of worktrees of the developer's current-directory repo — and close the worktree-lifecycle SPEC gaps.

**Architecture:** A new ~80-line `internal/repo` package resolves the managed repo at `<store.Dir()>/repo` and feeds the existing `internal/worktree.Manager` chokepoint. `worktree.Manager` gains preserve-work-before-delete, a `Status.Behind` field, and an injectable liveness seam for stale-worktree reclaim. `internal/conductor.BuildDAG` swaps its `GitRoot(cwd)→fail` block for `repo.Resolve`, so greenfield never fails and never touches the developer's tree.

**Tech Stack:** Go 1.26.1, `git` plumbing via `os/exec`, `internal/contextstore` for the project data dir, standard `testing` package.

## Global Constraints

- **Go toolchain:** `go build ./...`, `go vet ./...`, `go test ./...` — there is **no Makefile**; use the `go` commands directly.
- **Git plumbing:** every `git` exec skips the LFS smudge filter via env `GIT_LFS_SKIP_SMUDGE=1` (matches `internal/worktree` and `internal/conductor/gitdeliver.go`).
- **Managed-repo commit identity** (so a fresh/unconfigured repo can commit): `-c user.name=Orion -c user.email=orion@revelara.ai -c commit.gpgsign=false`.
- **Integration base branch:** greenfield managed repos are created on `main`; that is the worktree start point.
- **Test discipline:** deterministic `git`/filesystem assertions only (no LLM judgment). Full build-pipeline tests (`BuildDAG` end-to-end) MUST `t.Skip` under `testing.Short()`.
- **Single chokepoint:** no package outside `internal/worktree` shells out to `git worktree …`; no package outside `internal/repo` (this cycle) gains new repo init/clone logic.
- **YAGNI:** `Repo` carries only `Path` and `Base` (no speculative `ProjectID` — no consumer). `Status` drops the SPEC's "unpushed" field (managed repo has no remote).

---

## File Structure

- **Create** `internal/repo/repo.go` — `Resolve(ctx, store, Intake) (Repo, error)`; greenfield init + brownfield clone; git plumbing helpers. Single responsibility: "where is the project's repo and what is its base".
- **Create** `internal/repo/repo_test.go` — greenfield + brownfield resolution tests.
- **Modify** `internal/worktree/worktree.go` — add `Status.Behind`; preserve-work in `Remove`; liveness seam (`alive` field + `WithLivenessCheck`) + stale reclaim in `Reconcile`.
- **Modify** `internal/worktree/worktree_test.go` — add the three new lifecycle tests.
- **Modify** `internal/conductor/build.go:136-144` — replace `GitRoot(cwd)→fail` with `repo.Resolve`; pass `managed.Base` to `clusterWorktreeSet`.
- **Modify** `internal/conductor/build_test.go` — add `TestBuildDAGResolvesManagedRepoNotCwd`.

**Task dependency order:** Task 1 → Task 2 (same package); Tasks 3, 4, 5 are independent of each other and of 1/2 (worktree-only); Task 6 depends on Task 1 (imports `repo`). A reviewer can accept/reject each independently.

---

### Task 1: `internal/repo` — greenfield managed-repo resolution

**Files:**
- Create: `internal/repo/repo.go`
- Test: `internal/repo/repo_test.go`

**Interfaces:**
- Consumes: `contextstore.Store.Dir() string` (the project data dir).
- Produces:
  - `type Intake struct { Brownfield bool; Source string }`
  - `type Repo struct { Path string; Base string }`
  - `func Resolve(ctx context.Context, store *contextstore.Store, intake Intake) (Repo, error)` — greenfield only in this task; idempotent.

- [ ] **Step 1: Write the failing test**

Create `internal/repo/repo_test.go`:

```go
package repo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
)

func mustStore(t *testing.T) *contextstore.Store {
	t.Helper()
	s, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestResolveInitsManagedRepoForGreenfield(t *testing.T) {
	store := mustStore(t)
	ctx := context.Background()

	r, err := Resolve(ctx, store, Intake{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	wantPath := filepath.Join(store.Dir(), "repo")
	if r.Path != wantPath {
		t.Fatalf("Path = %q, want %q", r.Path, wantPath)
	}
	if r.Base != "main" {
		t.Fatalf("Base = %q, want main", r.Base)
	}
	// It is a real repo on main with one commit.
	if br := gitOut(t, r.Path, "rev-parse", "--abbrev-ref", "HEAD"); br != "main" {
		t.Fatalf("HEAD branch = %q, want main", br)
	}
	count := gitOut(t, r.Path, "rev-list", "--count", "HEAD")
	if count != "1" {
		t.Fatalf("commit count = %q, want 1 (the init commit)", count)
	}

	// Idempotent: re-resolve reuses the repo, adds no second commit.
	r2, err := Resolve(ctx, store, Intake{})
	if err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	if r2.Path != r.Path {
		t.Fatalf("re-resolve Path = %q, want %q", r2.Path, r.Path)
	}
	if c := gitOut(t, r.Path, "rev-list", "--count", "HEAD"); c != "1" {
		t.Fatalf("commit count after re-resolve = %q, want still 1 (idempotent)", c)
	}
	_ = os.Stat
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/repo/ -run TestResolveInitsManagedRepoForGreenfield`
Expected: FAIL — build error, `undefined: Resolve` / `undefined: Intake` / `undefined: Repo`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/repo/repo.go`:

```go
// Package repo resolves the Orion-managed git repository a project's worktrees
// branch off. Greenfield: git init a fresh repo under the project data dir.
// Brownfield: clone the target. It is the single owner of "where is this project's
// repo and what is its integration base" — a deterministic, shell-verifiable
// primitive feeding internal/worktree.
//
// Manifesto: local-first; side-effect isolation (the managed repo, not the
// developer's working tree, is where agents build).
package repo

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// Intake distinguishes how the managed repo is seeded.
type Intake struct {
	Brownfield bool   // false = greenfield (init); true = clone Source
	Source     string // brownfield only: path/URL of the target repo to clone
}

// Repo is the resolved managed git repo a project's worktrees branch off.
type Repo struct {
	Path string // <store.Dir()>/repo
	Base string // integration base branch: "main" (greenfield) | default branch (brownfield)
}

// gitID is the Orion commit identity, so a fresh/unconfigured managed repo can
// commit without relying on the host git config.
var gitID = []string{
	"-c", "user.name=Orion",
	"-c", "user.email=orion@revelara.ai",
	"-c", "commit.gpgsign=false",
}

// git runs `git -C dir args...` (LFS smudge skipped), returning combined output.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_LFS_SKIP_SMUDGE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// isRepo reports whether dir is the working tree of a git repo.
func isRepo(ctx context.Context, dir string) bool {
	out, err := git(ctx, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// currentBranch returns the checked-out branch name (the integration base).
func currentBranch(ctx context.Context, dir string) (string, error) {
	out, err := git(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Resolve returns the project's managed repo, creating it if absent. Idempotent:
// an existing valid repo at the path is reused unchanged.
func Resolve(ctx context.Context, store *contextstore.Store, intake Intake) (Repo, error) {
	path := filepath.Join(store.Dir(), "repo")
	if isRepo(ctx, path) {
		base, err := currentBranch(ctx, path)
		if err != nil {
			return Repo{}, err
		}
		return Repo{Path: path, Base: base}, nil
	}
	return initGreenfield(ctx, path)
}

// initGreenfield creates a fresh managed repo on `main` with one empty initial
// commit, so worktrees have a real base branch to branch off.
func initGreenfield(ctx context.Context, path string) (Repo, error) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return Repo{}, fmt.Errorf("mkdir managed repo: %w", err)
	}
	if _, err := git(ctx, path, "init", "-b", "main"); err != nil {
		return Repo{}, err
	}
	if _, err := git(ctx, path, append(append([]string{}, gitID...), "commit", "--allow-empty", "-m", "orion: managed repo init")...); err != nil {
		return Repo{}, err
	}
	return Repo{Path: path, Base: "main"}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/repo/ -run TestResolveInitsManagedRepoForGreenfield -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/repo/repo.go internal/repo/repo_test.go
git commit -m "feat(repo): managed-repo greenfield Resolve"
```

---

### Task 2: `internal/repo` — brownfield clone

**Files:**
- Modify: `internal/repo/repo.go` (add the brownfield branch to `Resolve` + `cloneBrownfield`)
- Test: `internal/repo/repo_test.go` (add the clone test)

**Interfaces:**
- Consumes: `Resolve`, `Intake`, `Repo` from Task 1.
- Produces: `Resolve` now honors `Intake{Brownfield:true, Source:...}` by cloning; `Base` = the cloned default branch.

- [ ] **Step 1: Write the failing test**

Append to `internal/repo/repo_test.go`:

```go
// newSourceRepo creates a throwaway upstream repo on `trunk` with one commit,
// returning its path — the brownfield clone target.
func newSourceRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitOut(t, dir, "init", "-b", "trunk")
	gitOut(t, dir, "config", "user.email", "src@example.com")
	gitOut(t, dir, "config", "user.name", "Src")
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitOut(t, dir, "add", ".")
	gitOut(t, dir, "commit", "-m", "upstream")
	return dir
}

func TestResolveClonesBrownfieldTarget(t *testing.T) {
	store := mustStore(t)
	ctx := context.Background()
	src := newSourceRepo(t)

	r, err := Resolve(ctx, store, Intake{Brownfield: true, Source: src})
	if err != nil {
		t.Fatalf("resolve brownfield: %v", err)
	}
	if r.Path != filepath.Join(store.Dir(), "repo") {
		t.Fatalf("Path = %q", r.Path)
	}
	// Base is the cloned upstream default branch, not a hardcoded "main".
	if r.Base != "trunk" {
		t.Fatalf("Base = %q, want trunk (the cloned default branch)", r.Base)
	}
	// The upstream content came across.
	if _, err := os.Stat(filepath.Join(r.Path, "app.go")); err != nil {
		t.Fatalf("cloned content missing: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/repo/ -run TestResolveClonesBrownfieldTarget -v`
Expected: FAIL — `Base = "main", want trunk` (Task-1 `Resolve` greenfields the brownfield intake instead of cloning).

- [ ] **Step 3: Write minimal implementation**

In `internal/repo/repo.go`, change `Resolve` to dispatch on `intake.Brownfield`, and add `cloneBrownfield`. Replace the final `return initGreenfield(ctx, path)` line in `Resolve` with:

```go
	if intake.Brownfield {
		return cloneBrownfield(ctx, path, intake.Source)
	}
	return initGreenfield(ctx, path)
```

Add this function below `initGreenfield`:

```go
// cloneBrownfield clones the target repo into the managed path; Base is the
// cloned default branch.
func cloneBrownfield(ctx context.Context, path, source string) (Repo, error) {
	if strings.TrimSpace(source) == "" {
		return Repo{}, fmt.Errorf("brownfield intake requires a Source repo")
	}
	if _, err := git(ctx, ".", "clone", source, path); err != nil {
		return Repo{}, err
	}
	base, err := currentBranch(ctx, path)
	if err != nil {
		return Repo{}, err
	}
	return Repo{Path: path, Base: base}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/repo/ -v`
Expected: PASS (both greenfield + brownfield tests).

- [ ] **Step 5: Commit**

```bash
git add internal/repo/repo.go internal/repo/repo_test.go
git commit -m "feat(repo): brownfield clone in Resolve"
```

---

### Task 3: `worktree.Manager` — `Status.Behind`

**Files:**
- Modify: `internal/worktree/worktree.go` (the `Status` struct + the `Status` method)
- Test: `internal/worktree/worktree_test.go`

**Interfaces:**
- Consumes: existing `Manager.New`, `Manager.Create`, `Manager.Status`, the `run`/`newRepo`/`mustStore` test helpers.
- Produces: `Status` gains `Behind int` (commits on the base not yet in the branch).

- [ ] **Step 1: Write the failing test**

Append to `internal/worktree/worktree_test.go`:

```go
// TestStatusReportsBehind: after the base branch advances past a worktree's
// branch point, Status reports how many commits the worktree is behind.
func TestStatusReportsBehind(t *testing.T) {
	repo := newRepo(t)
	m := New(repo, mustStore(t))
	ctx := context.Background()

	if _, err := m.Create(ctx, "or-bhd", "main"); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Advance main beyond the worktree's branch point (shared refs).
	if err := os.WriteFile(filepath.Join(repo, "more.md"), []byte("more\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repo, "git", "add", ".")
	run(t, repo, "git", "commit", "-m", "advance main")

	st, err := m.Status("or-bhd")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if st.Behind < 1 {
		t.Fatalf("Behind = %d, want >= 1 (main advanced past the branch)", st.Behind)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/worktree/ -run TestStatusReportsBehind`
Expected: FAIL — build error, `st.Behind undefined (type Status has no field or method Behind)`.

- [ ] **Step 3: Write minimal implementation**

In `internal/worktree/worktree.go`, add the field to the `Status` struct (after `Ahead`):

```go
	Ahead       int  // commits on the branch not yet in the integration base (un-integrated)
	Behind      int  // commits on the integration base not yet in the branch (un-rebased)
	Clean       bool // no uncommitted changes and nothing un-integrated
```

Then in the `Status` method, after the `ahead` block and before the `return`, add the `behind` computation and include it in the returned struct:

```go
	behind := 0
	if out, err := runGit(path, "rev-list", "--count", "HEAD.."+m.base); err == nil {
		fmt.Sscanf(strings.TrimSpace(out), "%d", &behind)
	}
	return Status{Uncommitted: uncommitted, Ahead: ahead, Behind: behind, Clean: !uncommitted && ahead == 0}, nil
```

(Replace the existing `return Status{Uncommitted: uncommitted, Ahead: ahead, Clean: !uncommitted && ahead == 0}, nil` line.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/worktree/ -run TestStatusReportsBehind -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/worktree/worktree.go internal/worktree/worktree_test.go
git commit -m "feat(worktree): Status.Behind for rebase/integration decisions"
```

---

### Task 4: `worktree.Manager` — preserve work before forced delete

**Files:**
- Modify: `internal/worktree/worktree.go` (the `Remove` method)
- Test: `internal/worktree/worktree_test.go`

**Interfaces:**
- Consumes: existing `Manager.Remove`, `runGit`, `Manager.Status`.
- Produces: a `--force` removal of a dirty worktree first WIP-commits the dirty work onto the branch (recoverable) before deleting the tree.

- [ ] **Step 1: Write the failing test**

Append to `internal/worktree/worktree_test.go`:

```go
// TestRemovePreservesUncommittedWorkAsWipCommit: --force over a dirty worktree
// does not silently lose work — the dirty changes are snapshotted as a WIP commit
// on the branch before the tree is deleted.
func TestRemovePreservesUncommittedWorkAsWipCommit(t *testing.T) {
	repo := newRepo(t)
	m := New(repo, mustStore(t))
	ctx := context.Background()

	wt, err := m.Create(ctx, "or-wip", "main")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	before, _ := runGit(wt.Path, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(wt.Path, "scratch.txt"), []byte("precious\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := m.Remove(ctx, "or-wip", RemoveOpts{Force: true}); err != nil {
		t.Fatalf("forced remove: %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree dir should be gone after forced remove")
	}
	// The branch advanced with a WIP commit preserving the work.
	after, err := runGit(repo, "rev-parse", "or-wip")
	if err != nil {
		t.Fatalf("branch or-wip should still exist with preserved work: %v", err)
	}
	if strings.TrimSpace(after) == strings.TrimSpace(before) {
		t.Fatal("expected a WIP commit on the branch preserving the dirty work")
	}
	out, err := runGit(repo, "show", "--stat", "or-wip")
	if err != nil || !strings.Contains(out, "scratch.txt") {
		t.Fatalf("WIP commit should contain scratch.txt; show=%q err=%v", out, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/worktree/ -run TestRemovePreservesUncommittedWorkAsWipCommit`
Expected: FAIL — `branch or-wip should still exist with preserved work` (today `--force` deletes the tree and the dirty work is gone; the branch tip never advanced).

- [ ] **Step 3: Write minimal implementation**

In `internal/worktree/worktree.go`, in `Remove`, insert the preserve block immediately **after** the `// §6.5 — mark removing before touching the filesystem.` block (the `if m.store != nil { ... SetStatus(..., "removing") ... }`) and **before** the `// §6.7 — remove via git …` block:

```go
	// §6.6 — best-effort preserve work before destroying the tree: under --force a
	// dirty worktree's uncommitted changes are snapshotted as a WIP commit on the
	// branch (recoverable via the managed repo's reflog), so --force never silently
	// loses work. Best-effort — failures must not block removal (crash-safety wins).
	if opts.Force {
		if st, _ := m.Status(issueID); st.Uncommitted {
			_, _ = runGit(path, "add", "-A")
			_, _ = runGit(path,
				"-c", "user.name=Orion", "-c", "user.email=orion@revelara.ai", "-c", "commit.gpgsign=false",
				"commit", "--no-verify", "-m", "orion: WIP snapshot before worktree removal")
		}
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/worktree/ -run TestRemovePreservesUncommittedWorkAsWipCommit -v`
Expected: PASS.

Then run the existing removal tests to confirm no regression (the clean-worktree and refuse paths are unaffected):

Run: `go test ./internal/worktree/ -run 'TestRemove' -v`
Expected: PASS (all `TestRemove*`).

- [ ] **Step 5: Commit**

```bash
git add internal/worktree/worktree.go internal/worktree/worktree_test.go
git commit -m "feat(worktree): preserve dirty work as a WIP commit before forced removal"
```

---

### Task 5: `worktree.Manager` — liveness reaping seam in `Reconcile`

**Files:**
- Modify: `internal/worktree/worktree.go` (the `Manager` struct, `New`, a new `WithLivenessCheck`, and `Reconcile`)
- Test: `internal/worktree/worktree_test.go`

**Interfaces:**
- Consumes: existing `Manager`, `Manager.Reconcile`, `Manager.Remove`, `Manager.List`.
- Produces: `func (m *Manager) WithLivenessCheck(f func(issueID string) bool) *Manager`. `Reconcile` now reclaims (preserve + remove) any live worktree whose owner the predicate reports not-alive. Default predicate: always alive (no reaping; backward-compatible).

- [ ] **Step 1: Write the failing test**

Append to `internal/worktree/worktree_test.go`:

```go
// TestReconcileReclaimsStaleViaLivenessHook: a worktree whose owning agent is no
// longer alive (per the injected predicate) is reclaimed by Reconcile; a live one
// survives.
func TestReconcileReclaimsStaleViaLivenessHook(t *testing.T) {
	repo := newRepo(t)
	m := New(repo, mustStore(t)).WithLivenessCheck(func(id string) bool {
		return id != "or-dead" // or-dead's owner is gone
	})
	ctx := context.Background()

	live, err := m.Create(ctx, "or-live", "main")
	if err != nil {
		t.Fatalf("create live: %v", err)
	}
	dead, err := m.Create(ctx, "or-dead", "main")
	if err != nil {
		t.Fatalf("create dead: %v", err)
	}

	if err := m.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, err := os.Stat(dead.Path); !os.IsNotExist(err) {
		t.Fatalf("stale worktree should be reclaimed/removed")
	}
	if _, err := os.Stat(live.Path); err != nil {
		t.Fatalf("live worktree should survive reconcile: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/worktree/ -run TestReconcileReclaimsStaleViaLivenessHook`
Expected: FAIL — build error, `m.WithLivenessCheck undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/worktree/worktree.go`:

(a) Add the field to the `Manager` struct (after the `inIntegration` field):

```go
	// alive reports whether the agent owning a worktree is still running.
	// Injectable; the agentruntime wires heartbeat/PID liveness. Default: always
	// alive (no reaping).
	alive func(issueID string) bool
```

(b) In `New`, set the default in the returned struct literal (after `inIntegration: func(string) bool { return false },`):

```go
		alive:         func(string) bool { return true },
```

(c) Add the builder, next to `WithIntegrationCheck`:

```go
// WithLivenessCheck injects the agent-liveness predicate for §7 stale reclaim
// (default: always alive). The agentruntime wires heartbeat/PID liveness.
func (m *Manager) WithLivenessCheck(f func(string) bool) *Manager {
	if f != nil {
		m.alive = f
	}
	return m
}
```

(d) In `Reconcile`, insert the reclaim loop immediately **after** the "Reap incomplete/empty dirs" block (after its closing `_ = m.Prune()` and `}`) and **before** the `// Repair Context Store drift` block:

```go
	// §7 — reclaim STALE worktrees: a live worktree whose owning agent is no longer
	// running is preserved (WIP) and removed, freeing the slot for re-allocation.
	for _, wt := range live {
		if !m.alive(wt.IssueID) {
			_ = m.Remove(ctx, wt.IssueID, RemoveOpts{Force: true})
		}
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/worktree/ -run TestReconcileReclaimsStaleViaLivenessHook -v`
Expected: PASS.

Then the full worktree suite (confirms the existing `TestReconcilePrunesDeletedAndReapsStale` still passes with the default always-alive predicate):

Run: `go test ./internal/worktree/`
Expected: PASS (ok).

- [ ] **Step 5: Commit**

```bash
git add internal/worktree/worktree.go internal/worktree/worktree_test.go
git commit -m "feat(worktree): liveness seam for stale-worktree reclaim in Reconcile"
```

---

### Task 6: `conductor.BuildDAG` — build off the managed repo

**Files:**
- Modify: `internal/conductor/build.go:136-144`
- Test: `internal/conductor/build_test.go`

**Interfaces:**
- Consumes: `repo.Resolve(ctx, store, repo.Intake{}) (repo.Repo, error)` (Task 1); `worktree.New(path, store).WithBase(base)`; existing `clusterWorktreeSet(ctx, mgr, clusters, base)`.
- Produces: no new exported symbols — behavioral change: `BuildDAG` resolves Orion's managed repo instead of requiring a cwd git repo.

- [ ] **Step 1: Write the failing test**

Append to `internal/conductor/build_test.go`:

```go
// TestBuildDAGResolvesManagedRepoNotCwd: the build no longer depends on a cwd git
// repo — it resolves Orion's managed repo under the store dir. Run from a non-git
// cwd, the build succeeds and the managed repo exists.
func TestBuildDAGResolvesManagedRepoNotCwd(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a service + proof; skipped in -short")
	}
	oc, ctx := ratifiedTimeService(t) // ratifies (chdirs into a throwaway git repo)
	t.Chdir(t.TempDir())              // move to a NON-git cwd

	if _, err := BuildDAG(ctx, oc.Store(), nil, nil, nil, ""); err != nil {
		t.Fatalf("BuildDAG should resolve the managed repo from a non-git cwd: %v", err)
	}
	managedGit := filepath.Join(oc.Store().Dir(), "repo", ".git")
	if _, err := os.Stat(managedGit); err != nil {
		t.Fatalf("managed repo should exist at <store.Dir()>/repo: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/conductor/ -run TestBuildDAGResolvesManagedRepoNotCwd`
Expected: FAIL — `BuildDAG should resolve the managed repo from a non-git cwd: orion build requires a git repository …` (today, `GitRoot(".")` returns `""` in the non-git temp cwd and the build errors out).

- [ ] **Step 3: Write minimal implementation**

In `internal/conductor/build.go`, replace the block at lines 136–144 — i.e. the comment ending `… (foundation for parallel + integrated builds).`, the `projectRoot := GitRoot(ctx, ".")` resolution, its `if projectRoot == "" { return … }` guard, the `wtMgr := worktree.New(projectRoot, store)` line, and the `clusterWorktreeSet(…, "HEAD")` call — with:

```go
	// Build in Orion's MANAGED repo (<store.Dir()>/repo), not the developer's
	// working tree: greenfield inits it, brownfield clones the target. Nothing
	// scribbles outside it, and greenfield no longer fails for "not in a git repo".
	managed, rerr := repo.Resolve(ctx, store, repo.Intake{})
	if rerr != nil {
		return BuildResult{}, fmt.Errorf("resolve managed repo: %w", rerr)
	}
	wtMgr := worktree.New(managed.Path, store).WithBase(managed.Base)
	clusterWT, cleanupWT, werr := clusterWorktreeSet(ctx, wtMgr, clusters, managed.Base)
```

Add the import to the `import (...)` block in `build.go`:

```go
	"github.com/revelara-ai/orion/internal/repo"
```

(Leave `GitRoot` and the delivery block at `build.go:252` untouched — `GitRoot` is still used there.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/conductor/ -run TestBuildDAGResolvesManagedRepoNotCwd -v`
Expected: PASS.

Then the full conductor suite (confirms existing build tests still pass now that the build uses the managed repo rather than the cwd repo):

Run: `go test ./internal/conductor/`
Expected: PASS (ok).

- [ ] **Step 5: Commit**

```bash
git add internal/conductor/build.go internal/conductor/build_test.go
git commit -m "feat(conductor): BuildDAG builds off the Orion-managed repo, not cwd"
```

---

### Task 7: Wire-up + full gate

**Files:** none (verification only).

- [ ] **Step 1: Build + vet the whole module**

Run: `go build ./... && go vet ./...`
Expected: both exit 0.

- [ ] **Step 2: Wire-up check on the new exported surface**

Confirm the new exported symbols are actually called from the runtime (not orphans):

Run: `grep -rn "repo.Resolve\|repo.Intake\|WithLivenessCheck\|\.Behind" --include='*.go' internal/ cmd/ | grep -v '_test.go'`
Expected: at least one non-test caller each — `repo.Resolve`/`repo.Intake` in `internal/conductor/build.go`; `.Behind` is a struct field (consumers grow with integration — note it if zero non-test reads, it is a deferred-consumer field documented here); `WithLivenessCheck` is the agentruntime seam (deferred consumer — documented in the design §4.2, acceptable as a wired-later seam).

If `repo.Resolve` has **zero** non-test callers, Task 6 was not wired — fix before proceeding.

- [ ] **Step 3: Full test suite**

Run: `go test ./internal/... ./cmd/...`
Expected: PASS (ok). (The two full-pipeline tests run here; they are skipped only under `-short`.)

- [ ] **Step 4: Commit (if Step 2 surfaced a doc-only note)**

Only if you added a `bd note`/allowlist entry for the deferred-consumer seams:

```bash
git add -A
git commit -m "docs(worktree): note Behind/WithLivenessCheck as integration-cycle consumers"
```

Otherwise skip — nothing to commit.

---

## Self-Review

**1. Spec coverage** (design doc §4 components → tasks):
- §4.1 `internal/repo` greenfield + brownfield → Tasks 1, 2. ✓
- §4.2 `Status.Behind` → Task 3 ✓; preserve-work §6.6 → Task 4 ✓; §7 reaping seam → Task 5 ✓.
- §4.3 `BuildDAG` rewire (repo.Resolve, `clusterWorktreeSet` off `Base`) → Task 6 ✓; `GitDeliver` untouched → respected (not modified). ✓
- §4.4 agent↔worktree binding → already wired (`buildOneTask` → `wt.Path`); Task 6 keeps it uniform off the managed repo. ✓
- §7 tests: all six named tests map to Tasks 1–6. ✓
- Non-goals (integration, managed→dev delivery, doctor, branch_template) → correctly absent. ✓

**2. Placeholder scan:** No "TBD/TODO/handle edge cases/similar to Task N". Every code step shows complete code; every command shows expected output. ✓ (Task 7 Step 2's "deferred consumer" note is an explicit, documented wireup decision per design §4.2 — not a placeholder.)

**3. Type consistency:** `Intake{Brownfield bool, Source string}`, `Repo{Path string, Base string}`, `Resolve(ctx, *contextstore.Store, Intake) (Repo, error)` are identical across Tasks 1, 2, 6. `Status.Behind int` defined in Task 3, read in the Task 3 test only (field). `WithLivenessCheck(func(string) bool) *Manager` defined + used in Task 5. `clusterWorktreeSet(ctx, *worktree.Manager, []decomposer.TaskCluster, string)` matches its existing signature; Task 6 passes `managed.Base` (string). ✓
