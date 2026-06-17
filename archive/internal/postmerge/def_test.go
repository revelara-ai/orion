package postmerge

import (
	"testing"
)

func TestSeverityString(t *testing.T) {
	tests := map[Severity]string{
		SeverityLow:        "low",
		SeverityMedium:     "medium",
		SeverityHigh:       "high",
		SeverityCritical:   "critical",
		Severity(99):      "severity(99)",
	}
	for sev, want := range tests {
		if got := sev.String(); got != want {
			t.Errorf("Severity(%d).String() = %q, want %q", sev, got, want)
		}
	}
}

func TestRefineInputValidate(t *testing.T) {
	tests := []struct {
		name    string
		input   RefineInput
		wantErr bool
		errSub  string
	}{
		{"Valid input", RefineInput{IncidentID: "inc-1", RunID: "run-001"}, false, ""},
		{"Empty IncidentID", RefineInput{RunID: "run-001"}, true, "IncidentID must be non-empty"},
		{"Empty RunID", RefineInput{IncidentID: "inc-2"}, true, "RunID must be non-empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.input.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate error = %v, wantError %v", err, tt.wantErr)
			}
			if tt.wantErr && err != nil && !containsSubstring(err.Error(), tt.errSub) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.errSub)
			}
		})
	}
}

func TestRefinementResultHasEvidence(t *testing.T) {
	tests := []struct {
		name    string
		result  RefinementResult
		want    bool
	}{
		{"With evidence", RefinementResult{IncidentID: "inc-1", Evidence: "bug found"}, true},
		{"With tags", RefinementResult{IncidentID: "inc-2", Tags: []string{"data-leak"}}, true},
		{"Empty", RefinementResult{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.result.HasEvidence(); got != tt.want {
				t.Errorf("HasEvidence() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewRefinerDefaults(t *testing.T) {
	ref := New()
	if ref == nil {
		t.Fatal("New returned nil")
	}
	if len(ref.baseWeights) != 4 {
		t.Errorf("baseWeights has %d entries, want 4", len(ref.baseWeights))
	}
	if len(ref.criticalData) != 3 {
		t.Errorf("criticalData has %d entries, want 3", len(ref.criticalData))
	}
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
