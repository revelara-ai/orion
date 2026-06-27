package conductor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/sandbox"
)

// typeErrorTimeService is structurally a Go service but contains a type error (string assigned
// to an int), so it does not compile — gopls flags it and the proof harness could not run it.
const typeErrorTimeService = `package main

import (
	"net/http"
	"os"
)

func handleTime(w http.ResponseWriter, r *http.Request) {
	var status int = "200" // TYPE ERROR: untyped string constant as int
	w.WriteHeader(status)
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

func writeTypeErrorService(dir string, gs sandbox.GenSpec) (sandbox.GeneratedArtifact, error) {
	if _, err := sandbox.GenerateTimeServiceFixture(dir, gs); err != nil {
		return sandbox.GeneratedArtifact{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(typeErrorTimeService), 0o644); err != nil {
		return sandbox.GeneratedArtifact{}, err
	}
	return sandbox.ArtifactFromDir(dir)
}

// TestBuildAndProveLSPGateCatchesTypeError (or-ykz.11): a generated file with a type error is
// flagged by the LSP diagnostics gate WITHIN the coding loop, before the behavioral proof
// runs. Attempt 1 ships uncompilable code; the gate (gopls) surfaces the diagnostic as
// feedback — never reaching the proof harness — and attempt 2 ships the correct service and
// proves Accept. Without the gate, the type error would hard-error the proof exec instead of
// converging.
func TestBuildAndProveLSPGateCatchesTypeError(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + proves a service; skipped in -short")
	}
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH — the LSP gate is a graceful no-op")
	}
	oc, ctx := ratifiedTimeService(t)

	var gotFeedback string
	gen := func(_ context.Context, gs sandbox.GenSpec, dir, feedback string) (sandbox.GeneratedArtifact, error) {
		if feedback == "" {
			return writeTypeErrorService(dir, gs) // attempt 1: type error → caught by the gate
		}
		gotFeedback = feedback
		return sandbox.GenerateTimeServiceFixture(dir, gs) // attempt 2: correct → Accept
	}

	res, err := BuildAndProve(ctx, oc.Store(), gen, nil, nil, "")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.Verdict != "Accept" || !res.Closed {
		t.Fatalf("the gate should feed the type error back and converge to Accept: %+v", res)
	}
	if !strings.Contains(gotFeedback, "language-server diagnostics") {
		t.Fatalf("attempt 2 should have received LSP diagnostics as feedback (proving the gate ran before proof):\n%s", gotFeedback)
	}
}
