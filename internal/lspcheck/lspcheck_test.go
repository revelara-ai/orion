package lspcheck

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeModule(t *testing.T, mainSrc string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module lsptest\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func requireGopls(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH")
	}
}

// TestDiagnoseFlagsTypeError (or-ykz.11): a generated file with a type error is flagged by the
// diagnostics gate (the DONE-WHEN). Also confirms gopls runs under the scrubbed safeenv.
func TestDiagnoseFlagsTypeError(t *testing.T) {
	requireGopls(t)
	dir := writeModule(t, "package main\n\nfunc main() {\n\tvar x int = \"nope\"\n\t_ = x\n}\n")
	res, err := Diagnose(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Skipped {
		t.Skip("gopls unusable in this environment")
	}
	if res.OK() {
		t.Fatal("expected a diagnostic for the type error, got none")
	}
	found := false
	for _, d := range res.Diagnostics {
		if filepath.Base(d.File) == "main.go" {
			found = true
		}
	}
	if !found {
		t.Fatalf("diagnostic did not reference main.go: %+v", res.Diagnostics)
	}
}

// TestDiagnoseCleanCode: well-typed code yields no diagnostics.
func TestDiagnoseCleanCode(t *testing.T) {
	requireGopls(t)
	dir := writeModule(t, "package main\n\nfunc main() { _ = 1 }\n")
	res, err := Diagnose(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Skipped && !res.OK() {
		t.Fatalf("clean code should have no diagnostics, got %+v", res.Diagnostics)
	}
}

// TestParseLine: the gopls output parser splits location from message at the first colon-space.
func TestParseLine(t *testing.T) {
	d := parseLine(`/tmp/x/main.go:4:14-26: cannot use "x" (untyped string) as int value`)
	if d.File != "/tmp/x/main.go" || d.Loc != "4:14-26" {
		t.Fatalf("parseLine: got file=%q loc=%q", d.File, d.Loc)
	}
	if d.Msg == "" {
		t.Fatal("parseLine: empty message")
	}
}
