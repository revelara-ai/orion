// Package skill is an Agent Skills registry in the open agentskills.io format: a skill is a
// directory containing a SKILL.md (YAML frontmatter + a markdown instruction body), optionally
// bundling scripts/references/assets. A skill authored for any skills-compatible client
// (Claude Code, Cursor, Codex, …) drops into Orion unchanged.
//
// Spec: https://agentskills.io/specification
// Client guide: https://agentskills.io/client-implementation/adding-skills-support
//
// Orion adds one runtime concern the standard leaves to the client: a TRUST tier. It is
// ASSIGNED BY THE LOAD SCOPE (see Registry.LoadDir), never self-declared in the SKILL.md — an
// untrusted project-supplied skill must not be able to elevate itself to a proof-immutable
// tier. Trust governs invariant 8: proof skills are immutable + non-hot-reloadable.
package skill

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Trust is the loader-assigned trust tier of a skill (NOT a frontmatter field).
type Trust string

const (
	// TrustGeneration: loaded from a project/untrusted scope — reloadable + mutable.
	TrustGeneration Trust = "generation"
	// TrustProof: a built-in/curated scope — immutable, NOT hot-reloadable (invariant 8).
	TrustProof Trust = "proof"
)

// Reloadable reports whether a skill at this trust tier may be hot-reloaded (or-ykz.3).
func (t Trust) Reloadable() bool { return t != TrustProof }

// Skill is one Agent Skill. The frontmatter fields + their yaml tags are exactly the
// agentskills.io standard's (so a standard SKILL.md round-trips); the remaining fields are
// runtime-derived and never read from the file.
type Skill struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       string            `yaml:"license,omitempty"`
	Compatibility string            `yaml:"compatibility,omitempty"`
	AllowedTools  string            `yaml:"allowed-tools,omitempty"`
	Metadata      map[string]string `yaml:"metadata,omitempty"`

	// Extra captures every non-standard frontmatter key verbatim (Claude Code extensions like
	// model / when_to_use / argument-hint / disable-model-invocation, and any unknown key) so a
	// skill authored for another client round-trips losslessly rather than silently dropping
	// fields. Never required for portability; read it only through Extension.
	Extra map[string]yaml.Node `yaml:",inline"`

	Body  string `yaml:"-"` // markdown instructions after the frontmatter
	Path  string `yaml:"-"` // absolute path to the SKILL.md file
	Dir   string `yaml:"-"` // base dir (parent of Path) for bundled resources
	Trust Trust  `yaml:"-"` // scope-assigned trust tier
}

// nameRE enforces the spec name format: 1-64 lowercase alphanumerics joined by single
// hyphens — no leading/trailing hyphen, no consecutive hyphens.
var nameRE = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// ValidName reports whether name meets the agentskills.io name constraints.
func ValidName(name string) bool {
	return len(name) >= 1 && len(name) <= 64 && nameRE.MatchString(name)
}

// Extension returns a non-standard (Claude Code / vendor) scalar frontmatter value preserved
// in Extra, e.g. "model" or "when_to_use"; ok is false if absent or non-scalar.
//
// "trust" is RESERVED and never returned: trust is scope-assigned by the loader (Skill.Trust),
// never self-declared in a SKILL.md, so a skill cannot smuggle a trust claim through Extra.
func (s Skill) Extension(key string) (value string, ok bool) {
	if key == "trust" {
		return "", false
	}
	n, present := s.Extra[key]
	if !present || n.Kind != yaml.ScalarNode {
		return "", false
	}
	return n.Value, true
}

