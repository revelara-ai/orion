package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/memory"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
	"github.com/revelara-ai/orion/internal/sandbox"
	"github.com/revelara-ai/orion/internal/selfevolve"
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
		gotFeedback = feedback                             // attempt 2 received the analysis
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

	// or-v9f.17: an escalating run must reach the operator out-of-band — both the
	// mid-run escalation.created and the end-of-run escalated event.
	var notifyMu sync.Mutex
	kinds := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var e struct {
			Kind string `json:"kind"`
		}
		_ = json.NewDecoder(r.Body).Decode(&e)
		notifyMu.Lock()
		kinds[e.Kind]++
		notifyMu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	t.Setenv("ORION_NOTIFY_WEBHOOK", srv.URL)

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

	notifyMu.Lock()
	defer notifyMu.Unlock()
	if kinds["escalation.created"] == 0 {
		t.Errorf("the mid-run escalation must notify out-of-band, got events: %v", kinds)
	}
	if kinds["escalated"] == 0 {
		t.Errorf("the end-of-run escalate must notify out-of-band, got events: %v", kinds)
	}
}

// TestRememberOutcomeContent (or-gb1.4 acceptance): after a two-attempt
// fixture build (fail then pass), the MTM pattern item carries the overcome
// failure analysis and the inter-attempt change summary — and `orion evolve`
// on that store emits a learned-* skill whose body is the procedure
// trajectory, not the old contentless template.
func TestRememberOutcomeContent(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + proves a service twice; skipped in -short")
	}
	oc, ctx := ratifiedTimeService(t)

	gen := func(_ context.Context, gs sandbox.GenSpec, dir, feedback string) (sandbox.GeneratedArtifact, error) {
		if feedback == "" {
			return writeBrokenTimeService(dir, gs) // attempt 1: broken → Reject
		}
		return sandbox.GenerateTimeServiceFixture(dir, gs) // attempt 2 → Accept
	}
	res, err := BuildAndProve(ctx, oc.Store(), gen, nil, nil, "")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.Attempts != 2 || res.Verdict != "Accept" {
		t.Fatalf("fixture must converge on attempt 2: %+v", res)
	}

	mem, err := memory.Open(filepath.Join(oc.Store().Dir(), "memory"))
	if err != nil {
		t.Fatalf("reopen memory: %v", err)
	}
	defer func() { _ = mem.Close() }()

	items, err := mem.Retrieve(ctx, "Proven task", memory.MTM)
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	var pattern string
	for _, it := range items {
		if it.Kind == memory.KindPattern && strings.Contains(it.Content, "Proven task") {
			pattern = it.Content
			break
		}
	}
	if pattern == "" {
		t.Fatalf("no pattern item found in MTM (%d items)", len(items))
	}
	for _, want := range []string{"converged on attempt 2", "overcame:", "passing attempt changed:", "modified"} {
		if !strings.Contains(pattern, want) {
			t.Fatalf("the pattern item must carry %q (or-gb1.4), got:\n%s", want, pattern)
		}
	}

	// The candidate → learned skill path carries the trajectory, and the old
	// contentless template phrase is gone (acceptance: grep -L "approach converged").
	// or-gb1.5: promotion fails closed without eval evidence — attach passing
	// deterministic evidence to every candidate first.
	cands, cerr := mem.ListCandidates(ctx)
	if cerr != nil || len(cands) == 0 {
		t.Fatalf("candidates: %v (%d)", cerr, len(cands))
	}
	for _, c := range cands {
		ev := fmt.Sprintf(`{"candidate_id":%q,"happy":[{"name":"h","input":"i","output":"ok","predicate":{"kind":"contains","arg":"ok"}}]}`, c.ID)
		if _, werr := mem.Write(ctx, memory.Item{Tier: memory.MTM, Kind: selfevolve.EvalEvidenceKind, Content: ev, TrustTier: memory.TrustProof, Heat: 1.0}); werr != nil {
			t.Fatal(werr)
		}
	}
	skillsDir := t.TempDir()
	names, _, err := selfevolve.PromoteCandidates(ctx, mem, skillsDir)
	if err != nil || len(names) == 0 {
		t.Fatalf("promote candidates: %v (%d)", err, len(names))
	}
	var body string
	for _, name := range names {
		b, rerr := os.ReadFile(filepath.Join(skillsDir, name, "SKILL.md"))
		if rerr == nil && strings.Contains(string(b), "procedure trajectory") {
			body = string(b)
			break
		}
	}
	if body == "" {
		t.Fatalf("no learned skill carries the procedure trajectory (promoted: %v)", names)
	}
	if strings.Contains(body, "approach converged") {
		t.Fatalf("the contentless template phrase must be gone from emitted skills:\n%s", body)
	}
	for _, want := range []string{"attempt 1 failed:", "passing fix:"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the learned skill must carry the trajectory step %q, got:\n%s", want, body)
		}
	}
}

