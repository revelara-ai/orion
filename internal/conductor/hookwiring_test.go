package conductor

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/hookbus"
	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/tools"
)

// or-ykz.2 wiring: a package on the Default bus reaches BOTH extension
// points — the generation registry's dispatch and the system prompt.
func TestHookBusWiredIntoRegistryAndPrompt(t *testing.T) {
	unregister, err := hookbus.Default.Register(hookbus.Hook{
		Name:   "wiring-test",
		Domain: hookbus.DomainGeneration,
		BeforeToolCall: func(tool string, input json.RawMessage) hookbus.ToolCallDecision {
			if tool == "victim" {
				return hookbus.ToolCallDecision{Block: true, Reason: "wiring-test: blocked"}
			}
			return hookbus.ToolCallDecision{Input: input}
		},
		PromptAppend: func() string { return "## wiring-test marker section" },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer unregister()

	// Registry path: a blocked dispatch returns the package's reason as a
	// handled tool error and the tool never runs.
	ran := false
	r := tools.NewRegistry()
	r.SetIntercept(hookbus.Default.BeforeToolCall)
	r.Register(tools.Tool{Name: "victim", Run: func(context.Context, json.RawMessage) (string, error) {
		ran = true
		return "ok", nil
	}})
	out, isErr := r.Dispatch(context.Background(), "victim", json.RawMessage(`{}`))
	if !isErr || !strings.Contains(out, "wiring-test: blocked") || ran {
		t.Fatalf("dispatch must honor the block: out=%q isErr=%v ran=%v", out, isErr, ran)
	}

	// Prompt path: the appended section rides the agent's system prompt.
	oc := orchestrator.NewWithStore(openStore(t))
	agent := NewOrionAgent(&fakeLLM{resp: nil}, oc, RoleTemplate{Project: "demo"})
	if sp := agent.systemPrompt(); !strings.Contains(sp, "wiring-test marker section") {
		t.Fatal("prompt append did not reach the system prompt")
	}
}

// or-ykz.17 follow-through: web_fetch (the fetch tool that existed all along)
// now consults the versioned SSRF guard — private ranges refuse, not just
// link-local/metadata.
func TestWebFetchRefusesPrivateRanges(t *testing.T) {
	httpc := &http.Client{Timeout: 2 * time.Second}
	for _, u := range []string{"http://10.0.0.7/x", "http://192.168.1.1/", "http://127.0.0.1:9/"} {
		if _, err := fetchURL(context.Background(), httpc, u); err == nil || !strings.Contains(err.Error(), "SSRF guard") {
			t.Fatalf("%s must be refused by the SSRF guard: %v", u, err)
		}
	}
}
