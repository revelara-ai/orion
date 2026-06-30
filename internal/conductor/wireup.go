package conductor

import (
	"sort"

	"github.com/revelara-ai/orion/internal/brownfield"
)

// systemWireupGate is the system-assembly proof (or-tcs.3): an assembled system that passes every
// per-task proof can still ship code that is NOT WIRED IN — a package built but reachable from no
// entry point (the "per-unit proof shipped 6 orphan packages" lesson). It proves, against the
// INTEGRATED tree, that every non-main package is reachable from some main via the internal import
// graph; the unreachable ones are orphans and REJECT the epic.
//
// It is deliberately scoped to the structural seam no single task owns — the per-merge whole-tree
// re-proof (or-tcs.1.6) already covers assembled BEHAVIOR; this covers assembled WIRING. With no
// main package (a library, not a service) wiring can't be rooted, so the gate abstains (ok=true)
// rather than flag every package — it never fabricates a verdict it can't ground.
func systemWireupGate(dir string) (ok bool, orphans []string) {
	m := brownfield.ScanRepoMap(dir)
	byDir := make(map[string]brownfield.GoPackage, len(m.Packages))
	var mains []string
	for _, p := range m.Packages {
		byDir[p.Dir] = p
		if p.Name == "main" {
			mains = append(mains, p.Dir)
		}
	}
	if len(mains) == 0 {
		return true, nil // nothing to root reachability on — abstain rather than rubber-stamp-reject
	}

	reached := map[string]bool{}
	var visit func(d string)
	visit = func(d string) {
		if reached[d] {
			return
		}
		reached[d] = true
		for _, imp := range byDir[d].Imports {
			if _, ok := byDir[imp]; ok {
				visit(imp)
			}
		}
	}
	for _, mn := range mains {
		visit(mn)
	}

	for _, p := range m.Packages {
		if p.Name == "main" || reached[p.Dir] {
			continue
		}
		orphans = append(orphans, p.Dir)
	}
	sort.Strings(orphans)
	return len(orphans) == 0, orphans
}