// Parse parses SKILL.md content: YAML frontmatter between --- delimiters, then the markdown
// body. Per the agentskills.io client guide it validates LENIENTLY — a cosmetic problem (a
// name that doesn't meet the format, an over-long field) is returned as a warning and the
// skill still loads; only an unusable skill (no frontmatter, unparseable YAML, or a
// missing/empty description — which is essential for progressive disclosure) is a fatal error.
func Parse(content []byte) (Skill, []string, error) {
	front, body, ok := splitFrontmatter(content)
	if !ok {
		return Skill{}, nil, fmt.Errorf("skill: missing YAML frontmatter (--- ... ---)")
	}
	var sk Skill
	if err := yaml.Unmarshal([]byte(front), &sk); err != nil {
		// Cross-client fallback (agentskills.io client guide): skills authored for other
		// runtimes sometimes contain unquoted colons in scalar values ("description: Use
		// when: ...") — technically invalid YAML. Quote those values and retry once.
		sk = Skill{}
		if ferr := yaml.Unmarshal([]byte(quoteUnquotedScalars(front)), &sk); ferr != nil {
			return Skill{}, nil, fmt.Errorf("skill: invalid frontmatter YAML: %w", err)
		}
	}
	sk.Name = strings.TrimSpace(sk.Name)
	sk.Description = strings.TrimSpace(sk.Description)
	sk.Body = strings.TrimSpace(body)

	if sk.Description == "" {
		return Skill{}, nil, fmt.Errorf("skill %q: a non-empty description is required", sk.Name)
	}
	if sk.Name == "" {
		return Skill{}, nil, fmt.Errorf("skill: a name is required")
	}
	// A name with path separators is a security concern (it becomes the registry key and could
	// later be used to build a path), so it is REJECTED, not lenient-loaded like a format nit.
	if strings.ContainsAny(sk.Name, `/\`) || strings.Contains(sk.Name, "..") {
		return Skill{}, nil, fmt.Errorf("skill %q: name contains path separators — rejected", sk.Name)
	}
	var warnings []string
	if !ValidName(sk.Name) {
		warnings = append(warnings, fmt.Sprintf("skill %q: name does not meet the agentskills.io format (1-64 lowercase alphanumerics + single hyphens) — loaded anyway", sk.Name))
	}
	if len(sk.Description) > 1024 {
		warnings = append(warnings, fmt.Sprintf("skill %q: description exceeds 1024 characters — loaded anyway", sk.Name))
	}
	if len(sk.Compatibility) > 500 {
		warnings = append(warnings, fmt.Sprintf("skill %q: compatibility exceeds 500 characters", sk.Name))
	}
	return sk, warnings, nil
}

// splitFrontmatter splits content into the YAML frontmatter block and the markdown body. The
// frontmatter is delimited by a leading line that is exactly "---" and the next line that is
// exactly "---". Returns ok=false if there is no opening/closing delimiter.
func splitFrontmatter(content []byte) (front, body string, ok bool) {
	text := strings.TrimPrefix(string(content), string([]byte{0xEF, 0xBB, 0xBF})) // strip a UTF-8 BOM
	text = strings.ReplaceAll(text, "\r\n", "\n")                                 // normalize CRLF
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], "\r ") != "---" {
		return "", "", false
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], "\r ") == "---" {
			return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n"), true
		}
	}
	return "", "", false
}

// scalarKeyColonRE matches a known scalar frontmatter key whose value contains an unquoted
// colon-space (the common cross-client YAML defect). Runs ONLY in the post-failure fallback,
// so it never touches already-valid YAML.
var scalarKeyColonRE = regexp.MustCompile(`(?m)^(\s*(?:name|description|license|compatibility|allowed-tools)\s*:\s+)([^"'\s].*?:\s.*?)\s*$`)

func quoteUnquotedScalars(front string) string {
	return scalarKeyColonRE.ReplaceAllStringFunc(front, func(line string) string {
		m := scalarKeyColonRE.FindStringSubmatch(line)
		if m == nil {
			return line
		}
		v := strings.ReplaceAll(m[2], `\`, `\\`) // escape backslashes before quotes (YAML double-quoted scalar)
		v = strings.ReplaceAll(v, `"`, `\"`)
		return m[1] + `"` + v + `"`
	})
}
