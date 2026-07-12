package conductor

import (
	"context"
	"os"
	"strconv"

	"github.com/revelara-ai/orion/pkg/llmclient"
)

// defaultRetryBudget: total retries ONE operation (a developer turn, a build
// run, a change run) may spend across every layer of the stack combined
// (or-mvr.1). Each provider call already carries its own small MaxRetries;
// this bounds their SUM so layered retries can never multiply.
const defaultRetryBudget = 20

// withRetryBudget installs the stack-wide retry budget at an operation root.
// Configurable via ORION_RETRY_BUDGET; an existing budget in ctx is kept (the
// outermost root wins, so a build inside a turn shares the turn's ceiling).
func withRetryBudget(ctx context.Context) context.Context {
	if llmclient.BudgetFrom(ctx) != nil {
		return ctx
	}
	n := defaultRetryBudget
	if v, err := strconv.Atoi(os.Getenv("ORION_RETRY_BUDGET")); err == nil && v >= 0 {
		n = v
	}
	return llmclient.WithRetryBudget(ctx, llmclient.NewRetryBudget(n))
}
