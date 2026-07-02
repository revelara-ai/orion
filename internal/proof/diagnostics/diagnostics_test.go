package diagnostics

import (
	"strings"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeMod(t *testing.T, dir, mainGo string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainGo), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCheckPassesCleanCode: a compilable, vet-clean program checks OK.
func TestCheckPassesCleanCode(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go vet")
	}
	dir := t.TempDir()
	writeMod(t, dir, "package main\n\nfunc main() { _ = add(2, 3) }\n\nfunc add(a, b int) int { return a + b }\n")
	if r := Check(context.Background(), dir); !r.OK {
		t.Fatalf("clean code should pass diagnostics: %s", r.Output)
	}
}

// TestCheckFailsNonCompiling: code that doesn't compile fails fast with the compiler
// error in the output (so the refinement loop can feed it back).
func TestCheckFailsNonCompiling(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go vet")
	}
	dir := t.TempDir()
	writeMod(t, dir, "package main\n\nfunc main() { x := nope() }\n") // undefined: nope, unused x
	r := Check(context.Background(), dir)
	if r.OK {
		t.Fatal("non-compiling code must fail diagnostics")
	}
	if r.Output == "" {
		t.Fatal("expected compiler diagnostics in the output")
	}
}

// TestCheckFailsVetFinding: code that compiles but has a vet finding (a Printf format
// mismatch) is caught — the richer-than-compile signal.
func TestCheckFailsVetFinding(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go vet")
	}
	dir := t.TempDir()
	writeMod(t, dir, "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Printf(\"%d\\n\", \"not an int\") }\n")
	if r := Check(context.Background(), dir); r.OK {
		t.Fatal("a vet finding (Printf format mismatch) must fail diagnostics")
	}
}

// TestCheckEntry (or-v9f.3): the CLI entry contract is verified at go/parser
// altitude — missing or mis-signed run() yields one targeted diagnostic.
func TestCheckEntry(t *testing.T) {
	write := func(t *testing.T, src string) string {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	good := `package main
import ("io"; "os")
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, env map[string]string) int { return 0 }
func main() { os.Exit(run(nil, nil, nil, nil, nil)) }
`
	if r := CheckEntry(write(t, good), "run"); !r.OK {
		t.Fatalf("conforming entry rejected: %s", r.Output)
	}
	missing := `package main
func main() {}
`
	if r := CheckEntry(write(t, missing), "run"); r.OK || !strings.Contains(r.Output, "no run function") {
		t.Fatalf("missing entry must yield the targeted diagnostic, got %+v", r)
	}
	misSigned := `package main
func run(args []string) error { return nil }
func main() {}
`
	if r := CheckEntry(write(t, misSigned), "run"); r.OK || !strings.Contains(r.Output, "does not match") {
		t.Fatalf("mis-signed entry must yield the targeted diagnostic, got %+v", r)
	}
}
