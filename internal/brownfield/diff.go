package brownfield

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// EvidenceDiff is the before/after differential of the regression gate's
// outputs (or-ykz.12): deterministic, reviewable evidence of WHAT changed
// behaviorally — not just whether the suite stayed green. Attached to the
// change's PR artifact so proof is reviewable.
type EvidenceDiff struct {
	PackageFlips    []PackageFlip // per-package status changes (ok→FAIL is a regression)
	NewFailures     []string      // test functions green before, red after — regressions in (possibly unchanged) behavior
	FixedFailures   []string      // red before, green after — intended repairs
	AddedPackages   []string      // packages that appear only after the change
	RemovedPackages []string      // packages that disappear after the change
}

// PackageFlip is one package whose suite status changed across the diff.
type PackageFlip struct {
	Package string
	Before  string // "ok" | "FAIL"
	After   string
}

var (
	// Horizontal whitespace only: go test emits a bare "FAIL" line before the
	// per-package one — \s would swallow its newline and capture the next line.
	pkgOKRe    = regexp.MustCompile(`(?m)^ok[ \t]+(\S+)`)
	pkgFAILRe  = regexp.MustCompile(`(?m)^FAIL[ \t]+(\S+)`)
	failTestRe = regexp.MustCompile(`(?m)^[ \t]*--- FAIL: (Test[A-Za-z0-9_]*)`)
)

// Diff computes the differential between the gate's before and after runs.
func Diff(reg RegressionResult) EvidenceDiff {
	beforePkgs, beforeFails := parseRun(reg.Before.Output)
	afterPkgs, afterFails := parseRun(reg.After.Output)

	var d EvidenceDiff
	for pkg, st := range beforePkgs {
		if ast, ok := afterPkgs[pkg]; !ok {
			d.RemovedPackages = append(d.RemovedPackages, pkg)
		} else if ast != st {
			d.PackageFlips = append(d.PackageFlips, PackageFlip{Package: pkg, Before: st, After: ast})
		}
	}
	for pkg := range afterPkgs {
		if _, ok := beforePkgs[pkg]; !ok {
			d.AddedPackages = append(d.AddedPackages, pkg)
		}
	}
	for tf := range afterFails {
		if !beforeFails[tf] {
			d.NewFailures = append(d.NewFailures, tf)
		}
	}
	for tf := range beforeFails {
		if !afterFails[tf] {
			d.FixedFailures = append(d.FixedFailures, tf)
		}
	}
	sort.Strings(d.NewFailures)
	sort.Strings(d.FixedFailures)
	sort.Strings(d.AddedPackages)
	sort.Strings(d.RemovedPackages)
	sort.Slice(d.PackageFlips, func(i, j int) bool { return d.PackageFlips[i].Package < d.PackageFlips[j].Package })
	return d
}

func parseRun(out string) (pkgs map[string]string, fails map[string]bool) {
	pkgs, fails = map[string]string{}, map[string]bool{}
	for _, m := range pkgOKRe.FindAllStringSubmatch(out, -1) {
		pkgs[m[1]] = "ok"
	}
	for _, m := range pkgFAILRe.FindAllStringSubmatch(out, -1) {
		pkgs[m[1]] = "FAIL"
	}
	for _, m := range failTestRe.FindAllStringSubmatch(out, -1) {
		fails[m[1]] = true
	}
	return pkgs, fails
}

// Markdown renders the evidence as the PR-artifact section.
func (d EvidenceDiff) Markdown() string {
	var b strings.Builder
	b.WriteString("## Before/after empirical evidence\n\n")
	if len(d.PackageFlips)+len(d.NewFailures)+len(d.FixedFailures)+len(d.AddedPackages)+len(d.RemovedPackages) == 0 {
		b.WriteString("no behavioral difference in the test surface (identical package statuses and failure sets)\n")
		return b.String()
	}
	for _, f := range d.PackageFlips {
		fmt.Fprintf(&b, "- package %s: %s → %s\n", f.Package, f.Before, f.After)
	}
	for _, t := range d.NewFailures {
		fmt.Fprintf(&b, "- REGRESSION: %s (green before, red after)\n", t)
	}
	for _, t := range d.FixedFailures {
		fmt.Fprintf(&b, "- fixed: %s (red before, green after)\n", t)
	}
	for _, p := range d.AddedPackages {
		fmt.Fprintf(&b, "- new package: %s\n", p)
	}
	for _, p := range d.RemovedPackages {
		fmt.Fprintf(&b, "- removed package: %s\n", p)
	}
	return b.String()
}
