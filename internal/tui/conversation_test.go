package tui

import (
	"context"
	"net"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/revelara-ai/orion/internal/conductor"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator"
)

// wireConversation builds a Conversation wired to an in-process Conductor agent
// over ACP (mirrors what Run() sets up), returning the model + a cleanup.
func wireConversation(t *testing.T) (Conversation, *orchestrator.Conductor, func()) {
	t.Helper()
	store, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	oc := orchestrator.NewWithStore(store)
	ctx, cancel := context.WithCancel(context.Background())
	clientEnd, agentEnd := net.Pipe()
	agent := conductor.NewConductorAgent(conductor.RoleTemplate{Project: "t"}, oc)
	go func() { _ = agent.Serve(ctx, agentEnd, agentEnd) }()
	client := NewACPClient(clientEnd, clientEnd, &ApprovalGate{}, nil)
	go func() { _ = client.Run(ctx) }()
	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	sid, err := client.SessionNew(ctx)
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	m := NewConversation(client, sid, oc)
	cleanup := func() { cancel(); _ = clientEnd.Close(); _ = agentEnd.Close(); _ = store.Close() }
	return m, oc, cleanup
}

func sendEnter(m Conversation, text string) Conversation {
	m.input.SetValue(text)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return next.(Conversation)
}

func transcript(m Conversation) string { return strings.Join(m.lines, "\n") }

// answerFromTranscript reads the most recent question and returns a valid answer.
func answerFromTranscript(m Conversation) string {
	for i := len(m.lines) - 1; i >= 0; i-- {
		l := strings.ToLower(m.lines[i])
		if strings.Contains(l, "ratify") || strings.Contains(l, "review the spec") {
			return "y"
		}
		switch {
		case strings.Contains(l, "format"):
			return "json"
		case strings.Contains(l, "timezone"):
			return "UTC"
		case strings.Contains(l, "port"):
			return "8080"
		case strings.Contains(l, "route"), strings.Contains(l, "path"):
			return "/time"
		}
	}
	return "x"
}

// TestConversationEmptyState: the fresh pane shows the empty-state prompt.
func TestConversationEmptyState(t *testing.T) {
	m, _, cleanup := wireConversation(t)
	defer cleanup()
	if !strings.Contains(m.View(), emptyState) {
		t.Fatalf("empty-state prompt missing:\n%s", m.View())
	}
}

// TestConversationDrivesCompletenessOverACP: typing an intent then answering the
// streamed questions (one at a time) ratifies the spec — entirely over the ACP
// seam, with the Conductor agent doing the questioning (or-6ck + or-owz).
func TestConversationDrivesCompletenessOverACP(t *testing.T) {
	m, oc, cleanup := wireConversation(t)
	defer cleanup()

	m = sendEnter(m, "build an http service that returns the current time")
	if !strings.Contains(transcript(m), "?") {
		t.Fatalf("no completeness question rendered after intent:\n%s", transcript(m))
	}

	guard := 0
	for !strings.Contains(transcript(m), "ratified") {
		m = sendEnter(m, answerFromTranscript(m))
		guard++
		if guard > 10 {
			t.Fatalf("did not ratify — answers not advancing over ACP:\n%s", transcript(m))
		}
	}

	// The spec really is accepted in the store (answers persisted via the agent).
	sv, err := oc.SpecView(context.Background())
	if err != nil {
		t.Fatalf("spec view: %v", err)
	}
	if sv.Status != "accepted" {
		t.Fatalf("spec status = %q, want accepted", sv.Status)
	}
}

// TestConversationEmptyInputNotSent: an empty Enter adds no transcript line.
func TestConversationEmptyInputNotSent(t *testing.T) {
	m, _, cleanup := wireConversation(t)
	defer cleanup()
	before := len(m.lines)
	m = sendEnter(m, "   ")
	if len(m.lines) != before {
		t.Fatalf("empty input produced transcript lines: %v", m.lines)
	}
}

// TestConversationQuit: Ctrl+C quits.
func TestConversationQuit(t *testing.T) {
	m, _, cleanup := wireConversation(t)
	defer cleanup()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !next.(Conversation).quitting {
		t.Fatal("ctrl+c did not set quitting")
	}
	if cmd == nil {
		t.Fatal("ctrl+c should return a quit command")
	}
}

// TestSpendIsSurfacedLiveInTUI: the always-on budget spend renders live.
func TestSpendIsSurfacedLiveInTUI(t *testing.T) {
	m, oc, cleanup := wireConversation(t)
	defer cleanup()
	if !strings.Contains(m.View(), "spend:") {
		t.Fatalf("spend line missing:\n%s", m.View())
	}
	oc.Budget().Record(1234, 0.56)
	view := m.View()
	if !strings.Contains(view, "1234 tok") || !strings.Contains(view, "$0.56") {
		t.Fatalf("live spend not surfaced:\n%s", view)
	}
}
