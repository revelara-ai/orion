package main

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/brownfield"
)

// TestFormatBaselineReportRedExpandsOnlyFailures (or-rbc): a red baseline shows
// the "N passed / M failed" headline and ONLY the failing packages' output — the
// green packages' noise (the whole-run dump this replaces) never appears.
func TestFormatBaselineReportRedExpandsOnlyFailures(t *testing.T) {
	out := strings.Join([]string{
		"ok  \texample.com/m/alpha\t0.20s",
		"--- FAIL: TestBeta (0.00s)",
		"    beta_test.go:9: want 4 got 5",
		"FAIL\texample.com/m/beta\t0.01s",
		"ok  \texample.com/m/delta\t0.05s",
	}, "\n")
	rep := formatBaselineReport(brownfield.TestResult{Passed: false, Output: out})

	if !strings.Contains(rep, "RED") || !strings.Contains(rep, "2 passed") || !strings.Contains(rep, "1 failed") {
		t.Fatalf("red report must headline the tally, got:\n%s", rep)
	}
	if !strings.Contains(rep, "example.com/m/beta") || !strings.Contains(rep, "want 4 got 5") {
		t.Fatalf("the failing package must be expanded, got:\n%s", rep)
	}
	// Failure-only: a passing package's own summary line must NOT be echoed back.
	if strings.Contains(rep, "example.com/m/alpha") {
		t.Fatalf("a green package must not appear in the red report, got:\n%s", rep)
	}
}

// TestFormatBaselineReportGreenIsOneLine: a green baseline is a single tally line
// with no per-package output at all.
func TestFormatBaselineReportGreenIsOneLine(t *testing.T) {
	out := strings.Join([]string{
		"ok  \texample.com/m/alpha\t0.20s",
		"ok  \texample.com/m/delta\t0.05s",
		"?   \texample.com/m/gamma\t[no test files]",
	}, "\n")
	rep := formatBaselineReport(brownfield.TestResult{Passed: true, Output: out})

	if !strings.Contains(rep, "GREEN") || !strings.Contains(rep, "2 packages") {
		t.Fatalf("green report must headline the pass count, got:\n%s", rep)
	}
	if strings.Contains(rep, "── ") {
		t.Fatalf("a green report must expand NO package blocks, got:\n%s", rep)
	}
}

// TestBaselineProgressTracksTally (or-rbc): the spinner state counts every
// package completion and separately tallies failures for the live status line.
func TestBaselineProgressTracksTally(t *testing.T) {
	st := &baselineProgress{}
	sink := st.sink()
	sink("baseline", "example.com/m/alpha ok (1 done)")
	sink("baseline", "example.com/m/beta FAIL (2 done)")
	sink("baseline", "example.com/m/gamma ok (3 done)")

	status := st.status('x')
	if !strings.Contains(status, "3 packages") {
		t.Fatalf("status must count all completions, got %q", status)
	}
	if !strings.Contains(status, "1 failing") {
		t.Fatalf("status must tally failures separately, got %q", status)
	}
	// Negative: with no failures, the status must NOT claim any failing.
	clean := &baselineProgress{}
	clean.sink()("baseline", "example.com/m/alpha ok (1 done)")
	if strings.Contains(clean.status('x'), "failing") {
		t.Fatalf("an all-green run must not report failures, got %q", clean.status('x'))
	}
}
