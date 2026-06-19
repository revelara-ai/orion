package empirical

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/proof/testsynth"
)

// greetService is an ARBITRARY (non-time) HTTP service: a different route and a
// different JSON shape. The empirical adapter must probe it from the spec.
const greetService = `package main

import (
	"encoding/json"
	"net/http"
	"os"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/greet", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "hello"})
	})
	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}
	_ = http.ListenAndServe(addr, mux)
}
`

// TestEmpiricalAdapterForArbitraryService: the empirical Lookout builds, runs, and
// probes a service that is NOT the time-service — proving the adapter generalizes
// from the spec (route + required JSON key) rather than hardcoding /time.
func TestEmpiricalAdapterForArbitraryService(t *testing.T) {
	if testing.Short() {
		t.Skip("builds + runs a service; skipped in -short")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module gen/greet\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(greetService), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	mode, pr, err := Prove(ctx, dir, testsynth.Contract{Route: "/greet", Format: "json", RequiredJSONKey: "message"})
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	if !pr.PortOpen {
		t.Fatalf("arbitrary service never served: %s", pr.Detail)
	}
	if !mode.Pass || !pr.ResponseContractSatisfied {
		t.Fatalf("empirical adapter failed on arbitrary service: %+v", pr)
	}
}
