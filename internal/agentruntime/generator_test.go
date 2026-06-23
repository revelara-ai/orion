package agentruntime

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// fixtureAgentDriver returns an ACPDriver whose "agent" writes a REAL, buildable
// Go service for req — but writes it through the ACP fs/write_text_file seam (the
// client serves it into the worktree). This stands in for a real vendor agent and
// exercises the full spawn-less generation path deterministically.
func fixtureAgentDriver(t *testing.T, req GenRequest) ACPDriver {
	t.Helper()
	return func(ctx context.Context, root string) (ACPSession, func(), error) {
		// Pre-render the real service the "agent" will author.
		stage := t.TempDir()
		if _, err := sandbox.GenerateTimeServiceFixture(stage, sandbox.GenSpec{
			Module: req.Module, Route: req.Route, Port: req.Port, Format: req.Format, TimeZone: req.TimeZone,
		}); err != nil {
			return nil, func() {}, err
		}
		authored := map[string]string{}
		_ = filepath.WalkDir(stage, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			b, _ := os.ReadFile(p)
			rel, _ := filepath.Rel(stage, p)
			authored[rel] = string(b)
			return nil
		})

		clientEnd, agentEnd := net.Pipe()
		client := acp.NewClient(clientEnd, clientEnd, nil, acp.ScopedFS{Root: root})
		go client.Run(ctx)

		var agentConn *acp.Conn
		handler := func(hctx context.Context, method string, params json.RawMessage) (any, error) {
			switch method {
			case "initialize":
				return map[string]any{"protocolVersion": 1}, nil
			case "session/new":
				return map[string]string{"sessionId": "s1"}, nil
			case "session/prompt":
				// Author the program by writing each file via the ACP fs seam.
				for path, content := range authored {
					if err := agentConn.Call(hctx, "fs/write_text_file",
						map[string]string{"path": path, "content": content}, nil); err != nil {
						return nil, err
					}
				}
				return acp.PromptResult{StopReason: "end_turn"}, nil
			}
			return nil, nil
		}
		agentConn = acp.NewConn(agentEnd, agentEnd, handler, nil)
		go agentConn.Run(ctx)

		cleanup := func() { clientEnd.Close(); agentEnd.Close() }
		return client, cleanup, nil
	}
}

func genInto(t *testing.T, req GenRequest) Artifact {
	t.Helper()
	dir := t.TempDir()
	g := AgentGenerator{Driver: fixtureAgentDriver(t, req)}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	art, err := g.Generate(ctx, req, dir)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return art
}

// TestGeneratorAgentWritesFromSpecNotTemplate: the generator drives the agent to
// write code from the spec — two DIFFERENT specs yield two DIFFERENT artifacts.
func TestGeneratorAgentWritesFromSpecNotTemplate(t *testing.T) {
	a := genInto(t, GenRequest{Module: "gen/a", Route: "/time", Port: 8080, Format: "json", TimeZone: "UTC"})
	b := genInto(t, GenRequest{Module: "gen/b", Route: "/clock", Port: 9090, Format: "text", TimeZone: "America/New_York"})

	if len(a.Files) == 0 || len(b.Files) == 0 {
		t.Fatalf("no files written: a=%v b=%v", a.Files, b.Files)
	}
	readMain := func(art Artifact) string {
		b, err := os.ReadFile(filepath.Join(art.Dir, "main.go"))
		if err != nil {
			t.Fatalf("read main.go: %v", err)
		}
		return string(b)
	}
	srcA, srcB := readMain(a), readMain(b)
	if srcA == srcB {
		t.Fatal("two different specs produced identical artifacts (still templated)")
	}
	if !strings.Contains(srcA, "/time") || !strings.Contains(srcB, "/clock") {
		t.Fatalf("artifacts do not reflect their spec routes")
	}
}

// TestGeneratorArtifactBuildsAndProves: the agent-written artifact builds and
// passes the proof harness (behavioral mode) — proof gates generated code.
func TestGeneratorArtifactBuildsAndProves(t *testing.T) {
	if testing.Short() {
		t.Skip("builds + proves: skipped in -short")
	}
	req := GenRequest{Module: "gen/proven", Route: "/time", Port: 8080, Format: "json", TimeZone: "UTC"}
	art := genInto(t, req)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	outcome, err := proof.ProveBehavioral(ctx, art.Dir, testsynth.Contract{Route: req.Route, Format: req.Format, TimeZone: req.TimeZone})
	if err != nil {
		t.Fatalf("prove behavioral: %v", err)
	}
	if outcome.Verdict != truthalign.Accept {
		t.Fatalf("agent-written artifact did not pass behavioral proof: %s", outcome.Verdict)
	}
}
