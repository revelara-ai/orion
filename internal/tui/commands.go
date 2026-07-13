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

// builtinCommands are the TUI-intrinsic slash-commands: they need model access (clear the
// transcript, quit, read the budget) so they are dispatched in handleCommand rather than as
// injected Command funcs. They also appear in /help and the palette.
func builtinCommands() []Command {
	return []Command{
		{Name: "help", Help: "show this list"},
		{Name: "clear", Help: "clear the conversation + reset context"},
		{Name: "compact", Help: "summarize history to reduce context"},
		{Name: "context", Help: "show token usage this session"},
		{Name: "model", Help: "show or switch the model (/model <id>)"},
		{Name: "fork", Help: "branch this conversation from a prior turn (/fork [turn])"},
		{Name: "clone", Help: "branch a full copy of this conversation"},
		{Name: "tree", Help: "show the session branch tree"},
		{Name: "switch", Help: "jump to another branch (/switch <id>)"},
		{Name: "exit", Help: "quit Orion"},
	}
}

// handleCommand dispatches a leading-slash line. Intrinsic commands (/clear, /exit,
// /context) are handled here (they touch the model); others fall to the injected
// Command set. An unknown command is reported, never sent to the conductor.
func (m *Conversation) handleCommand(text string) tea.Cmd {
	name, _, _ := strings.Cut(strings.TrimPrefix(text, "/"), " ")
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "clear":
		m.input.Reset()
		m.msgs = nil
		m.permExpanded = false
		m.newSession() // a fresh ACP session so the model's context is reset too
		m.render()
		return nil
	case "exit", "quit":
		return m.quit()
	case "context", "cost":
		m.input.Reset()
		m.msgs = append(m.msgs, msg{role: "you", text: text})
		m.msgs = append(m.msgs, msg{role: "orion", kind: "command", text: m.contextReport()})
		m.render()
		return nil
	case "compact":
		m.input.Reset()
		m.msgs = append(m.msgs, msg{role: "you", text: text})
		m.msgs = append(m.msgs, msg{role: "orion", kind: "command", text: "compacting the conversation…"})
		m.render()
		return m.controlCmd("compact", "")
	case "model":
		_, arg, _ := strings.Cut(strings.TrimPrefix(text, "/"), " ")
		m.input.Reset()
		m.msgs = append(m.msgs, msg{role: "you", text: text})
		m.render()
		return m.controlCmd("model", strings.TrimSpace(arg))
	// Tree-structured sessions (or-ykz.5): forwarded as control ops; a
	// "SESSION:<id>" result switches the active branch in Update.
	case "fork", "clone", "tree", "switch":
		_, arg, _ := strings.Cut(strings.TrimPrefix(text, "/"), " ")
		m.input.Reset()
		m.msgs = append(m.msgs, msg{role: "you", text: text})
		m.render()
		return m.controlCmd(strings.ToLower(strings.TrimSpace(name)), strings.TrimSpace(arg))
	}
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

// paletteMatches returns the slash-commands to show in the command palette: it is non-empty only
// while the input is a BARE /command prefix (a leading "/" with no space yet — once args are typed
// the palette closes), and lists every command (plus /help) whose name starts with that prefix.
func (m Conversation) paletteMatches() []Command {
	v := m.input.Value()
	if !strings.HasPrefix(v, "/") || strings.ContainsRune(v, ' ') {
		return nil
	}
	prefix := strings.ToLower(strings.TrimPrefix(v, "/"))
	all := append(builtinCommands(), m.commands...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].Name < all[j].Name })
	out := make([]Command, 0, len(all))
	for _, c := range all {
		if strings.HasPrefix(c.Name, prefix) {
			out = append(out, c)
		}
	}
	return out
}

// clampPalette bounds the palette selection index into [0, n).
func (m Conversation) clampPalette(n int) int {
	switch {
	case n <= 0 || m.paletteIdx < 0:
		return 0
	case m.paletteIdx >= n:
		return n - 1
	default:
		return m.paletteIdx
	}
}

// commandHelp lists the built-in intrinsic commands plus every injected command, sorted.
func (m *Conversation) commandHelp() string {
	rows := append(builtinCommands(), m.commands...)
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	var b strings.Builder
	b.WriteString("commands (prefix with /):\n")
	for _, c := range rows {
		b.WriteString("  /" + c.Name + strings.Repeat(" ", max(2, 10-len(c.Name))) + c.Help + "\n")
	}
	b.WriteString("\nanything else is sent to the conductor as an intent.")
	return b.String()
}
