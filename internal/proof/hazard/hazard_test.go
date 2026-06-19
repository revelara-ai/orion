package hazard

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// TestControlLoopFeedbackValidated: against the ratified model and a conforming
// artifact, every control action has a test and a closed feedback loop, no UCA is
// uncontrolled (open), and the accepted gaps are reported; removing a control
// makes its UCA uncontrolled and fails the mode.
func TestControlLoopFeedbackValidated(t *testing.T) {
	ctx := context.Background()
	model := stpa.RatifiedTimeServiceModel()

	good := t.TempDir()
	if _, err := sandbox.GenerateFixtureService(good, sandbox.GenSpec{Route: "/time", Port: 8080, Format: "json", TimeZone: "UTC"}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	mr, rep, err := Prove(ctx, good, model)
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	if !mr.Pass {
		t.Fatalf("conforming artifact failed hazard: %s", mr.Output)
	}
	if len(rep.UncontrolledUCAs) != 0 {
		t.Fatalf("uncontrolled UCAs = %v, want none", rep.UncontrolledUCAs)
	}
	if len(rep.ControlActions) == 0 {
		t.Fatal("no control actions reported")
	}
	for _, ca := range rep.ControlActions {
		if ca.Test == "" {
			t.Fatalf("control action %s has no test", ca.ID)
		}
		if !ca.FeedbackClosed {
			t.Fatalf("control action %s feedback loop does not close", ca.ID)
		}
	}
	if len(rep.AcceptedGaps) != 3 {
		t.Fatalf("accepted gaps = %v, want 3 (documented)", rep.AcceptedGaps)
	}

	// Remove a control (ReadHeaderTimeout → UCA4): it must become uncontrolled.
	src, _ := os.ReadFile(filepath.Join(good, "main.go"))
	tampered := t.TempDir()
	_ = os.WriteFile(filepath.Join(tampered, "go.mod"), []byte("module t\n\ngo 1.25\n"), 0o644)
	stripped := removeLine(string(src), "ReadHeaderTimeout")
	_ = os.WriteFile(filepath.Join(tampered, "main.go"), []byte(stripped), 0o644)

	mr2, rep2, _ := Prove(ctx, tampered, model)
	if mr2.Pass {
		t.Fatal("artifact missing a control should fail hazard")
	}
	if !contains(rep2.UncontrolledUCAs, "UCA4") {
		t.Fatalf("UCA4 should be uncontrolled when ReadHeaderTimeout is removed; got %v", rep2.UncontrolledUCAs)
	}
}

func removeLine(src, token string) string {
	var out []byte
	for _, line := range splitLines(src) {
		if !containsStr(line, token) {
			out = append(out, line...)
			out = append(out, '\n')
		}
	}
	return string(out)
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
