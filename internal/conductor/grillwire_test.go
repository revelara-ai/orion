package conductor

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/pkg/llm"
)

// grillFakeProvider answers every Chat with a canned report_questions call and
// counts invocations, so a test can observe whether the grill seam fired.
type grillFakeProvider struct {
	calls atomic.Int64
}

func (p *grillFakeProvider) Name() string { return "grill-fake" }

func (p *grillFakeProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	p.calls.Add(1)
	return &llm.ChatResponse{Content: []llm.ContentBlock{{
		Type: llm.BlockToolUse,
		ToolUse: &llm.ToolUse{
			ID:    "tu-1",
			Name:  "report_questions",
			Input: json.RawMessage(`{"questions":[{"key":"audience","question":"Who is this for?"}]}`),
		},
	}}}, nil
}

func (p *grillFakeProvider) ChatStream(ctx context.Context, req llm.ChatRequest, _ func(string)) (*llm.ChatResponse, error) {
	return p.Chat(ctx, req)
}

func (p *grillFakeProvider) Models(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (p *grillFakeProvider) Ping(context.Context) error                      { return nil }

// TestSpecToolsWiresGrillAtFirstIntake (or-kjcm.1): building the spec tool
// registry with a live provider must wire the grill elicitation driver BEFORE
// any tool runs, so the very first submit_intent of a fresh process gets
// open-ended grill questions ahead of the demoted floor. The historical bug:
// wiring lived only inside build_service's Run closure, which executes after
// ratify_spec — so first intake always fell back to the checklist floor.
func TestSpecToolsWiresGrillAtFirstIntake(t *testing.T) {
	t.Setenv("ORION_ELICITATION", "grill")
	fake := &grillFakeProvider{}
	c := orchestrator.NewWithStore(openStore(t))
	r := specTools(c, fake, &changeSession{}, nil)

	tool, ok := r.Get("submit_intent")
	if !ok {
		t.Fatal("submit_intent not registered")
	}
	// force_greenfield: the test cwd is a code-bearing repo, which would
	// otherwise route the intent to the brownfield change flow.
	out, err := tool.Run(context.Background(), json.RawMessage(
		`{"intent":"Build a matchmaking service for a co-op game. I expect this to be a large project.","force_greenfield":true}`))
	if err != nil {
		t.Fatalf("submit_intent: %v", err)
	}
	if fake.calls.Load() == 0 {
		t.Fatalf("grill agent was never invoked at first intake — the elicitation seam is unwired (output: %s)", out)
	}

	var conf struct {
		OpenDecisions []struct {
			Key string `json:"Key"`
		} `json:"open_decisions"`
	}
	if err := json.Unmarshal([]byte(out), &conf); err != nil {
		t.Fatalf("decode submit_intent output: %v\n%s", err, out)
	}
	if len(conf.OpenDecisions) == 0 {
		t.Fatal("no open decisions returned")
	}
	if got := conf.OpenDecisions[0].Key; got != "grill.audience" {
		t.Fatalf("grill questions must precede the demoted floor; first open decision = %q, want grill.audience\nout: %s", got, out)
	}
	foundFloor := false
	for _, d := range conf.OpenDecisions {
		if !strings.HasPrefix(d.Key, "grill.") {
			foundFloor = true
		}
	}
	if !foundFloor {
		t.Fatalf("the demoted floor must still surface after grill questions: %s", out)
	}
}

// TestSpecToolsNilProviderLeavesFloorDriving: without a provider (offline/CI)
// the registry must not wire a grill — the deterministic checklist drives,
// exactly as before.
func TestSpecToolsNilProviderLeavesFloorDriving(t *testing.T) {
	t.Setenv("ORION_ELICITATION", "grill")
	c := orchestrator.NewWithStore(openStore(t))
	r := specTools(c, nil, &changeSession{}, nil)
	tool, _ := r.Get("submit_intent")
	out, err := tool.Run(context.Background(), json.RawMessage(
		`{"intent":"Build a small JSON echo service.","force_greenfield":true}`))
	if err != nil {
		t.Fatalf("submit_intent: %v", err)
	}
	if strings.Contains(out, "grill.") {
		t.Fatalf("nil provider must leave the checklist driving, got grill questions: %s", out)
	}
}
