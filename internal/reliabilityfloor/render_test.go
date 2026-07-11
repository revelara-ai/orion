package reliabilityfloor

import (
	"strings"
	"testing"
)

func TestRenderContextEmpty(t *testing.T) {
	if RenderContext(nil) != "" {
		t.Fatal("empty signals must render empty string")
	}
}

func TestRenderContextFormat(t *testing.T) {
	out := RenderContext([]Signal{
		{ID: "RC-1", Title: "Outbound HTTP without timeout", Why: "inc-2024 took prod down", Severity: SevHigh},
	})
	if !strings.Contains(out, "# Reliability floor") {
		t.Fatalf("missing header: %q", out)
	}
	if !strings.Contains(out, "[HIGH] Outbound HTTP without timeout (RC-1) — inc-2024 took prod down") {
		t.Fatalf("bad bullet: %q", out)
	}
}
