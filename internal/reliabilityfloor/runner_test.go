package reliabilityfloor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestGoDirs(t *testing.T) {
	got := GoDirs([]string{"internal/a/x.go", "internal/a/y.go", "README.md", "cmd/z/main.go"})
	want := []string{"cmd/z", "internal/a"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GoDirs=%v want %v", got, want)
	}
}

func TestRunLintNilArgsSkips(t *testing.T) {
	r := RunLint(context.Background(), t.TempDir(), nil)
	if r.Ran || r.Skipped == "" {
		t.Fatalf("nil args must skip, got %+v", r)
	}
}

func TestRunLintMissingBinarySkips(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // empty dir: LookPath must fail
	r := RunLint(context.Background(), t.TempDir(), []string{"run"})
	if r.Ran || r.Skipped == "" {
		t.Fatalf("missing binary must skip, got %+v", r)
	}
}

// TestRunLintScrubsEnv proves the runner uses safeenv, not os.Environ: a fake
// golangci-lint that dumps its environment must not see host secrets.
func TestRunLintScrubsEnv(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\nenv\n"
	if err := os.WriteFile(filepath.Join(bin, "golangci-lint"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("ORION_TEST_SECRET", "leak-me")
	r := RunLint(context.Background(), t.TempDir(), []string{"run"})
	if !r.Ran {
		t.Fatalf("expected a run, got %+v", r)
	}
	if strings.Contains(r.Output, "ORION_TEST_SECRET") {
		t.Fatalf("host env leaked into the lint subprocess:\n%s", r.Output)
	}
}

func TestRunLintExecutesWhenInstalled(t *testing.T) {
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint not installed")
	}
	// This repo is a valid module; running an empty-target set is a Skip, so target this pkg dir.
	r := RunLint(context.Background(), ".", LintArgs(
		[]Signal{{Check: Check{Kind: CheckGolangciLint, Linters: []string{"errcheck"}}}},
		[]string{"."}))
	if !r.Ran {
		t.Fatalf("expected a run, got %+v", r)
	}
}
