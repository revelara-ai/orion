package completeness

import "testing"

// TestAnalyzeSurfacesEnumerableOptions (or-ykz.7): enumerable open decisions carry their
// option set; free-text ones do not.
func TestAnalyzeSurfacesEnumerableOptions(t *testing.T) {
	a := NewAnalyzer("http-service")
	byKey := map[string]OpenDecision{}
	for _, od := range a.Analyze("build a service", nil) {
		byKey[od.Key] = od
	}
	rf, ok := byKey["response_format"]
	if !ok {
		t.Fatal("response_format should be open for http-service")
	}
	if len(rf.Options) != 2 || rf.Options[0] != "json" || rf.Options[1] != "text" {
		t.Fatalf("response_format options = %v, want [json text]", rf.Options)
	}
	if sp := byKey["scale_profile"]; len(sp.Options) != 3 {
		t.Fatalf("scale_profile options = %v, want 3 (low/medium/high)", sp.Options)
	}
	if port, ok := byKey["port"]; ok && len(port.Options) != 0 {
		t.Fatalf("port is free-text and must have no options; got %v", port.Options)
	}
}
