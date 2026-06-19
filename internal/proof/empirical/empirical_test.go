package empirical

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// TestEmpiricalProbesRunningService: a conforming service opens its port and
// satisfies the contract.
func TestEmpiricalProbesRunningService(t *testing.T) {
	dir := t.TempDir()
	if _, err := sandbox.GenerateFixtureService(dir, sandbox.GenSpec{Route: "/time", Port: 8080, Format: "json", TimeZone: "UTC"}); err != nil {
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
