// Package tui is Orion's terminal UI, built on the charmbracelet stack
// (bubbletea Model/Update/View, lipgloss styling, bubbles viewport/textinput/
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
	"github.com/charmbracelet/bubbles/textinput"
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
	kind string // "" | agent_thought | agent_message | spec | plan | permission
	text string
}

// Conversation is the default pane: an async chat client over the Conductor agent.
type Conversation struct {
	client *ACPClient
	sid    string
	oc     *orchestrator.Conductor
	gate   *programGate

	input textinput.Model
	vp    viewport.Model
	sp    spinner.Model
	msgs  []msg

	width, height int
	ready         bool
	inFlight      bool
	pendingPerm   chan acp.PermissionResult
	quitting      bool
	brain         string // active brain label (native · model / offline …)

	// Init status banner (or-gik.3): the readiness report + identity, supplied by the launcher
	// (which owns version/branch + the cached Polaris probe). Rendered as the empty-state body.
	bannerReport health.Report
	bannerID     Identity
	bannerSet    bool

	// commands are the injected admin/management slash-commands (or-dz9).
	commands []Command
}

// NewConversation builds the pane bound to a connected ACP client + session.
func NewConversation(client *ACPClient, sid string, oc *orchestrator.Conductor, gate *programGate) Conversation {
	ti := textinput.New()
	ti.Placeholder = "" // blank: the banner already explains what to do (or-gik.3)
	ti.Prompt = "› "
	ti.PromptStyle = lipgloss.NewStyle().Foreground(cIndigo)
	ti.Cursor.Style = lipgloss.NewStyle().Foreground(cLavender)
	ti.PlaceholderStyle = lipgloss.NewStyle().Foreground(cFaint)
	ti.Focus()
	ti.CharLimit = 0
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(cLavender) // the star-glow accent
	return Conversation{client: client, sid: sid, oc: oc, gate: gate, input: ti, sp: sp}
}

// Init satisfies tea.Model.
func (m Conversation) Init() tea.Cmd { return textinput.Blink }

// Update handles input, window sizing, streamed updates, permission requests, and
// turn completion. The Update loop NEVER blocks on the Conductor.
func (m Conversation) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch t := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = t.Width, t.Height
		// Two panes inside borders: header(1) + top border(2) + input pane(4) +
		// hint(1) = 8 rows of chrome; the transcript fills the rest.
		bodyH := t.Height - 8
		if bodyH < 3 {
			bodyH = 3
		}
		bodyW := t.Width - 2 // inside the transcript pane's border
		if bodyW < 1 {
			bodyW = 1 // never floor ABOVE the terminal width — that overflows a tiny term
		}
		if !m.ready {
			m.vp = viewport.New(bodyW, bodyH)
			m.ready = true
		} else {
			m.vp.Width, m.vp.Height = bodyW, bodyH
		}
		if iw := t.Width - 6; iw > 8 { // inside the input pane border + padding + prompt
			m.input.Width = iw
		}
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
		m.msgs = append(m.msgs, msg{role: "orion", kind: "permission", text: t.req.Title})
		m.input.Placeholder = "y to ratify · e to edit"
		m.render()
		// A permission request BLOCKS the conversation and asks the developer to act
		// (press y/e). Force the card into view even if they had scrolled up to read
		// history — otherwise they're prompted to respond with no visible prompt.
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

	case spinner.TickMsg:
		if !m.inFlight {
			return m, nil
		}
		var cmd tea.Cmd
		m.sp, cmd = m.sp.Update(t)
		return m, cmd

	case tea.KeyMsg:
		switch t.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.quitting = true
			if m.pendingPerm != nil { // unblock the waiting gate goroutine
				m.pendingPerm <- acp.PermissionResult{Outcome: "denied"}
				m.pendingPerm = nil
			}
			m.oc.Interrupt()
			// Cancel is a pipe write; do it OFF the event loop so a stalled write can
			// never freeze quit. Run()'s deferred Close unblocks/ends this goroutine.
			if client, sid := m.client, m.sid; client != nil {
				go func() { _ = client.Cancel(context.Background(), sid) }()
			}
			return m, tea.Quit
		case tea.KeyEnter:
			return m, m.handleEnter()
		}
	}
	var cmd, vcmd tea.Cmd
	m.input, cmd = m.input.Update(message)
	m.vp, vcmd = m.vp.Update(message)
	return m, tea.Batch(cmd, vcmd)
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
		return m.handleCommand(text)
	}
	// A turn is still processing (and no permission is awaiting an answer): keep
	// the typed text in the box rather than resetting + silently dropping it. The
	// spinner already signals "working"; the developer can re-send when it clears.
	if m.pendingPerm == nil && m.inFlight {
		return nil
	}
	m.input.Reset()

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
	m.msgs = append(m.msgs, msg{role: "you", text: text})
	m.inFlight = true
	m.render()
	return tea.Batch(m.promptCmd(text), m.sp.Tick)
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

func (m Conversation) renderTranscript() string {
	w := m.vp.Width // wrap to the transcript pane's inner width
	if w <= 0 {
		w = m.width // fall back to the terminal width before the viewport is sized
	}
	if w <= 0 {
		w = 80
	}
	var b strings.Builder
	for _, mm := range m.msgs {
		b.WriteString(m.renderMsg(mm, w))
		b.WriteString("\n\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Conversation) renderMsg(mm msg, w int) string {
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
	case "plan":
		return label + planStyle.Width(cw).MarginLeft(2).Render(mm.text)
	case "build_report":
		card := buildTitle.Render("build — proof") + "\n" + colorizeReport(mm.text)
		return label + buildCard.Width(cw-4).MarginLeft(2).Render(card)
	case "command":
		return label + specCard.Width(cw-4).MarginLeft(2).Render(mm.text)
	default:
		return label + orionText.Width(cw).MarginLeft(2).Render(mm.text)
	}
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

	hint := dimStyle.Render("  enter send · ↑/↓ scroll · ctrl+c quit")

	return lipgloss.JoinVertical(lipgloss.Left, header, top, bottom, hint)
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
	p := tea.NewProgram(conv, tea.WithAltScreen())
	gate.setProgram(p)
	_, err = p.Run()
	if err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}
