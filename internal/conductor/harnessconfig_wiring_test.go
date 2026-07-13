package conductor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/sandbox"
)

// TestGenerationPromptHonorsExternalConfig (or-kzf.2 DONE-WHEN): editing the
// preamble template and rules file changes the Conductor's generation prompt
// WITHOUT a rebuild — and the structural contract (the behavioral cases)
// survives the override.
func TestGenerationPromptHonorsExternalConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORION_HARNESS_DIR", dir)
	gs := sandbox.GenSpec{Module: "acme/svc", Route: "/time", Format: "json"}

	before := GenerationPrompt(gs, "write now")
	if !strings.Contains(before, "You are Orion's code generator") {
		t.Fatal("no config → the compiled default preamble")
	}

	if err := os.WriteFile(filepath.Join(dir, "generation_preamble.tmpl"),
		[]byte("ACME HOUSE STYLE for {{.Module}}: ship boring Go."), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rules.md"),
		[]byte("- all responses include a request id"), 0o644); err != nil {
		t.Fatal(err)
	}

	after := GenerationPrompt(gs, "write now")
	if !strings.Contains(after, "ACME HOUSE STYLE for acme/svc") {
		t.Fatalf("the edited template must change the prompt without a rebuild:\n%s", after)
	}
	if strings.Contains(after, "You are Orion's code generator") {
		t.Fatal("the compiled preamble must be REPLACED, not doubled")
	}
	if !strings.Contains(after, "all responses include a request id") {
		t.Fatal("rules.md must ride the prompt")
	}
	if !strings.Contains(after, "these ARE the contract") {
		t.Fatal("the structural case contract must survive the override")
	}
}
