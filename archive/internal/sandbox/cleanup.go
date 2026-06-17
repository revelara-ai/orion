package sandbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// Cleaner orchestrates the per-run sandbox teardown. Per SPEC §10.5
// the cleanup hooks fire at each worker phase boundary; on run
// termination the namespace is deleted entirely.
//
// The implementation is intentionally thin: it stitches the
// NamespaceProvisioner + NetworkPolicyApplier + RepoCache together.
// All policy decisions (when to cleanup, what TTL to use) live in the
// caller — typically the Conductor's reaper.
type Cleaner struct {
	Namespaces      NamespaceProvisioner
	NetworkPolicies NetworkPolicyApplier
	Cache           *RepoCache
}

// NewCleaner builds a Cleaner. nil components are tolerated; the
// corresponding step is skipped (useful for tests that only care
// about one surface).
func NewCleaner(ns NamespaceProvisioner, np NetworkPolicyApplier, cache *RepoCache) *Cleaner {
	return &Cleaner{Namespaces: ns, NetworkPolicies: np, Cache: cache}
}

// CleanupRun tears down a run's sandbox: removes the NetworkPolicy,
// deletes the namespace, and releases worktrees for the named claims.
// Errors are collected and returned as a single joined error so the
// caller sees every failure even when one step succeeded.
func (c *Cleaner) CleanupRun(ctx context.Context, runID, tenantID uuid.UUID, repoURL string, claimIDs []uuid.UUID) error {
	var errs []error
	ns := RunNamespaceName(runID)

	if c.NetworkPolicies != nil {
		if err := c.NetworkPolicies.Remove(ctx, ns); err != nil {
			errs = append(errs, fmt.Errorf("network policy: %w", err))
		}
	}
	if c.Namespaces != nil {
		if err := c.Namespaces.Delete(ctx, ns); err != nil {
			errs = append(errs, fmt.Errorf("namespace: %w", err))
		}
	}
	if c.Cache != nil {
		for _, claimID := range claimIDs {
			if err := c.Cache.ReleaseWorktree(ctx, tenantID, claimID, repoURL); err != nil {
				errs = append(errs, fmt.Errorf("worktree %s: %w", claimID, err))
			}
		}
	}
	return errors.Join(errs...)
}

// GCExpiredWorktrees delegates to RepoCache. Returns 0 if Cache is nil.
func (c *Cleaner) GCExpiredWorktrees(ctx context.Context) (int, error) {
	if c.Cache == nil {
		return 0, nil
	}
	return c.Cache.GCExpiredWorktrees(ctx)
}
