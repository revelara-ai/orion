package brownfield

import (
	"strings"
	"testing"
)

const beforeOut = `ok  	example.com/m/a	0.01s
ok  	example.com/m/b	0.02s
ok  	example.com/m/c	0.30s
`

const afterOut = `ok  	example.com/m/a	0.01s
--- FAIL: TestUnrelatedInvariant (0.00s)
    b_test.go:12: boom
FAIL
FAIL	example.com/m/b	0.02s
ok  	example.com/m/d	0.10s
`

func regFor(before, after string, held bool) RegressionResult {
	return RegressionResult{
		Before: TestResult{Detected: true, Passed: true, Output: before},
		After:  TestResult{Detected: true, Passed: !strings.Contains(after, "FAIL"), Output: after},
		Held:   held,
	}
}

// TestDiffCatchesRegressionInUnchangedBehavior (or-ykz.12 Done-when): a test
// that was green before and red after is named by the diff — the reviewer sees
// WHICH unchanged behavior regressed, not just "the suite went red".
func TestDiffCatchesRegressionInUnchangedBehavior(t *testing.T) {
	d := Diff(regFor(beforeOut, afterOut, false))
	if len(d.NewFailures) != 1 || d.NewFailures[0] != "TestUnrelatedInvariant" {
		t.Fatalf("the regressed test must be named, got %+v", d.NewFailures)
	}
	flips := map[string]string{}
	for _, f := range d.PackageFlips {
		flips[f.Package] = f.Before + "->" + f.After
	}
	if flips["example.com/m/b"] != "ok->FAIL" {
		t.Fatalf("the flipped package must be named, got %+v", d.PackageFlips)
	}
}

func TestDiffTracksPackageSetChanges(t *testing.T) {
	d := Diff(regFor(beforeOut, afterOut, false))
	if len(d.RemovedPackages) != 1 || d.RemovedPackages[0] != "example.com/m/c" {
		t.Fatalf("removed packages: %+v", d.RemovedPackages)
	}
	if len(d.AddedPackages) != 1 || d.AddedPackages[0] != "example.com/m/d" {
		t.Fatalf("added packages: %+v", d.AddedPackages)
	}
}

func TestDiffCleanChangeIsQuiet(t *testing.T) {
	d := Diff(regFor(beforeOut, beforeOut, true))
	if len(d.NewFailures)+len(d.PackageFlips)+len(d.AddedPackages)+len(d.RemovedPackages) != 0 {
		t.Fatalf("identical outputs must diff empty, got %+v", d)
	}
	if !strings.Contains(d.Markdown(), "no behavioral difference") {
		t.Fatalf("clean evidence must say so:\n%s", d.Markdown())
	}
}

func TestDiffMarkdownCarriesTheEvidence(t *testing.T) {
	md := Diff(regFor(beforeOut, afterOut, false)).Markdown()
	for _, want := range []string{"Before/after empirical evidence", "TestUnrelatedInvariant", "example.com/m/b", "ok → FAIL"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown evidence missing %q:\n%s", want, md)
		}
	}
}

// TestDiffFixedFailures: a test red before and green after is a FIX, reported
// separately (evidence of intended repair, not regression).
func TestDiffFixedFailures(t *testing.T) {
	d := Diff(regFor(afterOut, beforeOut, false))
	if len(d.FixedFailures) != 1 || d.FixedFailures[0] != "TestUnrelatedInvariant" {
		t.Fatalf("fixed failures: %+v", d.FixedFailures)
	}
}
