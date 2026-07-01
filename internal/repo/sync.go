package repo

import (
	"context"
	"fmt"
	"strings"
)

// SyncStatus is the outcome of reconciling the managed repo's base branch with its remote.
type SyncStatus string

// The SyncStatus values SyncMain reports.
const (
	SyncNoRepo   SyncStatus = "no-repo"   // the managed repo doesn't exist yet (first intent)
	SyncNoRemote SyncStatus = "no-remote" // local-first repo with no origin — nothing to sync
	SyncInSync   SyncStatus = "in-sync"   // base already matches origin
	SyncResynced SyncStatus = "resynced"  // base was strictly behind origin → fast-forwarded
	SyncDiverged SyncStatus = "diverged"  // base and origin have conflicting commits — the developer's to resolve
)

// SyncMain reconciles the managed repo's base branch with origin before a new intent (or-tcs.8, step
// 11): the developer may have merged the epic's PR into remote main, so the local base must catch up
// before the next intent builds off a stale head. It:
//   - ABSTAINS (SyncNoRepo / SyncNoRemote) when there is no managed repo yet or no origin remote
//     (the local-first default) — nothing to reconcile;
//   - FAST-FORWARDS (SyncResynced) when the local base is STRICTLY behind origin;
//   - reports SyncDiverged WITHOUT touching the tree when the base and origin have conflicting
//     commits (or the local base is ahead) — an FF-only resync, matching the land policy; divergence
//     is the developer's to resolve, not Orion's to auto-merge.
//
// A fetch failure (offline / unreachable remote) is NON-fatal: it returns SyncInSync so an offline
// developer is never blocked — only a genuine divergence blocks a new intent.
func SyncMain(ctx context.Context, repoDir string) (SyncStatus, error) {
	if !isRepo(ctx, repoDir) {
		return SyncNoRepo, nil
	}
	base, err := currentBranch(ctx, repoDir)
	if err != nil {
		return "", err
	}
	if _, err := git(ctx, repoDir, "remote", "get-url", "origin"); err != nil {
		return SyncNoRemote, nil
	}
	if _, err := git(ctx, repoDir, "fetch", "--quiet", "origin", base); err != nil {
		return SyncInSync, nil // can't reach the remote → don't block; proceed on the local base
	}

	local, err := revParse(ctx, repoDir, base)
	if err != nil {
		return "", err
	}
	remote, err := revParse(ctx, repoDir, "origin/"+base)
	if err != nil {
		return SyncInSync, nil // remote is reachable + fetched but has no tracking ref for base → nothing to reconcile
	}
	if local == remote {
		return SyncInSync, nil
	}
	// Local strictly behind origin (base is an ancestor of origin/base) → fast-forward it. Use
	// `merge --ff-only` (not `reset --hard`): it advances the base pointer + tree identically for the
	// strictly-behind case, but REFUSES on a dirty working tree/index rather than silently discarding
	// uncommitted work — the managed base is expected clean, so a surfaced error beats data loss.
	if _, err := git(ctx, repoDir, "merge-base", "--is-ancestor", base, "origin/"+base); err == nil {
		if _, err := git(ctx, repoDir, "merge", "--ff-only", "origin/"+base); err != nil {
			return "", fmt.Errorf("fast-forward %s to origin: %w", base, err)
		}
		return SyncResynced, nil
	}
	return SyncDiverged, nil
}

// revParse resolves a ref to its commit hash.
func revParse(ctx context.Context, dir, ref string) (string, error) {
	out, err := git(ctx, dir, "rev-parse", ref)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
