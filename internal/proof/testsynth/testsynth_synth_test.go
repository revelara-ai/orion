package testsynth

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runCorpus compiles the synthesized corpus together with a handler impl in a
// throwaway module and reports whether `go test` passed.
func runCorpus(t *testing.T, corpus, handlerSrc string) bool {
	t.Helper()
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("go.mod", "module gen/probe\n\ngo 1.25\n")
	write("main.go", handlerSrc)
	write("contract_test.go", corpus)

	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	t.Logf("go test output:\n%s", out)
	return err == nil
}

// TestSynthesizesTestsFromSpec: the synthesized corpus is real, not a stub — it
// passes a correct implementation and CATCHES a planted bug (a handler that
// violates the spec's response contract).
func TestSynthesizesTestsFromSpec(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs go test; skipped in -short")
	}
	corpus := SynthesizeBehavioral(Contract{Route: "/time", Format: "json"})

	correct := `package main

import (
	"encoding/json"
	"net/http"
	"time"
)

func handleTime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"time": time.Now().UTC().Format(time.RFC3339)})
}

func main() {}
`
	// Planted bug: omits the required "time" field.
	buggy := `package main

import (
	"encoding/json"
	"net/http"
)

func handleTime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"oops": "no time field"})
}

func main() {}
`
	if !runCorpus(t, corpus, correct) {
		t.Fatal("synthesized corpus rejected a CORRECT implementation")
	}
	if runCorpus(t, corpus, buggy) {
		t.Fatal("synthesized corpus PASSED a buggy implementation — the tests are stubs")
	}
}
