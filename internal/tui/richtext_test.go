package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/revelara-ai/orion/internal/acp"
)

// renderMarkdown must actually PARSE Markdown (strip the syntax markers), not merely
// wrap raw text in ANSI — Glamour wraps ALL input in SGR, so "contains ANSI" proves
// nothing. We assert the structural delta: markers are consumed, text survives.
func TestRenderMarkdownParsesStructure(t *testing.T) {
	w := 60
	cases := []struct {
		name        string
		in          string
		mustHave    string
		mustNotHave string
	}{
		{"heading marker consumed", "# Heading", "Heading", "# "},
		{"bold marker consumed", "**bold**", "bold", "**"},
		{"link markup consumed", "[label](http://x)", "label", "]("},
	}
	for _, c := range cases {
		out := renderMarkdown(c.in, w)
		if !strings.Contains(out, c.mustHave) {
			t.Errorf("%s: output missing %q:\n%q", c.name, c.mustHave, out)
		}
		if strings.Contains(out, c.mustNotHave) {
			t.Errorf("%s: output still contains raw marker %q (markdown not parsed):\n%q", c.name, c.mustNotHave, out)
		}
	}
}

// Every rendered line must fit within width — including unbreakable tokens (URLs, long
// identifiers) that Glamour does NOT hard-break. This forces the ansi.Hardwrap step.
func TestRenderMarkdownHonorsWidthForUnbreakableTokens(t *testing.T) {
	w := 40
	inputs := []struct {
		name string
		in   string
	}{
		{"200-char token", strings.Repeat("x", 200)},
		{"long code identifier", "```go\nvar aVeryLongUnbreakableIdentifierNameThatExceedsFortyColumnsEasily = 1\n```"},
		{"long url in prose", "See https://example.com/a/very/long/path/that/keeps/going/way/past/forty/columns for details"},
	}
	for _, c := range inputs {
		out := renderMarkdown(c.in, w)
		for i, line := range strings.Split(out, "\n") {
			if lw := lipgloss.Width(line); lw > w {
				t.Errorf("%s: line %d width %d exceeds %d: %q", c.name, i, lw, w, line)
			}
		}
	}
	// A tiny width must not fall into Glamour's no-wrap regime and overflow.
	for _, line := range strings.Split(renderMarkdown("the quick brown fox jumps", 3), "\n") {
		if lw := lipgloss.Width(line); lw > 3 {
			t.Errorf("small-width line %q width %d > 3", line, lw)
		}
	}
}

// Syntax highlighting: a Go fence colors keywords differently than the same source in a
// plain-text fence. Chroma emits ANSI even under `go test`, so this delta is observable.
func TestRenderMarkdownHighlightsCode(t *testing.T) {
	w := 60
	goRender := renderMarkdown("```go\nfunc main() {}\n```", w)
	txtRender := renderMarkdown("```text\nfunc main() {}\n```", w)
	if !strings.Contains(goRender, "func") {
		t.Fatalf("go code body missing: %q", goRender)
	}
	if goRender == txtRender {
		t.Errorf("go fence should be highlighted differently than a text fence, but renders identically")
	}
}

// No panic on adversarial inputs; empty/all-whitespace returns the original (never empty
// for non-empty input, never a crash).
func TestRenderMarkdownRobustness(t *testing.T) {
	for _, in := range []string{"", "   ", "\n\n", strings.Repeat("word ", 5000), "```go\nunclosed fence", "plain text no markdown"} {
		out := renderMarkdown(in, 40) // must not panic
		if strings.TrimSpace(in) != "" && out == "" {
			t.Errorf("non-empty input %q rendered empty", in[:min(20, len(in))])
		}
	}
	if got := renderMarkdown("hi", 0); got == "" { // width<1 must not panic or blank out
		t.Error("width 0 blanked the output")
	}
}

