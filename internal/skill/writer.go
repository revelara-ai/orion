package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// WriteSkill materializes s as an agentskills.io SKILL.md at dir/<name>/SKILL.md (creating the
// directory). Only the standard frontmatter fields are emitted (the runtime fields Body/Path/
// Dir/Trust are yaml:"-"), followed by the markdown body — so the result round-trips through
// Parse. It is the inverse of Load and the means by which the self-evolution lifecycle turns a
// promoted candidate into a discoverable, reloadable skill. Overwrites an existing file of the
// same name (promotion is idempotent). Returns the SKILL.md path.
func WriteSkill(dir string, s Skill) (string, error) {
	if !ValidName(s.Name) {
		return "", fmt.Errorf("skill: refusing to write invalid name %q", s.Name)
	}
	if strings.TrimSpace(s.Description) == "" {
		return "", fmt.Errorf("skill %q: a non-empty description is required", s.Name)
	}
	front, err := yaml.Marshal(s) // runtime fields are yaml:"-", so this is exactly the frontmatter
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("---\n")
	b.Write(front)
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(s.Body))
	b.WriteString("\n")

	skillDir := filepath.Join(dir, s.Name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}
