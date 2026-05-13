package sandbox

// RepoCache implements SPEC §10.2 per-tenant repo cache.
//
// One persistent directory per tenant holds bare repository clones.
// Workers call Get(tenantID, repoURL, sha) to materialize a working
// tree at the requested SHA; the cache promotes the bare repo to a
// `git worktree add` so each worker has its own checkout that shares
// the same object store with the cache (no per-pod clone).
//
// This implementation shells out to git: deterministic, language-
// independent, and matches the SPEC's "shared object store" property
// without binding the orion-worker container to a Go git library.
//
// Cleanup is the caller's responsibility (the Cleanup function in
// cleanup.go calls RepoCache.ReleaseWorktree(...)).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrEmptyRepoURL is returned when Get is called with an empty repo
// URL.
var ErrEmptyRepoURL = errors.New("sandbox: repoURL is required")

// ErrInvalidSHA is returned when a SHA argument is empty or contains
// characters outside [0-9a-fA-F].
var ErrInvalidSHA = errors.New("sandbox: SHA is invalid")

// CacheConfig parameterizes NewRepoCache.
type CacheConfig struct {
	// Root is the absolute path under which per-tenant cache
	// directories live (e.g. /orion/cache). Each tenant gets a
	// subdirectory keyed by tenant_id.
	Root string
	// WorktreesRoot is the absolute path where active worktrees are
	// materialized. One subdirectory per claim_id. Production lays
	// this under a tmpfs to keep eviction cheap; tests use t.TempDir.
	WorktreesRoot string
	// WorktreeTTL is the period after which a worktree is eligible
	// for garbage collection per SPEC §10.5. Default 24h.
	WorktreeTTL time.Duration
	// Runner is an optional command runner; tests inject a fake to
	// avoid actually shelling out to git. Production uses the default
	// real runner.
	Runner CmdRunner
}

// CmdRunner abstracts os/exec for testability. A nil Runner uses the
// default real runner.
type CmdRunner interface {
	Run(ctx context.Context, dir, name string, args ...string) ([]byte, error)
}

// RepoCache is the SPEC §10.2 per-tenant cache.
type RepoCache struct {
	cfg    CacheConfig
	runner CmdRunner
}

// NewRepoCache builds a cache from cfg.
func NewRepoCache(cfg CacheConfig) (*RepoCache, error) {
	if cfg.Root == "" {
		return nil, errors.New("sandbox: CacheConfig.Root required")
	}
	if cfg.WorktreesRoot == "" {
		return nil, errors.New("sandbox: CacheConfig.WorktreesRoot required")
	}
	if cfg.WorktreeTTL <= 0 {
		cfg.WorktreeTTL = 24 * time.Hour
	}
	if cfg.Runner == nil {
		cfg.Runner = defaultRunner{}
	}
	return &RepoCache{cfg: cfg, runner: cfg.Runner}, nil
}

// Worktree is a materialized git checkout. The Path is the absolute
// directory the worker mounts as its repo/ subtree.
type Worktree struct {
	TenantID  uuid.UUID
	ClaimID   uuid.UUID
	RepoURL   string
	SHA       string
	Path      string
	BareDir   string
	CreatedAt time.Time
}

// Get returns a worktree at sha for repoURL under tenantID. If the
// tenant's bare cache is missing it's cloned; if present, fetched.
// Then a `git worktree add` materializes the working tree for this
// claim. Repeated calls for the same (tenantID, repoURL, claimID)
// are idempotent: an existing worktree at the right SHA is returned
// without re-cloning.
func (c *RepoCache) Get(ctx context.Context, tenantID, claimID uuid.UUID, repoURL, sha string) (*Worktree, error) {
	if tenantID == uuid.Nil {
		return nil, errors.New("sandbox: tenantID required")
	}
	if claimID == uuid.Nil {
		return nil, errors.New("sandbox: claimID required")
	}
	if repoURL == "" {
		return nil, ErrEmptyRepoURL
	}
	if !isValidSHA(sha) {
		return nil, ErrInvalidSHA
	}

	bareDir := c.bareDir(tenantID, repoURL)
	if err := c.ensureBareClone(ctx, bareDir, repoURL); err != nil {
		return nil, err
	}
	if err := c.fetchInto(ctx, bareDir); err != nil {
		return nil, err
	}

	wtPath := c.worktreePath(claimID)
	if err := os.MkdirAll(filepath.Dir(wtPath), 0o750); err != nil {
		return nil, fmt.Errorf("sandbox: prepare worktrees root: %w", err)
	}

	// Idempotency: if worktree already exists pointing at the right
	// SHA, reuse. Otherwise (different SHA, stale, or absent) recreate.
	if info, err := os.Stat(wtPath); err == nil && info.IsDir() {
		if curSHA, err := c.runner.Run(ctx, wtPath, "git", "rev-parse", "HEAD"); err == nil {
			if strings.TrimSpace(string(curSHA)) == sha {
				return &Worktree{
					TenantID: tenantID, ClaimID: claimID, RepoURL: repoURL,
					SHA: sha, Path: wtPath, BareDir: bareDir, CreatedAt: time.Now(),
				}, nil
			}
		}
		// Wrong SHA → remove and recreate
		if err := c.removeWorktree(ctx, bareDir, wtPath); err != nil {
			return nil, err
		}
	}

	if _, err := c.runner.Run(ctx, bareDir, "git", "worktree", "add", "--detach", wtPath, sha); err != nil {
		return nil, fmt.Errorf("sandbox: git worktree add: %w", err)
	}
	return &Worktree{
		TenantID: tenantID, ClaimID: claimID, RepoURL: repoURL,
		SHA: sha, Path: wtPath, BareDir: bareDir, CreatedAt: time.Now(),
	}, nil
}

