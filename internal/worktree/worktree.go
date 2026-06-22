// Package worktree is the deterministic git-plumbing primitive (or-yin.1, spec
// docs/SPEC/Orion-Worktree-Git.md). It is the SINGLE chokepoint for `git
// worktree …`: one worktree per task at `worktrees-<repo>/<issue-id>` on branch
// `<issue-id>`, created off the integration base with a shared object store (no
// re-clone). It owns the heavily safety-gated deletion path and
// filesystem-as-source-of-truth reconciliation. The sandbox mounts a worktree as
// the agent's only writable workdir.
//
// Manifesto: side-effect sandboxing, trust-domain isolation, robust to crashes.
package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/revelara-ai/orion/internal/contextstore"
)

// Worktree is one per-task working tree.
type Worktree struct {
	IssueID string
	Path    string
	Branch  string
}

// Status is the live cleanliness of a worktree.
type Status struct {
	Uncommitted bool // dirty working tree (git status --porcelain non-empty)
	Ahead       int  // commits on the branch not yet in the integration base (un-integrated)
	Behind      int  // commits on the integration base not yet in the branch (un-rebased)
	Clean       bool // no uncommitted changes and nothing un-integrated
}

// RemoveOpts controls deletion.
type RemoveOpts struct {
	Force bool
}

