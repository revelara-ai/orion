package newbehavior

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestVerifyCommandPasses (or-7ox): a clean verify command (exit 0, MustExitZero) makes the
// new-behavior verdict pass.
func TestVerifyCommandPasses(t *testing.T) {
	mr, err := ProveNewBehavior(context.Background(), t.TempDir(), []Case{
		{Modality: "verify_command", Verify: &VerifyCommand{Tool: "go", Args: []string{"version"}, MustExitZero: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !mr.Pass {
		t.Fatalf("a passing verify case should make mr.Pass; obligations=%+v\n%s", mr.Obligations, mr.Output)
	}
}

// TestVerifyMustExitZeroFails: a non-zero exit with MustExitZero does not pass.
func TestVerifyMustExitZeroFails(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module brk\n\ngo 1.23\n")
	write(t, filepath.Join(dir, "m.go"), "package brk\n\nvar _ = undefinedSymbol\n") // does not compile
	mr, err := ProveNewBehavior(context.Background(), dir, []Case{
		{Modality: "verify_command", Verify: &VerifyCommand{Tool: "go", Args: []string{"build", "./..."}, MustExitZero: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if mr.Pass {
		t.Fatal("a non-zero exit with MustExitZero must NOT pass")
	}
}

// TestVerifyConfigValidatesIndependentOfExit: ConfigValidates is decoupled from the exit code —
// a clean exit still fails if ConfigFailRE matches or ConfigOKRE does not (catching a tool that
// silently fell back to defaults).
func TestVerifyConfigValidatesIndependentOfExit(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// exit 0, but ConfigFailRE matches the output → must fail.
	mr, _ := ProveNewBehavior(ctx, dir, []Case{
		{Modality: "verify_command", Verify: &VerifyCommand{Tool: "go", Args: []string{"version"}, MustExitZero: true, ConfigValidates: true, ConfigFailRE: "go version"}},
	})
	if mr.Pass {
		t.Fatal("ConfigFailRE match must fail even on a clean exit")
	}

	// exit 0, but ConfigOKRE does not match → must fail (silent-default fallback caught).
	mr2, _ := ProveNewBehavior(ctx, dir, []Case{
		{Modality: "verify_command", Verify: &VerifyCommand{Tool: "go", Args: []string{"version"}, ConfigValidates: true, ConfigOKRE: "THIS_STRING_NEVER_APPEARS"}},
	})
	if mr2.Pass {
		t.Fatal("ConfigOKRE non-match must fail")
	}

	// exit 0 + ConfigOKRE matches + no FailRE → pass.
	mr3, _ := ProveNewBehavior(ctx, dir, []Case{
		{Modality: "verify_command", Verify: &VerifyCommand{Tool: "go", Args: []string{"version"}, MustExitZero: true, ConfigValidates: true, ConfigOKRE: "go version"}},
	})
	if !mr3.Pass {
		t.Fatalf("exit 0 + ConfigOKRE match should pass; %+v", mr3.Obligations)
	}
}

// TestVerifyEmptyInconclusive: no declared cases → Inconclusive, never a silent pass.
func TestVerifyEmptyInconclusive(t *testing.T) {
	mr, err := ProveNewBehavior(context.Background(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !mr.Inconclusive || mr.Pass {
		t.Fatalf("empty case set should be Inconclusive (not Pass): %+v", mr)
	}
}
