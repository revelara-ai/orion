// Package tui is Orion's terminal UI, built on the charmbracelet stack
// (bubbletea Model/Update/View, lipgloss styling, bubbles viewport/textarea/
// spinner). It is the authoritative control plane and conversational surface
// (PRD UI Navigation; SPEC §0 "chat-first, not a fleet dashboard").
//
// The Conversation pane is a thin, ASYNC ACP client (or-0u6): each developer line
// is sent to the auto-started, primed Conductor agent as session/prompt via a
// tea.Cmd (the Update loop never blocks); session/update notifications stream into
// the chat incrementally via Program.Send; a spinner runs while a turn is in
// flight. Spec ratification is a real, blocking session/request_permission gate
// (or-pp9): the Conductor asks, the TUI surfaces an approval card, and the human
// authorizes — the gate blocks in its own goroutine (off the read loop), so the
// UI stays responsive and nothing deadlocks over the in-process pipe.
package tui

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/conductor"
	"github.com/revelara-ai/orion/internal/health"
	"github.com/revelara-ai/orion/internal/llm"
	"github.com/revelara-ai/orion/internal/orchestrator"
)

// acpServer is the Conductor brain the TUI drives over ACP (native or fallback).
type acpServer interface {
	Serve(ctx context.Context, r io.Reader, w io.Writer) error
}

// conductorBrain selects the native LLM "Orion" agent when ANTHROPIC_API_KEY is
// set, else the deterministic conductor (offline/CI fallback). Both satisfy
// acp.PromptFunc, so the TUI is identical for either.
func conductorBrain(oc *orchestrator.Conductor) (acpServer, string) {
	role := conductor.RoleTemplate{Project: "orion"}
	if key := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); key != "" {
		model := os.Getenv("ORION_MODEL")
		if model == "" {
			model = llm.DefaultAnthropicModel
		}
		return conductor.NewOrionAgent(llm.NewAnthropic(key, model), oc, role), "native · " + model
	}
	return conductor.NewConductorAgent(role, oc), "offline — set ANTHROPIC_API_KEY for the full grill"
}

const emptyState = "Conductor ready (in-process, over ACP). Describe what you want to build."

// Revelara / Polaris palette — revelara-ai/design/colors_and_type.css. A deep
// indigo "void" night sky with electric-indigo, the lavender star-glow, and a
// warm-rose accent.
var (
	cIndigo   = lipgloss.Color("#6D3CC8") // electric indigo — primary accent
	cLavender = lipgloss.Color("#B99DE5") // star glow / links
	cRose     = lipgloss.Color("#D4818D") // warm rose — secondary accent
	cText     = lipgloss.Color("#F0ECF7") // primary text on dark
	cMuted    = lipgloss.Color("#A8A0BA") // secondary text
	cFaint    = lipgloss.Color("#8E859C") // metadata / disabled
	cSuccess  = lipgloss.Color("#5FB39A")
	cWarning  = lipgloss.Color("#E6B260")
	cDanger   = lipgloss.Color("#D4656D")

	bannerStyle = lipgloss.NewStyle().Bold(true).Foreground(cLavender)
	starStyle   = lipgloss.NewStyle().Foreground(cLavender)
	orionLabel  = lipgloss.NewStyle().Bold(true).Foreground(cIndigo)
	orionText   = lipgloss.NewStyle().Foreground(cText)
	youBlock    = lipgloss.NewStyle().Foreground(cMuted).Align(lipgloss.Right)
	youTag      = lipgloss.NewStyle().Foreground(cRose)
	dimStyle    = lipgloss.NewStyle().Foreground(cFaint)
	toolStyle   = lipgloss.NewStyle().Foreground(cFaint).Italic(true)
	planStyle   = lipgloss.NewStyle().Bold(true).Foreground(cSuccess)
	specCard    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cIndigo).Padding(0, 1)
	permCard    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cWarning).Padding(0, 1)
	buildCard   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cSuccess).Padding(0, 1)
	cardTitle   = lipgloss.NewStyle().Bold(true).Foreground(cIndigo)
	buildTitle  = lipgloss.NewStyle().Bold(true).Foreground(cSuccess)
	okGlyph     = lipgloss.NewStyle().Foreground(cSuccess)
	warnGlyph   = lipgloss.NewStyle().Foreground(cWarning)
	failGlyph   = lipgloss.NewStyle().Foreground(cDanger)

	cBorder   = lipgloss.Color("#362D50")                                                                    // divider on the void
	transPane = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cBorder)               // top: transcript
	inputPane = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cIndigo).Padding(0, 1) // bottom: input + status
)

// ── async message types ──────────────────────────────────────────────────────

