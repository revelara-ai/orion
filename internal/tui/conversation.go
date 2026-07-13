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
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/agentruntime"
	"github.com/revelara-ai/orion/internal/conductor"
	"github.com/revelara-ai/orion/internal/health"
	"github.com/revelara-ai/orion/internal/llmsetup"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/pkg/llm"
)

// acpServer is the Conductor brain the TUI drives over ACP (native or fallback).
type acpServer interface {
	Serve(ctx context.Context, r io.Reader, w io.Writer) error
}

// conductorBrain selects the brain via llmsetup (config file + ORION_MODEL +
// env keys): a native LLM "Orion" agent when a provider resolves, else the
// deterministic conductor (offline/CI fallback). Both satisfy acp.PromptFunc,
// so the TUI is identical for either.
func conductorBrain(oc *orchestrator.Conductor) (acpServer, string, llm.Provider, string) {
	role := conductor.RoleTemplate{Project: "orion"}
	b := llmsetup.Select()
	if b.Provider == nil {
		return conductor.NewConductorAgent(role, oc), "offline — " + b.Reason, nil, ""
	}
	agent := conductor.NewOrionAgent(b.Provider, oc, role)
	agent.SetModel(b.Ref, func(currentRef, m string) (llm.Provider, string, error) {
		return llmsetup.RebuildFrom(currentRef, m)
	}, llmsetup.ListModels)
	return agent, "native · " + b.Ref, b.Provider, b.Model
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

	cBorder      = lipgloss.Color("#362D50")                                                                    // divider on the void
	transPane    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cBorder)               // top: transcript
	inputPane    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cIndigo).Padding(0, 1) // bottom: input + status
	activityPane = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(cFaint)                // middle: live activity (dim border)
)

// ── async message types ──────────────────────────────────────────────────────

// activityTickMsg / streamMsg / turnDoneMsg all carry gen — the turn generation
// they belong to (or-5g2q). A cancel-then-resubmit overlaps two turns; the
// Update loop drops any message whose gen != the live turnGen so a stale turn's
// late stream, completion, or tick can't corrupt the live turn. gen 0 is the
// resting generation (tests that inject messages directly match it).
type activityTickMsg struct {
	t   time.Time
	gen int
}

func activityTick(gen int) tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return activityTickMsg{t: t, gen: gen} })
}

type streamMsg struct {
	u   acp.Update
	gen int
} // a streamed session/update
type turnDoneMsg struct {
	err error
	gen int
} // a prompt turn completed

// sessionResetMsg carries the outcome of an OFF-loop /clear session reset — the
// SessionNew round-trip must never block the Update loop (or-gony).
type sessionResetMsg struct {
	sid string
	err error
}
type permMsg struct {                 // the agent requested a permission
	req   acp.PermissionRequest
	reply chan acp.PermissionResult
}

// pendingPermission is one queued ACP permission awaiting the human's answer:
// the request (to render its card) plus the gate goroutine's reply channel.
type pendingPermission struct {
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
	turnGen       int // monotonic prompt-turn id; stale-gen async msgs are dropped (or-5g2q)
	// permQueue is the FIFO of in-flight permission requests (or-f06). Only the
	// head is surfaced to the human; answering it pops the head and surfaces the
	// next. A single slot would let a second concurrent request overwrite — and
	// orphan — the first gate goroutine; the queue serializes them, and
	// cancel/quit denies every entry so no gate goroutine is left blocked.
	permQueue     []pendingPermission
	permExpanded  bool // a tool-permission card's diff preview is expanded
	quitting      bool
	quitArmed     bool   // a Ctrl+C at an idle empty prompt arms quit; a second one exits
	brain         string // active brain label (native · model / offline …)

	// Init status banner (or-gik.3): the readiness report + identity, supplied by the launcher
	// (which owns version/branch + the cached Polaris probe). Rendered as the empty-state body.
	bannerReport health.Report
	bannerID     Identity
	bannerSet    bool

	brainProvider llm.Provider // active native provider (nil offline) — probed async at launch
	brainModel    string

	// activity is the live per-turn actor stack, phase strip, and log ring (Task 5).
	activity activityModel

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
	conv := Conversation{client: client, sid: sid, oc: oc, gate: gate, input: ti, sp: sp}
	conv.activity = newActivityModel()
	return conv
}

// Init satisfies tea.Model.
func (m Conversation) Init() tea.Cmd { return tea.Batch(m.input.Focus(), m.probeBrainCmd()) }

