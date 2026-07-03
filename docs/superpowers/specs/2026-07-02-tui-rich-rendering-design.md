# S1 · TUI Rich Rendering — Design

**Date:** 2026-07-02
**Status:** Approved (design); revised after adversarial spec review (20 empirically-verified findings folded in)
**Scope:** Sub-project S1 of the "Claude-Code-parity TUI" effort. Preserves Orion's
conductor/proof architecture (decision: *polish the conductor REPL*). TUI layer only —
no conductor, subagent, proof, or ACP changes.

> **Review note.** An adversarial review (5 dimensions × verify) reproduced, against the
> real glamour v1.0.0 + lipgloss v1.1.0 in the module cache, several assumptions in the
> first draft that were false. The corrections are woven in below and called out with
> **[R]** where they overturned the original design.

## 1. Overview

Orion's conversation transcript renders agent prose as plain, lipgloss-styled text.
This slice makes **Orion's conversational prose** render as **Markdown** (headings,
lists, bold/italic, links) with **syntax-highlighted code blocks**, and renders
**unified diffs with red/green line coloring** — matching the Claude Code "feel" while
keeping token streaming fast and flicker-free.

It is a rendering change isolated to `internal/tui`. The conductor still produces the
same text; only the transcript's presentation changes.

## 2. Goals / Non-goals

### Goals
- Markdown rendering of **Orion conversational prose** (`agent_message` / default kind).
- Syntax highlighting of fenced code blocks (via Glamour → Chroma).
- Unified-diff rendering: `+` green / `-` red / `@@` dim, applied only to content that is
  **structurally** a diff.
- Streaming stays fast: the actively-streaming message renders as raw text; it flips to
  rendered Markdown when the turn completes (`turnDoneMsg`).
- Correct word-wrap that reflows on resize, and — **[R]** — never overflows the pane even
  for unbreakable tokens (URLs, long paths, hashes, long code identifiers), because
  Glamour does **not** hard-break whitespace-free runs.
- No panics on empty input, huge paste, half-open code fences, non-Markdown text, or
  resize. A render error falls back to raw text — the transcript never crashes.

### Non-goals (other slices / follow-ups)
- Interactive diff **preview at permission time** → S2.
- **[R]** A custom Orion-palette Glamour theme → follow-up. First cut uses Glamour's
  built-in `dark` style, **whose accent colors (blue/cyan headings, teal links) will
  visibly differ from Orion's lavender/indigo chrome** — an accepted, temporary color
  seam, not an invisible one. Tuning a palette-matched Glamour style closes it later.
- `@`-file autocomplete, new slash commands, status chrome → S3.
- Any change to what the conductor generates, or to the proof pipeline.

## 3. Architecture

New file **`internal/tui/richtext.go`** — a self-contained rendering helper. It has one
new dependency (Glamour) and no knowledge of the conductor. `conversation.go` calls into
it from `renderMsg`.

### 3.1 `renderMarkdown(md string, width int) string`
Renders Markdown to ANSI that fits **exactly** within `width` columns, owning **100% of
wrapping and indentation** so the caller never re-wraps it.

Pipeline:
1. **Clamp the wrap width. [R]** Glamour disables wrapping entirely when its internal
   block width (`WordWrap − margin*2`, margin=2 for the dark style) drops to ≤0, i.e. for
   any `WordWrap ≤ 4` it emits full-length unwrapped lines. So pass Glamour
   `wrap := max(width, 20)` (a floor that stays safely in the wrapping regime); never pass
   `width < 1`.
2. **Render** via a **cached `*glamour.TermRenderer` keyed by the exact `wrap` int**
   (`map[int]*glamour.TermRenderer`, rebuilt only when a new width appears), with
   `glamour.WithStandardStyle("dark")` + `glamour.WithWordWrap(wrap)`.