type streamMsg struct{ u acp.Update } // a streamed session/update
type turnDoneMsg struct{ err error }  // a prompt turn completed
type permMsg struct {                 // the agent requested a permission
	req   acp.PermissionRequest
	reply chan acp.PermissionResult
}

// msg is one rendered turn in the conversation.
type msg struct {
	role string // "you" | "orion"
	kind string // "" | agent_thought | agent_message | spec | plan | permission | tool_permission
	text string
	tool string // for tool_permission: the mutating tool name (bash / write_file / edit_file)
}

// Conversation is the default pane: an async chat client over the Conductor agent.
type Conversation struct {
	client *ACPClient
	sid    string
	oc     *orchestrator.Conductor
	gate   *programGate

	input textarea.Model
	vp    viewport.Model
	sp    spinner.Model
	msgs  []msg

	width, height int
	ready         bool
	inFlight      bool
	pendingPerm   chan acp.PermissionResult
	permKind      string // the pending permission's kind ("" | spec_ratify | tool)
	permExpanded  bool   // a tool-permission card's diff preview is expanded
	quitting      bool
	quitArmed     bool   // a Ctrl+C at an idle empty prompt arms quit; a second one exits
	brain         string // active brain label (native · model / offline …)

	// Init status banner (or-gik.3): the readiness report + identity, supplied by the launcher
	// (which owns version/branch + the cached Polaris probe). Rendered as the empty-state body.
	bannerReport health.Report
	bannerID     Identity
	bannerSet    bool

	// commands are the injected admin/management slash-commands (or-dz9).
	commands []Command
	// paletteIdx is the highlighted row in the command palette (shown while the input is a bare
	// /command prefix; Tab cycles + completes it).
	paletteIdx int
	// Input history (or-d38): ↑/↓ recall previously-submitted lines, shell-style. histIdx is the
	// cursor (== len(history) at the live line); draft holds the unsent line stashed while browsing.
	history []string
	histIdx int
	draft   string
}

// NewConversation builds the pane bound to a connected ACP client + session.
func NewConversation(client *ACPClient, sid string, oc *orchestrator.Conductor, gate *programGate) Conversation {
	// A textarea (not a single-line textinput) so long lines WRAP and the box grows
	// vertically from the bottom instead of scrolling horizontally off the edge — the
	// height is driven each frame by relayout (wrappedRows). Line numbers off, cursor-
	// line highlight off, so it reads as a plain prompt inside the input pane.
	ti := textarea.New()
	ti.Placeholder = "" // blank: the banner already explains what to do (or-gik.3)
	ti.ShowLineNumbers = false
	ti.CharLimit = 0
	// Prompt only on the first display row; soft-wrapped continuation rows align
	// under the text (a single wrapped line reads as ONE entry, not several).
	ti.SetPromptFunc(2, func(displayLine int) string {
		if displayLine == 0 {
			return "› "
		}
		return ""
	})
	ti.FocusedStyle.Base = lipgloss.NewStyle()
	ti.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ti.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(cIndigo)
	ti.FocusedStyle.Text = lipgloss.NewStyle().Foreground(cText)
	ti.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(cFaint)
	ti.BlurredStyle = ti.FocusedStyle
	ti.Cursor.Style = lipgloss.NewStyle().Foreground(cLavender)
	ti.SetHeight(1)
	ti.Focus()
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(cLavender) // the star-glow accent
	return Conversation{client: client, sid: sid, oc: oc, gate: gate, input: ti, sp: sp}
}

// Init satisfies tea.Model.
func (m Conversation) Init() tea.Cmd { return m.input.Focus() }

