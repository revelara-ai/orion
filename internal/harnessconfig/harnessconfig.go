// Package harnessconfig externalizes the harness's drift-prone content —
// the generation prompt preamble, the per-projectType completeness
// checklists, extra rule/instruction text — to versioned, reviewable files
// (or-kzf.2, the Day-1 "treat prompts and checklists as code" gap). A team
// reviews and evolves the harness in PRs without recompiling Orion.
//
// Posture: files ABSENT → the compiled Go defaults (zero-config unchanged).
// Files INVALID → a loud warning + the Go defaults at the consumption site,
// and `orion doctor` reports the exact error — a bad edit degrades visibly,
// it never bricks a run and never silently half-applies.
package harnessconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// Dir resolves the harness config directory: $ORION_HARNESS_DIR, else
// ~/.orion/harness.
func Dir() string {
	if d := os.Getenv("ORION_HARNESS_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".orion", "harness")
}

const (
	preambleFile  = "generation_preamble.tmpl"
	checklistFile = "checklists.yaml"
	rulesFile     = "rules.md"
)

// PreambleData is what the generation-preamble template may reference.
type PreambleData struct {
	Module string
	Entry  string
	Family string // "" (http service) | "cli" | "library"
	Route  string
	Port   int
	Format string
}

// GenerationPreamble renders the externalized preamble for the generation
// prompt. ok=false means "use the compiled default" (file absent, or invalid
// after a loud warning).
func GenerationPreamble(data PreambleData) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(Dir(), preambleFile))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false
	}
	if err != nil {
		slog.Warn("harness config: preamble unreadable — using the compiled default", "err", err)
		return "", false
	}
	out, err := renderPreamble(string(raw), data)
	if err != nil {
		slog.Warn("harness config: preamble invalid — using the compiled default (run `orion doctor`)", "err", err)
		return "", false
	}
	return out, true
}

func renderPreamble(raw string, data PreambleData) (string, error) {
	t, err := template.New(preambleFile).Option("missingkey=error").Parse(raw)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "", err
	}
	if strings.TrimSpace(b.String()) == "" {
		return "", fmt.Errorf("preamble renders empty")
	}
	return b.String(), nil
}

// Rules returns the extra rule/instruction text appended to the generation
// prompt ("" when absent/invalid).
func Rules() string {
	raw, err := os.ReadFile(filepath.Join(Dir(), rulesFile))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// ChecklistDecision is one externalized completeness decision.
type ChecklistDecision struct {
	Key       string `yaml:"key"`
	Dimension string `yaml:"dimension"`
	Question  string `yaml:"question"`
	Fallback  string `yaml:"fallback"`
}

// Checklists is the externalized completeness checklist config.
type Checklists struct {
	// Functional maps projectType → its functional decisions (replacing the
	// compiled registry entry for that type; other types keep their defaults).
	Functional map[string][]ChecklistDecision `yaml:"functional"`
	// Universal, when non-empty, REPLACES the compiled universal dimensions.
	Universal []ChecklistDecision `yaml:"universal"`
}

// validDimensions is the closed vocabulary the completeness gate understands.
var validDimensions = map[string]bool{
	"functional": true, "scale": true, "observability": true, "oncall": true,
	"data": true, "slo": true, "security": true, "dependencies": true,
}

// LoadChecklists reads the externalized checklists. ok=false means "use the
// compiled defaults" (absent, or invalid after a loud warning).
func LoadChecklists() (Checklists, bool) {
	raw, err := os.ReadFile(filepath.Join(Dir(), checklistFile))
	if errors.Is(err, fs.ErrNotExist) {
		return Checklists{}, false
	}
	if err != nil {
		slog.Warn("harness config: checklists unreadable — using the compiled defaults", "err", err)
		return Checklists{}, false
	}
	c, err := parseChecklists(raw)
	if err != nil {
		slog.Warn("harness config: checklists invalid — using the compiled defaults (run `orion doctor`)", "err", err)
		return Checklists{}, false
	}
	return c, true
}

func parseChecklists(raw []byte) (Checklists, error) {
	var c Checklists
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return Checklists{}, err
	}
	check := func(where string, ds []ChecklistDecision) error {
		for _, d := range ds {
			if strings.TrimSpace(d.Key) == "" || strings.TrimSpace(d.Question) == "" {
				return fmt.Errorf("%s: every decision needs a key and a question (key=%q)", where, d.Key)
			}
			if !validDimensions[d.Dimension] {
				return fmt.Errorf("%s: decision %q has unknown dimension %q", where, d.Key, d.Dimension)
			}
		}
		return nil
	}
	for pt, ds := range c.Functional {
		if err := check("functional."+pt, ds); err != nil {
			return Checklists{}, err
		}
	}
	if err := check("universal", c.Universal); err != nil {
		return Checklists{}, err
	}
	return c, nil
}

// Validate checks every present config file and returns one error per invalid
// file — the `orion doctor` surface. Absent files are not errors.
func Validate() []error {
	var errs []error
	dir := Dir()
	if raw, err := os.ReadFile(filepath.Join(dir, preambleFile)); err == nil {
		if _, rerr := renderPreamble(string(raw), PreambleData{Module: "sample/mod", Entry: "handle", Format: "json"}); rerr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", preambleFile, rerr))
		}
	}
	if raw, err := os.ReadFile(filepath.Join(dir, checklistFile)); err == nil {
		if _, perr := parseChecklists(raw); perr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", checklistFile, perr))
		}
	}
	return errs
}
