package proofexec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPyInterpForResolutionOrder (or-4y7.10): ORION_PYTHON (operator pin) →
// the workdir's .python-version stack pin → python3. The stack pin resolves at
// interpreter-binary granularity (3.12.4 → python3.12).
func TestPyInterpForResolutionOrder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".python-version"), []byte("3.12.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORION_PYTHON", "")
	if got := pyInterpFor(dir); got != "python3.12" {
		t.Fatalf("stack pin must resolve to python3.12, got %q", got)
	}
	if got := pyInterpFor(t.TempDir()); got != "python3" {
		t.Fatalf("no pin must resolve to python3, got %q", got)
	}
	t.Setenv("ORION_PYTHON", "/opt/custom/python")
	if got := pyInterpFor(dir); got != "/opt/custom/python" {
		t.Fatalf("ORION_PYTHON must win over the stack pin, got %q", got)
	}
}

// TestPyMajorMinor (or-4y7.10): version-line parsing at binary granularity.
func TestPyMajorMinor(t *testing.T) {
	for raw, want := range map[string]string{
		"3.12.4\n": "3.12", "3.12": "3.12", "3": "3",
		"pypy3.10": "", "3.x": "", "": "", "  3.11  \n3.9": "3.11",
	} {
		if got := pyMajorMinor(raw); got != want {
			t.Errorf("pyMajorMinor(%q) = %q, want %q", raw, got, want)
		}
	}
}

// TestPyPinnedVersionMissingRefusesLoudly (or-4y7.10): a stack-pinned version
// the host lacks REFUSES with the missing interpreter named and an install
// hint — never a silent substitution with whatever python3 is around.
func TestPyPinnedVersionMissingRefusesLoudly(t *testing.T) {
	requirePython(t)
	t.Setenv("ORION_PYTHON", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".python-version"), []byte("9.99\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := RunTool(context.Background(), dir, "python", "python3", "-m", "py_compile")
	if err == nil || !strings.Contains(err.Error(), "python9.99") || !strings.Contains(err.Error(), "install") {
		t.Fatalf("a missing pinned interpreter must refuse naming it with an install hint, got %v", err)
	}
}