// looksLikeDiff must fire only on STRUCTURAL diffs, never on prose that merely uses
// +/-/@@ (a Markdown bullet, a math expression) — else colorizeDiff mis-colors chat.
func TestLooksLikeDiff(t *testing.T) {
	yes := []string{
		"diff --git a/x.go b/x.go\nindex 1..2\n--- a/x.go\n+++ b/x.go\n@@ -1,2 +1,3 @@\n line\n+add",
		"@@ -10,3 +10,4 @@ func f() {\n context\n-old\n+new",
	}
	no := []string{
		"- first bullet\n- second bullet",
		"pros and cons: + faster, - riskier",
		"an email @@ handle and a +1 vote",
		"",
		// A normal chat message that merely CONTAINS a fenced diff block must render as
		// Markdown (so the prose + fence highlight), not be treated wholesale as a diff.
		"Here is the **change**:\n\n```diff\n@@ -1,2 +1,3 @@\n-old\n+new\n```\n\nLet me know.",
	}
	for _, s := range yes {
		if !looksLikeDiff(s) {
			t.Errorf("should be detected as a diff:\n%q", s)
		}
	}
	for _, s := range no {
		if looksLikeDiff(s) {
			t.Errorf("prose wrongly detected as a diff:\n%q", s)
		}
	}
}

// diffLineKind classifies each unified-diff line type; this is the coloring LOGIC,
// testable without a terminal.
func TestDiffLineKind(t *testing.T) {
	cases := map[string]string{
		"+++ b/file.go":   "header",
		"--- a/file.go":   "header",
		"@@ -1,2 +1,3 @@": "hunk",
		"+added line":     "add",
		"-removed line":   "del",
		" context line":   "context",
		"plain text":      "context",
	}
	for line, want := range cases {
		if got := diffLineKind(line); got != want {
			t.Errorf("diffLineKind(%q) = %q, want %q", line, got, want)
		}
	}
}

// colorizeDiff must preserve every line's text (styling only, no content loss / reorder).
// Under `go test` lipgloss emits no color, so the output equals the input here — which is
// exactly the content-preservation guarantee we want to lock.
func TestColorizeDiffPreservesContent(t *testing.T) {
	in := "diff --git a/x b/x\n@@ -1,2 +1,2 @@\n context\n-old line\n+new line"
	out := colorizeDiff(in)
	if inN, outN := strings.Count(in, "\n"), strings.Count(out, "\n"); inN != outN {
		t.Fatalf("line count changed: in %d, out %d", inN, outN)
	}
	for _, line := range strings.Split(in, "\n") {
		if !strings.Contains(out, strings.TrimSpace(line)) {
			t.Errorf("colorizeDiff dropped content %q", line)
		}
	}
}

// The actively-streaming bubble renders RAW (markers intact); once the turn completes it
// flips to rendered Markdown (markers consumed). An env-independent content transform.
func TestStreamingBubbleRawThenMarkdown(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.WindowSizeMsg{Width: 60, Height: 20})
	m.inFlight = true // a turn is in flight
	m = feed(m, streamMsg{u: acp.Update{Kind: "agent_message", Text: "- item one"}})

	raw := m.renderTranscript()
	if !strings.Contains(raw, "- item one") {
		t.Fatalf("active streaming bubble should render RAW, got:\n%s", raw)
	}

	m = feed(m, turnDoneMsg{}) // turn completes → flip to markdown
	// Glamour splits words across SGR spans, so compare visible text (ANSI stripped).
	done := ansi.Strip(m.renderTranscript())
	if !strings.Contains(done, "item one") {
		t.Fatalf("completed bubble lost its text:\n%s", done)
	}
	if strings.Contains(done, "- item one") {
		t.Fatalf("completed bubble should render markdown (bullet consumed), still raw:\n%s", done)
	}
}

