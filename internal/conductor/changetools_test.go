package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/llm"
	"github.com/revelara-ai/orion/internal/orchestrator"
)

// TestChangeFlowToolsRegistered: the change-spec flow is exposed to the brain, build_change Destructive.
func TestChangeFlowToolsRegistered(t *testing.T) {
	r := specTools(orchestrator.NewWithStore(openStore(t)), nil, &changeSession{}, nil)
	for _, n := range []string{"submit_change_intent", "propose_cases", "add_case", "edit_case", "ratify_cases", "build_change"} {
		if _, ok := r.Get(n); !ok {
			t.Errorf("change-flow tool %q not registered", n)
		}
	}
	if bc, _ := r.Get("build_change"); !bc.Safety.Destructive {
		t.Error("build_change should be Destructive (it commits a change)")
	}
}

// TestCaseInputGround: synth_test needs pkg+call+want and a real package; command needs assert.
func TestCaseInputGround(t *testing.T) {
	pkgs := map[string]bool{"internal/foo": true}
	ok := []caseInput{
		{Modality: "synth_test", Pkg: "internal/foo", Call: "Add(1,2)", Want: "3"},
		{Modality: "command", Assert: []string{"./bin", "--check"}},
	}
	for _, ci := range ok {
		if _, why := ci.ground(pkgs); why != "" {
			t.Errorf("expected %+v to ground, got %q", ci, why)
		}
	}
	bad := []caseInput{
		{Modality: "synth_test", Pkg: "internal/missing", Call: "X()", Want: "1"}, // pkg absent
		{Modality: "synth_test", Pkg: "internal/foo", Call: "X()"},                // no want
		{Modality: "command"},                                                     // no assert
		{Modality: "bogus"},                                                       // unknown modality
	}
	for _, ci := range bad {
		if _, why := ci.ground(pkgs); why == "" {
			t.Errorf("expected %+v to be rejected", ci)
		}
	}
}

// changeStub is a deterministic llm.Provider: Chat answers propose_cases (the coordinator) with
// canned cases; ChatStream answers DiffGenerator with one write_file then ends. Lets the whole
// change-spec flow run offline.
type changeStub struct {
	cases    []caseInput       // what propose_cases returns
	genFiles map[string]string // what DiffGenerator writes
}

func (s *changeStub) Name() string                                    { return "changestub" }
func (s *changeStub) Models(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (s *changeStub) Ping(context.Context) error                      { return nil }

func (s *changeStub) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	// propose_cases coordinator call.
	for _, tl := range req.Tools {
		if tl.Name == "propose_behavioral_cases" {
			in, _ := json.Marshal(map[string]any{"cases": s.cases})
			return &llm.ChatResponse{StopReason: llm.StopToolUse, Content: []llm.ContentBlock{{
				Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{ID: "c0", Name: "propose_behavioral_cases", Input: in},
			}}}, nil
		}
	}
	return &llm.ChatResponse{StopReason: llm.StopEndTurn}, nil
}

func (s *changeStub) ChatStream(_ context.Context, req llm.ChatRequest, _ func(string)) (*llm.ChatResponse, error) {
	// DiffGenerator loop: write the files on turn 1, end after the tool_results return.
	if len(req.Messages) > 0 {
		for _, b := range req.Messages[len(req.Messages)-1].Content {
			if b.Type == llm.BlockToolResult {
				return &llm.ChatResponse{StopReason: llm.StopEndTurn, Content: []llm.ContentBlock{{Type: llm.BlockText, Text: "done"}}}, nil
			}
		}
	}
	var blocks []llm.ContentBlock
	i := 0
	for path, content := range s.genFiles {
		in, _ := json.Marshal(map[string]string{"path": path, "content": content})
		blocks = append(blocks, llm.ContentBlock{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{
			ID: fmt.Sprintf("w%d", i), Name: "write_file", Input: in}})
		i++
	}
	return &llm.ChatResponse{StopReason: llm.StopToolUse, Content: blocks}, nil
}

// TestProposeCasesGroundsAndDrops: the coordinator's grounded cases are kept; ungrounded dropped.
func TestProposeCasesGroundsAndDrops(t *testing.T) {
	repo := initVerdictRepo(t)
	t.Chdir(repo)
	stub := &changeStub{cases: []caseInput{
		{Modality: "synth_test", Pkg: ".", Call: "Verdict{Failures: 1}.Severity()", Want: `"critical"`},
		{Modality: "synth_test", Pkg: "internal/nope", Call: "X()", Want: "1"}, // ungrounded → dropped
	}}
	cs := &changeSession{}
	r := specTools(orchestrator.NewWithStore(openStore(t)), stub, cs, nil)
	mustDispatch(t, r, "submit_change_intent", `{"intent":"add Severity()"}`)
	out := mustDispatch(t, r, "propose_cases", `{}`)
	if !strings.Contains(out, "proposed 1 case") || !strings.Contains(out, "dropped (ungrounded)") {
		t.Fatalf("expected 1 grounded + 1 dropped, got:\n%s", out)
	}
	if _, cases, _, _ := cs.snapshot(); len(cases) != 1 {
		t.Fatalf("session should hold 1 grounded case, got %d", len(cases))
	}
}

