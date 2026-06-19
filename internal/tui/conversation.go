// Package tui is Orion's terminal UI, built on the charmbracelet stack
// (bubbletea Model/Update/View, lipgloss styling, bubbles viewport/textinput). It
// is the authoritative control plane and conversational surface (PRD UI
// Navigation; SPEC §0 "chat-first, not a fleet dashboard").
//
// The Conversation pane is a thin ACP CLIENT: it sends the developer's input to
// the auto-started, primed Conductor agent as session/prompt and renders the
// streamed session/update as a real chat — role-aligned message blocks, question
// cards, and a bordered spec-review card (or-2r8). The completeness "agent skill"
// (narrowing intent → reviewable spec → ratify) lives server-side in the Conductor
// agent (or-owz, or-owo).
package tui

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/conductor"
	"github.com/revelara-ai/orion/internal/orchestrator"
)

const emptyState = "Conductor ready (in-process, over ACP). Describe what you want to build."

var (
	bannerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63"))
	orionLabel  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	orionText   = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	youBlock    = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Align(lipgloss.Right)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	planStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("42"))
	specCard    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("39")).Padding(0, 1)
	cardTitle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
)

// msg is one rendered turn in the conversation.
type msg struct {
	role string // "you" | "orion"
	kind string // "" | agent_thought | agent_message | spec | plan
	text string
}

// Conversation is the default pane: a chat client over the Conductor agent.
type Conversation struct {
	client *ACPClient
	sid    string
	oc     *orchestrator.Conductor

	input    textinput.Model
	vp       viewport.Model
	msgs     []msg
	width    int
	height   int
	ready    bool
	quitting bool
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

// Update handles input + window sizing. Enter sends the line to the Conductor over
// ACP and renders the streamed reply; Ctrl+C / Esc cancel and quit; other keys go
// to the input and the viewport (scrollback).
func (m Conversation) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch t := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = t.Width, t.Height
		bodyH := t.Height - 6 // banner + input + status + hint
		if bodyH < 3 {
			bodyH = 3
		}
		if !m.ready {
			m.vp = viewport.New(t.Width, bodyH)
			m.ready = true
		} else {
			m.vp.Width, m.vp.Height = t.Width, bodyH
		}
		m.vp.SetContent(m.renderTranscript())
		m.vp.GotoBottom()
		return m, nil
	case tea.KeyMsg:
		switch t.Type {
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
	var cmd, vcmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.vp, vcmd = m.vp.Update(msg)
	return m, tea.Batch(cmd, vcmd)
}

// handleEnter sends the current line as a session/prompt and renders the reply.
func (m *Conversation) handleEnter() {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return
	}
	m.input.Reset()
	m.msgs = append(m.msgs, msg{role: "you", text: text})

	var got []acp.Update
	_, err := m.client.PromptWithUpdates(context.Background(), m.sid, text, func(u acp.Update) {
		got = append(got, u)
	})
	if err != nil {
		m.msgs = append(m.msgs, msg{role: "orion", text: "error: " + err.Error()})
	}
	for _, u := range got {
		m.msgs = append(m.msgs, msg{role: "orion", kind: u.Kind, text: u.Text})
		if u.Kind == "plan" && strings.Contains(u.Text, "ratified") {
			m.input.Placeholder = "new intent…"
		} else {
			m.input.Placeholder = "your reply…"
		}
	}
	if m.ready {
		m.vp.SetContent(m.renderTranscript())
		m.vp.GotoBottom()
	}
}

// renderTranscript styles the whole conversation for the viewport.
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
	if mm.role == "you" {
		return youBlock.Width(w).Render(mm.text + "  ⟵ you")
	}
	switch mm.kind {
	case "spec":
		card := cardTitle.Render("spec — review") + "\n" + mm.text
		return "  " + orionLabel.Render("◇ Orion") + "\n" + specCard.Render(card)
	case "plan":
		return "  " + orionLabel.Render("◇ Orion") + "\n  " + planStyle.Render(mm.text)
	default:
		return "  " + orionLabel.Render("◇ Orion") + "\n  " + orionText.Render(mm.text)
	}
}

// View renders the pane: banner, scrollable transcript, input, budget, hint.
func (m Conversation) View() string {
	if m.quitting {
		return "Goodbye.\n"
	}
	body := m.vp.View()
	if !m.ready || len(m.msgs) == 0 {
		body = dimStyle.Render(emptyState)
	}
	var b strings.Builder
	b.WriteString(bannerStyle.Render("Orion — Conversation"))
	b.WriteString("\n")
	b.WriteString(body)
	b.WriteString("\n")
	b.WriteString(m.input.View())
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(m.spendLine()))
	b.WriteString("  ·  ")
	b.WriteString(dimStyle.Render("enter send · ↑/↓ scroll · ctrl+c quit"))
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
// in-process and drives it over an ACP session — no separate conductor command.
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

	p := tea.NewProgram(NewConversation(client, sid, oc), tea.WithAltScreen())
	_, err = p.Run()
	cancel()
	_ = clientEnd.Close()
	_ = agentEnd.Close()
	if err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}
