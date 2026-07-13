package conductor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// or-nkcf: the generated analyzer subtree is the ONE directory the flat
// export carries — both ship vehicles (repo export + git delivery) route here.
func TestExportCarriesAnalyzer(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte("package main\nfunc main(){}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(src, "cmd", "analyze"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "cmd", "analyze", "main.go"), []byte("package main\nfunc main(){}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A scratch dir that must STAY excluded (the flat skip is deliberate).
	if err := os.MkdirAll(filepath.Join(src, "proofscratch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "proofscratch", "x.go"), []byte("package x\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	written, err := ExportProvenCode(src, dest, spec.ExecutableSpec{Intent: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "cmd", "analyze", "main.go")); err != nil {
		t.Fatalf("analyzer did not export: %v (written: %v)", err, written)
	}
	if _, err := os.Stat(filepath.Join(dest, "proofscratch")); !os.IsNotExist(err) {
		t.Fatal("scratch dir leaked into the export")
	}
}
