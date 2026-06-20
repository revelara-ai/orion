package proof

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
)

// A tz-aware time service that SATISFIES the behavioral cases: ?tz=zone returns
// that zone's time, invalid tz → 400 JSON error, default UTC. Embeds tzdata so
// LoadLocation works regardless of the host.
const tzService = `package main

import (
	"encoding/json"
	"net/http"
	"os"
	"time"
	_ "time/tzdata"
)

func handleTime(w http.ResponseWriter, r *http.Request) {
	loc := time.UTC
	if tz := r.URL.Query().Get("tz"); tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid timezone"})
			return
		}
		loc = l
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"time": time.Now().In(loc).Format(time.RFC3339)})
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/time", handleTime)
	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}
	_ = http.ListenAndServe(addr, mux)
}
`

// A service that IGNORES tz (always UTC, never 400) — the or-y9d shape: it serves
// the happy path but not the stated tz behavior.
const tzIgnoringService = `package main

import (
	"encoding/json"
	"net/http"
	"os"
	"time"
	_ "time/tzdata"
)

func handleTime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"time": time.Now().UTC().Format(time.RFC3339)})
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/time", handleTime)
	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}
	_ = http.ListenAndServe(addr, mux)
}
`

func writeService(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module tzsvc\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func tzCasesContract() testsynth.Contract {
	return testsynth.Contract{Route: "/time", Format: "json", TimeZone: "UTC", Cases: []spec.BehavioralCase{
		{ID: "default", Request: spec.RequestShape{Method: "GET", Path: "/time"}, Expect: spec.ExpectShape{Status: 200, ContentType: "application/json", Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyRFC3339, Key: "time"}}}},
		{ID: "tz_ny", Request: spec.RequestShape{Method: "GET", Path: "/time", Query: map[string]string{"tz": "America/New_York"}}, Expect: spec.ExpectShape{Status: 200, ContentType: "application/json", Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyInTZ, Key: "time", Value: "America/New_York"}}}},
		{ID: "tz_bad", Request: spec.RequestShape{Method: "GET", Path: "/time", Query: map[string]string{"tz": "Bogus/Zone"}}, Expect: spec.ExpectShape{Status: 400, ContentType: "application/json", Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONErrorPresent}}}},
	}}
}

// TestProveExecutesCasesSatisfied: a service that implements the tz cases makes all
// three obligations execute AND pass in both modes → Accept.
func TestProveExecutesCasesSatisfied(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a service + proof")
	}
	rep, err := Prove(context.Background(), writeService(t, tzService), tzCasesContract())
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	for _, id := range []string{"default", "tz_ny", "tz_bad"} {
		o := rep.ObligationResults[id]
		if !o.Executed || !o.Passed {
			t.Errorf("obligation %q: executed=%v passed=%v (want both true); detail=%+v", id, o.Executed, o.Passed, rep.ObligationResults)
		}
	}
	if rep.Outcome.Verdict != "Accept" {
		t.Fatalf("verdict = %s, want Accept", rep.Outcome.Verdict)
	}
}

// TestProveExecutesCasesFalsePassKilled: the or-y9d scenario — a service that
// ignores tz. The tz cases EXECUTE but FAIL, so the verdict is not Accept (the
// proof now reflects the spec, not just the happy path).
func TestProveExecutesCasesFalsePassKilled(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a service + proof")
	}
	rep, err := Prove(context.Background(), writeService(t, tzIgnoringService), tzCasesContract())
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	// The happy path still passes; the tz behaviors are executed but fail.
	for _, id := range []string{"tz_ny", "tz_bad"} {
		o := rep.ObligationResults[id]
		if !o.Executed {
			t.Errorf("obligation %q should have EXECUTED (a coverage hole would be worse)", id)
		}
		if o.Passed {
			t.Errorf("obligation %q passed against a tz-ignoring service — false pass not killed", id)
		}
	}
	if rep.Outcome.Verdict == "Accept" {
		t.Fatal("a service that ignores the stated tz behavior must NOT converge to Accept")
	}
}
