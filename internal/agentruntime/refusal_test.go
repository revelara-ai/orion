package agentruntime

import "testing"

// TestIsRefusalStop (or-mvr.15 iv): the vendor-agent stopReason vocabulary
// that classifies as a refusal — and everything else does not.
func TestIsRefusalStop(t *testing.T) {
	for _, yes := range []string{"refusal", "Refusal", " REFUSED ", "content_filter", "SAFETY"} {
		if !IsRefusalStop(yes) {
			t.Fatalf("%q must classify as a refusal", yes)
		}
	}
	for _, no := range []string{"", "end_turn", "max_tokens", "cancelled", "tool_use"} {
		if IsRefusalStop(no) {
			t.Fatalf("%q must NOT classify as a refusal", no)
		}
	}
}