3. **Hard-wrap every output line to `width`. [R]** Glamour only breaks at whitespace and
   never hard-breaks an unbreakable token (verified: `WordWrap(40)` → a URL renders ~69
   cols, a long code identifier ~67 cols, a 200-char token ~203 cols). Post-process the
   rendered output with `ansi.Hardwrap(out, width, true)` (from
   `github.com/charmbracelet/x/ansi`, already in Orion's module graph) so every line —
   including highlighted code — is cut at the wrap column with ANSI preserved.
4. **Trim leading AND trailing blank lines. [R]** The dark style prepends a document
   `block_prefix` `"\n"` and pads trailing blanks (`render("hello",w)` = `"\n hello…\n\n"`).
   Use `strings.Trim(out, "\n")` so there is no blank first row under the "✦ Orion" label.
5. **Never return empty for non-empty input. [R]** If the trimmed result is empty (Glamour
   emits nothing for all-whitespace input), return the original `md`. On any Glamour
   error, return the original `md`. Never panics.

**[R] The output is emitted VERBATIM by the caller.** It must NOT pass through any
wrapping lipgloss style (`.Width()`, word-wrap, or a `.MarginLeft()` implemented as
fixed-width padding): lipgloss would *re-wrap* the already-wrapped, Chroma-colored text,
splitting tokens mid-word and corrupting the SGR runs (verified). The "✦ Orion" label and
the 2-column left indent are applied as a **per-line string prefix**, not via a wrapping
style. `renderMarkdown` therefore owns wrapping/indent end-to-end.

### 3.2 `colorizeDiff(s string) string`
Line-oriented, mirroring `colorizeReport`: a leading `+` (not `+++`) → green; leading `-`
(not `---`) → red; a `@@ … @@` hunk header → dim/lavender; `+++`/`---` file headers → dim;
everything else unchanged. Pure string→string, uses the existing palette styles. **[R]**
Because lipgloss emits color only when the active color profile is a real terminal, its
output is plain in production-vs-test asymmetrically — see §6 for how the tests force a
color profile so the coloring is actually asserted.

### 3.3 Integration in `renderMsg(mm msg, w int, active bool)`
The method gains an `active bool` (is this the message currently streaming). Dispatch by
`mm.kind`:

| kind | rendering |
|------|-----------|
| `agent_message`, `""` (Orion prose) | `renderMarkdown` — **unless `active`**, then raw. Output emitted **verbatim** (no outer `.Width()`); label + 2-col indent via per-line prefix. |
| structurally-a-diff body (see §4.3) | `colorizeDiff` |
| `spec`, `command` | **unchanged — [R] NOT Markdown.** `formatSpecCard`/command bodies are **column-aligned, whitespace-significant key/value tables** (`fmt.Sprintf("%-13s …")`); Glamour would collapse the alignment. They keep their current plain `specCard` rendering. |
| `you`, `tool_call`, `plan`, `build_report`, `permission` | **unchanged** |

Only Orion prose flows through `renderMarkdown`. `build_report` keeps `colorizeReport`;
`you` stays plain; `tool_call` stays the dim activity line.

## 4. Data flow

### 4.1 Streaming state machine
- **Invariant [R]:** the *active* message is `msgs[len-1]` iff `m.inFlight` **and**
  `msgs[len-1]` is an Orion `agent_message`. This is a per-frame pure function of
  `inFlight` + last-message kind — no turn-scoped state. It naturally handles the common
  native shape `agent_message → tool_call → agent_message`: after a `tool_call` bubble the
  next streamed prose is a fresh `agent_message` tail, correctly marked active; a
  `tool_call`/`permission` tail is never active.
- **Empty/error tails [R]:** `turnDoneMsg`'s error path appends `msg{role:"orion",
  text:"error: …"}` with **empty kind** — the active rule does not mark it active (kind ≠
  `agent_message`), so it renders through the default path. `renderMarkdown` on an empty
  body returns the original (empty) → renders nothing. No special-case needed; documented
  so a future refactor doesn't regress it.
- **Flip:** when `turnDoneMsg` sets `inFlight=false`, the former active tail is no longer
  active → next render shows it as Markdown. The raw→Markdown line-count change only ever
  affects the **tail** bubble, so a scrolled-up reader (content above the tail) is not
  yanked — the existing "don't yank a scrolled-up user" guarantee is unaffected.

### 4.2 Resize
`WindowSizeMsg` → `relayout()` (from the input-wrap slice) updates the transcript inner
width, then `render()` rebuilds viewport content. `renderMarkdown` sees the new `w`,
builds/uses the cached renderer for that `wrap`, and everything reflows.

### 4.3 Identifying diff content
`colorizeDiff` is applied only when a message body is **structurally** a unified diff,
via `looksLikeDiff(s) bool` that requires a real signature — **[R]** a line matching
`^diff --git ` **or** a proper ranged hunk header `^@@ -\d+(,\d+)? \+\d+(,\d+)? @@`. The
loose "contains a `+`/`-` line" trigger is dropped, so ordinary prose (e.g. a Markdown
bullet `- item`, which anyway routes through `renderMarkdown`, not `colorizeDiff`) is
never mis-colored. `git`-tool results surfaced in the transcript thus get colored without
a new message kind.

## 5. Error handling
- Glamour render error → return raw `md`.
- `width` → wrap floored to `max(width, 20)` (§3.1 step 1); the final `ansi.Hardwrap` uses
  the true `width` (floored to ≥1) so nothing exceeds the pane.
- Empty / all-whitespace input → returns the original text (never empty for non-empty
  input, never panics).
- Half-open code fence mid-stream → never reaches Glamour (active bubble is raw); a
  completed message with a stray fence renders (Glamour tolerates it) or falls back to raw.

## 6. Testing (TDD)
All in `internal/tui`, provider-free. **[R] Two environment facts drive the assertions:**
(a) **Glamour/Chroma emit ANSI even under `go test`** (they don't gate on a TTY), so
markdown/highlight assertions CAN inspect SGR — but must test a **delta**, since Glamour
wraps *all* input (even plain prose) in SGR + padding. (b) **lipgloss strips color under
`go test`** (no TTY → Ascii profile), so any assertion on `colorizeDiff`'s colors must
first force a color profile (`lipgloss.SetColorProfile(termenv.TrueColor)`, restored via
`t.Cleanup`).

1. **renderMarkdown — structure delta, not "has ANSI":** `renderMarkdown("# Heading", w)`
   contains a heading-style SGR (e.g. a background-color param) that `renderMarkdown("Heading",
   w)` does not; a `- a\n- b` list renders bullet glyphs absent from the plain form.
2. **renderMarkdown — width is honored for unbreakable tokens:** inputs include a ~200-char
   whitespace-free token **and** a fenced code block with a long identifier;
   assert `lipgloss.Width(line) <= w` for every output line (forces the §3.1 hard-wrap).
   Also a case at small `w` (e.g. 3) asserting no unwrapped overflow (forces the clamp).
3. **renderMarkdown — links + syntax highlighting (Goals, currently uncovered):**
   `[t](u)` output carries the underline SGR that `t u` lacks; a ```go fence produces
   Chroma keyword/function SGR that the same source in a ```text fence does not.
4. **renderMarkdown — no panic** on empty, all-whitespace, huge paste, half-open fence,
   plain non-markdown; and returns the original for empty/all-whitespace input.
5. **colorizeDiff (color profile forced):** a sample unified diff colors `+`/`-`/`@@`
   lines and leaves context + `+++`/`---` headers as content; assert changed lines differ
   from their plain form while context lines are byte-identical. `looksLikeDiff` is true
   for a real `diff --git`/hunk and **false** for prose containing stray `+`/`-`/`@@`.
6. **Streaming flip — content transformation, not ANSI presence:** feed `- item` as
   `agent_message` deltas while `inFlight`; the active render (computed exactly as §4.1)
   contains the literal `- item`; after `turnDoneMsg` the same bubble renders the Markdown
   bullet (glyph/indent) — an env-independent difference.
7. **Resize safety (primed):** build via `newTestConvo`, feed `WindowSizeMsg{40,24}`, feed
   a `streamMsg` with markdown + a diff body (activates + fills the viewport), re-feed the
   size — mirroring `TestInputGrowsAndLayoutStaysExact`'s proven priming — assert no panic
   and the `lipgloss.Height(View()) == termH` invariant still holds across several widths.

## 7. Dependency & version impact **[R]**
Add `github.com/charmbracelet/glamour@v1.0.0`. This pulls a subtree that is **net-new** to
Orion's module graph (not just Chroma): `alecthomas/chroma/v2` (highlighting),
`yuin/goldmark` + `yuin/goldmark-emoji` (Markdown parse), `microcosm-cc/bluemonday` (HTML
sanitize), and their transitives. All are present in the local module cache (offline-OK).

**Version skew to verify:** under Go MVS, `x/ansi` stays at Orion's higher `v0.11.6`
(unchanged), but glamour requires a `lipgloss v1.1.1-0.2025…` pre-release that semver
ranks **above** Orion's pinned `v1.1.0`, so `go mod tidy` will **bump lipgloss**. Because
the input-wrap slice's layout math depends on lipgloss width/height behavior, the
implementation MUST, immediately after `go get`, run the **full `internal/tui` suite +
build + vet** to confirm the bump doesn't shift wrapping/measurement. If it regresses,
pin lipgloss back with an explicit `require`/`replace` and re-evaluate.

## 8. Rollout / commit plan
One slice, TDD. First step: `go get glamour@v1.0.0` + `go mod tidy`, then the tui-suite
regression check above (gate on the lipgloss bump) before writing any render code. Commit
as a single focused change once the full tui suite + build + vet are green. Degrades to
raw text on any render error — no feature flag needed.
