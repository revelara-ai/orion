package conductor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/revelara-ai/orion/internal/llm"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

func alignResp(aligned bool, sev, concern string) *llm.ChatResponse {
	in, _ := json.Marshal(map[string]any{"aligned": aligned, "severity": sev, "concern": concern})
	return &llm.ChatResponse{StopReason: llm.StopToolUse, Content: []llm.ContentBlock{
		{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{ID: "a1", Name: "report_alignment", Input: in}},
	}}
}

// A service that PASSES the cases (returns JSON with a "time" key that is valid
// RFC3339) but VIOLATES the intent — the timestamp is hardcoded, not the current
// time. This is the misalignment proof cannot catch.
const hardcodedTimeService = `package main

import (
	"encoding/json"
	"net/http"
)

func handleTime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"time": "2020-01-01T00:00:00Z"})
}

func main() { http.HandleFunc("/time", handleTime); _ = http.ListenAndServe(":8080", nil) }
`

// TestNativeAlignerCatchesHardcodedTime is the V3 Step 1 LIVE EXPERIMENT (set
// ANTHROPIC_API_KEY to run it): does the alignment judge flag a hardcoded
// timestamp that passes every "time" case? This is the single validation of the
// most novel + risky part of V3 — the align-judge quality. Run:
//
//	ANTHROPIC_API_KEY=... go test ./internal/conductor/ -run TestNativeAlignerCatchesHardcodedTime -v
func TestNativeAlignerCatchesHardcodedTime(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("set ANTHROPIC_API_KEY to run the live alignment demonstration")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(hardcodedTimeService), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []spec.BehavioralCase{{ID: "t", Request: spec.RequestShape{Method: "GET", Path: "/time"}, Expect: spec.ExpectShape{Status: 200, ContentType: "application/json", Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyRFC3339, Key: "time"}}}}}

	v, err := NativeAligner(llm.NewAnthropic(key, os.Getenv("ORION_MODEL")))(
		context.Background(), `Return the CURRENT time as JSON under a "time" key.`, dir, cases)
	if err != nil {
		t.Fatalf("align: %v", err)
	}
	t.Logf("alignment verdict: aligned=%v severity=%q concern=%q", v.Aligned, v.Severity, v.Concern)
	if v.Aligned {
		t.Errorf("the judge MISSED a hardcoded timestamp masquerading as the current time — the align-judge is the hard problem")
	}
}

// A CORRECT current-time service. The judge must NOT flag this — a paranoid judge
// that fails honest code is as useless as one that rubber-stamps drift.
const correctTimeService = `package main

import (
	"encoding/json"
	"net/http"
	"time"
)

func handleTime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"time": time.Now().UTC().Format(time.RFC3339)})
}

func main() { http.HandleFunc("/time", handleTime); _ = http.ListenAndServe(":8080", nil) }
`

// A SUBTLER drift: it IS the current time, but in LOCAL time when the intent said
// UTC. A genuine stress test of the judge's recall on non-obvious misalignment —
// logged, not asserted, because either outcome is informative.
const localTimeService = `package main

import (
	"encoding/json"
	"net/http"
	"time"
)

func handleTime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"time": time.Now().Format(time.RFC3339)})
}

func main() { http.HandleFunc("/time", handleTime); _ = http.ListenAndServe(":8080", nil) }
`

func runAligner(t *testing.T, intent, code string) AlignVerdict {
	t.Helper()
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("set ANTHROPIC_API_KEY to run the live alignment probes")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []spec.BehavioralCase{{ID: "t", Request: spec.RequestShape{Method: "GET", Path: "/time"}, Expect: spec.ExpectShape{Status: 200, ContentType: "application/json", Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyRFC3339, Key: "time"}}}}}
	v, err := NativeAligner(llm.NewAnthropic(key, os.Getenv("ORION_MODEL")))(context.Background(), intent, dir, cases)
	if err != nil {
		t.Fatalf("align: %v", err)
	}
	t.Logf("verdict: aligned=%v severity=%q concern=%q", v.Aligned, v.Severity, v.Concern)
	return v
}

// TestNativeAlignerPrecision (live): the judge must PASS correct code — no false
// positive — or it cannot be allowed to block.
func TestNativeAlignerPrecision(t *testing.T) {
	v := runAligner(t, `Return the CURRENT time as JSON under a "time" key.`, correctTimeService)
	if !v.Aligned {
		t.Errorf("FALSE POSITIVE: the judge flagged an honest current-time service — too paranoid to gate")
	}
}

// TestNativeAlignerSubtleDriftProbe (live, log-only): a recall stress test —
// local-time when UTC was intended.
func TestNativeAlignerSubtleDriftProbe(t *testing.T) {
	v := runAligner(t, `Return the current time in UTC as JSON under a "time" key.`, localTimeService)
	t.Logf("subtle drift (local-vs-UTC) → aligned=%v (informative either way)", v.Aligned)
}

// TestNativeAlignerParsesVerdict: the LLM judge's report_alignment tool call is
// decoded into an AlignVerdict.
func TestNativeAlignerParsesVerdict(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc handleTime() {}\n"), 0o644)

	prov := &fakeLLM{resp: []*llm.ChatResponse{alignResp(false, "high", "returns a hardcoded timestamp, not the current time")}}
	v, err := NativeAligner(prov)(context.Background(), "return the CURRENT time", dir, nil)
	if err != nil {
		t.Fatalf("align: %v", err)
	}
	if v.Aligned || v.Severity != "high" || v.Concern == "" {
		t.Fatalf("verdict not parsed: %+v", v)
	}
}

// TestAlignmentGateIsLogOnly: a build that PASSES proof, with an aligner that
// reports MISALIGNMENT, must record the concern but leave the verdict untouched —
// proof.Accept stays the sole right-to-ship (Step 1 is log-only, not blocking).
func TestAlignmentGateIsLogOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a service + proof")
	}
	oc, ctx := ratifiedTimeService(t) // canonical spec; fixture build proves green

	// An aligner that always reports misalignment (the LLM judge is mocked here; the
	// point is that a MISALIGNED verdict does not change a PASSING build).
	misaligned := func(context.Context, string, string, []spec.BehavioralCase) (AlignVerdict, error) {
		return AlignVerdict{Aligned: false, Severity: "high", Concern: "returns a constant"}, nil
	}
	res, err := BuildAndProve(ctx, oc.Store(), nil, misaligned, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.Verdict != "Accept" || !res.Closed {
		t.Fatalf("log-only alignment must NOT change a passing verdict: %+v", res)
	}
	if !res.Alignment.Ran || res.Alignment.Aligned {
		t.Fatalf("misalignment was not recorded: %+v", res.Alignment)
	}
}
