// Package tui is Orion's terminal UI, built on the charmbracelet stack
// (bubbletea Model/Update/View, lipgloss styling, bubbles components). It is the
// authoritative control plane and conversational surface (PRD UI Navigation).
//
// V2.0 skeleton (or-0d2): the Conversation pane — the developer types an intent,
// the Conductor accepts and echoes it. Later tasks add the Spec review, Plan,
// Fleet, Proof, Transcript, Escalations, Delivery, and Tier panes. State
// transitions are kept testable so Update can be driven by tea.Msg without a
// real terminal.
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/revelara-ai/orion/internal/orchestrator"
)

const emptyState = "Describe what you want to build, or point me at a repo or backlog."

var (
	bannerStyle = lipgloss.NewStyle().Bold(true)
	youStyle    = lipgloss.NewStyle().Faint(true)
	orionStyle  = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Faint(true)
)

// Conversation is the default pane: a conductor-backed chat loop.
type Conversation struct {
	conductor *orchestrator.Conductor
	input     textinput.Model
	lines     []string // rendered transcript lines
	quitting  bool
}

// NewConversation builds the Conversation pane bound to a Conductor.
func NewConversation(c *orchestrator.Conductor) Conversation {
	ti := textinput.New()
	ti.Placeholder = "your intent…"
	ti.Prompt = "› "
	ti.Focus()
	ti.CharLimit = 0
	return Conversation{conductor: c, input: ti}
}

// Init satisfies tea.Model and starts the input cursor blinking.
func (m Conversation) Init() tea.Cmd { return textinput.Blink }

// Update handles input. Enter submits the current intent to the Conductor and
// appends the confirmation; Ctrl+C / Esc quit.
func (m Conversation) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.quitting = true
			m.conductor.Interrupt() // cancel any in-flight work before exit
			return m, tea.Quit
		case tea.KeyEnter:
			m.submitCurrent()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// submitCurrent sends the current input to the Conductor and records the
// exchange. Empty/whitespace input is ignored (no silent acceptance).
func (m *Conversation) submitCurrent() {
	intent := strings.TrimSpace(m.input.Value())
	if intent == "" {
		return
	}
	m.input.Reset()
	m.lines = append(m.lines, youStyle.Render("you › ")+intent)

	conf, err := m.conductor.Submit(context.Background(), intent)
	if err != nil {
		m.lines = append(m.lines, orionStyle.Render("orion › ")+"I can't take that yet: "+err.Error())
		return
	}
	m.lines = append(m.lines, orionStyle.Render("orion › ")+conf.Message)
}

// View renders the pane: banner, transcript (or empty-state prompt), and input.
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
	b.WriteString(dimStyle.Render("enter: submit · ctrl+c: quit"))
	b.WriteString("\n")
	return b.String()
}

// Run launches the Conversation pane as a full-screen bubbletea program over the
// real terminal. Used by cmd/orion.
func Run(c *orchestrator.Conductor) error {
	p := tea.NewProgram(NewConversation(c))
	_, err := p.Run()
	if err != nil {
		return fmt.Errorf("tui: %w", err)
	}
	return nil
}
