package postmerge

import "testing"

func TestScoreInputValid(t *testing.T) {
	input := &RefineInput{IncidentID:"inc-1", RunID:"run-01", IssueCount:3, AffectingRuns:42, HasCrossTenant:true}
	result, err := New().ScoreInput(input, SeverityMedium, []string{"tag1"}, "evidence data")
	if err != nil {
		t.Fatalf("ScoreInput should succeed with valid input: %v", err)
	}
	if result == nil {
		t.Fatal("result is nil")
	}
	if result.IncidentID != "inc-1" { t.Errorf("IncidentID = %q, want inc-1", result.IncidentID) }
	if result.Tag != SeverityMedium { t.Errorf("Tag = %v, want Medium", result.Tag) }
	if !result.HasEvidence() { t.Error("HasEvidence should return true when evidence provided") }
}

func TestScoreInputNil(t *testing.T) {
	_, err := New().ScoreInput(nil, 0, nil, "")
	if err == nil {
		t.Fatal("expected error for nil input")
	}
}

func TestInferSeverity(t *testing.T) {
	tests := []struct{ score float64; want Severity }{
		{0.9, SeverityCritical},
		{0.5, SeverityHigh},
		{0.25, SeverityMedium},
		{0.1, SeverityLow},
	}
	for _, tt := range tests {
		if got := inferSeverity(tt.score); got != tt.want {
			t.Errorf("inferSeverity(%v) = %v, want %v", tt.score, got, tt.want)
		}
	}
}
