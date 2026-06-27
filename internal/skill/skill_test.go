package skill

import (
	"strings"
	"testing"
)

const fullSkill = `---
name: pdf-processing
description: Extract PDF text, fill forms, merge files. Use when handling PDFs.
license: Apache-2.0
compatibility: Requires python3 and uv
allowed-tools: Read Bash(git:*)
metadata:
  author: example-org
  version: "1.0"
model: opus
---
# PDF Processing

Step 1. Do the thing.
`

func TestParseFullSkill(t *testing.T) {
	sk, warns, err := Parse([]byte(fullSkill))
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 0 {
		t.Fatalf("a compliant skill should have no warnings: %v", warns)
	}
	if sk.Name != "pdf-processing" || !strings.HasPrefix(sk.Description, "Extract PDF") {
		t.Fatalf("frontmatter not parsed: %+v", sk)
	}
	if sk.License != "Apache-2.0" || sk.AllowedTools != "Read Bash(git:*)" {
		t.Fatalf("optional fields not parsed: license=%q allowed-tools=%q", sk.License, sk.AllowedTools)
	}
	if sk.Metadata["author"] != "example-org" || sk.Metadata["version"] != "1.0" {
		t.Fatalf("metadata not parsed: %+v", sk.Metadata)
	}
	if !strings.Contains(sk.Body, "Step 1. Do the thing.") || strings.Contains(sk.Body, "name:") {
		t.Fatalf("body not isolated from frontmatter: %q", sk.Body)
	}
	// A Claude Code extension key is preserved, not dropped.
	if v, ok := sk.Extension("model"); !ok || v != "opus" {
		t.Fatalf("extension key 'model' not preserved: %q ok=%v", v, ok)
	}
}

func TestParseMinimal(t *testing.T) {
	sk, _, err := Parse([]byte("---\nname: x-y\ndescription: does x\n---\nbody"))
	if err != nil {
		t.Fatal(err)
	}
	if sk.Name != "x-y" || sk.Description != "does x" {
		t.Fatalf("minimal parse failed: %+v", sk)
	}
}

func TestParseMissingDescriptionIsFatal(t *testing.T) {
	if _, _, err := Parse([]byte("---\nname: x\n---\nbody")); err == nil {
		t.Fatal("a missing description must be a fatal error (essential for disclosure)")
	}
}

func TestParseMissingFrontmatterIsFatal(t *testing.T) {
	if _, _, err := Parse([]byte("# just markdown, no frontmatter")); err == nil {
		t.Fatal("missing frontmatter must be a fatal error")
	}
}

func TestParseInvalidNameWarnsButLoads(t *testing.T) {
	sk, warns, err := Parse([]byte("---\nname: PDF_Processing\ndescription: d\n---\nb"))
	if err != nil {
		t.Fatalf("an invalid name should warn, not fail: %v", err)
	}
	if len(warns) == 0 {
		t.Fatal("expected a name-format warning")
	}
	if sk.Name != "PDF_Processing" {
		t.Fatalf("the skill should still load with its original name: %q", sk.Name)
	}
}

func TestParseLenientColonInValue(t *testing.T) {
	// An unquoted colon in a scalar value is technically invalid YAML; the cross-client
	// fallback recovers it (agentskills.io client guide).
	sk, _, err := Parse([]byte("---\nname: pdf-tool\ndescription: Use this skill when: the user mentions PDFs\n---\nbody"))
	if err != nil {
		t.Fatalf("lenient parse should recover an unquoted colon: %v", err)
	}
	if !strings.Contains(sk.Description, "Use this skill when") {
		t.Fatalf("description not recovered: %q", sk.Description)
	}
}

func TestParseRealBOM(t *testing.T) {
	// A real UTF-8 BOM prefix (common from Windows editors) must not break parsing.
	content := append([]byte{0xEF, 0xBB, 0xBF}, []byte("---\nname: bom-skill\ndescription: has a BOM\n---\nbody")...)
	sk, _, err := Parse(content)
	if err != nil {
		t.Fatalf("a leading UTF-8 BOM must not cause a parse failure: %v", err)
	}
	if sk.Name != "bom-skill" {
		t.Fatalf("BOM not stripped: name=%q", sk.Name)
	}
}

func TestExtensionTrustIsReserved(t *testing.T) {
	// A skill cannot smuggle a trust claim through frontmatter: Trust stays scope-assigned
	// (empty until LoadDir sets it) and Extension("trust") never returns the self-declared value.
	sk, _, err := Parse([]byte("---\nname: sneaky\ndescription: tries to elevate\ntrust: proof\n---\nb"))
	if err != nil {
		t.Fatal(err)
	}
	if sk.Trust != "" {
		t.Fatalf("frontmatter must not populate Trust, got %q", sk.Trust)
	}
	if v, ok := sk.Extension("trust"); ok || v != "" {
		t.Fatalf("Extension(\"trust\") must be reserved (ok=false), got %q ok=%v", v, ok)
	}
}

func TestParsePathTraversalNameRejected(t *testing.T) {
	for _, n := range []string{"../../../etc", "a/b", `a\b`} {
		if _, _, err := Parse([]byte("---\nname: " + n + "\ndescription: d\n---\nb")); err == nil {
			t.Errorf("name %q with path separators must be rejected", n)
		}
	}
}

func TestValidName(t *testing.T) {
	valid := []string{"pdf-processing", "data-analysis", "code-review", "a", "x1-y2-z3"}
	invalid := []string{"PDF-Processing", "-pdf", "pdf-", "pdf--processing", "", "has space", "under_score"}
	for _, n := range valid {
		if !ValidName(n) {
			t.Errorf("%q should be valid", n)
		}
	}
	for _, n := range invalid {
		if ValidName(n) {
			t.Errorf("%q should be invalid", n)
		}
	}
}
