package tui

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Command is a TUI slash-command — the always-on admin/management surface (status, doctor,
// skills, agents, evolve). Run returns text printed into the transcript. Commands are distinct
// from a conversational prompt (which goes to the conductor); they are injected by the launcher
// (cmd/orion owns store/env access) while the TUI owns dispatch + rendering.
type Command struct {
	Name string // without the leading slash, e.g. "skills"
	Help string // one-line description for /help
	Run  func(args string) string
	// Async, when set, takes precedence over Run: it returns an IMMEDIATE line to print now plus an
	// optional tea.Cmd for follow-up work that must not block the UI (e.g. an OAuth browser flow).
	// The tea.Cmd runs in bubbletea's goroutine and returns a CommandResultMsg with the outcome.
	Async func(args string) (string, tea.Cmd)
}

// CommandResultMsg carries the outcome of an async command's follow-up work back into the transcript.
type CommandResultMsg struct{ Text string }

// handleCommand dispatches a leading-slash line to a slash-command and prints its output into
// the transcript. /help is built in; an unknown command is reported, never sent to the
// conductor. It returns no tea.Cmd — command handlers are synchronous, fast, local admin ops.
func (m *Conversation) handleCommand(text string) tea.Cmd {
	m.input.Reset()
	m.msgs = append(m.msgs, msg{role: "you", text: text})
	// An async command prints its immediate line now and returns a tea.Cmd whose CommandResultMsg
	// appends the outcome later — so a slow op (OAuth) never blocks the Update loop.
	if immediate, cmd, ok := m.resolveAsyncCommand(text); ok {
		m.msgs = append(m.msgs, msg{role: "orion", kind: "command", text: immediate})
		m.render()
		return cmd
	}
	m.msgs = append(m.msgs, msg{role: "orion", kind: "command", text: m.resolveCommand(text)})
	m.render()
	return nil
}

// resolveAsyncCommand matches a leading-slash line to a command with an Async handler, returning its
// immediate line + the follow-up tea.Cmd. ok=false when the command is sync (or unknown).
func (m *Conversation) resolveAsyncCommand(text string) (string, tea.Cmd, bool) {
	name, args, _ := strings.Cut(strings.TrimPrefix(text, "/"), " ")
	name = strings.ToLower(strings.TrimSpace(name))
	for _, c := range m.commands {
		if c.Name == name && c.Async != nil {
			immediate, cmd := c.Async(strings.TrimSpace(args))
			return immediate, cmd, true
		}
	}
	return "", nil, false
}

// resolveCommand runs a slash-command line and returns its output text, with no side effects on
// the transcript — the testable core of handleCommand. /help is built in; an unknown command
// returns a hint (it is never forwarded to the conductor).
func (m *Conversation) resolveCommand(text string) string {
	name, args, _ := strings.Cut(strings.TrimPrefix(text, "/"), " ")
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" || name == "help" {
		return m.commandHelp()
	}
	for _, c := range m.commands {
		if c.Name == name {
			return c.Run(strings.TrimSpace(args))
		}
	}
	return "unknown command /" + name + " — type /help"
}

// commandHelp lists the built-in /help plus every injected command, sorted.
func (m *Conversation) commandHelp() string {
	rows := append([]Command{{Name: "help", Help: "show this list"}}, m.commands...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	var b strings.Builder
	b.WriteString("commands (prefix with /):\n")
	for _, c := range rows {
		b.WriteString("  /" + c.Name + strings.Repeat(" ", max(2, 10-len(c.Name))) + c.Help + "\n")
	}
	b.WriteString("\nanything else is sent to the conductor as an intent.")
	return b.String()
}
