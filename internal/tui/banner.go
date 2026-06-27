package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/revelara-ai/orion/internal/health"
)

// Identity is the init banner's left-column identity block — pure data, no I/O, so Render is
// deterministic and snapshot-testable.
type Identity struct {
	Version string // e.g. "0.0.0-dev+a1e93c1"
	Branch  string // git branch
	Brain   string // "native · claude-opus-4-8 · 1M context · Anthropic" or "offline — deterministic"
	Cwd     string
	Session string
	Budget  string // "0 tok · $0.00"
}

// orionWordmark is the ORION block wordmark, shown only on wide terminals.
const orionWordmark = ` ██████  ██████  ██  ██████  ███   ██
██    ██ ██   ██ ██ ██    ██ ████  ██
██    ██ ██████  ██ ██    ██ ██ ██ ██
██    ██ ██   ██ ██ ██    ██ ██  ████
 ██████  ██   ██ ██  ██████  ██   ███`

const (
	// At/above bannerFullWidth the banner shows the wordmark + two columns side by side; below
	// it the wordmark is hidden and the columns stack (Hermes likewise drops its wordmark on
	// narrow terminals). One threshold keeps the layout from overflowing at any width.
	bannerFullWidth = 92
	bannerLeftCol   = 38 // capped identity-column width
	bannerRightCol  = 48 // capped readiness-column width
)

// Render produces the branded init status banner: an optional wordmark, a bordered panel with a
// left identity column and a right readiness column (Pipeline + Subsystems, color-coded), and a
// summary line. It is pure (deterministic given its inputs) and width-responsive — it drops the
// wordmark and collapses to a single column on narrow terminals rather than overflowing. It
// reuses the existing Revelara palette/glyph styles (it does not redefine colors).
func Render(rep health.Report, id Identity, width int) string {
	if width <= 0 {
		width = 80
	}
	inner := max(min(width-2, 96), 20) // content width inside the border

	full := width >= bannerFullWidth

	var out strings.Builder
	if full {
		out.WriteString(bannerStyle.Render(orionWordmark))
		out.WriteString("\n\n")
	}

	title := orionLabel.Render(fmt.Sprintf("Orion %s · %s", id.Version, id.Branch))
	left := lipgloss.NewStyle().Width(bannerLeftCol).Render(renderBannerIdentity(id))
	right := lipgloss.NewStyle().Width(bannerRightCol).Render(renderBannerReadiness(rep))

	var body string
	if full {
		cols := lipgloss.JoinHorizontal(lipgloss.Top, left, lipgloss.NewStyle().Render("   "), right)
		body = lipgloss.JoinVertical(lipgloss.Left, title, "", cols)
	} else {
		body = lipgloss.JoinVertical(lipgloss.Left, title, "", right, "", left)
	}

	ok, warn, fail := rep.Summary()
	summary := dimStyle.Render(fmt.Sprintf("%d/%d ready · %d warning(s) · %d failing · /help for commands", ok, ok+warn+fail, warn, fail))
	body = lipgloss.JoinVertical(lipgloss.Left, body, "", summary)

	out.WriteString(transPane.Width(inner).Render(body))
	out.WriteString("\n")
	out.WriteString(orionText.Width(width).Render("Welcome to Orion. Describe an intent, or ask how the build works."))
	out.WriteString("\n")
	return out.String()
}

func renderBannerIdentity(id Identity) string {
	lines := []string{
		starStyle.Render("  ✦   ·   ✦"),
		orionText.Render(id.Brain),
		dimStyle.Render(id.Cwd),
		dimStyle.Render("Session: " + id.Session),
		dimStyle.Render("budget: " + id.Budget),
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func renderBannerReadiness(rep health.Report) string {
	var b strings.Builder
	b.WriteString(cardTitle.Render("Pipeline") + "\n")
	for _, c := range rep.Pipeline {
		b.WriteString(bannerReadinessRow(c) + "\n")
	}
	b.WriteString(cardTitle.Render("Subsystems") + "\n")
	for i, c := range rep.Subsystems {
		b.WriteString(bannerReadinessRow(c))
		if i < len(rep.Subsystems)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func bannerReadinessRow(c health.Check) string {
	row := bannerGlyph(c.Status) + " " + orionText.Render(c.Name)
	if c.Status != health.OK && c.Detail != "" {
		row += "  " + dimStyle.Render(truncateBanner(c.Detail, 44))
	}
	return row
}

func bannerGlyph(s health.Status) string {
	switch s {
	case health.OK:
		return okGlyph.Render("✓")
	case health.Warn:
		return warnGlyph.Render("⚠")
	default:
		return failGlyph.Render("✗")
	}
}

func truncateBanner(s string, max int) string {
	if lipgloss.Width(s) <= max {
		return s
	}
	r := []rune(s)
	if max > 1 && len(r) > max-1 {
		return string(r[:max-1]) + "…"
	}
	return s
}
