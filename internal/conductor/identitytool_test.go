package conductor

import (
	"strings"
	"testing"
)

// TestCheckCompletenessCarriesIdentity (or-hn15.3 DONE-WHEN a): check_completeness
// names the project its decisions belong to, so the agent can't mistake a stale
// active project's checklist for its own.
func TestCheckCompletenessCarriesIdentity(t *testing.T) {
	_, run := specRegistry(t) // active project: the Arc Raiders game intent
	out, err := run("check_completeness", `{}`)
	if err != nil {
		t.Fatalf("check_completeness: %v", err)
	}
	if !strings.Contains(out, "Arc Raiders") {
		t.Fatalf("check_completeness must name the target project (its intent), got:\n%s", out)
	}
}
