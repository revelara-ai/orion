package conductor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/revelara-ai/orion/internal/contextengine"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/orchestrator"
)

// Inter-module interface manifests (or-7et.5): on proof Accept, a module's
// ACTUAL exported surface is extracted from the accepted artifact (go/ast —
// never the proposer's claim, the trust wall) and persisted DETERMINISTICALLY
// keyed by task. Dependents get it injected as an always-present constraint
// (not heat-ranked recall, which deterministically evicts at 100-module
// scale), and the pre-merge conformance gate checks declared Requires against
// these extracted surfaces.

// extractModuleSurface parses ALL non-test .go files in the module's declared
// FileScope dirs within the accepted artifact (the whole artifact when the
// scope is empty/undeclared) and returns the exported surface: funcs, types,
// and registered routes. Generalizes the old main.go-only extraction.
func extractModuleSurface(artifactDir, fileScope string) []string {
	roots := []string{artifactDir}
	if scopes := leaseSet(fileScope); len(scopes) > 0 {
		roots = roots[:0]
		for _, sc := range scopes {
			roots = append(roots, filepath.Join(artifactDir, sc))
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, root := range roots {
		_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil //nolint:nilerr // best-effort walk: skip unreadable entries
			}
			name := info.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			src, rerr := os.ReadFile(path) // #nosec G304 -- proof-accepted artifact files // #nosec G122 -- harness-owned build tree walk, no hostile symlinks (sandbox-jailed writers)
			if rerr != nil {
				return nil
			}
			for _, d := range extractDecisions(string(src)) {
				if !seen[d] {
					seen[d] = true
					out = append(out, d)
				}
			}
			return nil
		})
	}
	sort.Strings(out)
	return out
}

// persistModuleSurface records a proven module's extracted surface, keyed by
// task. Best-effort: a persist miss never fails a green build.
func persistModuleSurface(ctx context.Context, store *contextstore.Store, mu *sync.Mutex, projectID, taskKey string, surface []string) {
	if store == nil || projectID == "" || len(surface) == 0 {
		return
	}
	withLock(mu, func() {
		_ = store.SaveStringListKind(ctx, projectID, contextstore.ModuleSurfaceKind+taskKey, surface)
	})
}

// loadModuleSurface returns a task's extracted surface (deterministic lookup).
func loadModuleSurface(ctx context.Context, store *contextstore.Store, projectID, taskKey string) ([]string, bool) {
	if store == nil || projectID == "" {
		return nil, false
	}
	out, ok, _ := store.LoadStringListKind(ctx, projectID, contextstore.ModuleSurfaceKind+taskKey)
	return out, ok
}

func loadDeclaredRequires(ctx context.Context, store *contextstore.Store, projectID, taskKey string) ([]string, bool) {
	if store == nil || projectID == "" {
		return nil, false
	}
	out, ok, _ := store.LoadStringListKind(ctx, projectID, contextstore.ModuleRequiresKind+taskKey)
	return out, ok
}

// requireSatisfied reports whether one declared requirement is met by a
// provides set: exact match, or match on the symbol name after the kind
// prefix ("Add" matches "func Add"; "/time" matches "route /time").
func requireSatisfied(req string, provides []string) bool {
	req = strings.TrimSpace(req)
	for _, p := range provides {
		if p == req || strings.HasSuffix(p, " "+req) {
			return true
		}
	}
	return false
}

// missingRequires returns the declared requirements a provides-union does not
// satisfy — the conformance gate's finding, named per symbol.
func missingRequires(requires, provides []string) []string {
	var missing []string
	for _, r := range requires {
		if !requireSatisfied(r, provides) {
			missing = append(missing, r)
		}
	}
	return missing
}

// conformanceGate (or-7et.5d) is the pre-merge interface check: a cluster
// merges only if every member task's DECLARED Requires is satisfied by the
// union of its direct dependencies' EXTRACTED Provides. On failure the
// missing symbols are named — as the returned error, the phase warning, and
// a persisted escalation (the targeted re-grind feedback) — instead of an
// epic-wide failure at the post-merge re-proof. Tasks with no declared
// Requires skip the gate (at-rest behavior unchanged).
func conformanceGate(ctx context.Context, store *contextstore.Store, projectID string, tasks []orchestrator.PlanTask, onPhase PhaseSink) func(cl decomposer.TaskCluster) error {
	if store == nil || projectID == "" {
		return nil
	}
	depsByTask := map[string][]string{}
	for _, t := range tasks {
		depsByTask[t.ID] = t.DependsOn
	}
	return func(cl decomposer.TaskCluster) error {
		for _, member := range cl.Members {
			requires, ok := loadDeclaredRequires(ctx, store, projectID, member)
			if !ok {
				continue
			}
			var union []string
			for _, dep := range depsByTask[member] {
				if surface, sok := loadModuleSurface(ctx, store, projectID, dep); sok {
					union = append(union, surface...)
				}
			}
			if missing := missingRequires(requires, union); len(missing) > 0 {
				detail := fmt.Sprintf("task %s requires symbols its dependencies' accepted artifacts do not export: %s — re-grind the module against the injected dependency surfaces",
					member, strings.Join(missing, ", "))
				onPhase.emit("Integrate", PhaseWarn, "conformance gate: "+detail)
				_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
					_, e := tx.Escalations().CreateDetailed(ctx, projectID, "", "interface conformance (pre-merge)", detail)
					return e
				})
				return fmt.Errorf("interface conformance: %s", detail)
			}
		}
		return nil
	}
}

// injectDepSurfaces appends every direct dependency's extracted surface to
// the bundle's always-injected constraints (or-7et.5c) — bounded to direct
// deps, deterministic by task key, immune to memory pressure.
func injectDepSurfaces(ctx context.Context, store *contextstore.Store, projectID string, deps []string, b *contextengine.Bundle) {
	for _, dep := range deps {
		if surface, ok := loadModuleSurface(ctx, store, projectID, dep); ok {
			b.Constraints = append(b.Constraints,
				"dependency "+dep+" provides (extracted from its proven artifact — call these, do not re-invent): "+strings.Join(surface, "; "))
		}
	}
}