// Manager wraps git worktree operations for one main repo.
type Manager struct {
	repoDir string
	store   *contextstore.Store // optional; records the worktrees txn
	base    string              // integration base (default "main")

	// inIntegration reports whether a task is mid-integration (in the queue or
	// holding a lease). Injectable; the integration module (V2.1) wires it.
	// Default: never.
	inIntegration func(issueID string) bool

	// alive reports whether the agent owning a worktree is still running.
	// Injectable; the agentruntime wires heartbeat/PID liveness. Default: always
	// alive (no reaping).
	alive func(issueID string) bool

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// New returns a worktree Manager for the given main repo directory.
func New(repoDir string, store *contextstore.Store) *Manager {
	return &Manager{
		repoDir:       repoDir,
		store:         store,
		base:          "main",
		inIntegration: func(string) bool { return false },
		alive:         func(string) bool { return true },
		locks:         map[string]*sync.Mutex{},
	}
}

// WithBase sets the integration base start point (default "main").
func (m *Manager) WithBase(base string) *Manager { m.base = base; return m }

// WithIntegrationCheck injects the mid-integration predicate (deletion gate §6.3).
func (m *Manager) WithIntegrationCheck(f func(string) bool) *Manager {
	if f != nil {
		m.inIntegration = f
	}
	return m
}

// WithLivenessCheck injects the agent-liveness predicate for §7 stale reclaim
// (default: always alive). The agentruntime wires heartbeat/PID liveness.
func (m *Manager) WithLivenessCheck(f func(string) bool) *Manager {
	if f != nil {
		m.alive = f
	}
	return m
}

func (m *Manager) lockFor(issueID string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.locks[issueID]
	if !ok {
		l = &sync.Mutex{}
		m.locks[issueID] = l
	}
	return l
}

func (m *Manager) repoName() string { return filepath.Base(m.repoDir) }
func (m *Manager) worktreesDir() string {
	return filepath.Join(filepath.Dir(m.repoDir), "worktrees-"+m.repoName())
}

// PathFor returns the worktree path for an issue id.
func (m *Manager) PathFor(issueID string) string {
	return filepath.Join(m.worktreesDir(), issueID)
}

// git runs `git -C <repoDir> args…` with the LFS smudge filter skipped.
func (m *Manager) git(args ...string) (string, error) { return runGit(m.repoDir, args...) }

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_LFS_SKIP_SMUDGE=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// Create adds a worktree off startPoint (default the integration base) on a new
// branch named by the issue id, and records it transactionally.
func (m *Manager) Create(ctx context.Context, issueID, startPoint string) (Worktree, error) {
	l := m.lockFor(issueID)
	l.Lock()
	defer l.Unlock()

	if startPoint == "" {
		startPoint = m.base
	}
	path := m.PathFor(issueID)
	branch := issueID
	if err := os.MkdirAll(m.worktreesDir(), 0o755); err != nil {
		return Worktree{}, fmt.Errorf("mkdir worktrees dir: %w", err)
	}
	if _, err := m.git("worktree", "add", path, "-b", branch, startPoint); err != nil {
		return Worktree{}, err
	}
	wt := Worktree{IssueID: issueID, Path: path, Branch: branch}
	if err := m.record(ctx, wt, "active"); err != nil {
		return Worktree{}, err
	}
	return wt, nil
}

// CreateResume reattaches an existing branch's worktree (after a crash/restart).
func (m *Manager) CreateResume(ctx context.Context, issueID, branch string) (Worktree, error) {
	l := m.lockFor(issueID)
	l.Lock()
	defer l.Unlock()
	if branch == "" {
		branch = issueID
	}
	path := m.PathFor(issueID)
	if err := os.MkdirAll(m.worktreesDir(), 0o755); err != nil {
		return Worktree{}, fmt.Errorf("mkdir worktrees dir: %w", err)
	}
	if _, err := m.git("worktree", "add", "--force", path, branch); err != nil {
		return Worktree{}, err
	}
	wt := Worktree{IssueID: issueID, Path: path, Branch: branch}
	if err := m.record(ctx, wt, "active"); err != nil {
		return Worktree{}, err
	}
	return wt, nil
}

func (m *Manager) record(ctx context.Context, wt Worktree, status string) error {
	if m.store == nil {
		return nil
	}
	return m.store.WithTx(ctx, func(tx *contextstore.Tx) error {
		return tx.Worktrees().Upsert(ctx, wt.IssueID, wt.Path, wt.Branch, status)
	})
}

// List parses `git worktree list --porcelain` (excludes the main repo).
func (m *Manager) List() ([]Worktree, error) {
	out, err := m.git("worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var wts []Worktree
	var cur Worktree
	flush := func() {
		if cur.Path != "" && cur.Path != m.repoDir {
			cur.IssueID = filepath.Base(cur.Path)
			wts = append(wts, cur)
		}
		cur = Worktree{}
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case line == "":
			flush()
		}
	}
	flush()
	return wts, nil
}

// Status reports the live cleanliness of a worktree (dirty / un-integrated).
func (m *Manager) Status(issueID string) (Status, error) {
	path := m.PathFor(issueID)
	if _, err := os.Stat(path); err != nil {
		return Status{}, fmt.Errorf("worktree %s: %w", issueID, contextstore.ErrNotFound)
	}
	porcelain, err := runGit(path, "status", "--porcelain")
	if err != nil {
		return Status{}, err
	}
	uncommitted := strings.TrimSpace(porcelain) != ""

	ahead := 0
	if out, err := runGit(path, "rev-list", "--count", m.base+"..HEAD"); err == nil {
		fmt.Sscanf(strings.TrimSpace(out), "%d", &ahead)
	}
	behind := 0
	if out, err := runGit(path, "rev-list", "--count", "HEAD.."+m.base); err == nil {
		fmt.Sscanf(strings.TrimSpace(out), "%d", &behind)
	}
	return Status{Uncommitted: uncommitted, Ahead: ahead, Behind: behind, Clean: !uncommitted && ahead == 0}, nil
}

// Prune wraps `git worktree prune`.
func (m *Manager) Prune() error {
	_, err := m.git("worktree", "prune")
	return err
}

// Remove deletes a worktree through the safety gates (spec §6).
func (m *Manager) Remove(ctx context.Context, issueID string, opts RemoveOpts) error {
	l := m.lockFor(issueID)
	l.Lock()
	defer l.Unlock()

	path := m.PathFor(issueID)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("remove: unknown worktree %s", issueID)
	}

	// §6.3 — refuse mid-integration even under --force.
	if m.inIntegration(issueID) {
		return fmt.Errorf("remove refused: task %s is mid-integration", issueID)
	}

	// §6.4 — refuse self-removal (developer's cwd inside the worktree).
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(path, wd); err == nil && !strings.HasPrefix(rel, "..") && rel != "" {
			return fmt.Errorf("remove refused: current directory is inside worktree %s", issueID)
		}
	}

	// §6.2 — refuse unsafe (uncommitted / un-integrated) work unless forced.
	if !opts.Force {
		st, err := m.Status(issueID)
		if err != nil {
			return err
		}
		if st.Uncommitted || st.Ahead > 0 {
			return fmt.Errorf("remove refused: worktree %s has unsafe work (uncommitted=%v, un-integrated=%d); use --force", issueID, st.Uncommitted, st.Ahead)
		}
	}

	// §6.5 — mark removing before touching the filesystem.
	if m.store != nil {
		_ = m.store.WithTx(ctx, func(tx *contextstore.Tx) error {
			return tx.Worktrees().SetStatus(ctx, issueID, "removing")
		})
	}

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

	// §6.7 — remove via git, fall back to RemoveAll, then prune + verify.
	if _, err := m.git("worktree", "remove", path); err != nil {
		if _, err2 := m.git("worktree", "remove", "--force", path); err2 != nil {
			if err3 := os.RemoveAll(path); err3 != nil {
				return fmt.Errorf("remove worktree %s: %w", issueID, err3)
			}
		}
	}
	_ = m.Prune()
	if _, err := os.Stat(path); err == nil {
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove worktree %s: residual dir: %w", issueID, err)
		}
	}
	if m.store != nil {
		_ = m.store.WithTx(ctx, func(tx *contextstore.Tx) error {
			return tx.Worktrees().Delete(ctx, issueID)
		})
	}
	return nil
}