// Markdown/diff content must reflow across widths without breaking the layout invariant
// (View height == terminal height) or panicking — the Bug-1 guarantee holds with rich content.
func TestRichContentResizeStaysExact(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.WindowSizeMsg{Width: 50, Height: 24})
	m = feed(m, streamMsg{u: acp.Update{Kind: "agent_message", Text: "# Title\n\nSome **bold** prose with a https://example.com/very/long/link and a list:\n- alpha\n- beta"}})
	m = feed(m, turnDoneMsg{}) // render as markdown
	// Realistic terminal widths (a ~25-col status line wraps below ~28, a pre-existing
	// narrow-width layout limitation independent of rich rendering).
	for _, w := range []int{40, 60, 80, 50} {
		m = feed(m, tea.WindowSizeMsg{Width: w, Height: 24})
		if h := lipgloss.Height(m.View()); h != 24 {
			t.Fatalf("width %d: View height %d != 24 (layout broke with rich content)", w, h)
		}
	}
}

// A hard-wrapped colored code line must not leave an SGR run open at end-of-line, or the
// style bleeds into the next row / the pane border.
func TestRenderMarkdownClosesSGRPerLine(t *testing.T) {
	out := renderMarkdown("```go\nvar x = aVeryLongUnbreakableIdentifierNameThatExceedsThirtyColumnsForSure\n```", 30)
	for i, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "\x1b[") && !strings.HasSuffix(line, "\x1b[0m") {
			t.Errorf("line %d leaves an SGR run open (bleed risk): %q", i, line)
		}
	}
}

// End-to-end: a message that IS a unified diff renders through the diff path (each line
// preserved on its own line), not markdown-collapsed into reflowed prose.
func TestDiffRendersThroughTranscript(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.WindowSizeMsg{Width: 70, Height: 20})
	diff := "diff --git a/x.go b/x.go\n@@ -1,2 +1,2 @@\n context\n-old removed line\n+new added line"
	m = feed(m, streamMsg{u: acp.Update{Kind: "agent_message", Text: diff}})
	m = feed(m, turnDoneMsg{})
	tr := ansi.Strip(m.renderTranscript())
	for _, want := range []string{"diff --git a/x.go", "-old removed line", "+new added line"} {
		if !strings.Contains(tr, want) {
			t.Errorf("diff line %q missing (was it markdown-collapsed?):\n%s", want, tr)
		}
	}
}

// The RAW-while-streaming bubble is the last ORION agent_message, tracked by backward
// scan — a trailing tool_call bubble must NOT flip the just-streamed narration to
// markdown mid-turn (which could render a half-open code fence).
func TestActiveTracksLastAgentMessageNotTail(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.WindowSizeMsg{Width: 60, Height: 20})
	m.inFlight = true
	m = feed(m, streamMsg{u: acp.Update{Kind: "agent_message", Text: "# still streaming"}})
	m = feed(m, streamMsg{u: acp.Update{Kind: "tool_call", Text: "run something"}})
	if tr := m.renderTranscript(); !strings.Contains(tr, "# still streaming") {
		t.Fatalf("agent_message before a trailing tool_call must stay RAW (heading marker intact):\n%s", tr)
	}
}

// Only Orion prose (agent_message / default) is markdown-rendered; other roles/kinds keep
// their text verbatim (markers not consumed).
func TestNonProseKindsStayLiteral(t *testing.T) {
	m := newTestConvo(t)
	m = feed(m, tea.WindowSizeMsg{Width: 70, Height: 20})
	cases := []struct {
		mm      msg
		literal string
	}{
		{msg{role: "you", text: "# not a heading"}, "# not a heading"},
		{msg{role: "orion", kind: "command", text: "**stays literal**"}, "**stays literal**"},
		{msg{role: "orion", kind: "tool_call", text: "- not a bullet"}, "- not a bullet"},
		{msg{role: "orion", kind: "plan", text: "## plan text"}, "## plan text"},
	}
	for _, c := range cases {
		out := ansi.Strip(m.renderMsg(c.mm, 70, false))
		if !strings.Contains(out, c.literal) {
			t.Errorf("kind %q/%q: marker consumed, want literal %q:\n%s", c.mm.role, c.mm.kind, c.literal, out)
		}
	}
}