// Update handles input, window sizing, streamed updates, permission requests, and
// turn completion. The Update loop NEVER blocks on the Conductor.
func (m Conversation) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch t := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = t.Width, t.Height
		if !m.ready {
			m.vp = viewport.New(1, 1) // real dimensions come from relayout below
			m.ready = true
		}
		m.relayout()
		m.render()
		return m, nil

	case streamMsg:
		// Streamed text deltas (agent_message) accumulate into the current Orion
		// bubble so a turn renders as one growing message, not one bubble per token.
		// Other kinds (tool_call, plan, spec, permission) are discrete bubbles, and
		// any of them ends the current text run.
		if t.u.Kind == "agent_message" && len(m.msgs) > 0 {
			if last := &m.msgs[len(m.msgs)-1]; last.role == "orion" && last.kind == "agent_message" {
				last.text += t.u.Text
				m.render()
				return m, nil
			}
		}
		m.msgs = append(m.msgs, msg{role: "orion", kind: t.u.Kind, text: t.u.Text})
		if t.u.Kind == "plan" && strings.Contains(t.u.Text, "ratified") {
			m.input.Placeholder = ""
		}
		m.render()
		return m, nil

	case permMsg:
		m.pendingPerm = t.reply
		m.permKind = t.req.Kind
		m.permExpanded = false
		if t.req.Kind == "tool" {
			// A mutating-tool approval: single-key y/a/n card with a diff/command preview.
			m.msgs = append(m.msgs, msg{role: "orion", kind: "tool_permission", tool: t.req.Tool, text: t.req.Preview})
			m.input.Placeholder = "y allow · a always · n deny"
		} else {
			m.msgs = append(m.msgs, msg{role: "orion", kind: "permission", text: t.req.Title})
			m.input.Placeholder = "y to ratify · e to edit"
		}
		m.render()
		// A permission request BLOCKS the conversation and asks the developer to act.
		// Force the card into view even if they had scrolled up to read history —
		// otherwise they're prompted to respond with no visible prompt.
		if m.ready {
			m.vp.GotoBottom()
		}
		return m, nil

	case turnDoneMsg:
		m.inFlight = false
		if t.err != nil {
			m.msgs = append(m.msgs, msg{role: "orion", text: "error: " + t.err.Error()})
		}
		m.render()
		return m, nil

	case CommandResultMsg:
		// The deferred outcome of an async slash-command (e.g. /mcp login) — appended when its
		// follow-up tea.Cmd completes.
		m.msgs = append(m.msgs, msg{role: "orion", kind: "command", text: t.Text})
		m.render()
		return m, nil

	case spinner.TickMsg:
		if !m.inFlight {
			return m, nil
		}
		var cmd tea.Cmd
		m.sp, cmd = m.sp.Update(t)
		return m, cmd

	case tea.KeyMsg:
		wasArmed := m.quitArmed
		m.quitArmed = false // any keypress disarms the pending double-Ctrl+C quit

		// Ctrl+D exits immediately (EOF), like a shell.
		if t.Type == tea.KeyCtrlD {
			return m, m.quit()
		}
		// Alt+Enter / Ctrl+J insert a newline into the multi-line input; plain Enter
		// submits. (Terminals rarely distinguish Shift+Enter, so Alt+Enter is the
		// portable "newline" chord.)
		if (t.Type == tea.KeyEnter && t.Alt) || t.Type == tea.KeyCtrlJ {
			m.input.InsertString("\n")
			m.relayout()
			return m, nil
		}
		// A pending TOOL-permission card is answered by single keys (y/a/n/e), never by
		// typing into the input or recalling history.
		if m.pendingPerm != nil && m.permKind == "tool" {
			switch {
			case t.Type == tea.KeyEnter, isRuneKey(t, 'y'):
				return m, m.answerToolPerm("allow_once")
			case isRuneKey(t, 'a'):
				return m, m.answerToolPerm("allow_always")
			case t.Type == tea.KeyEsc, isRuneKey(t, 'n'):
				return m, m.answerToolPerm("deny")
			case isRuneKey(t, 'e'):
				m.permExpanded = !m.permExpanded
				m.render()
				return m, nil
			}
			return m, nil // ignore other keys while the card is up
		}
		switch t.Type {
		case tea.KeyUp:
			// Recall the previous submitted line (shell-style input history). Transcript scroll moves
			// to pgup/pgdn + the mouse wheel (still handled by the viewport below).
			m.historyPrev()
			return m, nil
		case tea.KeyDown:
			m.historyNext()
			return m, nil
		case tea.KeyTab:
			// Tab cycles + completes the command palette (shown while typing a bare /prefix).
			if matches := m.paletteMatches(); len(matches) > 0 {
				m.paletteIdx = (m.clampPalette(len(matches)) + 1) % len(matches)
				m.input.SetValue("/" + matches[m.paletteIdx].Name)
				m.input.CursorEnd()
				m.relayout()
			}
			return m, nil
		}
		switch t.Type {
		case tea.KeyCtrlC:
			return m, m.handleCtrlC(wasArmed)
		case tea.KeyEsc:
			m.handleEsc()
			return m, nil
		case tea.KeyEnter:
			return m, m.handleEnter()
		}
	}
	wasBottom := m.vp.AtBottom()
	prevVPHeight := m.vp.Height
	var cmd, vcmd tea.Cmd
	m.input, cmd = m.input.Update(message)
	m.vp, vcmd = m.vp.Update(message)
	// Typing may have changed the wrapped input height — reflow the split so the box
	// grows/shrinks and the viewport tracks it. Re-pin the tail ONLY when the input
	// resized the viewport (never on a plain scroll event like the mouse wheel, which
	// must be free to move off the bottom).
	m.relayout()
	if wasBottom && m.vp.Height != prevVPHeight {
		m.vp.GotoBottom()
	}
	return m, tea.Batch(cmd, vcmd)
}

