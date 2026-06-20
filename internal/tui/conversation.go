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
func conductorBrain(oc *orchestrator.Conductor) acpServer {
	role := conductor.RoleTemplate{Project: "orion"}
	if key := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); key != "" {
		return conductor.NewOrionAgent(llm.NewAnthropic(key, os.Getenv("ORION_MODEL")), oc, role)
	}
	return conductor.NewConductorAgent(role, oc)
}

const emptyState = "Conductor ready (in-process, over ACP). Describe what you want to build."

var (
	bannerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	orionLabel  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	orionText   = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	youBlock    = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Align(lipgloss.Right)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	planStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	specCard    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("39")).Padding(0, 1)
	permCard    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("214")).Padding(0, 1)
	cardTitle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
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
}

// NewConversation builds the pane bound to a connected ACP client + session.
func NewConversation(client *ACPClient, sid string, oc *orchestrator.Conductor, gate *programGate) Conversation {
	ti := textinput.New()
	ti.Placeholder = "your intent…"
	ti.Prompt = "› "
	ti.Focus()
	ti.CharLimit = 0
	sp := spinner.New()
	sp.Spinner = spinner.Dot
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
		bodyH := t.Height - 6
		if bodyH < 3 {
			bodyH = 3
		}
		if !m.ready {
			m.vp = viewport.New(t.Width, bodyH)
			m.ready = true
		} else {
			m.vp.Width, m.vp.Height = t.Width, bodyH
		}
		if iw := t.Width - 4; iw > 8 { // bound the input to the terminal width
			m.input.Width = iw
		}
		m.render()
		return m, nil

	case streamMsg:
		m.msgs = append(m.msgs, msg{role: "orion", kind: t.u.Kind, text: t.u.Text})
		if t.u.Kind == "plan" && strings.Contains(t.u.Text, "ratified") {
			m.input.Placeholder = "new intent…"
		}
		m.render()
		return m, nil

	case permMsg:
		m.pendingPerm = t.reply
		m.msgs = append(m.msgs, msg{role: "orion", kind: "permission", text: t.req.Title})
		m.input.Placeholder = "y to ratify · e to edit"
		m.render()
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
	m.vp.SetContent(m.renderTranscript())
	m.vp.GotoBottom()
}

func (m Conversation) renderTranscript() string {
	w := m.width
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
		return youBlock.Width(w).Render(mm.text + "  ⟵ you")
	}
	label := "  " + orionLabel.Render("◇ Orion") + "\n"
	switch mm.kind {
	case "spec":
		card := cardTitle.Render("spec — review") + "\n" + mm.text
		return label + specCard.Width(cw-4).MarginLeft(2).Render(card)
	case "permission":
		card := cardTitle.Render("ratify") + "\n" + mm.text + "\n[y] ratify   [e] edit a field"
		return label + permCard.Width(cw-4).MarginLeft(2).Render(card)
	case "plan":
		return label + planStyle.Width(cw).MarginLeft(2).Render(mm.text)
	default:
		return label + orionText.Width(cw).MarginLeft(2).Render(mm.text)
	}
}

// View renders banner, scrollable transcript, input, budget, hint.
func (m Conversation) View() string {
	if m.quitting {
		return "Goodbye.\n"
	}
	body := m.vp.View()
	if !m.ready || len(m.msgs) == 0 {
		body = dimStyle.Render(emptyState)
	}
	status := m.spendLine()
	if m.inFlight {
		status = m.sp.View() + " working · " + status
	}
	var b strings.Builder
	b.WriteString(bannerStyle.Render("Orion — Conversation"))
	b.WriteString("\n")
	b.WriteString(body)
	b.WriteString("\n")
	b.WriteString(m.input.View())
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(status))
	b.WriteString("  ·  ")
	b.WriteString(dimStyle.Render("enter send · ↑/↓ scroll · ctrl+c quit"))
	b.WriteString("\n")
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
func Run(oc *orchestrator.Conductor) error {
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
	go func() { _ = conductorBrain(oc).Serve(ctx, agentEnd, agentEnd) }()

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

	p := tea.NewProgram(NewConversation(client, sid, oc, gate), tea.WithAltScreen())
	gate.setProgram(p)
	_, err = p.Run()
	if err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}
