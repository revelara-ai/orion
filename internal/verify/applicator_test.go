package verify

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyRejectsRelativePath(t *testing.T) {
	a := Applicator{}
	if err := a.Apply(context.Background(), "rel/path", "x"); !errors.Is(err, ErrInvalidInputs) {
		t.Errorf("expected ErrInvalidInputs, got %v", err)
	}
}

func TestApplyRejectsEmptyDiff(t *testing.T) {
	a := Applicator{}
	dir := t.TempDir()
	if err := a.Apply(context.Background(), dir, ""); !errors.Is(err, ErrPatchApply) {
		t.Errorf("expected ErrPatchApply for empty diff, got %v", err)
	}
}

func TestApplyAndResetWorkAgainstLocalRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not in PATH")
	}
	dir := t.TempDir()
	mustRun := func(name string, args ...string) {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
	}
	mustRun("git", "init", "-b", "main", ".")
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mustRun("git", "add", ".")
	mustRun("git", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-m", "init")

	diff := `--- a/hello.txt
+++ b/hello.txt
@@ -1 +1 @@
-hello
+hello orion
`
	a := Applicator{}
	if err := a.Apply(context.Background(), dir, diff); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if !strings.Contains(string(body), "orion") {
		t.Errorf("apply did not modify file: %q", string(body))
	}
	if err := a.Reset(context.Background(), dir); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	body, _ = os.ReadFile(filepath.Join(dir, "hello.txt"))
	if strings.Contains(string(body), "orion") {
		t.Errorf("reset did not undo apply: %q", string(body))
	}
}

func TestBuildRejectsRelativePath(t *testing.T) {
	a := Applicator{}
	if err := a.Build(context.Background(), "rel"); !errors.Is(err, ErrInvalidInputs) {
		t.Errorf("expected ErrInvalidInputs, got %v", err)
	}
}
