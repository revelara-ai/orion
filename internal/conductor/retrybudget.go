package conductor

import (
	"context"
	"os"
	"strconv"
	"sync"

	"github.com/revelara-ai/orion/pkg/llmclient"
)

// defaultRetryBudget: total retries ONE operation (a developer turn, a build
// run, a change run) may spend across every layer of the stack combined
// (or-mvr.1). Each provider call already carries its own small MaxRetries;
// this bounds their SUM so layered retries can never multiply.
const defaultRetryBudget = 20

// defaultMaxInflightLLM: process-wide ceiling on concurrent model calls
// (or-mvr.3) — coordinator inference, dispatched agents, and shadow runs all
// draw from this ONE cap (no first-party bypass lane; inc-qdi C5).
const defaultMaxInflightLLM = 4

var (
	inflightOnce sync.Once
	inflightGate *llmclient.InflightGate
)

// sharedInflightGate returns the process-wide in-flight gate, sized by
// ORION_MAX_INFLIGHT_LLM (default 4).
func sharedInflightGate() *llmclient.InflightGate {
	inflightOnce.Do(func() {
		n := defaultMaxInflightLLM
		if v, err := strconv.Atoi(os.Getenv("ORION_MAX_INFLIGHT_LLM")); err == nil && v > 0 {
			n = v
		}
		inflightGate = llmclient.NewInflightGate(n)
	})
	return inflightGate
}

// withLLMGuards installs the operation-scoped LLM guards at an operation root:
// the stack-wide retry budget (or-mvr.1) and the process-wide in-flight cap
// (or-mvr.3). An existing budget in ctx is kept (the outermost root wins, so a
// build inside a turn shares the turn's ceiling).
func withLLMGuards(ctx context.Context) context.Context {
	ctx = llmclient.WithInflightGate(ctx, sharedInflightGate())
	if llmclient.BudgetFrom(ctx) != nil {
		return ctx
	}
	n := defaultRetryBudget
	if v, err := strconv.Atoi(os.Getenv("ORION_RETRY_BUDGET")); err == nil && v >= 0 {
		n = v
	}
	return llmclient.WithRetryBudget(ctx, llmclient.NewRetryBudget(n))
}
