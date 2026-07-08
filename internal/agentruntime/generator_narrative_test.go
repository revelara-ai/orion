package agentruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/acp"
)

// narrativeSession is a fake ACPSession that streams agent text + writes one file.
type narrativeSession struct{ dir string }

func (s narrativeSession) Initialize(context.Context) error           { return nil }
func (s narrativeSession) SessionNew(context.Context) (string, error) { return "s1", nil }
func (s narrativeSession) Prompt(_ context.Context, _, _ string, onUpdate func(acp.Update)) (acp.PromptResult, error) {
	onUpdate(acp.Update{Kind: "agent_thought", Text: "The timezone handling was wrong; fixing it."})
	onUpdate(acp.Update{Kind: "tool_call", Text: ""}) // empty text is ignored
	onUpdate(acp.Update{Kind: "agent_message", Text: "Wrote main.go with proper tz parsing."})
	_ = os.WriteFile(filepath.Join(s.dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	return acp.PromptResult{StopReason: "end_turn"}, nil
}

// TestAgentGeneratorCapturesNarrative (or-7mr): the generator accumulates the agent's streamed
// self-report into Artifact.Narrative (previously discarded), so a failed build can record the
// quarantined "what the agent thought went wrong".
func TestAgentGeneratorCapturesNarrative(t *testing.T) {
	dir := t.TempDir()
	g := AgentGenerator{Driver: func(_ context.Context, root string) (ACPSession, func(), error) {
		return narrativeSession{dir: root}, func() {}, nil
	}}
	art, err := g.Generate(context.Background(), GenRequest{Description: "build a time service"}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(art.Narrative, "timezone handling was wrong") ||
		!strings.Contains(art.Narrative, "proper tz parsing") {
		t.Fatalf("agent narrative not captured: %q", art.Narrative)
	}
	if len(art.Files) == 0 {
		t.Fatal("expected the agent-written file to be listed")
	}
}