// probeBrainCmd asynchronously verifies the active brain can drive tools and
// surfaces a transcript warning when it can't (spec: probe + warn, fail only
// tool flows). Providers that advertise tool support via Models() (Anthropic,
// Gemini) are skipped — zero extra calls or launch cost on the default path.
// Runs in a bubbletea Cmd goroutine, so a slow local model never blocks launch.
func (m Conversation) probeBrainCmd() tea.Cmd {
	prov, model := m.brainProvider, m.brainModel
	if prov == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if llm.AdvertisesTools(ctx, prov, model) {
			return nil
		}
		ok, err := llm.Probe(ctx, prov)
		if ok {
			return nil
		}
		reason := "the model did not call the probe tool"
		if err != nil {
			reason = err.Error()
		}
		return CommandResultMsg{Text: "⚠ tools probe failed for " + model + " — agent flows may not work (" + reason + ")"}
	}
}

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
		if t.gen != m.turnGen {
			return m, nil // a superseded (cancelled) turn's late update — drop it
		}
		// Activity updates fold into the activity model only — they NEVER become
		// transcript bubbles. Return early before the agent_message accumulation.
		if t.u.Kind == acp.ActivityKind {
			m.activity.apply(t.u)
			m.render()
			return m, nil
		}
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
		// Enqueue; only surface the card when it becomes the head. A second
		// concurrent request waits its turn instead of overwriting — and
		// orphaning — the first gate goroutine (or-f06).
		m.permQueue = append(m.permQueue, pendingPermission(t))
		if len(m.permQueue) == 1 {
			m.surfacePerm(m.permQueue[0])
		}
		return m, nil

	case turnDoneMsg:
		if t.gen != m.turnGen {
			return m, nil // a superseded turn completing — must not reset the live turn
		}
		m.inFlight = false
		m.activity.finish()
		if t.err != nil {
			m.msgs = append(m.msgs, msg{role: "orion", text: "error: " + t.err.Error()})
		}
		m.render()
		return m, nil

	case sessionResetMsg:
		// The off-loop /clear reset landed. On success adopt the fresh session id
		// (subsequent prompts get a clean context); on failure keep the old id but
		// tell the developer the context wasn't actually reset.
		if t.err == nil && t.sid != "" {
			m.sid = t.sid
		} else if t.err != nil {
			m.msgs = append(m.msgs, msg{role: "orion", kind: "command", text: "context reset failed: " + t.err.Error()})
			m.render()
		}
		return m, nil

	case CommandResultMsg:
		// The deferred outcome of an async slash-command (e.g. /mcp login, /compact, /model)
		// — appended when its follow-up tea.Cmd completes. A "MODEL:<id> · <text>" result
		// from /model updates the brain label too.
		txt := t.Text
		if rest, ok := strings.CutPrefix(txt, "MODEL:"); ok {
			id, disp, _ := strings.Cut(rest, " · ")
			m.brain = "native · " + id
			txt = disp
		}
		// A "SESSION:<id> · <text>" result (/fork, /clone, /switch) moves the TUI
		// onto that branch — subsequent prompts go to the new session (or-ykz.5).
		if rest, ok := strings.CutPrefix(txt, "SESSION:"); ok {
			id, disp, _ := strings.Cut(rest, " · ")
			if id = strings.TrimSpace(id); id != "" {
				m.sid = id
			}
			txt = disp
		}
		m.msgs = append(m.msgs, msg{role: "orion", kind: "command", text: txt})
		m.render()
		return m, nil

	case spinner.TickMsg:
		if !m.inFlight {
			return m, nil
		}
		var cmd tea.Cmd
		m.sp, cmd = m.sp.Update(t)
		return m, cmd

	case activityTickMsg:
		if t.gen != m.turnGen || !m.inFlight {
			return m, nil // stale turn's ticker (or turn ended) — let it die, don't re-arm
		}
		if m.activity.bus.Tick(t.t) {
			m.render() // a heartbeat was emitted → refresh the panel
		}
		return m, activityTick(t.gen)

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
		if !m.hasPerm() && ((t.Type == tea.KeyEnter && t.Alt) || t.Type == tea.KeyCtrlJ) {
			// The input is inert while a permission card is up (single-key/free-text
			// answer), so the newline chord must not edit it there (or-ns8).
			m.input.InsertString("\n")
			m.relayout()
			return m, nil
		}
		// A pending TOOL-permission card is answered by single keys (y/a/n/e), never by
		// typing into the input or recalling history.
		if m.hasPerm() && m.permKind() == "tool" {
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
			// While a completion is open, ↑ moves its selection (as the palette
			// footer advertises); otherwise it recalls the previous submitted line
			// (shell-style history). Transcript scroll is pgup/pgdn + the wheel.
			if m.cycleAtFile(-1) || m.cyclePalette(-1) {
				return m, nil
			}
			m.historyPrev()
			return m, nil
		case tea.KeyDown:
			if m.cycleAtFile(1) || m.cyclePalette(1) {
				return m, nil
			}
			m.historyNext()
			return m, nil
		case tea.KeyTab:
			// Tab cycles + completes an @-file token, else the command palette.
			if m.cycleAtFile(1) || m.cyclePalette(1) {
				return m, nil
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

// cyclePalette moves the command-palette selection by dir (+1/-1, wrapping) and
// writes the selected command into the input. Returns false if no palette is
// open (a bare /prefix with matches). Shared by Tab and ↑/↓ (or-ns8).
func (m *Conversation) cyclePalette(dir int) bool {
	matches := m.paletteMatches()
	if len(matches) == 0 {
		return false
	}
	n := len(matches)
	m.paletteIdx = (m.clampPalette(n) + dir + n) % n
	m.input.SetValue("/" + matches[m.paletteIdx].Name)
	m.input.CursorEnd()
	m.relayout()
	return true
}

// cycleAtFile moves the @-file completion by dir and writes it into the input,
// re-prepending the '@' sigil that globFiles strips (or-ns8 — without it the
// completed path is no longer a recognized file directive on submit). Returns
// false if no @-token is being completed.
func (m *Conversation) cycleAtFile(dir int) bool {
	token, matches := m.atCompletions()
	if len(matches) == 0 {
		return false
	}
	n := len(matches)
	m.paletteIdx = (m.clampPalette(n) + dir + n) % n
	m.input.SetValue(strings.TrimSuffix(m.input.Value(), token) + "@" + matches[m.paletteIdx])
	m.input.CursorEnd()
	m.relayout()
	return true
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
	if !m.hasPerm() && m.inFlight {
		return nil
	}
	m.input.Reset()
	m.relayout() // collapse the box back to one row now that it's cleared

	if m.hasPerm() {
		outcome := "denied"
		if l := strings.ToLower(text); l == "y" || l == "yes" {
			outcome = "granted"
		}
		m.msgs = append(m.msgs, msg{role: "you", text: text})
		m.resolveHeadPerm(outcome) // reply + pop + surface the next queued card, if any
		if !m.hasPerm() {
			m.input.Placeholder = "your reply…"
		}
		m.render()
		return nil // the in-flight turn continues; updates arrive via Program.Send
	}
	m.recordHistory(text)
	// or-ykz.6: expand input directives (@file inline, !cmd run+send, !!cmd
	// run-only) before dispatch. A !!cmd is a LOCAL action — its output shows
	// in the transcript and NO prompt is sent.
	if strings.HasPrefix(text, "!") || strings.Contains(text, "@") {
		exp := ExpandDirectives(text, nil)
		if !exp.Send {
			m.msgs = append(m.msgs, msg{role: "you", text: text})
			if exp.Local != "" {
				m.msgs = append(m.msgs, msg{role: "orion", kind: "command", text: exp.Local})
			}
			m.render()
			return nil // local action — the model is never prompted
		}
		text = exp.Text
	}
	m.msgs = append(m.msgs, msg{role: "you", text: text})
	m.turnGen++ // a new turn: its async messages carry this gen; a stale turn's do not
	m.inFlight = true
	m.activity.reset()
	m.render()
	return tea.Batch(m.promptCmd(text, m.turnGen), m.sp.Tick, activityTick(m.turnGen))
}

// isRuneKey reports whether a key message is a single bare rune r (a tool-permission
// shortcut like y/a/n/e).
func isRuneKey(t tea.KeyMsg, r rune) bool {
	return t.Type == tea.KeyRunes && len(t.Runes) == 1 && t.Runes[0] == r && !t.Alt
}

// hasPerm reports whether a permission is awaiting the human's answer.
func (m Conversation) hasPerm() bool { return len(m.permQueue) > 0 }

// permKind is the kind of the permission currently awaiting an answer
// ("" if none) — "spec_ratify" | "tool".
func (m Conversation) permKind() string {
	if len(m.permQueue) == 0 {
		return ""
	}
	return m.permQueue[0].req.Kind
}

// surfacePerm renders p's approval card as the active prompt and scrolls it in.
func (m *Conversation) surfacePerm(p pendingPermission) {
	m.permExpanded = false
	if p.req.Kind == "tool" {
		m.msgs = append(m.msgs, msg{role: "orion", kind: "tool_permission", tool: p.req.Tool, text: p.req.Preview})
		m.input.Placeholder = "y allow · a always · n deny"
	} else {
		m.msgs = append(m.msgs, msg{role: "orion", kind: "permission", text: p.req.Title})
		m.input.Placeholder = "y to ratify · n to reject"
	}
	m.render()
	// Force the card into view even if the developer scrolled up to read history.
	if m.ready {
		m.vp.GotoBottom()
	}
}

// resolveHeadPerm replies to the head permission, pops it, and surfaces the
// next queued card (if any). No-op on an empty queue.
func (m *Conversation) resolveHeadPerm(outcome string) {
	if len(m.permQueue) == 0 {
		return
	}
	m.permQueue[0].reply <- acp.PermissionResult{Outcome: outcome}
	m.permQueue = m.permQueue[1:]
	if len(m.permQueue) > 0 {
		m.surfacePerm(m.permQueue[0])
	}
}

// denyAllPerms replies "denied" to every queued permission and clears the FIFO
// (cancel/quit), so no gate goroutine is left blocked. Returns whether anything
// was denied.
func (m *Conversation) denyAllPerms() bool {
	if len(m.permQueue) == 0 {
		return false
	}
	for _, p := range m.permQueue {
		p.reply <- acp.PermissionResult{Outcome: "denied"}
	}
	m.permQueue = nil
	m.permExpanded = false
	return true
}

// answerToolPerm resolves a pending tool-permission card with the given outcome
// (allow_once | allow_always | deny), unblocks the waiting approver, and records the
// decision in the transcript. The turn then continues (the tool runs, or is skipped).
func (m *Conversation) answerToolPerm(outcome string) tea.Cmd {
	if !m.hasPerm() {
		return nil
	}
	decision := map[string]string{
		"allow_once":   "✓ allowed once",
		"allow_always": "✓ allowed — always this session",
		"deny":         "⨯ denied",
	}[outcome]
	m.msgs = append(m.msgs, msg{role: "orion", text: decision})
	m.resolveHeadPerm(outcome) // reply + pop + surface the next queued card, if any
	if !m.hasPerm() {
		m.permExpanded = false
		m.input.Placeholder = ""
	}
	m.render()
	return nil // the in-flight turn continues; updates arrive via Program.Send
}

// cancelInFlight interrupts a running turn (or a pending permission) WITHOUT exiting the
// app. Returns whether anything was actually cancelled. The pipe Cancel runs OFF the
// event loop so a stalled write can never freeze the UI.
func (m *Conversation) cancelInFlight() bool {
	cancelled := false
	if m.denyAllPerms() { // unblock EVERY waiting gate goroutine, not just the head
		cancelled = true
	}
	if m.inFlight {
		m.inFlight = false
		// Advance the turn generation so the cancelled turn's still-alive
		// goroutine (its Call is uncancellable today) cannot append its tail to
		// the transcript after "cancelled" — its late gen-stamped messages no
		// longer match turnGen and are dropped (or-5g2q).
		m.turnGen++
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

// controlCmd runs an out-of-turn session control op (/compact, /model) off the Update
// loop and returns its result as a CommandResultMsg. Compaction makes an LLM call, so it
// is generously time-bounded.
func (m Conversation) controlCmd(op, arg string) tea.Cmd {
	client, sid := m.client, m.sid
	return func() tea.Msg {
		if client == nil {
			return CommandResultMsg{Text: op + ": not connected"}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		res, err := client.Control(ctx, sid, op, arg)
		if err != nil {
			return CommandResultMsg{Text: op + ": " + err.Error()}
		}
		return CommandResultMsg{Text: res}
	}
}

// promptCmd runs one prompt turn in its own goroutine (the Update loop stays
// free); streamed updates are pushed back via Program.Send.
func (m Conversation) promptCmd(text string, gen int) tea.Cmd {
	client, sid, prog := m.client, m.sid, m.gate.program()
	return func() tea.Msg {
		res, err := client.PromptWithUpdates(context.Background(), sid, text, func(u acp.Update) {
			if prog != nil {
				prog.Send(streamMsg{u: u, gen: gen})
			}
		})
		// or-mvr.15: a refusal stopReason is a CLASSIFIED outcome, not a
		// silent partial success — tell the developer what happened.
		if err == nil && agentruntime.IsRefusalStop(res.StopReason) {
			err = fmt.Errorf("the agent refused this request (stopReason=%s) — rephrase, or escalate if the refusal looks wrong", res.StopReason)
		}
		return turnDoneMsg{err: err, gen: gen}
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
		card := cardTitle.Render("ratify") + "\n" + mm.text + "\n[y] ratify   [n] reject"
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
func truncRunes(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	if limit < 1 {
		return ""
	}
	return string(r[:limit-1]) + "…"
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

// View renders banner, scrollable transcript, activity pane (when in-flight or
// carrying an idle summary), input, budget, hint.
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
	if palette == "" {
		palette = m.renderAtPalette() // @-file completion popup (mutually exclusive with /palette)
	}

	// Activity pane: rendered once here so its height can be subtracted from the
	// transcript viewport before we call m.vp.View() (the layout is height-exact).
	// A pending permission BLOCKS the turn on the human — nothing is "working" — so
	// the live pane is suppressed rather than wedged between the approval card and the
	// y/a/n prompt (the collision). inFlight stays true across the gate, so the pane
	// returns as soon as the developer answers.
	act := m.activity.render(paneW, m.inFlight && !m.hasPerm())

	// On a terminal too short to keep the transcript >= 3 rows beside the
	// activity pane + palette, drop the (passive) activity pane rather than
	// overflow the alt-screen — the transcript + input are essential (or-gony).
	paletteChrome := 0
	if palette != "" {
		paletteChrome = lipgloss.Height(palette)
	}
	if act != "" && m.vp.Height-lipgloss.Height(act)-paletteChrome < 3 {
		act = ""
	}

	// Shrink the viewport height for any chrome inserted between the transcript
	// and the input (activity pane and/or palette). This must happen before
	// m.vp.View() so the bordered transcript pane renders the right number of rows
	// in ALL states (empty, active, in-flight) — the layout is always height-exact.
	atBottom := m.vp.AtBottom()
	if act != "" || palette != "" {
		vpH := m.vp.Height
		if act != "" {
			vpH -= lipgloss.Height(act)
		}
		if palette != "" {
			vpH -= lipgloss.Height(palette)
		}
		m.vp.Height = max(3, vpH)
		// Re-pin the tail after shrinking: render()'s GotoBottom scrolled to the
		// tail at the FULL height, which is now below this reduced window — so the
		// newest lines would be clipped during a turn without this (or-gony).
		if atBottom {
			m.vp.GotoBottom()
		}
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
			raw := dimStyle.Render(es)
			// When ready, constrain the empty-state body to the viewport height so the
			// transcript pane renders the correct number of rows in all states (including
			// in-flight with an activity pane) and the layout stays height-exact.
			if m.ready {
				body = lipgloss.NewStyle().Height(m.vp.Height).Width(paneW).Render(raw)
			} else {
				body = raw
			}
		}
	}

	// Header: the Polaris identity line + the active brain (amber when offline).
	brainTint := lipgloss.NewStyle().Foreground(cLavender)
	if offline {
		brainTint = lipgloss.NewStyle().Foreground(cWarning)
	}
	header := bannerStyle.Render("✦ Orion") + dimStyle.Render("  ·  ") + brainTint.Render(m.brain) + dimStyle.Render("  ·  "+shortCwd())

	// Top pane: the conversation transcript (scrollable viewport).
	top := transPane.Width(paneW).Render(body)

	// Bottom pane: agent status (working / spend) above the input.
	status := dimStyle.Render(m.spendLine())
	if m.inFlight {
		status = m.sp.View() + " " + lipgloss.NewStyle().Foreground(cLavender).Render("working") + dimStyle.Render(" · "+m.spendLine())
	}
	bottom := inputPane.Width(paneW).Render(status + "\n" + m.input.View())

	// The program grabs the mouse (WithMouseCellMotion) for wheel-scroll, which puts
	// the terminal in mouse-reporting mode and suppresses native drag-select. Surface
	// the escape hatch — hold Shift (Option/⌥ on macOS) to select for copy/paste —
	// in place of the esc/ctrl+c-cancel and ctrl+d-quit hints, which are intuitive.
	hint := dimStyle.Render("  enter send · alt+enter newline · ↑/↓ history · pgup/pgdn scroll · tab complete · shift/⌥-drag to select·copy")
	if m.quitArmed {
		hint = warnGlyph.Render("  press ctrl+c again to exit") + dimStyle.Render(" · or any key to keep going")
	} else if m.inFlight {
		hint = dimStyle.Render("  esc/ctrl+c cancel · pgup/pgdn scroll · ctrl+d quit")
	}

	parts := []string{header, top}
	if act != "" {
		parts = append(parts, act)
	}
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

// atCompletions detects an "@<prefix>" file token being typed at the end of the input and
// returns that token plus matching paths under the working directory. Empty unless the
// last whitespace-separated field starts with "@" and sits at the cursor.
func (m Conversation) atCompletions() (string, []string) {
	v := m.input.Value()
	i := strings.LastIndexAny(v, " \t\n")
	last := v[i+1:] // the token under the cursor
	if !strings.HasPrefix(last, "@") {
		return "", nil
	}
	return last, globFiles(last[1:], 8)
}

// globFiles lists up to limit paths (relative to the cwd) whose path starts with prefix,
// marking directories with a trailing slash.
func globFiles(prefix string, limit int) []string {
	matches, _ := filepath.Glob(prefix + "*")
	if len(matches) > limit {
		matches = matches[:limit]
	}
	for i, p := range matches {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			matches[i] = p + "/"
		}
	}
	return matches
}

// renderAtPalette renders the @-file completion popup (matching paths + the selection),
// mirroring the slash-command palette. Empty when no @token is being typed.
func (m Conversation) renderAtPalette() string {
	_, matches := m.atCompletions()
	if len(matches) == 0 {
		return ""
	}
	idx := m.clampPalette(len(matches))
	sel := lipgloss.NewStyle().Foreground(cLavender)
	var b strings.Builder
	b.WriteString(dimStyle.Render("files · tab complete"))
	for i, f := range matches {
		if i == idx {
			b.WriteString("\n" + sel.Render("▸ @"+f))
		} else {
			b.WriteString("\n" + dimStyle.Render("  @"+f))
		}
	}
	return b.String()
}

func (m Conversation) spendLine() string {
	s := m.oc.Budget().Snapshot()
	line := fmt.Sprintf("spend: %d tok · $%.2f · %s · ctx ~%s", s.Tokens, s.Dollars, s.Wall.Round(time.Second), humanTokens(s.Tokens))
	if s.HasCeiling {
		line += fmt.Sprintf(" · ceiling:%s", s.State)
	}
	return line
}

// contextReport is the /context (a.k.a. /cost) output: token/spend usage this session.
func (m Conversation) contextReport() string {
	s := m.oc.Budget().Snapshot()
	out := fmt.Sprintf("context this session: ~%s tokens · $%.2f · %s", humanTokens(s.Tokens), s.Dollars, s.Wall.Round(time.Second))
	if s.HasCeiling {
		out += fmt.Sprintf(" · ceiling:%s", s.State)
	}
	return out
}

// shortCwd is the working directory with $HOME collapsed to ~ (empty if unavailable).
func shortCwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	if home, herr := os.UserHomeDir(); herr == nil && home != "" && strings.HasPrefix(wd, home) {
		return "~" + wd[len(home):]
	}
	return wd
}

// humanTokens renders a token count compactly (e.g. 12000 → "12.0k").
func humanTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

// newSessionCmd opens a fresh ACP session OFF the Update loop and posts a
// sessionResetMsg with the result. The model's per-session history is keyed by
// the session id, so a new id starts a clean context. SessionNew is a blocking
// round-trip (up to 2s), so /clear must dispatch it as a tea.Cmd rather than
// call it inline — the Update loop never blocks on the Conductor (or-gony).
func (m *Conversation) newSessionCmd() tea.Cmd {
	client := m.client
	if client == nil {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		sid, err := client.SessionNew(ctx)
		return sessionResetMsg{sid: sid, err: err}
	}
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
	defer func() { _ = clientEnd.Close() }()
	defer func() { _ = agentEnd.Close() }()

	// Brain selection (SPEC §0 amendment): a native LLM "Orion" agent when an API
	// key is present, else the deterministic conductor (offline/CI fallback). Both
	// satisfy acp.PromptFunc — the rest of this function is identical.
	brain, brainLabel, brainProv, brainModel := conductorBrain(oc)
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
	conv.brainProvider = brainProv
	conv.brainModel = brainModel
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
