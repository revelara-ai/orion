package skill

import (
	"strings"
	"testing"
)

// TestWriteSkillRoundTrips: a skill written by WriteSkill is parsed back by Load with its
// frontmatter, metadata, and body intact (WriteSkill is the inverse of Load).
func TestWriteSkillRoundTrips(t *testing.T) {
	dir := t.TempDir()
	orig := Skill{
		Name:        "learned-abc12345",
		Description: "does a learned thing",
		Body:        "Step 1.\nStep 2.",
		Trust:       TrustGeneration,
		Metadata:    map[string]string{"orion-source": "self-evolved"},
	}
	path, err := WriteSkill(dir, orig)
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := Load(path, TrustGeneration)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != orig.Name || got.Description != orig.Description {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.Metadata["orion-source"] != "self-evolved" {
		t.Fatalf("metadata not round-tripped: %+v", got.Metadata)
	}
	if !strings.Contains(got.Body, "Step 1.") {
		t.Fatalf("body not round-tripped: %q", got.Body)
	}
}

// TestWriteSkillRejectsInvalidName: WriteSkill refuses an invalid name (and a path-separator
// name) so it can never write outside its directory.
func TestWriteSkillRejectsInvalidName(t *testing.T) {
	for _, n := range []string{"Bad Name", "../escape", ""} {
		if _, err := WriteSkill(t.TempDir(), Skill{Name: n, Description: "d"}); err == nil {
			t.Errorf("WriteSkill should reject name %q", n)
		}
	}
}
