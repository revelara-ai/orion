package harnessconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPromptArtifactsHotReloadMidSession (or-0sk, A2): the externalized prompt
// artifacts (generation preamble, rules) are re-read from disk on EVERY render
// — editing one mid-session applies on the very next use with no restart and
// no /reload, and deleting it reverts to the compiled default immediately (a
// cached stale copy would break both directions).
func TestPromptArtifactsHotReloadMidSession(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ORION_HARNESS_DIR", dir)

	// Preamble v1 → renders v1.
	writeCfg(t, "generation_preamble.tmpl", "PREAMBLE v1 for {{.Module}}")
	if out, ok := GenerationPreamble(PreambleData{Module: "m"}); !ok || !strings.Contains(out, "PREAMBLE v1") {
		t.Fatalf("v1 should render: ok=%v out=%q", ok, out)
	}
	// EDIT mid-session → the very next render serves v2 (no cache, no reload).
	writeCfg(t, "generation_preamble.tmpl", "PREAMBLE v2 for {{.Module}}")
	out, ok := GenerationPreamble(PreambleData{Module: "m"})
	if !ok || !strings.Contains(out, "PREAMBLE v2") {
		t.Fatalf("an edited preamble must apply on the next render: ok=%v out=%q", ok, out)
	}
	if strings.Contains(out, "PREAMBLE v1") {
		t.Fatalf("stale v1 content served after the edit (cached?): %q", out)
	}
	// DELETE mid-session → immediately back to the compiled default (ok=false),
	// never a stale copy of v2.
	if err := os.Remove(filepath.Join(dir, "generation_preamble.tmpl")); err != nil {
		t.Fatal(err)
	}
	if _, ok := GenerationPreamble(PreambleData{Module: "m"}); ok {
		t.Fatal("a deleted preamble must revert to the compiled default on the next render")
	}

	// Rules: same contract.
	writeCfg(t, "rules.md", "RULE ONE")
	if r := Rules("k"); r != "RULE ONE" {
		t.Fatalf("rules v1: %q", r)
	}
	writeCfg(t, "rules.md", "RULE TWO")
	if r := Rules("k"); r != "RULE TWO" {
		t.Fatalf("edited rules must apply on the next read, got %q", r)
	}
	if err := os.Remove(filepath.Join(dir, "rules.md")); err != nil {
		t.Fatal(err)
	}
	if r := Rules("k"); r != "" {
		t.Fatalf("deleted rules must vanish on the next read, got %q", r)
	}
}
