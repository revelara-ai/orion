package conductor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestChangedFilesParsesPorcelainPaths: git porcelain lines are "XY PATH" with a fixed
// 2-col status field (often space-padded, e.g. " M pkg/x.go"). changedFiles must not trim
// that leading column, or the reported path is corrupted.
func TestChangedFilesParsesPorcelainPaths(t *testing.T) {
	dir := t.TempDir()
	gitc := func(args ...string) {
		t.Helper()
		full := append([]string{"-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=T"}, args...)
		if out, err := exec.Command("git", full...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg", "x.go"), []byte("package pkg\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitc("init", "-b", "main")
	gitc("add", ".")
	gitc("commit", "-m", "init")
	// Unstaged modification → porcelain " M pkg/x.go" (leading status space).
	if err := os.WriteFile(filepath.Join(dir, "pkg", "x.go"), []byte("package pkg\n\nfunc X() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := changedFiles(context.Background(), dir)
	if len(got) != 1 || got[0] != "pkg/x.go" {
		t.Fatalf("changedFiles = %q, want [\"pkg/x.go\"] (leading porcelain status col must not be trimmed)", got)
	}
}
