package conductor

import (
	"context"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/pkg/llm"
)

// or-3p5.4 residual: the proven change gets an ADVISORY alignment audit —
// the concern is recorded and surfaced, the commit verdict never flips, and
// the judge sees ONLY the changed surface (never the whole repo).
func TestChangeAlignmentAdvisory(t *testing.T) {
	if testing.Short() {
		t.Skip("git worktree + go test + LLM loop")
	}
	repo := gitInitGreenRepo(t)
	prov := &fakeLLM{resp: []*llm.ChatResponse{
		tuResp("1", "write_file", `{"path":"extra.go","content":"package t\n\nfunc Mul(a, b int) int { return a * b }\n"}`),
		endTurn("added Mul"),
		// The alignment judge's turn: a medium advisory concern.
		tuResp("2", "report_alignment", `{"aligned":false,"severity":"medium","concern":"multiplies but never validates inputs"}`),
	}}

	res, err := ChangeAndProve(context.Background(), repo, nil, prov, "add a Mul helper", nil, nil, nil)
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if !res.Committed || res.Delivery != "deliver" {
		t.Fatalf("an ADVISORY concern must never flip a proven commit: %+v", res)
	}
	if !res.Alignment.Ran || res.Alignment.Aligned || res.Alignment.Severity != "medium" {
		t.Fatalf("advisory audit not recorded: %+v", res.Alignment)
	}
	if !strings.Contains(res.Alignment.Concern, "validates") {
		t.Fatalf("concern text lost: %+v", res.Alignment)
	}

	// Scope: the judge's request carries the CHANGED file, not the repo.
	var judgeReq string
	for _, m := range prov.lastReq.Messages {
		for _, b := range m.Content {
			judgeReq += b.Text
		}
	}
	if !strings.Contains(judgeReq, "func Mul") {
		t.Fatal("judge never saw the changed surface")
	}
	if strings.Contains(judgeReq, "func Add(a, b int) int") {
		t.Fatal("judge saw the UNCHANGED repo source — the audit must scope to the change")
	}
}
