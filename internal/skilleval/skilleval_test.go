package skilleval

import (
	"strings"
	"testing"
)

func passingEval() Eval {
	return Eval{
		CandidateID: "cand1",
		Happy: []Fixture{{
			Name: "greets", Input: "hello", Output: "hello back",
			LatencyMS: 10, Predicate: Predicate{Kind: "contains", Arg: "hello"},
		}},
		Adversarial: []Fixture{{
			Name: "underspecified intent raises questions", Input: "do the thing",
			Output: `{"open_decisions": 3}`, LatencyMS: 12,
			Predicate: Predicate{Kind: "not_contains", Arg: `"open_decisions": 0`},
		}},
		LatencySLOMS: 100,
	}
}

// TestSkillEval_HappyPath (PRD-named): cassette happy-path fixtures judged by
// deterministic predicates — pass passes, a failing predicate is NAMED.
func TestSkillEval_HappyPath(t *testing.T) {
	if res := Run(passingEval()); !res.Pass {
		t.Fatalf("a passing eval must pass: %s", res.Failing)
	}
	bad := passingEval()
	bad.Happy[0].Output = "goodbye"
	res := Run(bad)
	if res.Pass || !strings.Contains(res.Failing, `happy-path fixture "greets"`) {
		t.Fatalf("the failing predicate must be named: %+v", res)
	}
}

// TestSkillEval_AdversarialInput (PRD-named): an adversarial fixture (e.g.
// underspecified intent must raise open decisions) fails deterministically.
func TestSkillEval_AdversarialInput(t *testing.T) {
	bad := passingEval()
	bad.Adversarial[0].Output = `{"open_decisions": 0}`
	res := Run(bad)
	if res.Pass || !strings.Contains(res.Failing, "adversarial fixture") {
		t.Fatalf("a failing adversarial fixture must fail with its class named: %+v", res)
	}
}

// TestSkillEvalLatencySLO: a recorded latency over the SLO fails the gate.
func TestSkillEvalLatencySLO(t *testing.T) {
	slow := passingEval()
	slow.Happy[0].LatencyMS = 500
	res := Run(slow)
	if res.Pass || !strings.Contains(res.Failing, "latency 500ms exceeds the 100ms SLO") {
		t.Fatalf("an SLO breach must fail with the numbers: %+v", res)
	}
}

// TestEvalHarnessRejectsNonDeterministicPredicate (PRD-named): an LLM-as-judge
// predicate is a LOAD error — it never reaches Run.
func TestEvalHarnessRejectsNonDeterministicPredicate(t *testing.T) {
	raw := []byte(`{"candidate_id":"c1","happy":[{"name":"h","input":"i","output":"o","predicate":{"kind":"llm-judge","arg":"is this good?"}}]}`)
	_, err := Load(raw)
	if err == nil || !strings.Contains(err.Error(), "not deterministic") {
		t.Fatalf("LLM-as-judge predicates must be rejected at load: %v", err)
	}
	// Structural requirements too: evidence without a happy path is invalid.
	if _, err := Load([]byte(`{"candidate_id":"c1"}`)); err == nil {
		t.Fatal("evidence with no happy-path fixtures must be rejected")
	}
	if _, err := Load([]byte(`{"happy":[{"name":"h","input":"i","output":"o","predicate":{"kind":"contains","arg":"o"}}]}`)); err == nil {
		t.Fatal("evidence without a candidate_id must be rejected")
	}
}
