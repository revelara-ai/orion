package hazard

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
)

// TestProberRegistry (or-4y7.7): Go is the default prober; an unregistered
// language resolves to nil (never a silent Go prober).
func TestProberRegistry(t *testing.T) {
	if proberFor("") == nil || proberFor("") != proberFor("go") || proberFor("go").Language() != "go" {
		t.Fatal(`proberFor("") must resolve to the go prober`)
	}
	if proberFor("python") != nil {
		t.Fatal("an unregistered language must resolve to nil")
	}
}

// TestGoProberScansAllSource (or-4y7.7): the Go prober reads control-bearing
// source from ANY package file, not just main.go — a control expressed in a
// subpackage is now seen (fixing the latent multi-file miss). Single-file
// artifacts are byte-identical.
func TestGoProberScansAllSource(t *testing.T) {
	pr := proberFor("go")
	uca := stpa.UCA{Verify: []string{"ReadHeaderTimeout"}}

	// The control lives in a NON-main package file (the old main.go-only read
	// would miss it and wrongly report the UCA uncontrolled).
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "server"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "internal", "server", "s.go"),
		[]byte("package server\n// ReadHeaderTimeout guards slowloris.\nvar _ = \"ReadHeaderTimeout\"\n"), 0o644)
	if !pr.ControlPresent(pr.SourceText(dir), uca) {
		t.Fatal("a control in a subpackage file must be found (multi-file fix)")
	}

	// Test files are NOT part of the control surface.
	dir2 := t.TempDir()
	os.WriteFile(filepath.Join(dir2, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644)
	os.WriteFile(filepath.Join(dir2, "x_test.go"), []byte("package main\n// ReadHeaderTimeout\n"), 0o644)
	if pr.ControlPresent(pr.SourceText(dir2), uca) {
		t.Fatal("a token only in a _test.go file must NOT count as control present")
	}
}
