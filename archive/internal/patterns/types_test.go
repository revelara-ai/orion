package patterns

import (
	"testing"
)

func TestValidatePattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern Pattern
		wantErr bool
		errSub  string
	}{
		{"Valid pattern", Pattern{ID: "p1", Name: "TestPattern", Description: "A test", Rules: []Rule{{Name: "test-rule", Condition: "count >= 5"}}, Enabled: true}, false, ""},
		{"Name empty", Pattern{ID: "p2", Description: "No name"}, true, "Name cannot be empty"},
		{"Rules empty", Pattern{Name: "NoRules"}, true, "at least one Rule required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.pattern.Validate()
			if (got != nil) != tt.wantErr {
				t.Errorf("Validate(%q) error = %v, wantError %v", tt.name, got, tt.wantErr)
			}
			if tt.wantErr && tt.errSub != "" && got != nil {
				if !containsSubstring(got.Error(), tt.errSub) {
					t.Errorf("error %q does not contain %q", got.Error(), tt.errSub)
				}
			}
		})
	}
}

func TestValidateRule(t *testing.T) {
	tests := []struct {
		name    string
		rule    Rule
		wantErr bool
	}{
		{"Valid rule", Rule{Name: "test", Condition: "x > 0"}, false},
		{"Empty name", Rule{Condition: "x > 0"}, true},
		{"Empty condition", Rule{Name: "test"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.rule.Validate()
			if (got != nil) != tt.wantErr {
				t.Errorf("Validate rule error = %v, wantError %v", got, tt.wantErr)
			}
		})
	}
}

func TestSeverityString(t *testing.T) {
	tests := map[Severity]string{
		SeverityInfo: "info", SeverityWarning: "warning", SeverityCritical: "critical"}
	for sev, want := range tests {
		if got := sev.String(); got != want {
			t.Errorf("Severity.String(%d) = %q, want %q", sev, got, want)
		}
	}
}

func TestObservationCopy(t *testing.T) {
	ev := []string{"e1", "e2"}
	obs := NewObservation(SeverityWarning, 10.5, ev)
	if got := obs.Evidence(); len(got) != len(ev) {
		t.Fatalf("Evidence() length = %d, want %d", len(got), len(ev))
	}
	ev[0] = "modified"
	if obs.Evidence()[0] == ev[0] {
		t.Error("Evidence returned mutable reference — should have copied")
	}
}

func TestPatternMatch(t *testing.T) {
	p := Pattern{ID: "p1", Name: "HighSeverity", Rules: []Rule{{Name: "critical", Condition: "severity >= warning", Severity: SeverityWarning}}}
	obs := NewObservation(SeverityCritical, 5.0, []string{"found bug"})
	res := p.Match(*obs)
	if !res.Matched {
		t.Error("expected match for Critical against Warning threshold")
	}
	if res.Score <= 0 {
		t.Errorf("Matched score = %v, must be positive", res.Score)
	}
	if len(res.Evidence) != 1 || res.Evidence[0] != "found bug" {
		t.Errorf("Evidence = %q, want [\"found bug\"]", res.Evidence)
	}
}

func TestPatternMatchNoMatch(t *testing.T) {
	p := Pattern{ID: "p2", Name: "LowSeverity", Rules: []Rule{{Name: "info rule", Condition: "severity >= critical", Severity: SeverityCritical}}}
	obs := NewObservation(SeverityInfo, 0.0, nil)
	res := p.Match(*obs)
	if res.Matched {
		t.Error("expected no match for Info against Critical threshold")
	}
	if res.Score != 0 {
		t.Errorf("non-matching score = %v, want 0", res.Score)
	}
}

func TestObservationScore(t *testing.T) {
	obs := NewObservation(SeverityWarning, 10.5, nil)
	if got := obs.WeightedScore(); got != 10.5 {
		t.Errorf("WeightedScore() = %v, want 10.5", got)
	}
}

func TestMatchResultEmptyEvidence(t *testing.T) {
	p := Pattern{ID: "p3", Rules: []Rule{{Name: "r1", Condition: "x", Severity: SeverityInfo}}}
	res := p.Match(*NewObservation(SeverityInfo, 0, nil))
	if res.Evidence == nil {
		t.Error("MatchResult.Evidence should be non-nil slice even when empty")
	}
}

func TestPatternWithMultipleRules(t *testing.T) {
	p := Pattern{ID: "p4", Rules: []Rule{{Name: "critical-rule", Condition: "x", Severity: SeverityCritical}, {Name: "warning-rule", Condition: "y", Severity: SeverityWarning}}}
	obs := NewObservation(SeverityInfo, 1.0, nil)
	res := p.Match(*obs)
	if res.Matched {
		t.Error("expected no match for Info against Critical or Warning threshold")
	}
}

func TestRuleMatchByResource(t *testing.T) {
	rule := Rule{Name: "res-rule", Condition: "x", Severity: SeverityWarning, Resources: []string{"database"}}
	obs := NewObservation(SeverityCritical, 5.0, nil)
	if !rule.Matches(*obs) {
		t.Error("resource-filtered rule should pass when severity is sufficient")
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
