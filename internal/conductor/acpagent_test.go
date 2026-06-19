package conductor

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
)

// TestConductorSpeaksACPAgent: the Conductor exposes the ACP agent interface and,
// backed by the orchestrator, runs the completeness conversation — the intent
// turn streams a clarifying question (the agent skill), not a canned echo.
func TestConductorSpeaksACPAgent(t *testing.T) {
	clientEnd, agentEnd := net.Pipe()
	defer clientEnd.Close()
	defer agentEnd.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ca := NewConductorAgent(RoleTemplate{Project: "demo", Tier: "standard"}, orchestrator.NewWithStore(openStore(t)))
	go ca.Serve(ctx, agentEnd, agentEnd)

	client := acp.NewClient(clientEnd, clientEnd, nil, nil)
	go client.Run(ctx)

	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	sid, err := client.SessionNew(ctx)
	if err != nil || sid == "" {
		t.Fatalf("session/new: id=%q err=%v", sid, err)
	}
	var texts []string
	res, err := client.Prompt(ctx, sid, "build a time service", func(u acp.Update) {
		texts = append(texts, u.Text)
	})
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if res.StopReason != "end_turn" {
		t.Fatalf("stop reason = %q", res.StopReason)
	}
	// The intent turn asks a completeness question (one at a time, with guidance).
	joined := strings.Join(texts, " | ")
	if !strings.Contains(joined, "?") || !strings.Contains(joined, "to answer") {
		t.Fatalf("conductor did not ask a completeness question over ACP: %v", texts)
	}
}

// TestRoleTemplateRenders: the role template covers every personality
// responsibility and states the non-override invariant.
func TestRoleTemplateRenders(t *testing.T) {
	out := RoleTemplate{Project: "demo", Tier: "critical"}.Render()
	for _, s := range RoleSections {
		if !strings.Contains(out, s) {
			t.Fatalf("role template missing section %q:\n%s", s, out)
		}
	}
	if !strings.Contains(out, "critical") {
		t.Fatal("role template should carry the tier context")
	}
	if !strings.Contains(strings.ToLower(out), "override") {
		t.Fatal("role template must state the deterministic-gate non-override invariant")
	}
}

// TestConductorCannotOverrideProofVerdict: no matter what the Conductor agent
// reasons, the deterministic gate decides closure — a Reject converged verdict
// does NOT close the task.
func TestConductorCannotOverrideProofVerdict(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	sm := New(s)
	taskID := seedTask(t, s)

	// The "agent" claims the work is complete, but the deterministic proof rejects.
	report := proof.Report{
		Outcome: truthalign.Converge(
			truthalign.ModeResult{Mode: "behavioral", Pass: true, Metrics: map[string]float64{"run_count": 1}},
			truthalign.ModeResult{Mode: "empirical", Pass: false, Metrics: map[string]float64{"run_count": 1}},
		),
		Modes: []proof.ModeReport{
			{Result: truthalign.ModeResult{Mode: "behavioral", Pass: true}},
			{Result: truthalign.ModeResult{Mode: "empirical", Pass: false}},
		},
	}
	closed, err := sm.ProveAndCloseReport(ctx, taskID, report)
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	if closed {
		t.Fatal("the Conductor must not be able to close a task the proof harness rejected")
	}
	if taskStatus(t, s, taskID) == "done" {
		t.Fatal("task reached done despite a Reject verdict — gate overridden")
	}
}
