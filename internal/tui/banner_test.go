package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/revelara-ai/orion/internal/health"
)

func sampleReport() health.Report {
	return health.Report{
		Pipeline: []health.Check{
			{Name: "intent→spec", Status: health.OK, Detail: "wired"},
			{Name: "proof harness", Status: health.OK, Detail: "behavioral · empirical · hazard"},
			{Name: "lsp gate", Status: health.Warn, Detail: "gopls not found"},
		},
		Subsystems: []health.Check{
			{Name: "sandbox-backend", Status: health.OK, Detail: "bwrap available"},
			{Name: "polaris", Status: health.Warn, Detail: "not logged in"},
			{Name: "agent-preset", Status: health.OK, Detail: "fixture"},
		},
	}
}

func sampleIdentity() Identity {
	return Identity{
		Version: "0.0.0-dev+abc1234", Branch: "main",
		Brain:   "native · claude-opus-4-8 · 1M context · Anthropic",
		Cwd:     "~/go/src/orion", Session: "20260627_1003", Budget: "0 tok · $0.00",
	}
}

// TestBannerContent (or-gik.2): the banner carries the title, every check name, the brain line,
// the section headers, and the status glyphs.
func TestBannerContent(t *testing.T) {
	out := Render(sampleReport(), sampleIdentity(), 100)
	for _, want := range []string{
		"Orion 0.0.0-dev+abc1234 · main", "intent→spec", "proof harness", "lsp gate",
		"sandbox-backend", "polaris", "agent-preset", "Pipeline", "Subsystems",
		"native · claude-opus-4-8", "ready",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("banner missing %q", want)
		}
	}
	for _, g := range []string{"✓", "⚠"} {
		if !strings.Contains(out, g) {
			t.Errorf("banner missing glyph %q", g)
		}
	}
}

// TestBannerWordmarkByWidth: the wordmark shows on wide terminals and is hidden on narrow ones.
func TestBannerWordmarkByWidth(t *testing.T) {
	if !strings.Contains(Render(sampleReport(), sampleIdentity(), 100), "██") {
		t.Error("wide banner should show the wordmark")
	}
	if strings.Contains(Render(sampleReport(), sampleIdentity(), 80), "██") {
		t.Error("narrow banner should hide the wordmark")
	}
}

// TestBannerNoOverflow: no rendered line exceeds the requested width, at any width.
func TestBannerNoOverflow(t *testing.T) {
	for _, w := range []int{50, 64, 80, 92, 100, 120} {
		out := Render(sampleReport(), sampleIdentity(), w)
		for _, line := range strings.Split(out, "\n") {
			if lw := lipgloss.Width(line); lw > w {
				t.Errorf("width %d: line overflows to %d cols: %q", w, lw, line)
			}
		}
	}
}

// TestBannerSummary: the summary line reflects the ok/warn/fail counts (4 ok, 2 warn, 0 fail).
func TestBannerSummary(t *testing.T) {
	out := Render(sampleReport(), sampleIdentity(), 100)
	if !strings.Contains(out, "4/6 ready") || !strings.Contains(out, "2 warning") {
		t.Errorf("summary line wrong:\n%s", out)
	}
}
