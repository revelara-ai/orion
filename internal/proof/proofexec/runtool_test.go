package proofexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeTinyModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "go.mod"), "module probe\n\ngo 1.23\n")
	mustWrite(t, filepath.Join(dir, "m.go"), "package probe\n\n// Add adds two ints.\nfunc Add(a, b int) int { return a + b }\n")
	return dir
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRunToolRejectsDisallowedTool (or-75c): only the allowlist runs.
func TestRunToolRejectsDisallowedTool(t *testing.T) {
	_, _, _, err := RunTool(context.Background(), t.TempDir(), "bash", "-c", "echo hi")
	if err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("a non-allowlisted tool must be rejected, got %v", err)
	}
}

// TestRunToolRejectsMake: make is NOT on the allowlist — it can't load in the lib-less sandbox
// and its $(shell ...) parse-time expansion is an exec vector; Makefiles are proven statically.
func TestRunToolRejectsMake(t *testing.T) {
	if _, _, _, err := RunTool(context.Background(), t.TempDir(), "make", "-n", "build"); err == nil ||
		!strings.Contains(err.Error(), "allowlist") {
		t.Fatal("make must be rejected as non-allowlisted")
	}
}

// TestRunToolRejectsGoRunGenerate: the go arm refuses subcommands that compile-and-run arbitrary
// code (go run / go generate / go get / go install / go tool), even though `go` is allowlisted.
func TestRunToolRejectsGoRunGenerate(t *testing.T) {
	for _, sub := range []string{"run", "generate", "get", "install", "tool"} {
		_, _, _, err := RunTool(context.Background(), t.TempDir(), "go", sub, "./...")
		if err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Errorf("`go %s` must be rejected, got %v", sub, err)
		}
	}
}

// TestRunToolFailsClosedForNonGoUnderNone: a non-go tool over generated content must not run
// without namespace isolation.
func TestRunToolFailsClosedForNonGoUnderNone(t *testing.T) {
	t.Setenv("ORION_SANDBOX_ISOLATION", "none")
	_, _, _, err := RunTool(context.Background(), t.TempDir(), "golangci-lint", "config", "path")
	if err == nil || !strings.Contains(err.Error(), "namespace sandbox") {
		t.Fatalf("non-go tool under 'none' backend must fail closed, got %v", err)
	}
}

// TestRunToolGolangciLintSandboxed: golangci-lint runs over a clean module under the namespace
// sandbox (proving the GOROOT + host-binary ro-binds + scrubbed env work end-to-end).
func TestRunToolGolangciLintSandboxed(t *testing.T) {
	requireBwrap(t)
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint not on PATH")
	}
	dir := writeTinyModule(t)
	stdout, stderr, exit, err := RunTool(context.Background(), dir, "golangci-lint", "run", "./...")
	if err != nil {
		t.Fatalf("golangci-lint under sandbox failed to launch: %v\n%s%s", err, stdout, stderr)
	}
	if exit != 0 {
		t.Fatalf("a clean module should lint exit 0, got %d\n%s%s", exit, stdout, stderr)
	}
}