// recordHistory appends a just-submitted line and resets the recall cursor to the live position.
// Consecutive duplicates are collapsed so re-running the same command doesn't clutter the history.
func (m *Conversation) recordHistory(line string) {
	if n := len(m.history); n == 0 || m.history[n-1] != line {
		m.history = append(m.history, line)
	}
	m.histIdx = len(m.history)
	m.draft = ""
}

// historyPrev recalls the previous (older) submitted line, stashing the live draft the first time.
func (m *Conversation) historyPrev() {
	if len(m.history) == 0 || m.histIdx == 0 {
		return
	}
	if m.histIdx == len(m.history) {
		m.draft = m.input.Value()
	}
	m.histIdx--
	m.input.SetValue(m.history[m.histIdx])
	m.input.CursorEnd()
	m.relayout()
}

// historyNext moves toward newer lines, restoring the stashed draft at the live line.
func (m *Conversation) historyNext() {
	if m.histIdx >= len(m.history) {
		return
	}
	m.histIdx++
	if m.histIdx == len(m.history) {
		m.input.SetValue(m.draft)
	} else {
		m.input.SetValue(m.history[m.histIdx])
	}
	m.input.CursorEnd()
	m.relayout()
}

// handleEnter routes the current line: a permission response if one is pending,
// otherwise a new prompt (dispatched async). Returns the tea.Cmd to run.
func (m *Conversation) handleEnter() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return nil
	}
	// Slash-commands are the always-on admin surface: intercept them BEFORE the in-flight /
	// permission routing so /status, /doctor, etc. work even mid-turn or while a ratification
	// is pending (a "/" line must never be read as a y/n answer or a conversational intent).
	if strings.HasPrefix(text, "/") {
		m.recordHistory(text)
		return m.handleCommand(text)
	}
	// A turn is still processing (and no permission is awaiting an answer): keep
	// the typed text in the box rather than resetting + silently dropping it. The
	// spinner already signals "working"; the developer can re-send when it clears.
	if m.pendingPerm == nil && m.inFlight {
		return nil
	}
	m.input.Reset()
	m.relayout() // collapse the box back to one row now that it's cleared

	if m.pendingPerm != nil {
		outcome := "denied"
		if l := strings.ToLower(text); l == "y" || l == "yes" {
			outcome = "granted"
		}
		m.msgs = append(m.msgs, msg{role: "you", text: text})
		m.pendingPerm <- acp.PermissionResult{Outcome: outcome}
		m.pendingPerm = nil
		m.input.Placeholder = "your reply…"
		m.render()
		return nil // the in-flight turn continues; updates arrive via Program.Send
	}
	m.recordHistory(text)
	m.msgs = append(m.msgs, msg{role: "you", text: text})
	m.inFlight = true
	m.render()
	return tea.Batch(m.promptCmd(text), m.sp.Tick)
}

// isRuneKey reports whether a key message is a single bare rune r (a tool-permission
// shortcut like y/a/n/e).
func isRuneKey(t tea.KeyMsg, r rune) bool {
	return t.Type == tea.KeyRunes && len(t.Runes) == 1 && t.Runes[0] == r && !t.Alt
}

// answerToolPerm resolves a pending tool-permission card with the given outcome
// (allow_once | allow_always | deny), unblocks the waiting approver, and records the
// decision in the transcript. The turn then continues (the tool runs, or is skipped).
func (m *Conversation) answerToolPerm(outcome string) tea.Cmd {
	if m.pendingPerm == nil {
		return nil
	}
	m.pendingPerm <- acp.PermissionResult{Outcome: outcome}
	m.pendingPerm = nil
	m.permKind = ""
	m.permExpanded = false
	m.input.Placeholder = ""
	decision := map[string]string{
		"allow_once":   "✓ allowed once",
		"allow_always": "✓ allowed — always this session",
		"deny":         "⨯ denied",
	}[outcome]
	m.msgs = append(m.msgs, msg{role: "orion", text: decision})
	m.render()
	return nil // the in-flight turn continues; updates arrive via Program.Send
}

