package conductor

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/pkg/llm"
)

func seedSession(a *OrionAgent, id string, turns int) {
	var msgs []llm.Message
	for i := 0; i < turns; i++ {
		msgs = append(msgs,
			llm.TextMessage(llm.RoleUser, "question "+string(rune('A'+i))),
			llm.TextMessage(llm.RoleAssistant, "answer "+string(rune('A'+i))),
		)
	}
	a.mu.Lock()
	a.sessions[id] = msgs
	a.mu.Unlock()
}

func treeAgent(t *testing.T) *OrionAgent {
	t.Helper()
	return NewOrionAgent(&fakeLLM{resp: []*llm.ChatResponse{endTurn("ok")}}, orchestrator.NewWithStore(openStore(t)), RoleTemplate{Project: "demo"})
}

// TestForkCreatesBranchSharingAncestry (or-ykz.5 Done-when): /fork from a
// prior turn creates a NEW branch whose history is the shared prefix; the
// original branch is untouched.
func TestForkCreatesBranchSharingAncestry(t *testing.T) {
	a := treeAgent(t)
	seedSession(a, "s1", 3)

	out, err := a.Control(context.Background(), "s1", "fork", "2")
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	if !strings.HasPrefix(out, "SESSION:") {
		t.Fatalf("fork must return the SESSION sentinel for the TUI to switch, got %q", out)
	}
	newID := strings.TrimPrefix(strings.SplitN(out, " ", 2)[0], "SESSION:")

	a.mu.Lock()
	src, branch := a.sessions["s1"], a.sessions[newID]
	a.mu.Unlock()
	if len(src) != 6 {
		t.Fatalf("the ORIGINAL branch must be untouched (6 messages), got %d", len(src))
	}
	if len(branch) != 4 {
		t.Fatalf("fork at turn 2 must carry turns 1-2 (4 messages), got %d", len(branch))
	}
	if branch[3].Content[0].Text != "answer B" {
		t.Fatalf("branch must share ancestry with the source, got %q", branch[3].Content[0].Text)
	}
	// Diverge the branch; the source must not see it.
	a.mu.Lock()
	a.sessions[newID] = append(a.sessions[newID], llm.TextMessage(llm.RoleUser, "divergent"))
	srcLen := len(a.sessions["s1"])
	src5 := a.sessions["s1"][4].Content[0].Text // an aliased backing array would be CLOBBERED here
	a.mu.Unlock()
	if srcLen != 6 || src5 != "question C" {
		t.Fatalf("divergence on the branch must never touch the source (len=%d, msg5=%q)", srcLen, src5)
	}
}

func TestCloneCopiesFullHistory(t *testing.T) {
	a := treeAgent(t)
	seedSession(a, "s1", 2)
	out, err := a.Control(context.Background(), "s1", "clone", "")
	if err != nil {
		t.Fatal(err)
	}
	newID := strings.TrimPrefix(strings.SplitN(out, " ", 2)[0], "SESSION:")
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.sessions[newID]) != len(a.sessions["s1"]) {
		t.Fatalf("clone must copy the full history: %d vs %d", len(a.sessions[newID]), len(a.sessions["s1"]))
	}
}

// TestTreeNavigatesBranches (or-ykz.5 Done-when): /tree renders the ancestry
// tree — parent, forks with their fork points, and the current marker.
func TestTreeNavigatesBranches(t *testing.T) {
	a := treeAgent(t)
	seedSession(a, "s1", 3)
	out1, _ := a.Control(context.Background(), "s1", "fork", "1")
	f1 := strings.TrimPrefix(strings.SplitN(out1, " ", 2)[0], "SESSION:")
	out2, _ := a.Control(context.Background(), "s1", "fork", "2")
	f2 := strings.TrimPrefix(strings.SplitN(out2, " ", 2)[0], "SESSION:")

	view, err := a.Control(context.Background(), f1, "tree", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"s1", f1, f2, "turn 1", "turn 2", "you are here"} {
		if !strings.Contains(view, want) {
			t.Fatalf("tree view missing %q:\n%s", want, view)
		}
	}
}

func TestSwitchValidatesTarget(t *testing.T) {
	a := treeAgent(t)
	seedSession(a, "s1", 1)
	if out, err := a.Control(context.Background(), "s1", "switch", "s1"); err != nil || !strings.HasPrefix(out, "SESSION:s1") {
		t.Fatalf("switch to a known session must return its sentinel, got %q err=%v", out, err)
	}
	if out, _ := a.Control(context.Background(), "s1", "switch", "ghost"); strings.HasPrefix(out, "SESSION:") {
		t.Fatalf("switch to an unknown session must not emit the sentinel, got %q", out)
	}
}

func TestForkRefusesBadTurn(t *testing.T) {
	a := treeAgent(t)
	seedSession(a, "s1", 2)
	if out, _ := a.Control(context.Background(), "s1", "fork", "9"); strings.HasPrefix(out, "SESSION:") {
		t.Fatalf("fork beyond the last turn must refuse, got %q", out)
	}
	if out, _ := a.Control(context.Background(), "s1", "fork", "zero"); strings.HasPrefix(out, "SESSION:") {
		t.Fatalf("non-numeric turn must refuse, got %q", out)
	}
}