// TestBuildPersistsRunEvents (or-v9f.16): a real build leaves its full phase
// trail in the store — run start/end plus per-cluster attributed events — so
// attach/resume outlive the terminal.
func TestBuildPersistsRunEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + proves a service; skipped in -short")
	}
	oc, ctx := ratifiedTimeService(t)
	if _, err := BuildAndProve(ctx, oc.Store(), nil, nil, nil, ""); err != nil {
		t.Fatalf("build: %v", err)
	}
	proj, _, err := oc.Store().CurrentOrLastDeliveredProjectSpec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runID, ok, err := oc.Store().LatestRunID(ctx, proj.ID)
	if err != nil || !ok {
		t.Fatalf("a build must record its run: ok=%v err=%v", ok, err)
	}
	events, err := oc.Store().ListRunEventsAfter(ctx, runID, 0)
	if err != nil || len(events) == 0 {
		t.Fatalf("no persisted run events (%v)", err)
	}
	if events[0].Phase != "Run" || events[0].Status != "running" {
		t.Fatalf("the first event must open the run, got %+v", events[0])
	}
	last := events[len(events)-1]
	if last.Phase != "Run" || last.Status != "done" {
		t.Fatalf("the last event must close the run, got %+v", last)
	}
	var attributed bool
	for _, e := range events {
		if e.TaskID != "" {
			attributed = true
			break
		}
	}
	if !attributed {
		t.Fatal("per-cluster events must carry task attribution")
	}
}

// TestGenScopeEnforcedRejectsOutOfScopeArtifact (or-tcs.11b, full lane): with
// ORION_SCOPE_LEASE=enforce, an artifact whose observed writes fall outside
// the declared scope is routed to refinement (feedback names the paths) and
// the task ends non-Accept — never silently integrated. Uses the fixture
// generator, whose root-level main.go violates the template's declared
// cmd?/internal layout by construction.
func TestGenScopeEnforcedRejectsOutOfScopeArtifact(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the build loop; skipped in -short")
	}
	t.Setenv("ORION_SCOPE_LEASE", "enforce")
	oc, ctx := ratifiedTimeService(t)

	var feedbacks []string
	gen := func(_ context.Context, gs sandbox.GenSpec, dir, feedback string) (sandbox.GeneratedArtifact, error) {
		if feedback != "" {
			feedbacks = append(feedbacks, feedback)
		}
		return sandbox.GenerateTimeServiceFixture(dir, gs) // always writes main.go at the worktree root
	}
	res, err := BuildAndProve(ctx, oc.Store(), gen, nil, nil, "")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.Verdict == "Accept" {
		t.Fatalf("an out-of-scope artifact must not Accept under enforcement: %+v", res)
	}
	if !strings.Contains(res.FailureAnalysis, "OUTSIDE the module's declared file scope") || !strings.Contains(res.FailureAnalysis, "main.go") {
		t.Fatalf("the failure analysis must name the out-of-scope paths:\n%s", res.FailureAnalysis)
	}
}

// TestCheckpointEmitsMidRunE2E (or-v9f.26 acceptance, full lane): a real DAG
// run in advisory mode emits at least one Checkpoint phase event BEFORE the
// run's terminal event — the proactive touchpoint exists on the live path.
func TestCheckpointEmitsMidRunE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("full DAG fixture run; skipped in -short")
	}
	t.Setenv("ORION_CHECKPOINT_EVERY", "1")
	t.Setenv("ORION_CHECKPOINT_MODE", "advisory")
	oc, ctx := ratifiedTimeService(t)
	var seq []string
	sink := func(e PhaseEvent) { seq = append(seq, e.Phase+":"+string(e.Status)) }
	if _, err := BuildAndProve(ctx, oc.Store(), nil, nil, sink, ""); err != nil {
		t.Fatalf("build: %v", err)
	}
	checkpointAt, runDoneAt := -1, -1
	for i, s := range seq {
		if strings.HasPrefix(s, "Checkpoint:") && checkpointAt == -1 {
			checkpointAt = i
		}
		if s == "Run:done" {
			runDoneAt = i
		}
	}
	if checkpointAt == -1 {
		t.Fatalf("a multi-cluster advisory run must emit a Checkpoint phase, got %v", seq)
	}
	if runDoneAt != -1 && checkpointAt > runDoneAt {
		t.Fatal("the checkpoint must land MID-run, before the terminal event")
	}
}
