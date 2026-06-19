// Package tui is Orion's terminal UI, built on the charmbracelet stack
// (bubbletea Model/Update/View, lipgloss styling, bubbles components). It is the
// authoritative control plane and conversational surface (PRD UI Navigation).
//
// The Conversation pane narrows an intent into a ratified spec by asking the
// blocking completeness questions ONE AT A TIME (or-lut): the developer types an
// intent, then answers each required decision in turn; each answer is recorded
// and the next question is shown. State transitions are kept testable so Update
// can be driven by tea.Msg without a real terminal.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
)

const emptyState = "Conductor ready (in-process). Describe what you want to build, or point me at a repo or backlog."

var (
	bannerStyle = lipgloss.NewStyle().Bold(true)
	youStyle    = lipgloss.NewStyle().Faint(true)
	orionStyle  = lipgloss.NewStyle().Bold(true)
	dimStyle    = lipgloss.NewStyle().Faint(true)
)

// convPhase tracks where the conversation is in the intent → spec flow.
type convPhase int

const (
	phaseIntent convPhase = iota // awaiting the developer's intent
	phaseGrill                   // asking the blocking completeness questions, one at a time
	phaseReady                   // spec ratified; ready to build
)

// Conversation is the default pane: a conductor-backed chat loop.
type Conversation struct {
	conductor *orchestrator.Conductor
	input     textinput.Model
	lines     []string                    // rendered transcript lines
	pending   []completeness.OpenDecision // blocking questions still to answer
	phase     convPhase
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

// Update handles input. Enter advances the intent → grill → ready state machine;
// Ctrl+C / Esc quit.
func (m Conversation) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.quitting = true
			m.conductor.Interrupt() // cancel any in-flight work before exit
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

// handleEnter does exactly one thing for the current phase, so answers register
// and the same question never re-appears.
func (m *Conversation) handleEnter() {
	ctx := context.Background()
	val := strings.TrimSpace(m.input.Value())

	switch m.phase {
	case phaseIntent:
		if val == "" {
			return
		}
		m.input.Reset()
		m.say(youStyle, "you", val)
		conf, err := m.conductor.Submit(ctx, val)
		if err != nil {
			m.say(orionStyle, "orion", "I can't take that yet: "+err.Error())
			return
		}
		m.say(orionStyle, "orion", conf.Message)
		m.pending = blockingDecisions(conf.OpenDecisions)
		if len(m.pending) == 0 {
			m.finalize(ctx)
			return
		}
		m.phase = phaseGrill
		m.input.Placeholder = "your answer…"
		m.ask()

	case phaseGrill:
		od := m.pending[0]
		if val == "" {
			m.say(orionStyle, "orion", "That one needs an answer — "+od.Question)
			return
		}
		m.input.Reset()
		m.say(youStyle, "you", val)
		if err := m.conductor.RecordAnswer(ctx, od.Key, val); err != nil {
			m.say(orionStyle, "orion", "couldn't record that: "+err.Error())
			return
		}
		// Recompute the remaining blocking questions from the persisted answers —
		// the single source of "what's still open".
		if sv, err := m.conductor.SpecView(ctx); err == nil {
			m.pending = blockingDecisions(sv.OpenDecisions)
		} else if len(m.pending) > 0 {
			m.pending = m.pending[1:]
		}
		if len(m.pending) == 0 {
			m.finalize(ctx)
			return
		}
		m.ask()

	case phaseReady:
		if val == "" {
			return
		}
		// A fresh line starts a new intent.
		m.phase = phaseIntent
		m.input.Placeholder = "your intent…"
		m.handleEnter()
	}
}

// ask shows the current question — one at a time, with its dimension and how many
// remain — so the developer always knows exactly what to answer.
func (m *Conversation) ask() {
	od := m.pending[0]
	m.say(dimStyle, "orion", fmt.Sprintf("[%s] %s   (%d to answer)", od.Dimension, od.Question, len(m.pending)))
}

// finalize ratifies the spec; fallback-eligible dimensions resolve to presets.
func (m *Conversation) finalize(ctx context.Context) {
	es, err := m.conductor.ApproveSpec(ctx)
	if err != nil {
		m.say(orionStyle, "orion", "I can't finalize the spec yet: "+err.Error())
		return
	}
	m.phase = phaseReady
	m.pending = nil
	m.input.Placeholder = "new intent…"
	m.say(orionStyle, "orion", fmt.Sprintf("Spec ratified ✓  route=%s  format=%s  (hash %s)",
		es.ResponseContract.Route, es.Decisions["response_format"], shortHash(es.Hash)))
	m.say(dimStyle, "orion", "Ready to build — run `orion run` to generate + prove, or type a new intent.")
}

func (m *Conversation) say(style lipgloss.Style, who, text string) {
	m.lines = append(m.lines, style.Render(who+" › ")+text)
}

// blockingDecisions keeps only decisions with no safe default — the ones the
// developer must answer. Fallback-eligible decisions resolve to presets at
// approve time, so we never make the developer answer what we can default.
func blockingDecisions(open []completeness.OpenDecision) []completeness.OpenDecision {
	var b []completeness.OpenDecision
	for _, od := range open {
		if od.Fallback == "" {
			b = append(b, od)
		}
	}
	return b
}

func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

// View renders the pane: banner, transcript (or empty-state prompt), input,
// budget, and a phase-aware hint.
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
	b.WriteString(dimStyle.Render(m.footerHint()))
	b.WriteString("\n")
	return b.String()
}

// footerHint tailors the key hint to the current phase.
func (m Conversation) footerHint() string {
	switch m.phase {
	case phaseGrill:
		return "answer the question above · enter: submit · ctrl+c: quit"
	case phaseReady:
		return "type a new intent · `orion run` to build · ctrl+c: quit"
	default:
		return "describe your intent · enter: submit · ctrl+c: quit"
	}
}

// spendLine renders the always-on, live budget spend (read fresh each frame).
func (m Conversation) spendLine() string {
	s := m.conductor.Budget().Snapshot()
	line := fmt.Sprintf("spend: %d tok · $%.2f · %s", s.Tokens, s.Dollars, s.Wall.Round(time.Second))
	if s.HasCeiling {
		line += fmt.Sprintf(" · ceiling:%s", s.State)
	}
	return line
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
