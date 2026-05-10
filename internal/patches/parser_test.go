package patches

import (
	"errors"
	"strings"
	"testing"
)

func TestParseExtractsFromCodeFence(t *testing.T) {
	raw := "Here is the diff:\n\n```diff\n--- a/client.go\n+++ b/client.go\n@@ -10,3 +10,5 @@\n func Call() {\n+\tctx, cancel := context.WithTimeout(ctx, time.Second)\n+\tdefer cancel()\n }\n```\n\nThat should fix it."
	p, err := Parse(raw, ExtractDiffOptions{Pattern: PatternTimeout, ExpectedTargetPath: "client.go"})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.TargetPath != "client.go" {
		t.Errorf("TargetPath = %q, want client.go", p.TargetPath)
	}
	if !strings.Contains(p.UnifiedDiff, "context.WithTimeout") {
		t.Errorf("diff missing hint: %q", p.UnifiedDiff)
	}
	if p.TargetRange[0] != 10 {
		t.Errorf("range start = %d, want 10", p.TargetRange[0])
	}
}

func TestParseRejectsTargetMismatch(t *testing.T) {
	raw := "```diff\n--- a/wrong.go\n+++ b/wrong.go\n@@ -1,1 +1,2 @@\n+context.WithTimeout(ctx, time.Second)\n```"
	_, err := Parse(raw, ExtractDiffOptions{Pattern: PatternTimeout, ExpectedTargetPath: "client.go"})
	if !errors.Is(err, ErrInvalidDiff) {
		t.Errorf("expected ErrInvalidDiff, got %v", err)
	}
}

func TestParseRejectsEmpty(t *testing.T) {
	_, err := Parse("", ExtractDiffOptions{Pattern: PatternTimeout})
	if !errors.Is(err, ErrInvalidDiff) {
		t.Errorf("expected ErrInvalidDiff, got %v", err)
	}
}

func TestParseRejectsBadHunk(t *testing.T) {
	raw := "```diff\n--- a/x.go\n+++ b/x.go\n+context.WithTimeout\n```"
	_, err := Parse(raw, ExtractDiffOptions{Pattern: PatternTimeout})
	if !errors.Is(err, ErrInvalidDiff) {
		t.Errorf("expected ErrInvalidDiff, got %v", err)
	}
}

func TestParseAcceptsBareDiff(t *testing.T) {
	raw := "--- a/x.go\n+++ b/x.go\n@@ -1,1 +1,2 @@\n line\n+context.WithTimeout(ctx, time.Second)\n"
	_, err := Parse(raw, ExtractDiffOptions{Pattern: PatternTimeout})
	if err != nil {
		t.Errorf("Parse bare: %v", err)
	}
}

func TestParseExtractsRangeFromHunkHeader(t *testing.T) {
	raw := "--- a/x.go\n+++ b/x.go\n@@ -42,7 +42,10 @@\n line\n+context.WithTimeout(ctx, time.Second)\n"
	p, err := Parse(raw, ExtractDiffOptions{Pattern: PatternTimeout})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.TargetRange != [2]int{42, 51} {
		t.Errorf("range = %v, want [42 51]", p.TargetRange)
	}
}
