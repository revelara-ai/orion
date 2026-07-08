package conductor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/acp"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/pkg/llm"
)

// fakeLLM is a deterministic provider that replays scripted responses — it stands
// in for a real model so Phase 3 is provable without a live API key (the live
// path is exercised manually).
type fakeLLM struct {
	resp []*llm.ChatResponse
	i    int
}

func (f *fakeLLM) Name() string                                    { return "fake" }
func (f *fakeLLM) Models(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (f *fakeLLM) Ping(context.Context) error                      { return nil }
func (f *fakeLLM) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	r := f.resp[f.i]
	if f.i < len(f.resp)-1 {
		f.i++
	}
	return r, nil
}
func (f *fakeLLM) ChatStream(ctx context.Context, req llm.ChatRequest, onText func(string)) (*llm.ChatResponse, error) {
	r, err := f.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	if t := r.Text(); t != "" {
		onText(t)
	}
	return r, nil
}

func tuResp(id, name, input string) *llm.ChatResponse {
	return &llm.ChatResponse{StopReason: llm.StopToolUse, Content: []llm.ContentBlock{
		{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{ID: id, Name: name, Input: json.RawMessage(input)}},
	}}
}
func endTurn(text string) *llm.ChatResponse {
	return &llm.ChatResponse{StopReason: llm.StopEndTurn, Content: []llm.ContentBlock{{Type: llm.BlockText, Text: text}}}
}

// TestOrionAgentDrivesSpecToRatification: the native agent, calling ONLY the
// deterministic spec tools, takes an intent through submit → answer → preview →
// ratify and lands an accepted spec. The model never writes the store directly;
// the gates (which would reject an incomplete spec) are the truth source.
func TestOrionAgentDrivesSpecToRatification(t *testing.T) {
	oc := orchestrator.NewWithStore(openStore(t))
	prov := &fakeLLM{resp: []*llm.ChatResponse{
		tuResp("1", "submit_intent", `{"intent":"build a time service"}`),
		tuResp("2", "record_answer", `{"key":"response_format","value":"json"}`),
		tuResp("3", "record_answer", `{"key":"timezone","value":"UTC"}`),
		tuResp("4", "record_answer", `{"key":"port","value":"8080"}`),
		tuResp("5", "record_answer", `{"key":"route","value":"/time"}`),
		tuResp("6", "preview_spec", `{}`),
		tuResp("6b", "approve_assumptions", `{}`), // developer confirmed the previewed assumptions
		tuResp("7", "ratify_spec", `{}`),
		endTurn("Spec ratified ✓ — ready to build."),
	}}
	agent := NewOrionAgent(prov, oc, RoleTemplate{Project: "demo"})

	var updates []acp.Update
	_, err := agent.Prompt(context.Background(), "s1", "build a time service",
		func(u acp.Update) { updates = append(updates, u) },
		func(acp.PermissionRequest) (acp.PermissionResult, error) { return acp.PermissionResult{}, nil })
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}

	sv, err := oc.SpecView(context.Background())
	if err != nil {
		t.Fatalf("specview: %v", err)
	}
	if sv.Status != "accepted" {
		t.Fatalf("status = %q, want accepted (updates=%+v)", sv.Status, updates)
	}
	var sawPlan, sawTool bool
	for _, u := range updates {
		if u.Kind == "plan" {
			sawPlan = true
		}
		if u.Kind == "tool_call" && strings.Contains(u.Text, "ratify_spec") {
			sawTool = true
		}
	}
	if !sawPlan {
		t.Fatalf("ratification was not surfaced as a plan update: %+v", updates)
	}
	if !sawTool {
		t.Fatalf("ratify_spec tool call not streamed to Fleet: %+v", updates)
	}
}

