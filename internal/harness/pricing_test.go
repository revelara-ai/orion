package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/revelara-ai/orion/internal/budget"
	"github.com/revelara-ai/orion/internal/tools"
	"github.com/revelara-ai/orion/pkg/llm"
)

// pricedProvider reports a priced model and heavy usage per turn.
type pricedProvider struct {
	scriptedProvider
	name string
}

func (p *pricedProvider) Name() string { return p.name }

// TestDollarsCeilingHaltsPricedLoop (or-v9f.28 DONE-WHEN a): with a real
// pricing entry, ORION_BUDGET_MAX_DOLLARS crosses and the loop halts — the
// dead dollars axis is live again.
func TestDollarsCeilingHaltsPricedLoop(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(tools.Tool{
		Name: "probe", Description: "p", InputSchema: json.RawMessage(`{"type":"object"}`),
		Safety: tools.Safety{ReadOnly: true},
		Run:    func(context.Context, json.RawMessage) (string, error) { return "ok", nil },
	})
	n := 0
	p := &pricedProvider{name: "anthropic"}
	p.next = func() *llm.ChatResponse {
		n++
		r := toolUseResp(fmt.Sprintf("id%d", n), "probe", fmt.Sprintf(`{"n":%d}`, n))
		r.Model = "claude-opus-9"
		r.Usage = llm.Usage{InputTokens: 500_000, OutputTokens: 100_000} // ≈ $15 per turn
		return r
	}
	acct := budget.NewWithCeiling(budget.Ceiling{MaxDollars: 20})
	l := &Loop{Provider: p, Tools: reg, System: "t", Role: "generator",
		Supervisor: Supervisor{MaxIterations: 50, Budget: acct}}
	_, _, err := l.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil)
	if !errors.Is(err, ErrBudgetHalt) {
		t.Fatalf("a priced loop crossing MaxDollars must halt: %v (turns=%d)", err, n)
	}
	snap := acct.Snapshot()
	if snap.Dollars < 20 {
		t.Fatalf("dollars must be REAL, got $%.2f", snap.Dollars)
	}
	rs := snap.ByRole["generator"]
	if rs.Dollars <= 0 || rs.Model != "claude-opus-9" || rs.Unpriced {
		t.Fatalf("spend must attribute to the role+model, priced: %+v", rs)
	}
}

// TestUnpricedModelBooksTokensOnly: an unknown/local model books tokens with
// the UNPRICED flag — never a silent $0 pretending to be free.
func TestUnpricedModelBooksTokensOnly(t *testing.T) {
	acct := budget.New()
	p := &pricedProvider{name: "lmstudio"}
	resp := endResp("done")
	resp.Model = "qwen-local"
	resp.Usage = llm.Usage{InputTokens: 1000, OutputTokens: 200}
	p.resp = []*llm.ChatResponse{resp}
	l := &Loop{Provider: p, Tools: tools.NewRegistry(), System: "t", Role: "generator",
		Supervisor: Supervisor{MaxIterations: 5, Budget: acct}}
	if _, _, err := l.Run(context.Background(), []llm.Message{llm.TextMessage(llm.RoleUser, "go")}, nil); err != nil {
		t.Fatal(err)
	}
	rs := acct.Snapshot().ByRole["generator"]
	if rs.Tokens != 1200 || rs.Dollars != 0 || !rs.Unpriced {
		t.Fatalf("unknown model → tokens counted, $0, UNPRICED flag: %+v", rs)
	}
}
