package conductor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/delivery"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

// TestPRBodyCapturesProvenance (or-tcs.7): the PR description a developer reviews carries the
// epic's provenance — intent, spec anchor, the accept verdict + evidence, the SystemValidate
// drift/wireup line, the diff, and a runbook pointer.
func TestPRBodyCapturesProvenance(t *testing.T) {
	es := spec.ExecutableSpec{
		Intent:           "Build an HTTP service that returns the current time",
		Hash:             "abc123def456",
		ResponseContract: spec.ResponseContract{Route: "/time", Port: 8080},
	}
	rb := delivery.Runbook{Sections: map[string]string{"Rollback": "revert the orion branch"}}
	driftLine := "spec↔build drift check — aligned: coverage 2/2 spec obligations proven; wireup clean"
	diffstat := " time-service/main.go | 42 ++++++\n 1 file changed, 42 insertions(+)"

	body := prBody(es, truthalign.Accept, driftLine, "", diffstat, rb)

	for _, want := range []string{
		"Build an HTTP service that returns the current time", // intent
		"abc123de",    // spec anchor (short hash)
		"/time",       // route
		"Accept",      // verdict
		"drift check", // the SystemValidate line
		"coverage 2/2",
		"wireup clean",
		"1 file changed", // diff stat
		"Rollback",       // runbook section
		"proven by Orion",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("PR body missing %q\n---\n%s", want, body)
		}
	}
}

// gitInit makes a throwaway repo with one commit on the default branch, returning its path.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "--allow-empty", "-m", "base"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

// TestPRHandoffLocalFirstNoRemote (or-tcs.7): with no ORION_GIT_PR opt-in and no remote, the
// handoff ALWAYS writes the local PR artifact and records the exact push/PR commands — and takes
// ZERO outward action (no push, no gh).
func TestPRHandoffLocalFirstNoRemote(t *testing.T) {
	t.Setenv("ORION_GIT_PR", "") // explicitly not opted in
	repoRoot := t.TempDir()
	storeDir := t.TempDir()
	gitInit(t, repoRoot)

	es := spec.ExecutableSpec{Intent: "time service", Hash: "deadbeefcafe", ResponseContract: spec.ResponseContract{Route: "/time", Port: 8080}}
	d := GitDelivery{Branch: "orion-time-service", Commit: "abc1234", Path: repoRoot}

	res, err := PRHandoff(context.Background(), repoRoot, storeDir, d, es, truthalign.Accept,
		"spec↔build drift check — aligned: coverage 1/1 spec obligations proven; wireup clean",
		"- t-bad: empirical probe: port never opened\n",
		delivery.Runbook{Sections: map[string]string{"Rollback": "revert the orion branch"}})
	if err != nil {
		t.Fatalf("PRHandoff: %v", err)
	}
	if res.Opened {
		t.Error("must NOT open a real PR without opt-in + remote")
	}
	if res.ArtifactPath == "" {
		t.Fatal("must always write a local PR artifact")
	}
	got, rerr := os.ReadFile(res.ArtifactPath)
	if rerr != nil {
		t.Fatalf("artifact not written: %v", rerr)
	}
	if !strings.Contains(string(got), "time service") || !strings.Contains(string(got), "drift check") {
		t.Errorf("artifact missing provenance:\n%s", got)
	}
	// or-v9f.5: a partial delivery's PR must show the reviewer what is NOT in it.
	if !strings.Contains(string(got), "Escalated remainder") || !strings.Contains(string(got), "t-bad") {
		t.Errorf("artifact missing the escalated remainder section:\n%s", got)
	}
	if filepath.Dir(res.ArtifactPath) != storeDir {
		t.Errorf("artifact should live in the store dir %q, got %q", storeDir, res.ArtifactPath)
	}
	joined := strings.Join(res.Commands, "\n")
	if !strings.Contains(joined, "push -u origin") || !strings.Contains(joined, "gh pr create") {
		t.Errorf("must record the exact push/PR commands for the developer, got: %v", res.Commands)
	}
	if !strings.Contains(joined, "orion-time-service") {
		t.Errorf("commands should name the feature branch, got: %v", res.Commands)
	}
}

// TestChangePRBodyCarriesEvidence (or-ykz.12): the before/after differential
// rides the PR artifact so the proof is reviewable, and an empty evidence
// section is omitted rather than rendered blank.
func TestChangePRBodyCarriesEvidence(t *testing.T) {
	ev := "## Before/after empirical evidence\n\n- REGRESSION: TestX (green before, red after)\n"
	body := changePRBody("fix it", "standard", "1 file changed", ev)
	if !strings.Contains(body, "Before/after empirical evidence") || !strings.Contains(body, "REGRESSION: TestX") {
		t.Fatalf("evidence must ride the PR body:\n%s", body)
	}
	if strings.Contains(changePRBody("fix it", "standard", "", ""), "Before/after") {
		t.Fatal("empty evidence must be omitted")
	}
}
