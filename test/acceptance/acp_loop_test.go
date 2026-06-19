package acceptance

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
	"github.com/revelara-ai/orion/internal/agentruntime"
	"github.com/revelara-ai/orion/internal/conductor"
	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/hazard/stpa"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
	"github.com/revelara-ai/orion/internal/sandbox"
	"github.com/revelara-ai/orion/internal/tui"
)

// answerForQuestion maps a streamed completeness question to a valid answer (the
// human reads + types these in the real TUI).
func answerForQuestion(q string) string {
	ql := strings.ToLower(q)
	if strings.Contains(ql, "ratify") || strings.Contains(ql, "review the spec") {
		return "y"
	}
	switch {
	case strings.Contains(ql, "format"):
		return "json"
	case strings.Contains(ql, "timezone"):
		return "UTC"
	case strings.Contains(ql, "port"):
		return "8080"
	case strings.Contains(ql, "route"), strings.Contains(ql, "path"):
		return "/time"
	default:
		return "unspecified"
	}
}

// fixtureACPDriver returns an ACPDriver whose "agent" authors a real, buildable
// service for req — written through the ACP fs/write_text_file seam — so the
// generation half of the loop is genuinely exercised over ACP.
func fixtureACPDriver(t *testing.T, req agentruntime.GenRequest) agentruntime.ACPDriver {
	t.Helper()
	return func(ctx context.Context, root string) (agentruntime.ACPSession, func(), error) {
		stage := t.TempDir()
		if _, err := sandbox.GenerateFixtureService(stage, sandbox.GenSpec{
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
				for path, content := range authored {
					if err := agentConn.Call(hctx, "fs/write_text_file", map[string]string{"path": path, "content": content}, nil); err != nil {
						return nil, err
					}
				}
				return acp.PromptResult{StopReason: "end_turn"}, nil
			}
			return nil, nil
		}
		agentConn = acp.NewConn(agentEnd, agentEnd, handler, nil)
		go agentConn.Run(ctx)
		return client, func() { clientEnd.Close(); agentEnd.Close() }, nil
	}
}

// TestV20LoopOverACP is the ACP verification bookend (or-0i5): it drives the
// canonical V2.0 loop end-to-end through the ACP seam — a conversation with the
// primed Conductor agent, permission gates honored, and a proven, runnable
// service generated over the ACP fs seam — replacing the internal-channel path.
func TestV20LoopOverACP(t *testing.T) {
	if testing.Short() {
		t.Skip("builds + proves a real service; skipped in -short")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// spec-ratify is honored; a destructive op is gated (denied).
	gate := &tui.ApprovalGate{Decide: func(r acp.PermissionRequest) acp.PermissionResult {
		if r.Kind == "destructive" {
			return acp.PermissionResult{Outcome: "denied"}
		}
		return acp.PermissionResult{Outcome: "granted"}
	}}

	// --- Phase 1: agent-driven completeness conversation over ACP ---
	store, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	cEnd, aEnd := net.Pipe()
	defer cEnd.Close()
	defer aEnd.Close()
	ca := conductor.NewConductorAgent(conductor.RoleTemplate{Project: "smoke", Tier: "standard"}, orchestrator.NewWithStore(store))
	go ca.Serve(ctx, aEnd, aEnd)
	client := tui.NewACPClient(cEnd, cEnd, gate, nil)
	go client.Run(ctx)
	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("acp initialize: %v", err)
	}
	sid, err := client.SessionNew(ctx)
	if err != nil {
		t.Fatalf("acp session/new: %v", err)
	}
	// Intent, then answer each streamed question until the spec ratifies — all
	// driven through the ACP seam by the Conductor agent (the completeness skill).
	if _, err := client.Prompt(ctx, sid, "build an http service that returns the current time"); err != nil {
		t.Fatalf("acp intent: %v", err)
	}
	ratified := false
	for i := 0; i < 8 && !ratified; i++ {
		_, plan, _, _ := client.Panes.Snapshot()
		for _, line := range plan {
			if strings.Contains(line, "ratified") {
				ratified = true
			}
		}
		if ratified {
			break
		}
		conv, _, _, _ := client.Panes.Snapshot()
		if len(conv) == 0 {
			t.Fatal("no question streamed to the Conversation pane")
		}
		if _, err := client.Prompt(ctx, sid, answerForQuestion(conv[len(conv)-1])); err != nil {
			t.Fatalf("acp answer: %v", err)
		}
	}
	if !ratified {
		conv, plan, _, _ := client.Panes.Snapshot()
		t.Fatalf("completeness conversation did not ratify over ACP; conv=%v plan=%v", conv, plan)
	}

	// --- Phase 2: session/request_permission gates honored over ACP ---
	pEnd, paEnd := net.Pipe()
	defer pEnd.Close()
	defer paEnd.Close()
	pc := tui.NewACPClient(pEnd, pEnd, gate, nil)
	go pc.Run(ctx)
	peer := acp.NewConn(paEnd, paEnd, nil, nil)
	go peer.Run(ctx)
	var ratify, destructive acp.PermissionResult
	if err := peer.Call(ctx, "session/request_permission", acp.PermissionRequest{Kind: "spec_ratify", Title: "ratify spec"}, &ratify); err != nil {
		t.Fatalf("spec_ratify request: %v", err)
	}
	if err := peer.Call(ctx, "session/request_permission", acp.PermissionRequest{Kind: "destructive", Title: "rm -rf prod"}, &destructive); err != nil {
		t.Fatalf("destructive request: %v", err)
	}
	if ratify.Outcome != "granted" {
		t.Fatalf("spec ratify must be honored (granted), got %q", ratify.Outcome)
	}
	if destructive.Outcome != "denied" {
		t.Fatalf("destructive op must be gated (denied), got %q", destructive.Outcome)
	}

	// --- Phase 3: proven, runnable service generated over the ACP fs seam ---
	req := agentruntime.GenRequest{Module: "gen/smoke", Route: "/time", Port: 8080, Format: "json", TimeZone: "UTC"}
	gen := agentruntime.AgentGenerator{Driver: fixtureACPDriver(t, req)}
	art, err := gen.Generate(ctx, req, t.TempDir())
	if err != nil {
		t.Fatalf("generate over ACP fs seam: %v", err)
	}
	report, err := proof.ProveAll(ctx, art.Dir,
		testsynth.Contract{Route: req.Route, Format: req.Format, TimeZone: req.TimeZone},
		stpa.RatifiedTimeServiceModel())
	if err != nil {
		t.Fatalf("multi-modal proof: %v", err)
	}
	if report.Outcome.Verdict != truthalign.Accept {
		t.Fatalf("end-to-end ACP loop did not converge to Accept: %s", report.Outcome.Verdict)
	}
}