// cancelInFlight interrupts a running turn (or a pending permission) WITHOUT exiting the
// app. Returns whether anything was actually cancelled. The pipe Cancel runs OFF the
// event loop so a stalled write can never freeze the UI.
func (m *Conversation) cancelInFlight() bool {
	cancelled := false
	if m.pendingPerm != nil { // unblock the waiting gate goroutine
		m.pendingPerm <- acp.PermissionResult{Outcome: "denied"}
		m.pendingPerm = nil
		m.permKind = ""
		m.permExpanded = false
		cancelled = true
	}
	if m.inFlight {
		m.inFlight = false
		cancelled = true
	}
	if cancelled {
		m.oc.Interrupt()
		if client, sid := m.client, m.sid; client != nil {
			go func() { _ = client.Cancel(context.Background(), sid) }()
		}
	}
	return cancelled
}

// handleCtrlC implements shell-style Ctrl+C: cancel a turn in flight; else clear a
// drafted line; else (idle + empty) arm on the first press and exit on the second.
func (m *Conversation) handleCtrlC(wasArmed bool) tea.Cmd {
	if m.cancelInFlight() {
		m.msgs = append(m.msgs, msg{role: "orion", text: "⨯ cancelled"})
		m.render()
		return nil
	}
	if strings.TrimSpace(m.input.Value()) != "" {
		m.input.Reset()
		m.relayout()
		return nil
	}
	if !wasArmed {
		m.quitArmed = true // first press at an empty prompt: arm, don't exit
		return nil
	}
	return m.quit()
}

// handleEsc cancels an in-flight turn (stops streaming); otherwise clears a drafted line.
func (m *Conversation) handleEsc() {
	if m.cancelInFlight() {
		m.msgs = append(m.msgs, msg{role: "orion", text: "⨯ cancelled"})
		m.render()
		return
	}
	if strings.TrimSpace(m.input.Value()) != "" {
		m.input.Reset()
		m.relayout()
	}
}

// quit tears the session down: interrupt the conductor, cancel the session (off the
// event loop), and end the program.
func (m *Conversation) quit() tea.Cmd {
	m.quitting = true
	m.cancelInFlight()
	m.oc.Interrupt() // interrupt even if nothing was in flight
	if client, sid := m.client, m.sid; client != nil {
		go func() { _ = client.Cancel(context.Background(), sid) }()
	}
	return tea.Quit
}

// promptCmd runs one prompt turn in its own goroutine (the Update loop stays
// free); streamed updates are pushed back via Program.Send.
func (m Conversation) promptCmd(text string) tea.Cmd {
	client, sid, prog := m.client, m.sid, m.gate.program()
	return func() tea.Msg {
		_, err := client.PromptWithUpdates(context.Background(), sid, text, func(u acp.Update) {
			if prog != nil {
				prog.Send(streamMsg{u: u})
			}
		})
		return turnDoneMsg{err: err}
	}
}

// inputChromeRows is the fixed vertical chrome the layout spends outside the
// transcript viewport content and the input's own wrapped rows: header(1) +
// transcript border(2) + input pane border(2) + status line(1) + hint(1). The
// viewport gets whatever height remains after the input box claims its rows, so the
// box grows UP from the bottom as text wraps and the whole layout still fills the
// terminal exactly (no overflow, no tear).
const inputChromeRows = 7

// relayout recomputes the responsive split between the transcript viewport and the
// auto-growing input box for the current terminal size + input content. Called on
// resize and on every input change so the box tracks wrapping in real time.
func (m *Conversation) relayout() {
	if !m.ready {
		return
	}
	paneW := m.width - 2
	if paneW < 1 {
		paneW = 1 // tiny terminal: degrade narrow rather than overflow the width
	}
	// The input pane is bordered + padded (0,1); its inner text area is paneW-2 wide,
	// and the textarea fits its prompt + wrapped content into exactly that.
	inputOuter := paneW - 2
	if inputOuter < 3 {
		inputOuter = 3
	}
	m.input.SetWidth(inputOuter)
	// Grow to fit the wrapped content, capped so the transcript keeps at least 3 rows.
	maxRows := m.height - inputChromeRows - 3
	if maxRows < 1 {
		maxRows = 1
	}
	rows := min(wrappedRows(m.input.Value(), m.input.Width()), maxRows)
	if rows < 1 {
		rows = 1
	}
	m.input.SetHeight(rows)
	vpH := m.height - inputChromeRows - rows
	if vpH < 1 {
		vpH = 1
	}
	m.vp.Width, m.vp.Height = paneW, vpH
}

func (m *Conversation) render() {
	if !m.ready {
		return
	}
	// Auto-follow the tail only when already pinned there — so a user scrolled up
	// to re-read context isn't yanked back to the bottom by every streamed token.
	wasAtBottom := m.vp.AtBottom()
	m.vp.SetContent(m.renderTranscript())
	if wasAtBottom {
		m.vp.GotoBottom()
	}
}

