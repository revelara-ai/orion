// Package skilleval is the PRD-normative pre-activation regression gate
// (or-gb1.5, PRD 'Extension & Package Security' + 'Self-evolution
// lifecycle'): a self-evolved skill candidate is promoted into the
// generation catalog ONLY with passing, DETERMINISTIC eval evidence.
//
// The contract: cassette happy-path fixtures + adversarial fixtures, each
// judged by a deterministic predicate over the fixture's recorded output,
// plus a latency SLO. Per the PRD's hybrid-eval amendment an LLM rater may
// grade reasoning but may NEVER set pass/fail — the harness REJECTS
// LLM-as-judge predicate kinds at eval-definition load. FAIL CLOSED: a
// candidate with no evidence attached is not promoted.
package skilleval

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Eval is one candidate's evidence: recorded fixtures + the latency SLO.
type Eval struct {
	CandidateID  string    `json:"candidate_id"`
	Happy        []Fixture `json:"happy"`
	Adversarial  []Fixture `json:"adversarial"`
	LatencySLOMS int       `json:"latency_slo_ms"` // 0 = no SLO declared
}

// Fixture is one recorded cassette exchange: the input that was replayed,
// the output that came back, the measured latency, and the deterministic
// predicate that judges it.
type Fixture struct {
	Name      string    `json:"name"`
	Input     string    `json:"input"`
	Output    string    `json:"output"` // the recorded (cassette) output
	LatencyMS int       `json:"latency_ms"`
	Predicate Predicate `json:"predicate"`
}

// Predicate is a deterministic verdict over a fixture's recorded output.
type Predicate struct {
	Kind string `json:"kind"` // contains | not_contains | equals | min_len
	Arg  string `json:"arg"`
}

// deterministicKinds is the CLOSED predicate vocabulary. Anything else —
// llm-judge, model-graded, vibes — is rejected at load (rule 4).
var deterministicKinds = map[string]bool{
	"contains": true, "not_contains": true, "equals": true, "min_len": true,
}

// Load parses + validates an eval definition. Nondeterministic predicate
// kinds are a LOAD error, not a runtime skip.
func Load(raw []byte) (Eval, error) {
	var e Eval
	if err := json.Unmarshal(raw, &e); err != nil {
		return Eval{}, fmt.Errorf("skilleval: parse: %w", err)
	}
	if e.CandidateID == "" {
		return Eval{}, fmt.Errorf("skilleval: candidate_id is required")
	}
	if len(e.Happy) == 0 {
		return Eval{}, fmt.Errorf("skilleval: at least one happy-path fixture is required")
	}
	for _, f := range append(append([]Fixture(nil), e.Happy...), e.Adversarial...) {
		kind := strings.ToLower(strings.TrimSpace(f.Predicate.Kind))
		if !deterministicKinds[kind] {
			return Eval{}, fmt.Errorf("skilleval: predicate %q on fixture %q is not deterministic — an LLM rater may grade reasoning but may never set pass/fail (allowed: contains, not_contains, equals, min_len)", f.Predicate.Kind, f.Name)
		}
	}
	return e, nil
}

// Result is the gate's verdict for one candidate.
type Result struct {
	Pass    bool
	Failing string // the first failing predicate/fixture, named
}

// Run evaluates the evidence deterministically: every fixture's predicate
// must hold and every recorded latency must meet the SLO.
func Run(e Eval) Result {
	check := func(class string, fs []Fixture) string {
		for _, f := range fs {
			if !holds(f.Predicate, f.Output) {
				return fmt.Sprintf("%s fixture %q: predicate %s(%q) failed", class, f.Name, f.Predicate.Kind, f.Predicate.Arg)
			}
			if e.LatencySLOMS > 0 && f.LatencyMS > e.LatencySLOMS {
				return fmt.Sprintf("%s fixture %q: latency %dms exceeds the %dms SLO", class, f.Name, f.LatencyMS, e.LatencySLOMS)
			}
		}
		return ""
	}
	if why := check("happy-path", e.Happy); why != "" {
		return Result{Failing: why}
	}
	if why := check("adversarial", e.Adversarial); why != "" {
		return Result{Failing: why}
	}
	return Result{Pass: true}
}

func holds(p Predicate, output string) bool {
	switch strings.ToLower(strings.TrimSpace(p.Kind)) {
	case "contains":
		return strings.Contains(output, p.Arg)
	case "not_contains":
		return !strings.Contains(output, p.Arg)
	case "equals":
		return output == p.Arg
	case "min_len":
		n := 0
		_, _ = fmt.Sscanf(p.Arg, "%d", &n)
		return len(output) >= n
	}
	return false // unknown kinds never pass (Load already rejects them)
}
