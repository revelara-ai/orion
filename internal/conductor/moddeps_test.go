package conductor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// or-mkxd: ensureModDeps provisions a generated change's module deps
// HOST-side (network allowed here) so the hermetic proof env can build from
// the pre-fetched cache. Contract testable without network: `go mod tidy`
// drops an unused require — the helper runs, reports the change, leaves the
// module consistent.
func TestEnsureModDepsTidiesModule(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the go tool")
	}
	dir := t.TempDir()
	must := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("go.mod", "module example.com/t\n\ngo 1.25\n\nrequire example.com/unused v1.0.0\n")
	must("go.sum", "")
	must("main.go", "package main\n\nfunc main() {}\n")

	changed, err := ensureModDeps(t.Context(), dir)
	if err != nil {
		t.Fatalf("ensureModDeps: %v", err)
	}
	if !changed {
		t.Fatal("dropping an unused require must report changed=true")
	}
	mod, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	if string(mod) == "" || filepath.Base(dir) == "" {
		t.Fatal("sanity")
	}
	if got := string(mod); len(got) > 0 && strings.Contains(got, "example.com/unused") {
		t.Fatalf("tidy must drop the unused require, go.mod:\n%s", got)
	}

	// Idempotent: a second run reports no change.
	changed, err = ensureModDeps(t.Context(), dir)
	if err != nil {
		t.Fatalf("second ensureModDeps: %v", err)
	}
	if changed {
		t.Fatal("an already-tidy module must report changed=false")
	}
}