// Reconcile makes the filesystem the source of truth (spec §7): prune deleted
// worktrees, reap incomplete/empty dirs, and repair Context Store drift. Runs on
// startup and before allocation.
func (m *Manager) Reconcile(ctx context.Context) error {
	if err := m.Prune(); err != nil {
		return err
	}

	live, err := m.List()
	if err != nil {
		return err
	}
	liveByIssue := map[string]bool{}
	for _, wt := range live {
		liveByIssue[wt.IssueID] = true
	}

	// Reap incomplete/empty dirs under the worktrees dir (no valid .git → not a
	// real worktree git knows about).
	if entries, err := os.ReadDir(m.worktreesDir()); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			issueID := e.Name()
			dir := filepath.Join(m.worktreesDir(), issueID)
			if liveByIssue[issueID] {
				continue // a real, live worktree
			}
			// Orphan/incomplete/empty dir git doesn't track → reap.
			_ = os.RemoveAll(dir)
		}
		_ = m.Prune()
	}

	// §7 — reclaim STALE worktrees: a live worktree whose owning agent is no longer
	// running is preserved (WIP) and removed, freeing the slot for re-allocation.
	for _, wt := range live {
		if !m.alive(wt.IssueID) {
			_ = m.Remove(ctx, wt.IssueID, RemoveOpts{Force: true})
		}
	}

	// Repair Context Store drift: a recorded worktree with no live dir → mark gone.
	if m.store != nil {
		_ = m.store.WithTx(ctx, func(tx *contextstore.Tx) error {
			recs, err := tx.Worktrees().List(ctx)
			if err != nil {
				return err
			}
			for _, r := range recs {
				if _, statErr := os.Stat(r.Path); statErr != nil && r.Status != "gone" {
					_ = tx.Worktrees().SetStatus(ctx, r.IssueID, "gone")
				}
			}
			return nil
		})
	}
	return nil
}
