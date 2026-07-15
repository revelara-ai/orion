package diagnostics

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestCheckerRegistry (or-4y7.4): Go is the default checker; an unregistered
// language resolves to nil (never a silent Go checker); the Go checker delegates
// to the verbatim go-vet path.
func TestCheckerRegistry(t *testing.T) {
	if For("") == nil || For("") != For("go") || For("go").Language() != "go" {
		t.Fatal(`For("") must resolve to the go checker (== For("go"))`)
	}
	if For("python") != nil {
		t.Fatal("an unregistered language must resolve to nil")
	}

	// The Go checker delegates to the package go-vet path: a clean module is OK,
	// and the method result matches the free function on the same input.
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module probe\n\ngo 1.23\n")
	write(t, filepath.Join(dir, "m.go"), "package probe\n\n// Add adds.\nfunc Add(a, b int) int { return a + b }\n")
	ctx := context.Background()
	if got, want := For("go").Check(ctx, dir).OK, Check(ctx, dir).OK; !got || got != want {
		t.Fatalf("goChecker.Check must delegate to Check: got %v want %v", got, want)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
