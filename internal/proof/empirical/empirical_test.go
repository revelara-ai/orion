package empirical

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// TestEmpiricalProbesRunningService: a conforming service opens its port and
// satisfies the contract.
func TestEmpiricalProbesRunningService(t *testing.T) {
	dir := t.TempDir()
	if _, err := sandbox.GenerateTimeServiceFixture(dir, sandbox.GenSpec{Module: "orion-generated/svc", Route: "/time", Port: 8080, Format: "json", TimeZone: "UTC"}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	mr, pr, err := Prove(context.Background(), dir, testsynth.Contract{Route: "/time", Format: "json", TimeZone: "UTC"})
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	if !pr.PortOpen || !pr.ResponseContractSatisfied || !mr.Pass {
		t.Fatalf("conforming service failed empirical: %+v (%s)", pr, mr.Output)
	}
}

// TestEmpiricalCatchesNonServingArtifact: an artifact whose handler is correct
// (behavioral would pass) but whose main() never serves fails empirically.
func TestEmpiricalCatchesNonServingArtifact(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module noserve\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// handleTime is correct, but main() exits immediately — nothing listens.
	src := `package main
import ("encoding/json";"net/http";"time")
func handleTime(w http.ResponseWriter, r *http.Request){
	w.Header().Set("Content-Type","application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"time": time.Now().UTC().Format(time.RFC3339)})
}
func main(){ _ = handleTime } // never serves
`
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	_, pr, err := Prove(context.Background(), dir, testsynth.Contract{Route: "/time", Format: "json", TimeZone: "UTC"})
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	if pr.PortOpen {
		t.Fatalf("non-serving artifact reported port open: %+v", pr)
	}
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestVarianceIsInconclusive (or-v9f.20): a service that flickers between probe
// rounds must converge Inconclusive — a flaky pass never reads as Accept.
func TestVarianceIsInconclusive(t *testing.T) {
	t.Setenv("ORION_PROOF_RUN_COUNT", "3")
	dir := t.TempDir()
	// The handler alternates 200/500 per REQUEST: round 1 passes, round 2 fails.
	flaky := `package main

import (
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
)

var n int64

func handleTime(w http.ResponseWriter, r *http.Request) {
	// Monotonic flicker: healthy for the first 5 requests, then broken forever.
	// Probe rounds' graded requests strictly increase across rounds, so round 1
	// passes (warmup+graded <= 5) and a later round fails — variance by design,
	// robust to the readiness loop's variable warmup count.
	if atomic.AddInt64(&n, 1) > 5 {
		w.WriteHeader(500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, ` + "`" + `{"time":"2026-01-01T00:00:00Z"}` + "`" + `)
}

func main() {
	http.HandleFunc("/time", handleTime)
	_ = http.ListenAndServe(":"+os.Getenv("PORT"), nil)
}
`
	writeFile(t, dir, "go.mod", "module flaky\n\ngo 1.23\n")
	writeFile(t, dir, "main.go", flaky)

	mode, _, err := Prove(context.Background(), dir, testsynth.Contract{Route: "/time", Format: "json", TimeZone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if mode.Pass {
		t.Fatal("a flickering service must not pass the empirical mode")
	}
	if !mode.Inconclusive {
		t.Fatalf("mixed probe rounds must converge Inconclusive, got %+v", mode)
	}
	if mode.Metrics["run_count"] != 3 {
		t.Errorf("run_count must record the real rounds, got %v", mode.Metrics["run_count"])
	}
	if r := mode.Metrics["empirical_pass_rate"]; r <= 0 || r >= 1 {
		t.Errorf("a mixed outcome has a fractional pass rate, got %v", r)
	}
	if !strings.Contains(mode.Output, "VARIANCE") {
		t.Errorf("the output must name the variance, got: %s", mode.Output)
	}
}
