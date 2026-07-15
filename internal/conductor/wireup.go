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
// re-proof (or-tcs.1.6) already covers assembled BEHAVIOR; this covers assembled WIRING.

// WireupVerdict is the THREE-valued outcome of the system-wireup check (or-4y7.8).
// The old two-valued (ok bool) form collapsed "cannot be grounded" into "clean",
// which is a FALSE GREEN for a non-Go tree (no Go mains → abstain → pass). The
// distinct Unverified state is never reported as clean.
type WireupVerdict int

const (
	WireupWired      WireupVerdict = iota // rooted from a main, every package reachable
	WireupOrphaned                        // rooted, but unreachable package(s) exist
	WireupUnverified                      // cannot be grounded (no analyzer for the language, or no rootable entry)
)

// wireupAnalyzer roots a language's reachability graph; a language with no
// analyzer is Unverified, never rubber-stamped clean.
type wireupAnalyzer interface {
	Language() string
	Analyze(dir string) (WireupVerdict, []string)
}

var wireupAnalyzers = map[string]wireupAnalyzer{}

func registerWireupAnalyzer(a wireupAnalyzer) { wireupAnalyzers[a.Language()] = a }

func wireupAnalyzerFor(language string) wireupAnalyzer {
	if language == "" {
		language = "go"
	}
	return wireupAnalyzers[language]
}

// systemWireupGate roots reachability for the artifact's language. An
// unregistered language yields Unverified (the caller reports it distinctly and
// does not treat it as clean).
func systemWireupGate(dir, language string) (WireupVerdict, []string) {
	an := wireupAnalyzerFor(language)
	if an == nil {
		return WireupUnverified, nil
	}
	return an.Analyze(dir)
}

// goWireup is the default: the V2.0 Go import-graph reachability, verbatim —
// except a no-main tree is now honestly Unverified (it was a silent pass).
type goWireup struct{}

func (goWireup) Language() string { return "go" }

func (goWireup) Analyze(dir string) (WireupVerdict, []string) {
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
		// A library (no main) can't be reachability-rooted — Unverified, not a
		// fabricated "clean". The caller keeps it non-blocking.
		return WireupUnverified, nil
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

	var orphans []string
	for _, p := range m.Packages {
		if p.Name == "main" || reached[p.Dir] {
			continue
		}
		orphans = append(orphans, p.Dir)
	}
	sort.Strings(orphans)
	if len(orphans) == 0 {
		return WireupWired, nil
	}
	return WireupOrphaned, orphans
}

func init() { registerWireupAnalyzer(goWireup{}) }
