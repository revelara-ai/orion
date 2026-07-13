package conductor

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

func acceptReport() proof.Report {
	var r proof.Report
	r.Outcome.Verdict = truthalign.Accept
	r.Outcome.Modes = []truthalign.ModeResult{{Mode: "behavioral", Pass: true}, {Mode: "empirical", Pass: true}}
	return r
}

// TestChangeSummaryNamesDecls (or-gb1.4): the inter-attempt change summary
// names the modified/added/removed top-level declarations — harness-derived
// substance, not a template.
func TestChangeSummaryNamesDecls(t *testing.T) {
	before := "package main\n\nfunc handleTime() string { return \"wrong\" }\n\nfunc dead() {}\n\nfunc main() {}\n"
	after := "package main\n\nfunc handleTime() string { return \"right\" }\n\nfunc helper() int { return 1 }\n\nfunc main() {}\n"
	got := changeSummary(before, after)
	for _, want := range []string{"modified func handleTime", "added func helper", "removed func dead"} {
		if !strings.Contains(got, want) {
			t.Fatalf("summary must name %q, got: %s", want, got)
		}
	}
	if strings.Contains(got, "func main") {
		t.Fatalf("unchanged decls must not be reported: %s", got)
	}
}

// TestChangeSummaryFallsBackOnUnparsableSource: a non-Go artifact still gets a
// line-stat summary — never a panic, never empty.
func TestChangeSummaryFallsBackOnUnparsableSource(t *testing.T) {
	got := changeSummary("not go at all {{{", "still not go\nbut longer\n")
	if !strings.Contains(got, "lines") {
		t.Fatalf("unparsable source must fall back to the line stat, got: %q", got)
	}
}

// TestSummarizeOutcomeCarriesTrajectory (or-gb1.4): a multi-attempt win
// remembers WHAT was overcome and WHAT changed; a first-attempt win stays
// compact.
func TestSummarizeOutcomeCarriesTrajectory(t *testing.T) {
	traj := &buildTrajectory{
		Attempts:      2,
		Overcame:      []string{"verdict Reject: case tzcase FAILING — time not in zone"},
		ChangeSummary: "modified func handleTime (+3 lines)",
	}
	got := summarizeOutcome("T1", acceptReport(), traj)
	for _, want := range []string{"converged on attempt 2", "overcame:", "time not in zone", "passing attempt changed: modified func handleTime"} {
		if !strings.Contains(got, want) {
			t.Fatalf("outcome item must carry %q, got:\n%s", want, got)
		}
	}
	first := summarizeOutcome("T1", acceptReport(), &buildTrajectory{Attempts: 1})
	if strings.Contains(first, "overcame") {
		t.Fatalf("a first-attempt win has no trajectory to report: %s", first)
	}
}

// TestSummarizeCandidateIsSubstantive (or-gb1.4): the candidate body is a
// procedure trajectory, and the old contentless template phrase is gone.
func TestSummarizeCandidateIsSubstantive(t *testing.T) {
	traj := &buildTrajectory{
		Attempts:      3,
		Overcame:      []string{"attempt 1 analysis", "attempt 2 analysis"},
		ChangeSummary: "modified func handleTime",
	}
	got := summarizeCandidate("T1", acceptReport(), traj)
	for _, want := range []string{"procedure trajectory (3 attempts)", "attempt 1 failed: attempt 1 analysis", "attempt 2 failed: attempt 2 analysis", "passing fix: modified func handleTime"} {
		if !strings.Contains(got, want) {
			t.Fatalf("candidate must carry %q, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "approach converged") {
		t.Fatalf("the contentless template phrase must be gone: %s", got)
	}
}
