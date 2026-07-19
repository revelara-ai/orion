package newbehavior

import (
	"context"
	"os"
	"os/exec"
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

// TestVerifyCurateGolangciRejectsPlugin: a generated golangci config declaring a plugin makes
// the obligation fail closed (Executed=false) — curation rejects it before the command runs, so
// a malicious config can never certify (no sandbox needed: the reject precedes execution).
func TestVerifyCurateGolangciRejectsPlugin(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".golangci.yml"), "version: \"2\"\nlinters-settings:\n  custom:\n    evil:\n      path: ./e.so\n")
	mr, err := ProveNewBehavior(context.Background(), dir, []Case{
		{Modality: "verify_command", Verify: &VerifyCommand{
			Tool: "golangci-lint", Args: []string{"run", "--config", ".orion-golangci.yml"},
			CurateGolangci: true, MustExitZero: true,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if mr.Pass {
		t.Fatal("a plugin golangci config must fail closed (curation reject), not certify")
	}
	for _, st := range mr.Obligations {
		if st.Executed {
			t.Fatal("curation reject must leave the obligation un-executed")
		}
	}
}

// TestVerifyFileAssertion: the "file" pseudo-tool statically proves a Makefile target is defined
// and wired (no execution) — and fails on a missing target or a missing file.
func TestVerifyFileAssertion(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Makefile"), "lint:\n\tgolangci-lint run ./...\nvet:\n\tgo vet ./...\n")

	mr, err := ProveNewBehavior(context.Background(), dir, []Case{
		{Modality: "verify_command", Verify: &VerifyCommand{Tool: "file", Args: []string{"Makefile"},
			ConfigOKRE: `(?m)^lint:`, ConfigFailRE: `TODO`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !mr.Pass {
		t.Fatalf("a Makefile defining lint: should pass the file assertion: %+v", mr.Obligations)
	}

	mr2, _ := ProveNewBehavior(context.Background(), dir, []Case{
		{Modality: "verify_command", Verify: &VerifyCommand{Tool: "file", Args: []string{"Makefile"}, ConfigOKRE: `(?m)^deploy:`}},
	})
	if mr2.Pass {
		t.Fatal("asserting a target that isn't defined must fail")
	}

	mr3, _ := ProveNewBehavior(context.Background(), dir, []Case{
		{Modality: "verify_command", Verify: &VerifyCommand{Tool: "file", Args: []string{"nope.mk"}, ConfigOKRE: `x`}},
	})
	if mr3.Pass {
		t.Fatal("asserting on a missing file must fail")
	}
}

// TestVerifyCurateRequiresConfigArg: curate_golangci without --config <curated> fails closed —
// otherwise golangci-lint would CWD-pick the (uncurated) generated .golangci.yml. The check
// precedes execution, so no sandbox is needed.
func TestVerifyCurateRequiresConfigArg(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".golangci.yml"), "version: \"2\"\nlinters:\n  enable:\n    - staticcheck\n")
	mr, err := ProveNewBehavior(context.Background(), dir, []Case{
		{Modality: "verify_command", Verify: &VerifyCommand{Tool: "golangci-lint", Args: []string{"run", "./..."},
			CurateGolangci: true, MustExitZero: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if mr.Pass {
		t.Fatal("curate_golangci without --config <curated> must fail closed")
	}
	for _, st := range mr.Obligations {
		if st.Executed {
			t.Fatal("missing --config must leave the obligation un-executed (checked before run)")
		}
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

// TestVerifyGolangciConfigOfflineFallback (or-yyhq follow-on): a source-built
// golangci-lint has no embedded config schema, so `config verify` fails trying
// to FETCH it (network denied under proof) — a tool-build failure, not a config
// failure. proveVerify degrades to the offline-equivalent `linters --config`
// load: a VALID config passes, and a BROKEN config (unknown linter) still fails
// — the fallback must never weaken the gate. Both hold regardless of which
// golangci-lint build (embedded schema or not) is on the host.
func TestVerifyGolangciConfigOfflineFallback(t *testing.T) {
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint not on PATH")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not on host (verify tools refuse the none backend)")
	}
	ctx := context.Background()

	good := t.TempDir()
	write(t, filepath.Join(good, "go.mod"), "module good\n\ngo 1.24\n")
	write(t, filepath.Join(good, ".orion-golangci.yml"), "version: \"2\"\nlinters:\n  default: none\n  enable:\n    - staticcheck\n")
	mr, err := ProveNewBehavior(ctx, good, []Case{
		{Modality: "verify_command", Verify: &VerifyCommand{
			Tool: "golangci-lint", Args: []string{"config", "verify", "--config", ".orion-golangci.yml"},
			MustExitZero: true, ConfigValidates: true,
			ConfigFailRE: "(can't load|cannot load|unknown linter|unsupported version|invalid config)",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !mr.Pass {
		t.Fatalf("a valid config must verify (directly or via the offline fallback):\n%s", mr.Output)
	}

	bad := t.TempDir()
	write(t, filepath.Join(bad, "go.mod"), "module bad\n\ngo 1.24\n")
	write(t, filepath.Join(bad, ".orion-golangci.yml"), "version: \"2\"\nlinters:\n  default: none\n  enable:\n    - not-a-real-linter-xyz\n")
	mr, err = ProveNewBehavior(ctx, bad, []Case{
		{Modality: "verify_command", Verify: &VerifyCommand{
			Tool: "golangci-lint", Args: []string{"config", "verify", "--config", ".orion-golangci.yml"},
			MustExitZero: true, ConfigValidates: true,
			ConfigFailRE: "(can't load|cannot load|unknown linter|unsupported version|invalid config)",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if mr.Pass {
		t.Fatalf("a broken config must FAIL even through the offline fallback:\n%s", mr.Output)
	}
}
