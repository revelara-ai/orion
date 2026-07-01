package conductor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// brokenTimeService compiles + runs (so the proof can execute it) and exposes the
// proof's handleTime entry symbol — but it returns the WRONG body (no valid time),
// so the behavioral + empirical contract proof rejects it.
const brokenTimeService = `package main

import (
	"net/http"
	"os"
)

func handleTime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(` + "`{\"oops\":\"not the time\"}`" + `))
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/time", handleTime)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	_ = http.ListenAndServe(":"+port, mux)
}
`

// writeBrokenTimeService starts from the fixture (for a valid, version-matched
// go.mod) then overwrites main.go with the broken handler.
func writeBrokenTimeService(dir string, gs sandbox.GenSpec) (sandbox.GeneratedArtifact, error) {
	if _, err := sandbox.GenerateTimeServiceFixture(dir, gs); err != nil {
		return sandbox.GeneratedArtifact{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(brokenTimeService), 0o644); err != nil {
		return sandbox.GeneratedArtifact{}, err
	}
	return sandbox.ArtifactFromDir(dir)
}

// TestAnalyzeFailureNamesFailingCases: the causal analysis names the case that ran
// but failed, flags the case that never ran (coverage hole), and carries the
// failing mode's raw diagnostic — without leaking proof-corpus source.
func TestAnalyzeFailureNamesFailingCases(t *testing.T) {
	cases := []spec.BehavioralCase{
		{ID: "okcase", Request: spec.RequestShape{Method: "GET", Path: "/time"}, Expect: spec.ExpectShape{Status: 200, ContentType: "application/json"}},
		{ID: "tzcase", Request: spec.RequestShape{Method: "GET", Path: "/time", Query: map[string]string{"tz": "America/New_York"}},
			Expect: spec.ExpectShape{Status: 200, ContentType: "application/json", Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyInTZ, Key: "time", Value: "America/New_York"}}}},
		{ID: "errcase", Request: spec.RequestShape{Method: "GET", Path: "/time", Query: map[string]string{"tz": "Bogus"}}, Expect: spec.ExpectShape{Status: 400, ContentType: "application/json"}},
	}
	rep := proof.Report{
		Outcome: truthalign.Outcome{Verdict: truthalign.Reject, Dissenting: []string{"behavioral"}},
		Modes:   []proof.ModeReport{{Result: truthalign.ModeResult{Mode: "behavioral", Pass: false, Output: "--- FAIL: time not in zone America/New_York"}}},
		ObligationResults: map[string]proof.ObligationResult{
			"okcase": {Executed: true, Passed: true},
			"tzcase": {Executed: true, Passed: false}, // ran but failed
			// errcase absent → unexecuted (coverage hole)
		},
	}
	got := analyzeFailure(rep, cases)
	for _, want := range []string{"Reject", "FAILING", "America/New_York", "UNEXECUTED", "status 400", "behavioral mode diagnostic", "not in zone"} {
		if !strings.Contains(got, want) {
			t.Fatalf("analysis missing %q:\n%s", want, got)
		}
	}
	// The passing case must NOT be reported as failing or unexecuted.
	failBlock := got[strings.Index(got, "FAILING"):]
	if strings.Contains(failBlock, "okcase") {
		t.Fatalf("the passing case leaked into the failure analysis:\n%s", got)
	}
}

// TestBuildAndProveRefinesUntilAccept: a Reject on the first attempt triggers the
// causal analysis → feedback → fix loop. Attempt 1 ships a broken handler (Reject);
// the analysis is fed back; attempt 2 ships the correct service and PROVES Accept.
// This is exactly the "loop through the problems until resolved" the developer asked
// for — converging, not escalating immediately.
func TestBuildAndProveRefinesUntilAccept(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + proves a service twice; skipped in -short")
	}
	oc, ctx := ratifiedTimeService(t)

	var gotFeedback string
	gen := func(_ context.Context, gs sandbox.GenSpec, dir, feedback string) (sandbox.GeneratedArtifact, error) {
		if feedback == "" {
			return writeBrokenTimeService(dir, gs) // attempt 1: broken → Reject
		}
		gotFeedback = feedback                         // attempt 2 received the analysis
		return sandbox.GenerateTimeServiceFixture(dir, gs) // correct service → Accept
	}

	res, err := BuildAndProve(ctx, oc.Store(), gen, nil, nil, "")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.Attempts != 2 {
		t.Fatalf("refinement should take 2 attempts (broken → fixed), got %d (%+v)", res.Attempts, res)
	}
	if res.Verdict != "Accept" || !res.Closed {
		t.Fatalf("the loop should converge to a proven, closed task: %+v", res)
	}
	if res.FailureAnalysis != "" {
		t.Fatalf("an accepted build should carry no residual failure analysis: %q", res.FailureAnalysis)
	}
	if !strings.Contains(gotFeedback, "Reject") || !strings.Contains(gotFeedback, "diagnostic") {
		t.Fatalf("attempt 2 did not receive a causal analysis of attempt 1's failure:\n%s", gotFeedback)
	}
}

// TestBuildAndProveStopsWhenGeneratorCannotRefine: if the generator re-emits an
// identical artifact in response to the analysis (it cannot fix the failure), the
// loop detects the no-change and stops — escalating the verdict rather than burning
// the whole attempt budget on a guaranteed-identical re-prove.
func TestBuildAndProveStopsWhenGeneratorCannotRefine(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + proves a service; skipped in -short")
	}
	oc, ctx := ratifiedTimeService(t)

	gen := func(_ context.Context, gs sandbox.GenSpec, dir, _ string) (sandbox.GeneratedArtifact, error) {
		return writeBrokenTimeService(dir, gs) // same broken artifact every attempt
	}

	res, err := BuildAndProve(ctx, oc.Store(), gen, nil, nil, "")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.Attempts != 1 {
		t.Fatalf("no-change guard should stop after the first prove, not re-prove an identical artifact (attempts=%d)", res.Attempts)
	}
	if res.Verdict == "Accept" {
		t.Fatalf("a broken service must not prove Accept: %+v", res)
	}
	if res.FailureAnalysis == "" {
		t.Fatal("a non-Accept build must surface a causal analysis for the developer")
	}

	// or-v9f.4: the escalation lands in the inbox attributed to the FAILING task
	// and carries its causal analysis as the decision payload. or-v9f.6 slice A:
	// it was filed at EXHAUSTION time (the mid-run reason survives — the bar-time
	// pass dedups), and one task never accumulates two open rows.
	if err := oc.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
		open, e := tx.Escalations().ListOpen(ctx)
		if e != nil {
			return e
		}
		if len(open) != 1 {
			t.Fatalf("want exactly ONE open escalation for the one failing task, got %d: %+v", len(open), open)
		}
		esc := open[0]
		if esc.TaskID != res.TaskID {
			t.Errorf("escalation attributed to %q, want the failing task %q", esc.TaskID, res.TaskID)
		}
		if esc.Detail == "" {
			t.Errorf("escalation must carry the failing task's causal analysis, got empty detail: %+v", esc)
		}
		if !strings.Contains(esc.Reason, "attempt") {
			t.Errorf("the exhaustion-time reason must survive (filed mid-run, bar pass dedups), got: %q", esc.Reason)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
