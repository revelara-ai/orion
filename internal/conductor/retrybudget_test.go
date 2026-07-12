package conductor

import (
	"context"
	"testing"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/pkg/llm"
	"github.com/revelara-ai/orion/pkg/llmclient"
)

func TestWithRetryBudgetInstallsAndRespectsEnvAndOuter(t *testing.T) {
	t.Setenv("ORION_RETRY_BUDGET", "2")
	ctx := withLLMGuards(context.Background())
	b := llmclient.BudgetFrom(ctx)
	if b == nil {
		t.Fatal("root must install a budget")
	}
	if llmclient.GateFrom(ctx) == nil {
		t.Fatal("root must install the shared in-flight gate (or-mvr.3)")
	}
	if llmclient.GateFrom(withLLMGuards(context.Background())) != llmclient.GateFrom(ctx) {
		t.Fatal("every operation root must share ONE process-wide gate — a per-root gate would be the inc-qdi bypass lane")
	}
	if !b.Take() || !b.Take() || b.Take() {
		t.Fatal("env-configured budget must be 2")
	}
	// Outermost wins: a nested root keeps the existing budget.
	if llmclient.BudgetFrom(withLLMGuards(ctx)) != b {
		t.Fatal("a nested operation root must keep the outer turn's budget")
	}
}

// budgetProbeLLM records whether a stack-wide retry budget was visible from
// INSIDE the provider call — i.e. the turn root actually installed it.
type budgetProbeLLM struct {
	fakeLLM
	sawBudget bool
}

func (p *budgetProbeLLM) ChatStream(ctx context.Context, req llm.ChatRequest, onText func(string)) (*llm.ChatResponse, error) {
	p.sawBudget = llmclient.BudgetFrom(ctx) != nil
	return &llm.ChatResponse{StopReason: llm.StopEndTurn, Content: []llm.ContentBlock{{Type: llm.BlockText, Text: "ok"}}}, nil
}

func TestPromptInstallsStackWideRetryBudget(t *testing.T) {
	oc := orchestrator.NewWithStore(openStore(t))
	prov := &budgetProbeLLM{}
	agent := NewOrionAgent(prov, oc, RoleTemplate{Project: "demo"})
	_, err := agent.Prompt(context.Background(), "s1", "hi",
		func(acp.Update) {}, func(acp.PermissionRequest) (acp.PermissionResult, error) { return acp.PermissionResult{}, nil })
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if !prov.sawBudget {
		t.Fatal("the turn's retry budget must be visible inside the provider call (or-mvr.1)")
	}
}