// wrappedRows reports how many display rows a value occupies once soft-wrapped to
// the given content width — the height oracle for the auto-growing input box. Each
// hard newline starts a new row; a logical line wider than width wraps to more. It
// measures with lipgloss so the count matches the rendered layout exactly (no
// over/under-draw), which is what keeps the input box from tearing on wrap.
func wrappedRows(s string, width int) int {
	if width < 1 {
		width = 1
	}
	rows := 0
	for _, line := range strings.Split(s, "\n") {
		h := lipgloss.Height(lipgloss.NewStyle().Width(width).Render(line))
		if h < 1 {
			h = 1
		}
		rows += h
	}
	if rows < 1 {
		rows = 1
	}
	return rows
}

func (m Conversation) renderTranscript() string {
	w := m.vp.Width // wrap to the transcript pane's inner width
	if w <= 0 {
		w = m.width // fall back to the terminal width before the viewport is sized
	}
	if w <= 0 {
		w = 80
	}
	// The active (streaming) message renders RAW (fast, no per-token markdown); every
	// other message renders as Markdown. It is the most recent ORION agent_message
	// bubble while a turn is in flight — tracked by a backward scan, NOT just the tail,
	// so a trailing tool_call bubble doesn't flip the just-streamed narration to
	// markdown mid-turn (which could markdown-render a half-open code fence).
	active := -1
	if m.inFlight {
		for i := len(m.msgs) - 1; i >= 0; i-- {
			if m.msgs[i].role == "orion" && m.msgs[i].kind == "agent_message" {
				active = i
				break
			}
		}
	}
	var b strings.Builder
	for i, mm := range m.msgs {
		b.WriteString(m.renderMsg(mm, w, i == active))
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// indentLines prefixes every line of s with prefix, without a wrapping style — used to
// indent rendered-Markdown/diff output whose wrapping is already final (a lipgloss width
// style here would re-wrap and corrupt the ANSI).
func indentLines(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}

func (m Conversation) renderMsg(mm msg, w int, active bool) string {
	if w < 8 {
		w = 8
	}
	// Body content wraps to (w - 2) and is indented 2 columns via MarginLeft, so a
	// rendered line never exceeds w — nothing flows off the terminal edge.
	cw := w - 2
	if cw < 6 {
		cw = 6
	}
	if mm.role == "you" {
		return youBlock.Width(w).Render(mm.text + youTag.Render("  ⟵ you"))
	}
	if mm.kind == "tool_call" {
		return toolStyle.Render("    " + mm.text) // a dim activity line under Orion, no label
	}
	label := "  " + starStyle.Render("✦ ") + orionLabel.Render("Orion") + "\n"
	switch mm.kind {
	case "spec":
		card := cardTitle.Render("spec — review") + "\n" + mm.text
		return label + specCard.Width(cw-4).MarginLeft(2).Render(card)
	case "permission":
		card := cardTitle.Render("ratify") + "\n" + mm.text + "\n[y] ratify   [e] edit a field"
		return label + permCard.Width(cw-4).MarginLeft(2).Render(card)
	case "tool_permission":
		return label + permCard.Width(cw-4).MarginLeft(2).Render(m.toolPermCard(mm))
	case "plan":
		return label + planStyle.Width(cw).MarginLeft(2).Render(mm.text)
	case "build_report":
		card := buildTitle.Render("build — proof") + "\n" + colorizeReport(mm.text)
		return label + buildCard.Width(cw-4).MarginLeft(2).Render(card)
	case "command":
		return label + specCard.Width(cw-4).MarginLeft(2).Render(mm.text)
	default:
		// Orion conversational prose (kind "" | "agent_message"). While this bubble is
		// actively streaming, render it RAW (fast, no per-token markdown, no half-open
		// code fences). A completed bubble renders as Markdown — or, if it is structurally
		// a unified diff, with +/- coloring. Both own their own wrapping, so they are
		// emitted verbatim with a 2-column indent (never re-wrapped through lipgloss).
		if active {
			return label + orionText.Width(cw).MarginLeft(2).Render(mm.text)
		}
		return label + indentLines(renderProse(mm.text, cw), "  ")
	}
}

// permPreviewLines caps the tool-permission diff/command preview before it truncates
// with an expand affordance.
const permPreviewLines = 12

// toolPermCard renders the pending mutating-tool approval: an amber title, a colorized
// (truncated) command/diff preview, and the single-key choices.
func (m Conversation) toolPermCard(mm msg) string {
	title := warnGlyph.Render("⚠ permission") + dimStyle.Render(" · ") + orionLabel.Render(mm.tool)
	maxW := m.vp.Width - 10
	if maxW < 12 {
		maxW = 12
	}
	all := strings.Split(mm.text, "\n")
	shown := all
	truncated := false
	if !m.permExpanded && len(all) > permPreviewLines {
		shown = all[:permPreviewLines]
		truncated = true
	}
	// Clip long lines (plain runes, before colorizing) so the card never overflows and
	// no ANSI run gets re-wrapped.
	clipped := make([]string, len(shown))
	for i, l := range shown {
		clipped[i] = truncRunes(l, maxW)
	}
	body := colorizeDiff(strings.Join(clipped, "\n"))
	choices := okGlyph.Render("y") + dimStyle.Render(" allow once   ") +
		starStyle.Render("a") + dimStyle.Render(" allow always   ") +
		failGlyph.Render("n") + dimStyle.Render(" deny")
	foot := choices
	if truncated {
		foot = dimStyle.Render(fmt.Sprintf("… +%d more · e expand", len(all)-len(shown))) + "\n" + choices
	}
	return title + "\n" + body + "\n\n" + foot
}

// truncRunes clips s to at most max display runes, marking the cut with an ellipsis.
func truncRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max < 1 {
		return ""
	}
	return string(r[:max-1]) + "…"
}

// colorizeReport tints the status glyph at the start of each phase line (✓ green,
// ⚠ amber, ✗ red) so the build card reads at a glance.
func colorizeReport(s string) string {
	var b strings.Builder
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch {
		case strings.HasPrefix(line, "✓"):
			b.WriteString(okGlyph.Render("✓") + line[len("✓"):])
		case strings.HasPrefix(line, "⚠"):
			b.WriteString(warnGlyph.Render("⚠") + line[len("⚠"):])
		case strings.HasPrefix(line, "✗"):
			b.WriteString(failGlyph.Render("✗") + line[len("✗"):])
		default:
			b.WriteString(line)
		}
	}
	return b.String()
}

