package harnessconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCfg(t *testing.T, name, content string) string {
	t.Helper()
	dir := os.Getenv("ORION_HARNESS_DIR")
	if dir == "" {
		dir = t.TempDir()
		t.Setenv("ORION_HARNESS_DIR", dir)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestGenerationPreambleOverride (or-kzf.2): a reviewable template file
// replaces the compiled preamble — with template data — and an invalid or
// absent file falls back loudly to the compiled default.
func TestGenerationPreambleOverride(t *testing.T) {
	t.Setenv("ORION_HARNESS_DIR", t.TempDir())
	if _, ok := GenerationPreamble(PreambleData{Module: "m"}); ok {
		t.Fatal("absent file must fall back to the compiled default")
	}
	writeCfg(t, "generation_preamble.tmpl", "TEAM PREAMBLE for {{.Module}} entry {{.Entry}}")
	out, ok := GenerationPreamble(PreambleData{Module: "acme/svc", Entry: "handleX"})
	if !ok || !strings.Contains(out, "TEAM PREAMBLE for acme/svc entry handleX") {
		t.Fatalf("override must render with data: ok=%v out=%q", ok, out)
	}
	// Invalid template (unknown field) → fallback, and doctor's Validate names it.
	writeCfg(t, "generation_preamble.tmpl", "{{.NoSuchField}}")
	if _, ok := GenerationPreamble(PreambleData{Module: "m"}); ok {
		t.Fatal("an invalid template must fall back, never half-apply")
	}
	if errs := Validate(); len(errs) != 1 || !strings.Contains(errs[0].Error(), "generation_preamble.tmpl") {
		t.Fatalf("Validate must name the broken file: %v", errs)
	}
}

// TestChecklistsParseAndValidate (or-kzf.2): the closed dimension vocabulary
// and key/question requirements hold; a valid file loads.
func TestChecklistsParseAndValidate(t *testing.T) {
	t.Setenv("ORION_HARNESS_DIR", t.TempDir())
	if _, ok := LoadChecklists("k"); ok {
		t.Fatal("absent checklists must fall back")
	}
	writeCfg(t, "checklists.yaml", `
functional:
  http-service:
    - {key: auth_model, dimension: security, question: "What auth?", fallback: "none"}
universal:
  - {key: slo, dimension: slo, question: "What SLO?", fallback: "tier default"}
`)
	c, ok := LoadChecklists("k")
	if !ok || len(c.Functional["http-service"]) != 1 || len(c.Universal) != 1 {
		t.Fatalf("valid checklists must load: ok=%v %+v", ok, c)
	}
	writeCfg(t, "checklists.yaml", `
universal:
  - {key: x, dimension: made-up, question: "?"}
`)
	if _, ok := LoadChecklists("k"); ok {
		t.Fatal("an unknown dimension must reject the file")
	}
	if errs := Validate(); len(errs) != 1 || !strings.Contains(errs[0].Error(), "made-up") {
		t.Fatalf("Validate must name the bad dimension: %v", errs)
	}
	writeCfg(t, "checklists.yaml", `
universal:
  - {dimension: slo, question: "?"}
`)
	if _, ok := LoadChecklists("k"); ok {
		t.Fatal("a keyless decision must reject the file")
	}
}

// TestRules: present file returns trimmed text; absent returns "".
func TestRules(t *testing.T) {
	t.Setenv("ORION_HARNESS_DIR", t.TempDir())
	if Rules("k") != "" {
		t.Fatal("absent rules → empty")
	}
	writeCfg(t, "rules.md", "  - never log secrets\n")
	if got := Rules("k"); got != "- never log secrets" {
		t.Fatalf("rules must load trimmed, got %q", got)
	}
}
