package postmerge

// ScoreInput computes a reliability impact score for the given refinement input,
// returning a complete RefinementResult with severity tag and evidence fields populated.
func (r *Refiner) ScoreInput(input *RefineInput, tag Severity, tags []string, evidence string) (*RefinementResult, error) {
	if input == nil {
		return nil, &ScoreError{"empty input"}
	}
	score := r.computeScore(input)
	if tag == 0 {
		tag = inferSeverity(score)
	}
	result := &RefinementResult{
		IncidentID: input.IncidentID,
		RunID:      input.RunID,
		Score:      score,
		Tag:        tag,
		Tags:       tags,
		Evidence:   evidence,
	}
	return result, nil
}

// computeScore calculates a 0–1 impact score using the Refiner's configured weights.
func (r *Refiner) computeScore(input *RefineInput) float64 {
	var total float64
	if input.IssueCount > 0 {
		total += (float64(input.IssueCount) / 10.0) * r.baseWeights["issue_count"]
	}
	if input.AffectingRuns > 10 {
		total += 0.8 * r.baseWeights["affecting_runs"]
	} else if input.AffectingRuns > 0 {
		total += float64(input.AffectingRuns) / 100.0 * r.baseWeights["affecting_runs"]
	}
	for _, dc := range input.DataClasses {
		for _, critical := range r.criticalData {
			if dc == critical {
				total += 0.3 * r.baseWeights["data_classes"]
				break
			}
		}
	}
	if input.HasCrossTenant {
		total += r.baseWeights["cross_tenant"]
	}
	if total > 1.0 {
		total = 1.0
	}
	return total
}

// inferSeverity maps a numeric score to a severity tier.
func inferSeverity(score float64) Severity {
	switch {
	case score >= 0.8:
		return SeverityCritical
	case score >= 0.5:
		return SeverityHigh
	case score >= 0.25:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

// ScoreError is returned when ScoreInput encounters invalid input.
type ScoreError struct {
	reason string
}

func (e *ScoreError) Error() string { return "postmerge.ScoreError: " + e.reason }