// View renders banner, scrollable transcript, input, budget, hint.
func (m Conversation) View() string {
	if m.quitting {
		return "Goodbye.\n"
	}
	offline := strings.HasPrefix(m.brain, "offline")
	paneW := m.width - 2
	if paneW < 1 {
		paneW = 1 // tiny terminal: degrade narrow rather than overflow the width
	}
	palette := m.renderPalette()
	body := m.vp.View()
	if !m.ready || len(m.msgs) == 0 {
		if m.bannerSet {
			// or-gik.3: the branded init status banner is the empty-state body (rendered inline
			// so the transcript pane provides the single frame).
			body = RenderInline(m.bannerReport, m.bannerID, paneW)
		} else {
			es := emptyState
			if offline {
				es = warnGlyph.Render("⚠ Offline mode (deterministic)") +
					dimStyle.Render(" — records single values only; it cannot grill or capture conditional behavior.\n   Set ANTHROPIC_API_KEY and restart for the full conversational spec build.\n\n"+emptyState)
			}
			body = dimStyle.Render(es)
		}
	} else if palette != "" {
		// Active transcript: shrink the viewport by the palette's height so the total layout stays
		// within the terminal (the palette renders between the transcript and the input).
		m.vp.Height = max(3, m.vp.Height-lipgloss.Height(palette))
		body = m.vp.View()
	}

	// Header: the Polaris identity line + the active brain (amber when offline).
	brainTint := lipgloss.NewStyle().Foreground(cLavender)
	if offline {
		brainTint = lipgloss.NewStyle().Foreground(cWarning)
	}
	header := bannerStyle.Render("✦ Orion") + dimStyle.Render("  ·  ") + brainTint.Render(m.brain)

	// Top pane: the conversation transcript (scrollable viewport).
	top := transPane.Width(paneW).Render(body)

	// Bottom pane: agent status (working / spend) above the input.
	status := dimStyle.Render(m.spendLine())
	if m.inFlight {
		status = m.sp.View() + " " + lipgloss.NewStyle().Foreground(cLavender).Render("working") + dimStyle.Render(" · "+m.spendLine())
	}
	bottom := inputPane.Width(paneW).Render(status + "\n" + m.input.View())

	hint := dimStyle.Render("  enter send · alt+enter newline · ↑/↓ history · pgup/pgdn scroll · tab complete · esc/ctrl+c cancel · ctrl+d quit")
	if m.quitArmed {
		hint = warnGlyph.Render("  press ctrl+c again to exit") + dimStyle.Render(" · or any key to keep going")
	} else if m.inFlight {
		hint = dimStyle.Render("  esc/ctrl+c cancel · pgup/pgdn scroll · ctrl+d quit")
	}

	parts := []string{header, top}
	if palette != "" {
		parts = append(parts, palette)
	}
	parts = append(parts, bottom, hint)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// renderPalette renders the command palette — the matching slash-commands with the selection marked
// — shown while the input is a bare /command prefix. Empty when the palette is closed. A short
// window keeps it bounded so it never dominates the transcript.
func (m Conversation) renderPalette() string {
	matches := m.paletteMatches()
	if len(matches) == 0 {
		return ""
	}
	idx := m.clampPalette(len(matches))
	const window = 8
	start := 0
	if idx >= window {
		start = idx - window + 1
	}
	sel := lipgloss.NewStyle().Foreground(cLavender)
	var b strings.Builder
	b.WriteString(dimStyle.Render("commands · ↑/↓ select · tab complete · enter run"))
	for i := start; i < len(matches) && i < start+window; i++ {
		c := matches[i]
		gap := strings.Repeat(" ", max(2, 12-len(c.Name)))
		if i == idx {
			b.WriteString("\n" + sel.Render("▸ /"+c.Name) + dimStyle.Render(gap+c.Help))
		} else {
			b.WriteString("\n" + dimStyle.Render("  /"+c.Name+gap+c.Help))
		}
	}
	return b.String()
}

func (m Conversation) spendLine() string {
	s := m.oc.Budget().Snapshot()
	line := fmt.Sprintf("spend: %d tok · $%.2f · %s", s.Tokens, s.Dollars, s.Wall.Round(time.Second))
	if s.HasCeiling {
		line += fmt.Sprintf(" · ceiling:%s", s.State)
	}
	return line
}

// programGate is the TUI's ACP permission gate: it surfaces a request to the
// Update loop via Program.Send and BLOCKS (in its own ACP serve goroutine, off
// the read loop) until the human answers — so the UI never deadlocks.
type programGate struct {
	mu   sync.Mutex
	prog *tea.Program
}

func (g *programGate) setProgram(p *tea.Program) { g.mu.Lock(); g.prog = p; g.mu.Unlock() }
func (g *programGate) program() *tea.Program     { g.mu.Lock(); defer g.mu.Unlock(); return g.prog }

// RequestPermission satisfies acp.PermissionGate.
func (g *programGate) RequestPermission(ctx context.Context, req acp.PermissionRequest) (acp.PermissionResult, error) {
	p := g.program()
	if p == nil {
		return acp.PermissionResult{Outcome: "denied"}, nil
	}
	reply := make(chan acp.PermissionResult, 1)
	p.Send(permMsg{req: req, reply: reply})
	select {
	case res := <-reply:
		return res, nil
	case <-ctx.Done():
		return acp.PermissionResult{Outcome: "denied"}, ctx.Err()
	}
}

// Run launches the Conversation pane. It auto-starts the Conductor agent
// in-process and drives it over an ACP session — no separate conductor command.
func Run(oc *orchestrator.Conductor, bannerReport health.Report, bannerID Identity, commands []Command) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clientEnd, agentEnd := net.Pipe()
	// Close the pipe ends on EVERY return path: cancelling ctx does NOT unblock a
	// read loop already blocked in net.Pipe.Read (the loop only checks ctx between
	// Scans) — only Close frees it. Without these defers an early handshake error
	// would leak both read-loop goroutines.
	defer clientEnd.Close()
	defer agentEnd.Close()

	// Brain selection (SPEC §0 amendment): a native LLM "Orion" agent when an API
	// key is present, else the deterministic conductor (offline/CI fallback). Both
	// satisfy acp.PromptFunc — the rest of this function is identical.
	brain, brainLabel := conductorBrain(oc)
	go func() { _ = brain.Serve(ctx, agentEnd, agentEnd) }()

	gate := &programGate{}
	client := NewACPClient(clientEnd, clientEnd, gate, nil)
	go func() { _ = client.Run(ctx) }()
	if err := client.Initialize(ctx); err != nil {
		return fmt.Errorf("tui: initialize conductor: %w", err)
	}
	sid, err := client.SessionNew(ctx)
	if err != nil {
		return fmt.Errorf("tui: open session: %w", err)
	}

	conv := NewConversation(client, sid, oc, gate)
	conv.brain = brainLabel
	conv.bannerReport = bannerReport
	conv.bannerID = bannerID
	conv.bannerSet = true
	conv.commands = commands
	// WithMouseCellMotion enables mouse reporting so the wheel scrolls the transcript
	// viewport (which already handles wheel events). Trade-off: native terminal text
	// selection now needs Shift (or Option on macOS) held.
	p := tea.NewProgram(conv, tea.WithAltScreen(), tea.WithMouseCellMotion())
	gate.setProgram(p)
	_, err = p.Run()
	if err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}
