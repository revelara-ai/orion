package conductor

import (
	"fmt"
	"strings"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof"
)

// driftReport re-evaluates the INTEGRATED build against the spec artifact (or-tcs.10): the spec is
// the drift reference the development loop checks back against. Two dimensions it can judge today:
//   - COVERAGE (spec → build): every required spec obligation (a behavioral case id) must be
//     executed AND passing in the assembled proof — nothing in the spec went unbuilt.
//   - WIREUP (build → reachable): no orphan package — nothing built went unwired (from or-tcs.3).
//   - UNTRACED (build → spec, or-hik): no artifact route/export without spec lineage — scope creep.
//
// It returns a human-readable re-evaluation + whether DRIFT was found, and reports ONLY the two
// dimensions it actually re-evaluates against the build (coverage + wireup) — it makes no claim it
// did not check. The coverage gate (EnforceObligations) and the wireup gate already REJECT on drift;
// this SURFACES the spec↔build alignment so the developer sees it, and is the structured hook the
// third dimension — scope creep (built ↔ spec, needing code↔requirement traceability) — extends once
// builds produce distinct modules. `report` MUST be the proof of the ASSEMBLED tree (so coverage is
// judged against the delivered whole, not one cluster's slice).
// untracedSurface (or-hik, dimension 3): the artifact surface with NO spec
// lineage — routes no case requests, exported funcs no case calls and that
// are not the declared entry point. Support TYPES are never flagged (helper
// types are legitimate implementation freedom); main/envMap are wiring.
func untracedSurface(es spec.ExecutableSpec, entrySymbol, buildDir string) []string {
	surface := extractModuleSurface(buildDir, "")
	if len(surface) == 0 {
		return nil
	}
	tracedRoutes := map[string]bool{}
	tracedFuncs := map[string]bool{"main": true, "envMap": true}
	if entrySymbol != "" {
		tracedFuncs[entrySymbol] = true
	}
	for _, c := range es.ResponseContract.Cases {
		if p := strings.TrimSpace(c.Request.Path); p != "" {
			tracedRoutes[p] = true
		}
		// Unit-kind cases name the exports their steps call: "Add(1,2)" → Add.
		if c.Unit != nil {
			for _, step := range c.Unit.Steps {
				call := strings.TrimSpace(step.Call)
				if call == "" {
					continue
				}
				if i := strings.IndexAny(call, "(."); i > 0 {
					tracedFuncs[strings.TrimSpace(call[:i])] = true
				} else {
					tracedFuncs[call] = true
				}
			}
		}
	}
	var untraced []string
	for _, entry := range surface {
		switch {
		case strings.HasPrefix(entry, "routes: "):
			for _, r := range strings.Split(strings.TrimPrefix(entry, "routes: "), ", ") {
				if r = strings.TrimSpace(r); r != "" && !tracedRoutes[r] {
					untraced = append(untraced, "route "+r)
				}
			}
		case strings.HasPrefix(entry, "exports: "):
			for _, e := range strings.Split(strings.TrimPrefix(entry, "exports: "), ", ") {
				e = strings.TrimSpace(e)
				if f, ok := strings.CutPrefix(e, "func "); ok && !tracedFuncs[f] {
					untraced = append(untraced, e)
				}
				// "type X" entries are never flagged: helper types are
				// legitimate implementation freedom.
			}
		}
	}
	return untraced
}

func driftReport(es spec.ExecutableSpec, report proof.Report, wireupVerdict WireupVerdict, orphans, untraced []string) (string, bool) {
	// Distinct required obligations: RequiredCaseIDs can repeat a content-addressed id when two
	// cases collapse to the same (request, expect) — e.g. a requirement restating the bare happy
	// path. Count each distinct obligation ONCE so the coverage fraction this report cites is honest.
	seen := make(map[string]bool)
	var required []string
	for _, id := range es.ResponseContract.RequiredCaseIDs() {
		if !seen[id] {
			seen[id] = true
			required = append(required, id)
		}
	}
	var uncovered []string
	for _, id := range required {
		if o, ok := report.ObligationResults[id]; !ok || !o.Executed || !o.Passed {
			uncovered = append(uncovered, id)
		}
	}
	drift := len(uncovered) > 0 || len(orphans) > 0 || len(untraced) > 0

	var b strings.Builder
	b.WriteString("spec↔build drift check — ")
	if drift {
		b.WriteString("DRIFT")
	} else {
		b.WriteString("aligned")
	}
	fmt.Fprintf(&b, ": coverage %d/%d spec obligations proven", len(required)-len(uncovered), len(required))
	if len(uncovered) > 0 {
		fmt.Fprintf(&b, " (unbuilt: %s)", strings.Join(uncovered, ", "))
	}
	switch {
	case len(orphans) > 0:
		fmt.Fprintf(&b, "; wireup: orphan package(s) %s", strings.Join(orphans, ", "))
	case wireupVerdict == WireupUnverified:
		// or-4y7.8: not language-groundable — reported distinctly, NEVER "clean".
		b.WriteString("; wireup unverified (not language-groundable)")
	default:
		b.WriteString("; wireup clean")
	}
	// or-hik dimension 3: BUILT-NOT-IN-SPEC — proven behavior with no spec
	// lineage is scope creep, and it ESCALATES (the caller files the inbox
	// escalation), never just a log line.
	if len(untraced) > 0 {
		fmt.Fprintf(&b, "; untraced: %s (built with no spec lineage — scope creep)", strings.Join(untraced, ", "))
	} else {
		b.WriteString("; traceability clean")
	}
	return b.String(), drift
}
