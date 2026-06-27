package toolingcfg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/proof/proofexec"
	"github.com/revelara-ai/orion/internal/sandbox"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCurateRejectsPluginKey (slice 3): a config declaring a custom/module plugin is rejected
// (it would make golangci-lint load arbitrary code).
func TestCurateRejectsPluginKey(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, ".golangci.yml")
	writeFile(t, src, "linters:\n  enable:\n    - staticcheck\nlinters-settings:\n  custom:\n    evil:\n      path: ./evil.so\n")
	if _, err := CurateGolangciConfig(src, dir); err == nil || !strings.Contains(err.Error(), "custom") {
		t.Fatalf("a custom-plugin config must be rejected, got %v", err)
	}
	// plugins: section (v2) is also rejected.
	writeFile(t, src, "version: \"2\"\nplugins:\n  - module: example.com/evil\n")
	if _, err := CurateGolangciConfig(src, dir); err == nil {
		t.Fatal("a plugins: section must be rejected")
	}
}

// TestCurateCleanConfig: a clean config is curated to the Orion-controlled path, content intact.
func TestCurateCleanConfig(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, ".golangci.yml")
	writeFile(t, src, "linters:\n  enable:\n    - staticcheck\n")
	curated, err := CurateGolangciConfig(src, dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(curated) != CuratedConfigName {
		t.Fatalf("curated path should be %s, got %s", CuratedConfigName, curated)
	}
	got, _ := os.ReadFile(curated)
	if !strings.Contains(string(got), "staticcheck") {
		t.Fatalf("curated config lost content: %q", got)
	}
}

func TestCurateInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, ".golangci.yml")
	writeFile(t, src, "linters: [unclosed\n  bad: :\n")
	if _, err := CurateGolangciConfig(src, dir); err == nil {
		t.Fatal("invalid YAML must error")
	}
}

// TestCuratedConfigUsableUnderSandbox: the curated config is loadable by golangci-lint via
// --config inside the sandbox (proving curation + RunTool's --config plumbing end-to-end).
func TestCuratedConfigUsableUnderSandbox(t *testing.T) {
	if _, err := sandbox.New("bwrap"); err != nil {
		t.Skipf("bwrap unavailable: %v", err)
	}
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint not on PATH")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module probe\n\ngo 1.23\n")
	writeFile(t, filepath.Join(dir, "m.go"), "package probe\n\n// Add adds.\nfunc Add(a, b int) int { return a + b }\n")
	writeFile(t, filepath.Join(dir, ".golangci.yml"), "version: \"2\"\nlinters:\n  enable:\n    - staticcheck\n")

	curated, err := CurateGolangciConfig(filepath.Join(dir, ".golangci.yml"), dir)
	if err != nil {
		t.Fatal(err)
	}
	// Pass the curated config by a path relative to the workdir (resolves inside the sandbox).
	stdout, stderr, exit, err := proofexec.RunTool(context.Background(), dir, "golangci-lint", "run", "--config", filepath.Base(curated), "./...")
	if err != nil {
		t.Fatalf("RunTool: %v\n%s%s", err, stdout, stderr)
	}
	if exit != 0 {
		t.Fatalf("clean module + curated config should lint exit 0, got %d\n%s%s", exit, stdout, stderr)
	}
}