// TestBuildChangeRequiresRatify: build_change refuses before ratify_cases (the oracle gate).
func TestBuildChangeRequiresRatify(t *testing.T) {
	cs := &changeSession{intent: "x", cases: nil}
	r := specTools(orchestrator.NewWithStore(openStore(t)), &changeStub{}, cs, nil)
	bc, _ := r.Get("build_change")
	if _, err := bc.Run(context.Background(), json.RawMessage(`{}`)); err == nil {
		t.Fatal("build_change must refuse when cases are not ratified")
	}
}

// TestChangeFlowDogfoodSeverity is the §10 acceptance: the change-spec flow proves + commits a
// real behavior addition end to end — submit → propose (3 branch cases) → ratify → build_change
// (regression + synth_test proof) → COMMITTED — driven offline by changeStub.
func TestChangeFlowDogfoodSeverity(t *testing.T) {
	repo := initVerdictRepo(t)
	t.Chdir(repo)
	stub := &changeStub{
		cases: []caseInput{
			{Modality: "synth_test", Pkg: ".", Call: "Verdict{Failures: 1}.Severity()", Want: `"critical"`},
			{Modality: "synth_test", Pkg: ".", Call: "Verdict{Warnings: 1}.Severity()", Want: `"warn"`},
			{Modality: "synth_test", Pkg: ".", Call: "Verdict{}.Severity()", Want: `"ok"`},
		},
		genFiles: map[string]string{"verdict.go": verdictWithSeverity},
	}
	cs := &changeSession{}
	r := specTools(orchestrator.NewWithStore(openStore(t)), stub, cs, nil)
	mustDispatch(t, r, "submit_change_intent", `{"intent":"add a Severity() method to Verdict returning critical|warn|ok"}`)
	mustDispatch(t, r, "propose_cases", `{}`)
	if _, cases, _, _ := cs.snapshot(); len(cases) != 3 {
		t.Fatalf("expected 3 grounded cases, got %d", len(cases))
	}
	mustDispatch(t, r, "ratify_cases", `{}`)
	out := mustDispatch(t, r, "build_change", `{}`)

	if !strings.Contains(out, "regression: do-no-harm held=true") {
		t.Errorf("regression should hold:\n%s", out)
	}
	if !strings.Contains(out, "verification: pass=true") {
		t.Errorf("the 3 ratified cases should pass:\n%s", out)
	}
	if !strings.Contains(out, "COMMITTED") {
		t.Fatalf("a proven behavior change must be COMMITTED:\n%s", out)
	}
}

func mustDispatch(t *testing.T, r interface {
	Dispatch(context.Context, string, json.RawMessage) (string, bool)
}, name, args string) string {
	t.Helper()
	out, isErr := r.Dispatch(context.Background(), name, json.RawMessage(args))
	if isErr {
		t.Fatalf("%s errored: %s", name, out)
	}
	return out
}

const verdictWithSeverity = `package prov

// Verdict is a dependency-provenance verdict.
type Verdict struct {
	Failures int
	Warnings int
}

// Severity classifies the verdict.
func (v Verdict) Severity() string {
	if v.Failures > 0 {
		return "critical"
	}
	if v.Warnings > 0 {
		return "warn"
	}
	return "ok"
}
`

// initVerdictRepo: a throwaway module with a Verdict type (no Severity yet) + a green baseline.
func initVerdictRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.name", "T")
	git("config", "user.email", "t@example.com")
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module prov\n\ngo 1.23\n")
	write("verdict.go", "package prov\n\n// Verdict is a dependency-provenance verdict.\ntype Verdict struct {\n\tFailures int\n\tWarnings int\n}\n")
	write("verdict_test.go", "package prov\n\nimport \"testing\"\n\nfunc TestVerdictZero(t *testing.T) {\n\tif (Verdict{}).Failures != 0 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	git("add", "-A")
	git("-c", "commit.gpgsign=false", "commit", "-q", "-m", "init")
	return dir
}

// TestProposeCasesRefusesWhenAlreadyRatified: after the oracle is ratified,
// re-drafting via propose_cases must be refused (never silently replace the
// ratified cases) — the trust gate requires the oracle to predate the diff. This
// guards against a post-compaction model re-proposing after losing that memory.
func TestProposeCasesRefusesWhenAlreadyRatified(t *testing.T) {
	cs := &changeSession{intent: "add Severity()", ratified: true}
	r := specTools(orchestrator.NewWithStore(openStore(t)), nil, cs, nil)
	pc, ok := r.Get("propose_cases")
	if !ok {
		t.Fatal("propose_cases not registered")
	}
	_, err := pc.Run(context.Background(), json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "ratified") {
		t.Fatalf("propose_cases must refuse to re-draft a ratified oracle, got err=%v", err)
	}
	if !cs.ratified {
		t.Fatal("propose_cases must not clear the ratified flag when refusing")
	}
}
