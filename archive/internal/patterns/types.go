package patterns

import (
	"fmt"
)

// Pattern represents a reusable risk or safety rule pattern per Orion spec.
type Pattern struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Rules       []Rule   `json:"rules"`
	Scope       string   `json:"scope"    example:"full,agentic,inventory_only"`
	Version     int      `json:"version"  example:"1"`
	Enabled     bool     `json:"enabled"`
}

// Rule represents a single assertion within a Pattern.
type Rule struct {
	Name      string   `json:"name"`
	Condition string   `json:"condition" example:"severity >= warning"`
	Severity  Severity `json:"severity"`
	Resources []string `json:"resources,omitempty"` // e.g., ["database", "network"]
}

// Severity indicates how urgent a pattern match is.
type Severity int32

const (
	SeverityInfo    Severity = iota
	SeverityWarning Severity = iota + 10
	SeverityCritical Severity = iota + 20
)

func (s Severity) String() string {
	switch s {
	case SeverityInfo:
		return "info"
	case SeverityWarning:
		return "warning"
	case SeverityCritical:
		return "critical"
	default:
		return fmt.Sprintf("severity(%d)", s)
	}
}

// MatchResult represents the outcome of evaluating a pattern against observed data.
type MatchResult struct {
	PatternID string   `json:"pattern_id"`
	RuleName  string   `json:"rule_name"`
	Matched   bool     `json:"matched"`
	Score     float64  `json:"score"`
	Evidence  []string `json:"evidence,omitempty"`
}

// Validate checks that a Pattern has sane field values.
func (p *Pattern) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("patterns.Validate: Name cannot be empty")
	}
	if len(p.Rules) == 0 {
		return fmt.Errorf("patterns.Validate: at least one Rule required")
	}
	for _, r := range p.Rules {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("patterns.Validate: rule %q: %w", r.Name, err)
		}
	}
	return nil
}

// Validate checks that a Rule is valid for inclusion in a pattern.
func (r *Rule) Validate() error {
	if r.Name == "" {
		return fmt.Errorf("patterns.ValidateRule: Name must be non-empty")
	}
	if r.Condition == "" {
		return fmt.Errorf("patterns.ValidateRule: Condition must be non-empty")
	}
	return nil
}

// Match evaluates a Pattern's rules against an Observation and returns the best-matching result.
func (p *Pattern) Match(obs Observation) *MatchResult {
	for _, r := range p.Rules {
		if r.Matches(obs) {
			score := float64(r.Severity) + obs.WeightedScore()
			return &MatchResult{
				PatternID: p.ID,
				RuleName:  r.Name,
				Matched:   true,
				Score:     score,
				Evidence:  append([]string{}, obs.Evidence()...),
			}
		}
	}
	return &MatchResult{PatternID: p.ID, Matched: false, Score: 0}
}

// Observation captures what the system observed during a run.
type Observation struct {
	severity   Severity
	score      float64
	evidence   []string
}

// NewObservation creates an Observation from raw data.
func NewObservation(severity Severity, score float64, evidence []string) *Observation {
	copied := make([]string, len(evidence))
	copy(copied, evidence)
	return &Observation{
		severity: severity,
		score:    score,
		evidence: copied,
	}
}

func (o *Observation) WeightedScore() float64 { return o.score }
func (o *Observation) Evidence() []string     { return append([]string{}, o.evidence...) }

// Matches checks if a Rule's severity matches against an Observation.
func (r *Rule) Matches(obs Observation) bool {
	if obs.severity < r.Severity {
		return false
	}
	for _, res := range r.Resources {
		_ = res // placeholder for future resource filtering
	}
	return true
}