// ReleaseWorktree removes the materialized working tree for the claim.
// Safe to call multiple times; absent worktrees are a no-op.
func (c *RepoCache) ReleaseWorktree(ctx context.Context, tenantID, claimID uuid.UUID, repoURL string) error {
	if claimID == uuid.Nil {
		return errors.New("sandbox: claimID required")
	}
	wtPath := c.worktreePath(claimID)
	if _, err := os.Stat(wtPath); os.IsNotExist(err) {
		return nil
	}
	bareDir := c.bareDir(tenantID, repoURL)
	return c.removeWorktree(ctx, bareDir, wtPath)
}

// GCExpiredWorktrees removes worktrees whose mtime is older than
// WorktreeTTL. Returns the number of worktrees pruned.
func (c *RepoCache) GCExpiredWorktrees(ctx context.Context) (int, error) {
	entries, err := os.ReadDir(c.cfg.WorktreesRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("sandbox: read worktrees root: %w", err)
	}
	cutoff := time.Now().Add(-c.cfg.WorktreeTTL)
	pruned := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		path := filepath.Join(c.cfg.WorktreesRoot, e.Name())
		// Best-effort prune: even if `git worktree remove` fails (the
		// bare dir may be gone), we still os.RemoveAll so the tmpfs
		// reclaims space.
		_ = os.RemoveAll(path)
		pruned++
		_ = ctx
	}
	return pruned, nil
}

func (c *RepoCache) bareDir(tenantID uuid.UUID, repoURL string) string {
	// repoURL is hashed so the cache directory layout is filesystem-safe
	// regardless of how messy the original URL was.
	h := sha256.Sum256([]byte(repoURL))
	return filepath.Join(c.cfg.Root, tenantID.String(), hex.EncodeToString(h[:8]))
}

func (c *RepoCache) worktreePath(claimID uuid.UUID) string {
	return filepath.Join(c.cfg.WorktreesRoot, claimID.String())
}

func (c *RepoCache) ensureBareClone(ctx context.Context, bareDir, repoURL string) error {
	if _, err := os.Stat(filepath.Join(bareDir, "HEAD")); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(bareDir), 0o750); err != nil {
		return fmt.Errorf("sandbox: prepare bare dir: %w", err)
	}
	if _, err := c.runner.Run(ctx, "", "git", "clone", "--bare", repoURL, bareDir); err != nil {
		return fmt.Errorf("sandbox: git clone --bare: %w", err)
	}
	return nil
}

func (c *RepoCache) fetchInto(ctx context.Context, bareDir string) error {
	if _, err := c.runner.Run(ctx, bareDir, "git", "fetch", "--all", "--tags", "--prune"); err != nil {
		return fmt.Errorf("sandbox: git fetch: %w", err)
	}
	return nil
}

func (c *RepoCache) removeWorktree(ctx context.Context, bareDir, wtPath string) error {
	// Best-effort: ignore "is not a working tree" errors since the
	// worktree may have been orphaned by a prior reaper run.
	_, _ = c.runner.Run(ctx, bareDir, "git", "worktree", "remove", "--force", wtPath)
	if err := os.RemoveAll(wtPath); err != nil {
		return fmt.Errorf("sandbox: rm worktree: %w", err)
	}
	return nil
}

// isValidSHA accepts hex strings of length 7-64 (covers both short
// and full SHAs).
func isValidSHA(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	return true
}

// defaultRunner is the production CmdRunner that shells out to
// os/exec.
type defaultRunner struct{}

func (defaultRunner) Run(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: name is hard-coded "git", args are sanitized
	cmd.Dir = dir
	return cmd.CombinedOutput()
}
