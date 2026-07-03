package tui

import (
	"regexp"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// mdMinWrap is the smallest word-wrap width Glamour's dark style still WRAPS at: the
// style reserves a 2-col document margin each side, so any wrap ≤ 4 makes Glamour's
// block width ≤ 0 and it stops wrapping, emitting full-length overflowing lines. We keep
// the wrap comfortably in the wrapping regime and clamp the final output to the true
// width with ansi.Hardwrap instead.
const mdMinWrap = 20

var (
	mdMu         sync.Mutex
	mdRenderer   *glamour.TermRenderer
	mdRenderWrap int
)

// getMDRenderer returns a cached dark-style Glamour renderer for the wrap width.
// Constructing a renderer compiles a Chroma style, so it is cached; a SINGLE entry
// suffices because a width change re-renders the entire transcript at the new width, so
// the old-width renderer becomes dead immediately (no unbounded map growth). Returns nil
// on construction error (renderMarkdown then falls back to raw).
func getMDRenderer(wrap int) *glamour.TermRenderer {
	mdMu.Lock()
	defer mdMu.Unlock()
	if mdRenderer != nil && mdRenderWrap == wrap {
		return mdRenderer
	}
	r, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"), glamour.WithWordWrap(wrap))
	if err != nil {
		return nil
	}
	mdRenderer, mdRenderWrap = r, wrap
	return r
}

// renderMarkdown renders md to ANSI that fits EXACTLY within width columns, owning 100%
// of wrapping and trimming so the caller emits the result VERBATIM (never re-wrapping it
// through a lipgloss width style, which would corrupt Chroma's SGR runs — see the S1
// rich-rendering design §3.1). It never panics and falls back to the raw text on any
// error or empty render.
func renderMarkdown(md string, width int) string {
	if width < 1 {
		width = 1
	}
	wrap := width
	if wrap < mdMinWrap {
		wrap = mdMinWrap
	}
	r := getMDRenderer(wrap)
	if r == nil {
		return md
	}
	out, err := r.Render(md)
	if err != nil {
		return md
	}
	// Glamour word-wraps only at whitespace — it never hard-breaks an unbreakable token
	// (URL, long path, long code identifier) and writes code lines verbatim. Clamp every
	// line to the true width so nothing overflows the pane; then close any SGR run left
	// open at a break so a wrapped colored token can't bleed style into the next line.
	out = closeSGRPerLine(ansi.Hardwrap(out, width, false))
	// The dark style brackets output with a document margin: a leading and trailing line
	// that is VISUALLY blank but made of color-filled spaces (not a bare "\n"), so trim by
	// visible content, not by newline — else a blank colored row sits under the "✦ Orion"
	// label.
	out = trimBlankLines(out)
	if out == "" {
		return md // all-whitespace / empty render → hand back the original
	}
	return out
}

// trimBlankLines drops leading and trailing lines that are visually empty (whitespace
// once ANSI is stripped), preserving interior blank lines (paragraph spacing).
func trimBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	blank := func(l string) bool { return strings.TrimSpace(ansi.Strip(l)) == "" }
	start, end := 0, len(lines)
	for start < end && blank(lines[start]) {
		start++
	}
	for end > start && blank(lines[end-1]) {
		end--
	}
	return strings.Join(lines[start:end], "\n")
}

// closeSGRPerLine appends a reset to any line that still has an open SGR run at its end,
// so ansi.Hardwrap breaking a colored token mid-run cannot bleed style into the next line
// or the pane border. A redundant reset is a no-op, so this is always safe and does not
// change display width (resets are zero-width).
func closeSGRPerLine(s string) string {
	const reset = "\x1b[0m"
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if strings.Contains(l, "\x1b[") && !strings.HasSuffix(l, reset) {
			lines[i] = l + reset
		}
	}
	return strings.Join(lines, "\n")
}

type proseKey struct {
	w    int
	text string
}

var (
	proseMu    sync.Mutex
	proseCache = map[proseKey]string{}
)

// renderProse renders a COMPLETED Orion-prose bubble to width columns: a structural
// unified diff gets +/- coloring (clamped + SGR-closed), everything else Markdown. It is
// MEMOIZED by (width, text): completed messages are immutable, so a long transcript
// re-renders in O(1) per message on each streamed token/keystroke instead of re-running
// Glamour over the whole history every frame.
func renderProse(text string, width int) string {
	k := proseKey{width, text}
	proseMu.Lock()
	if v, ok := proseCache[k]; ok {
		proseMu.Unlock()
		return v
	}
	proseMu.Unlock()

	var v string
	if looksLikeDiff(text) {
		v = closeSGRPerLine(ansi.Hardwrap(colorizeDiff(text), width, false))
	} else {
		v = renderMarkdown(text, width)
	}

	proseMu.Lock()
	if len(proseCache) > 1024 { // a width change makes prior entries dead; bound growth
		proseCache = map[proseKey]string{}
	}
	proseCache[k] = v
	proseMu.Unlock()
	return v
}

// diffHunk matches a proper ranged unified-diff hunk header, e.g. "@@ -1,3 +1,4 @@".
var diffHunk = regexp.MustCompile(`(?m)^@@ -\d+(,\d+)? \+\d+(,\d+)? @@`)

// looksLikeDiff reports whether s IS a unified diff — its first non-blank line is a
// "diff " header or a ranged hunk header. Anchoring to the START (not "contains
// anywhere") means ordinary prose that merely embeds a fenced ```diff block, a Markdown
// bullet, or a stray "@@" is never mistaken for a whole-message diff; such a message
// falls through to Markdown (which highlights the fenced block).
func looksLikeDiff(s string) bool {
	t := strings.TrimLeft(s, "\n")
	return strings.HasPrefix(t, "diff --git ") || strings.HasPrefix(t, "diff -") ||
		(strings.HasPrefix(t, "@@ ") && diffHunk.MatchString(t))
}

// diffLineKind classifies one diff line: "header" (+++/--- file headers), "hunk" (@@ …),
// "add" (+…), "del" (-…), or "context". Pure + env-independent, so the diff-coloring
// LOGIC is testable without a terminal (lipgloss emits no color under `go test`).
func diffLineKind(line string) string {
	switch {
	case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
		return "header"
	case strings.HasPrefix(line, "@@"):
		return "hunk"
	case strings.HasPrefix(line, "+"):
		return "add"
	case strings.HasPrefix(line, "-"):
		return "del"
	default:
		return "context"
	}
}

// colorizeDiff tints a unified diff line-by-line by diffLineKind: added lines green,
// removed red, hunk headers lavender, file headers dim, context unchanged. Mirrors
// colorizeReport. It preserves every line's text (styling only).
func colorizeDiff(s string) string {
	addStyle := lipgloss.NewStyle().Foreground(cSuccess)
	delStyle := lipgloss.NewStyle().Foreground(cDanger)
	var b strings.Builder
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch diffLineKind(line) {
		case "header":
			b.WriteString(dimStyle.Render(line))
		case "hunk":
			b.WriteString(starStyle.Render(line)) // lavender
		case "add":
			b.WriteString(addStyle.Render(line))
		case "del":
			b.WriteString(delStyle.Render(line))
		default:
			b.WriteString(line)
		}
	}
	return b.String()
}
