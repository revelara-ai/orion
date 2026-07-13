package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/tools"
)

// Lease-grounded generation (or-tcs.11): S1 leases used to trust the
// LLM-DECLARED FileScope while the generator could write anywhere in the
// cluster worktree. Three layers close the gap: the write tools refuse
// out-of-lease paths in-turn (native path), the post-generate gate diffs
// OBSERVED writes against the lease (covers ACP too), and integration leases
// prefer the observed scope over the declaration.

// scopeLeaseEnforced (or-tcs.11): the advisory→blocking rollout switch, the
// same staging as every V3 gate. Default = OBSERVE (out-of-scope writes are
// warned + recorded, never rejected) because today's DECLARED scopes are
// aspirational — the template declares cmd?/internal/ layouts while the
// single-artifact generator writes main.go at the root. Flip to enforce once
// declarations are grounded (they now converge via the observed-scope record).
func scopeLeaseEnforced() bool { return os.Getenv("ORION_SCOPE_LEASE") == "enforce" }

// leaseGuarded wraps a write-capable tool so a path outside the lease
// prefixes is refused with a corrective error naming the lease. An empty
// lease (or observe mode) keeps today's whole-worktree behavior. go.mod/
// go.sum are always allowed — every module owns its build metadata at root.
func leaseGuarded(t tools.Tool, lease []string) tools.Tool {
	if len(lease) == 0 || !scopeLeaseEnforced() {
		return t
	}
	inner := t.Run
	t.Run = func(ctx context.Context, in json.RawMessage) (string, error) {
		var p struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(in, &p); err != nil {
			return "", err
		}
		if !leaseAllows(lease, p.Path) {
			return "", fmt.Errorf("path %q is outside this module's declared file scope %v — write only within your scope (go.mod/go.sum are always allowed)", p.Path, lease)
		}
		return inner(ctx, in)
	}
	return t
}

// leaseAllows reports whether a relative path falls inside any lease prefix.
func leaseAllows(lease []string, rel string) bool {
	rel = filepath.ToSlash(filepath.Clean(strings.TrimSpace(rel)))
	base := filepath.Base(rel)
	if base == "go.mod" || base == "go.sum" {
		return true
	}
	for _, l := range lease {
		l = strings.TrimRight(filepath.ToSlash(strings.TrimSpace(l)), "/")
		if l == "" {
			return true // an empty prefix leases the whole tree
		}
		if rel == l || strings.HasPrefix(rel, l+"/") {
			return true
		}
	}
	return false
}

// observedWrites lists the paths the generator actually touched in the
// cluster worktree (vs its base) — new, modified, or renamed-to.
func observedWrites(ctx context.Context, dir string) []string {
	out, err := gitIn(ctx, dir, "status", "--porcelain", "-uall")
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		p := strings.TrimSpace(line[3:])
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+4:] // a rename's destination is the observed write
		}
		p = strings.Trim(p, `"`)
		if p != "" {
			paths = append(paths, filepath.ToSlash(p))
		}
	}
	return paths
}

// outOfLease returns the observed writes that violate the lease.
func outOfLease(lease, observed []string) []string {
	if len(lease) == 0 {
		return nil
	}
	var bad []string
	for _, p := range observed {
		if !leaseAllows(lease, p) {
			bad = append(bad, p)
		}
	}
	return bad
}

// observedScopeFor builds the integration-lease resolver (or-tcs.11.4): a
// cluster whose member tasks all have RECORDED observed scopes leases the
// union of what was actually written; otherwise it falls back to the
// declaration (and its undeclared-=-whole-tree fail-safe).
func observedScopeFor(ctx context.Context, store *contextstore.Store, projectID string) func(cl decomposer.TaskCluster) []string {
	if store == nil || projectID == "" {
		return nil
	}
	return func(cl decomposer.TaskCluster) []string {
		var union []string
		for _, member := range cl.Members {
			obs, ok, _ := store.LoadStringListKind(ctx, projectID, contextstore.ObservedScopeKind+member)
			if !ok {
				return nil // any unrecorded member → the declaration decides
			}
			union = append(union, obs...)
		}
		return union
	}
}
