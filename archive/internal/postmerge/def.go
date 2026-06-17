package postmerge

import "fmt"

// Severity indicates the reliability impact tier of a refined incident.
type Severity int32

const (
	SeverityLow        Severity = iota + 1
	SeverityMedium     Severity = iota + 10
	SeverityHigh       Severity = iota + 20
	SeverityCritical   Severity = iota + 30
)

func (s Severity) String() string {
	switch s {
	case SeverityLow:
		return "low"
	case SeverityMedium:
		return "medium"
	case SeverityHigh:
		return "high"
	case SeverityCritical:
		return "critical"
	default:
		return fmt.Sprintf("severity(%d)", int(s))
	}
}

// SeverityName maps a severity value to its canonical string name.
var SeverityName = map[Severity]string{
	SeverityLow:        "low",
	SeverityMedium:     "medium",
	SeverityHigh:       "high",
	SeverityCritical:   "critical",
}

// RefineInput holds the raw data from a completed Orion run's output for scoring.
type RefineInput struct {
	IncidentID    string
	RunID         string
	IssueCount    int
	AffectingRuns int
	DataClasses   []string
	HasCrossTenant bool
	Description   string
}

// Validate checks that a RefineInput has sane values.
func (r *RefineInput) Validate() error {
	if r.IncidentID == "" {
		return fmt.Errorf("postmerge.ValidateInput: IncidentID must be non-empty")
	}
	if r.RunID == "" {
		return fmt.Errorf("postmerge.ValidateInput: RunID must be non-empty")
	}
	return nil
}

// RefinementResult is the output of the post-merge refiner scoring an incident.
type RefinementResult struct {
	IncidentID string
	RunID      string
	Score      float64 // 0–1 reliability impact score
	Tag        Severity
	Tags       []string
	Evidence   string
}

// HasEvidence returns true if this refinement has supporting data.
func (r *RefinementResult) HasEvidence() bool {
	return r.Evidence != "" || len(r.Tags) > 0
}

// Refiner implements the post-merge incident relevance scoring logic (§16.6 of Orion spec).
type Refiner struct {
	baseWeights   map[string]float64
	criticalData  []string
}

// New creates a Refiner with default configuration.
func New() *Refiner {
	return &Refiner{
		baseWeights: map[string]float64{
			"issue_count":    0.25,
			"affecting_runs": 0.35,
			"data_classes":   0.20,
			"cross_tenant":   0.20,
		},
		criticalData: []string{"credentials", "pii", "secrets"},
	}
}
