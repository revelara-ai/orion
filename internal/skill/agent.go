package skill

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// StrList accepts a list written EITHER as a YAML sequence OR as a single comma/space-separated
// scalar string — both forms occur for a subagent's `tools` field across the ecosystem — and
// normalizes to []string.
type StrList []string

func (l *StrList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		*l = strings.FieldsFunc(n.Value, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t' || r == '\n'
		})
		return nil
	case yaml.SequenceNode:
		var xs []string
		if err := n.Decode(&xs); err != nil {
			return err
		}
		*l = xs
		return nil
	default:
		return fmt.Errorf("expected a string or a list")
	}
}

// Agent is an open agent (subagent) definition: YAML frontmatter (name, description required;
// tools, model optional) + a markdown body that IS the agent's system prompt. It is discovered
// as a single .md file (unlike a skill, which is a directory containing SKILL.md). Trust is
// scope-assigned by the loader, never self-declared (invariant 8) — same model as skills.
type Agent struct {
	Name        string  `yaml:"name"`
	Description string  `yaml:"description"`
	Tools       StrList `yaml:"tools,omitempty"` // omitted ⇒ inherit all tools
	Model       string  `yaml:"model,omitempty"`

	// Extra preserves non-standard keys verbatim (disallowedTools, skills, color, …).
	Extra map[string]yaml.Node `yaml:",inline"`

	Body  string `yaml:"-"` // markdown system prompt
	Path  string `yaml:"-"`
	Trust Trust  `yaml:"-"`
}

// ParseAgent parses a subagent .md file (frontmatter + system-prompt body) with the same
// lenient rules as skills: name + description are required; a non-conforming name warns and
// still loads; a name with path separators is rejected.
func ParseAgent(content []byte) (Agent, []string, error) {
	front, body, ok := splitFrontmatter(content)
	if !ok {
		return Agent{}, nil, fmt.Errorf("agent: missing YAML frontmatter (--- ... ---)")
	}
	var a Agent
	if err := yaml.Unmarshal([]byte(front), &a); err != nil {
		a = Agent{}
		if ferr := yaml.Unmarshal([]byte(quoteUnquotedScalars(front)), &a); ferr != nil {
			return Agent{}, nil, fmt.Errorf("agent: invalid frontmatter YAML: %w", err)
		}
	}
	a.Name = strings.TrimSpace(a.Name)
	a.Description = strings.TrimSpace(a.Description)
	a.Body = strings.TrimSpace(body)

	if a.Description == "" {
		return Agent{}, nil, fmt.Errorf("agent %q: a non-empty description is required", a.Name)
	}
	if a.Name == "" {
		return Agent{}, nil, fmt.Errorf("agent: a name is required")
	}
	if strings.ContainsAny(a.Name, `/\`) || strings.Contains(a.Name, "..") {
		return Agent{}, nil, fmt.Errorf("agent %q: name contains path separators — rejected", a.Name)
	}
	var warnings []string
	if !ValidName(a.Name) {
		warnings = append(warnings, fmt.Sprintf("agent %q: name does not meet the lowercase-alphanumeric+hyphen format — loaded anyway", a.Name))
	}
	return a, warnings, nil
}

// AgentsDoc is a freeform AGENTS.md (the cross-tool agent-instructions convention): plain
// markdown context, NOT a structured/invokable agent.
type AgentsDoc struct {
	Content string
	Path    string
}
