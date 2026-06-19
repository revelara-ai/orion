// Package tui is Orion's terminal UI, built on the charmbracelet stack
// (bubbletea Model/Update/View, lipgloss styling, bubbles components). It is the
// authoritative control plane and conversational surface (PRD UI Navigation).
//
// The Conversation pane is a thin ACP CLIENT (or-6ck): it sends the developer's
// input to the spawned/primed Conductor agent as session/prompt and renders the
// streamed session/update into the transcript. The completeness "agent skill"
// (narrowing intent → ratified spec, one question at a time) lives server-side in
// the Conductor agent (or-owz), not here. The Conductor is started automatically
// in-process when the TUI launches — no separate `orion conductor start`.
package tui

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/revelara-ai/orion/internal/conductor"
	"github.com/revelara-ai/orion/internal/orchestrator"
)

const emptyState = "Conductor ready (in-process, over ACP). Describe what you want to build."

var (
	bannerStyle = lipgloss.NewStyle().Bold(true)
	youStyle    = lipgloss.NewStyle().Faint(true)
	orionStyle  = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Faint(true)
)

// Conversation is the default pane: a thin ACP client over the Conductor agent.
type Conversation struct {
	client    *ACPClient
	sid       string
	oc        *orchestrator.Conductor // for the live budget line + interrupt
	input     textinput.Model
	lines     []string
	shownConv int // pane lines already rendered into the transcript
	shownPlan int
	quitting  bool
}

// NewConversation builds the pane bound to a connected ACP client + session.
func NewConversation(client *ACPClient, sid string, oc *orchestrator.Conductor) Conversation {
	ti := textinput.New()
	ti.Placeholder = "your intent…"
	ti.Prompt = "› "
	ti.Focus()
	ti.CharLimit = 0
	return Conversation{client: client, sid: sid, oc: oc, input: ti}
}

// Init satisfies tea.Model and starts the input cursor blinking.
func (m Conversation) Init() tea.Cmd { return textinput.Blink }

// Update handles input. Enter sends the line to the Conductor over ACP and renders
// the streamed reply; Ctrl+C / Esc cancel the session and quit.
func (m Conversation) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.quitting = true
			m.oc.Interrupt()
			_ = m.client.Cancel(context.Background(), m.sid)
			return m, tea.Quit
		case tea.KeyEnter:
			m.handleEnter()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// handleEnter sends the current line as a session/prompt and renders the reply.
// The in-process Conductor turn is fast (deterministic gate), so a synchronous
// prompt keeps the UI simple; a slow spawned agent would move this to a tea.Cmd.
func (m *Conversation) handleEnter() {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return
	}
	m.input.Reset()
	m.lines = append(m.lines, youStyle.Render("you › ")+text)
	if _, err := m.client.Prompt(context.Background(), m.sid, text); err != nil {
		m.lines = append(m.lines, orionStyle.Render("orion › ")+"error: "+err.Error())
		return
	}
	m.input.Placeholder = "your answer…"
	m.renderNew()
}

// renderNew appends the pane updates streamed since the last turn.
func (m *Conversation) renderNew() {
	conv, plan, _, _ := m.client.Panes.Snapshot()
	for _, l := range conv[m.shownConv:] {
		m.lines = append(m.lines, orionStyle.Render("orion › ")+l)
	}
	m.shownConv = len(conv)
	for _, l := range plan[m.shownPlan:] {
		m.lines = append(m.lines, dimStyle.Render("plan › ")+l)
		if strings.Contains(l, "ratified") {
			m.input.Placeholder = "new intent…"
		}
	}
	m.shownPlan = len(plan)
}

// View renders the pane: banner, transcript (or empty-state), input, budget, hint.
func (m Conversation) View() string {
	if m.quitting {
		return "Goodbye.\n"
	}
	var b strings.Builder
	b.WriteString(bannerStyle.Render("Orion — Conversation"))
	b.WriteString("\n\n")
	if len(m.lines) == 0 {
		b.WriteString(dimStyle.Render(emptyState))
	} else {
		b.WriteString(strings.Join(m.lines, "\n"))
	}
	b.WriteString("\n\n")
	b.WriteString(m.input.View())
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(m.spendLine()))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render("enter: send · ctrl+c: quit"))
	b.WriteString("\n")
	return b.String()
}

// spendLine renders the always-on, live budget spend (read fresh each frame).
func (m Conversation) spendLine() string {
	s := m.oc.Budget().Snapshot()
	line := fmt.Sprintf("spend: %d tok · $%.2f · %s", s.Tokens, s.Dollars, s.Wall.Round(time.Second))
	if s.HasCeiling {
		line += fmt.Sprintf(" · ceiling:%s", s.State)
	}
	return line
}

// Run launches the Conversation pane. It auto-starts the Conductor agent
// in-process and drives it over an ACP session — the developer never runs a
// separate conductor command for the TUI.
func Run(oc *orchestrator.Conductor) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	clientEnd, agentEnd := net.Pipe()
	agent := conductor.NewConductorAgent(conductor.RoleTemplate{Project: "orion"}, oc)
	go func() { _ = agent.Serve(ctx, agentEnd, agentEnd) }()

	client := NewACPClient(clientEnd, clientEnd, &ApprovalGate{}, nil)
	go func() { _ = client.Run(ctx) }()
	if err := client.Initialize(ctx); err != nil {
		return fmt.Errorf("tui: initialize conductor: %w", err)
	}
	sid, err := client.SessionNew(ctx)
	if err != nil {
		return fmt.Errorf("tui: open session: %w", err)
	}

	p := tea.NewProgram(NewConversation(client, sid, oc))
	_, err = p.Run()
	cancel()
	_ = clientEnd.Close()
	_ = agentEnd.Close()
	if err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}
