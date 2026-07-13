package conductor

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextwindow"
	"github.com/revelara-ai/orion/internal/decomposer"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/sandbox"
	"github.com/revelara-ai/orion/pkg/llm"
)

// tinyWindowProvider reports a deliberately small context window.
type tinyWindowProvider struct{ ruleProvider }

func (tinyWindowProvider) ContextWindow() int { return 2000 }

func fitSpec(nCases int) spec.ExecutableSpec {
	es := spec.ExecutableSpec{Intent: "build a service"}
	for i := 0; i < nCases; i++ {
		es.ResponseContract.Cases = append(es.ResponseContract.Cases, spec.BehavioralCase{
			ID:      strings.Repeat("c", 3) + string(rune('a'+i%26)) + strings.Repeat("x", i%7),
			Request: spec.RequestShape{Method: "GET", Path: "/x"},
			Expect:  spec.ExpectShape{Status: 200, ContentType: "application/json"},
		})
	}
	return es
}

// TestModuleFitEstimatorBudgetsAgainstProviderWindow (or-7et.3): the estimate
// scales with the module's spec slice + file scope, and the budget is the
// fraction of the ACTIVE provider's window — a giant slice busts a small
// window, a small slice fits, and the estimate covers the refinement
// re-injection (>= 2x the single prompt).
func TestModuleFitEstimatorBudgetsAgainstProviderWindow(t *testing.T) {
	es := fitSpec(60)
	fit := NewModuleFitEstimator(tinyWindowProvider{}, es)

	var all []string
	for _, c := range es.ResponseContract.Cases {
		all = append(all, c.ID)
	}
	giant := decomposer.ProposedModule{Key: "everything", Covers: all, FileScope: strings.Repeat("srcbytes ", 2000)}
	small := decomposer.ProposedModule{Key: "ping", Covers: all[:1]}

	ge := fit(giant)
	se := fit(small)
	if ge.Budget != 1000 { // 0.5 * 2000
		t.Fatalf("budget must be the fraction of the provider window, got %d", ge.Budget)
	}
	if ge.Tokens <= ge.Budget {
		t.Fatalf("a 60-case module with a huge file scope must bust a 2k window: %+v", ge)
	}
	if se.Tokens > se.Budget {
		t.Fatalf("a one-case module must fit: %+v", se)
	}
	// Refinement re-injection: the estimate is at least twice the bare prompt.
	gs := sandboxGenSpecFor(small, es)
	bare := llm.EstimateTokens(llm.ChatRequest{System: GenerationPrompt(gs, "")})
	if se.Tokens < 2*bare {
		t.Fatalf("the estimate must cover the refinement re-injection (>=2x prompt), got %d < 2*%d", se.Tokens, bare)
	}

	// And the default-window path: the same giant module fits a 128k window.
	fitBig := NewModuleFitEstimator(ruleProvider{}, es)
	if e := fitBig(giant); e.Tokens > e.Budget {
		t.Fatalf("the same module must fit the default %d window: %+v", contextwindow.DefaultWindow, e)
	}
}

// sandboxGenSpecFor mirrors the estimator's slice construction for the test's
// re-injection arithmetic.
func sandboxGenSpecFor(m decomposer.ProposedModule, es spec.ExecutableSpec) (gs sandbox.GenSpec) {
	covered := map[string]bool{}
	for _, c := range m.Covers {
		covered[c] = true
	}
	gs.Module = "orion-generated/" + m.Key
	for _, c := range es.ResponseContract.Cases {
		if covered[c.ID] {
			gs.Cases = append(gs.Cases, c)
		}
	}
	return gs
}