// TestOrionAgentRatifiesThenBuildsInOneShot: the native agent chains ratify_spec
// → build_service in a single turn (the developer's "ratify then build in one
// shot" flow). Asserts the agent invoked the build and the spec is anchored;
// build correctness itself is proven by TestBuildAndProveFixture.
func TestOrionAgentRatifiesThenBuildsInOneShot(t *testing.T) {
	if testing.Short() {
		t.Skip("build_service compiles + proves a service; skipped in -short")
	}
	oc := orchestrator.NewWithStore(openStore(t))
	prov := &fakeLLM{resp: []*llm.ChatResponse{
		tuResp("1", "submit_intent", `{"intent":"build a time service"}`),
		tuResp("2", "record_answer", `{"key":"response_format","value":"json"}`),
		tuResp("3", "record_answer", `{"key":"timezone","value":"UTC"}`),
		tuResp("4", "record_answer", `{"key":"port","value":"8080"}`),
		tuResp("5", "record_answer", `{"key":"route","value":"/time"}`),
		tuResp("6", "preview_spec", `{}`),
		tuResp("6b", "approve_assumptions", `{}`), // developer confirmed the previewed assumptions
		tuResp("7", "ratify_spec", `{}`),
		tuResp("8", "build_service", `{}`),
		endTurn("Built and proven."),
	}}
	agent := NewOrionAgent(prov, oc, RoleTemplate{Project: "demo"})

	var updates []acp.Update
	_, err := agent.Prompt(context.Background(), "s1", "build a time service",
		func(u acp.Update) { updates = append(updates, u) },
		func(acp.PermissionRequest) (acp.PermissionResult, error) { return acp.PermissionResult{}, nil })
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if sv, _ := oc.SpecView(context.Background()); sv.Status != "accepted" {
		t.Fatalf("spec not accepted: %q", sv.Status)
	}
	var sawBuild bool
	for _, u := range updates {
		if u.Kind == "tool_call" && strings.Contains(u.Text, "build_service") {
			sawBuild = true
		}
	}
	if !sawBuild {
		t.Fatalf("agent did not chain build_service after ratify: %+v", updates)
	}
}

// TestOrionAgentCapturesRequirementThenRatifies: the dead-end is gone — the agent
// records conditional tz behavior via add_requirement (record_answer can't hold it),
// so the spec is COMPLETE and ratifies with all the cases the developer asked for.
func TestOrionAgentCapturesRequirementThenRatifies(t *testing.T) {
	oc := orchestrator.NewWithStore(openStore(t))
	tzReq := `{"text":"tz query param","cases":[` +
		`{"request":{"method":"GET","path":"/time","query":{"tz":"America/New_York"}},"expect":{"status":200,"content_type":"application/json","assertions":[{"kind":"json_key_in_tz","key":"time","value":"America/New_York"}]}},` +
		`{"request":{"method":"GET","path":"/time","query":{"tz":"Bogus"}},"expect":{"status":400,"content_type":"application/json","assertions":[{"kind":"json_error_present"}]}}` +
		`]}`
	prov := &fakeLLM{resp: []*llm.ChatResponse{
		tuResp("1", "submit_intent", `{"intent":"build a time service with tz support"}`),
		tuResp("2", "record_answer", `{"key":"response_format","value":"json"}`),
		tuResp("3", "record_answer", `{"key":"timezone","value":"UTC"}`),
		tuResp("4", "record_answer", `{"key":"port","value":"8080"}`),
		tuResp("5", "record_answer", `{"key":"route","value":"/time"}`),
		tuResp("6", "add_requirement", tzReq),
		tuResp("7", "preview_spec", `{}`),
		tuResp("7b", "approve_assumptions", `{}`), // developer confirmed the previewed assumptions
		tuResp("8", "ratify_spec", `{}`),
		endTurn("Ratified with the tz cases ✓"),
	}}
	agent := NewOrionAgent(prov, oc, RoleTemplate{Project: "demo"})

	_, err := agent.Prompt(context.Background(), "s1", "time service with tz",
		func(acp.Update) {}, func(acp.PermissionRequest) (acp.PermissionResult, error) { return acp.PermissionResult{}, nil })
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	es, err := oc.RecallSpec(context.Background()) // accepted + anchor-verified
	if err != nil {
		t.Fatalf("recall (spec should be ratified): %v", err)
	}
	// default case + the 2 tz cases = 3 — the feature is IN the contract.
	if len(es.ResponseContract.Cases) != 3 {
		t.Fatalf("contract has %d cases, want 3 (default + 2 tz) — the requirement was dropped", len(es.ResponseContract.Cases))
	}
}

// TestOrionAgentRejectsBadDecisionKey: a hallucinated decision key is rejected by
// the tool (the gate), not silently written — the model gets an error tool_result
// it can recover from, and the spec never ratifies.
func TestOrionAgentRejectsBadDecisionKey(t *testing.T) {
	oc := orchestrator.NewWithStore(openStore(t))
	prov := &fakeLLM{resp: []*llm.ChatResponse{
		tuResp("1", "submit_intent", `{"intent":"build a time service"}`),
		tuResp("2", "record_answer", `{"key":"made_up_key","value":"x"}`),
		endTurn("Hmm, that key was rejected."),
	}}
	agent := NewOrionAgent(prov, oc, RoleTemplate{Project: "demo"})
	_, err := agent.Prompt(context.Background(), "s1", "build a time service",
		func(acp.Update) {}, func(acp.PermissionRequest) (acp.PermissionResult, error) { return acp.PermissionResult{}, nil })
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	sv, _ := oc.SpecView(context.Background())
	if sv.Status == "accepted" {
		t.Fatal("spec must not be accepted after a rejected key")
	}
}
