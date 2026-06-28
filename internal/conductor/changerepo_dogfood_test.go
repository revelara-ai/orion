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
	"github.com/revelara-ai/orion/internal/proof/newbehavior"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// This is the end-to-end DOGFOOD of the user's exact case: prove + deliver a behavior-less
// tooling change (a v2 .golangci.yml enabling staticcheck with archive/ excluded + a Makefile
// with lint/vet targets) through the REAL ChangeAndProve pipeline — worktree → regression gate
// (do-no-harm) → verify oracle → commit-iff-both-hold — with a deterministic stub generator in
// place of the live model (no API key, no cost, no external call). The corruption variants prove
// the gate BITES: a plugin config or a mis-wired Makefile must come back NOT committed.

// the v2 config that golangci-lint config verify accepts (linters.exclusions.paths is the v2 way;
// issues.exclude-dirs is the rejected v1 spelling).
const dogfoodGolangci = `version: "2"
linters:
  enable:
    - staticcheck
  exclusions:
    paths:
      - archive/
`

const dogfoodMakefile = ".PHONY: lint vet\nlint:\n\tgolangci-lint run ./...\nvet:\n\tgo vet ./...\n"

// dogfoodCases is the ratified verify oracle (the same set authored for `orion change --cases`):
// the config is valid+curated (executed), and the artifacts are wired (static file assertions).
func dogfoodCases() []newbehavior.Case {
	mk := func(v *newbehavior.VerifyCommand) newbehavior.Case {
		return newbehavior.Case{Modality: "verify_command", Verify: v}
	}
	return []newbehavior.Case{
		mk(&newbehavior.VerifyCommand{
			Tool: "golangci-lint", Args: []string{"config", "verify", "--config", ".orion-golangci.yml"},
			MustExitZero: true, CurateGolangci: true, ConfigValidates: true,
			ConfigFailRE: "(can't load|cannot load|unknown linter|unsupported version|invalid config|failed to)",
		}),
		mk(&newbehavior.VerifyCommand{Tool: "file", Args: []string{".golangci.yml"}, ConfigOKRE: "staticcheck"}),
		mk(&newbehavior.VerifyCommand{Tool: "file", Args: []string{".golangci.yml"}, ConfigOKRE: "archive"}),
		mk(&newbehavior.VerifyCommand{Tool: "file", Args: []string{"Makefile"}, ConfigOKRE: `(?ms)^lint:.*golangci-lint`}),
		mk(&newbehavior.VerifyCommand{Tool: "file", Args: []string{"Makefile"}, ConfigOKRE: `(?ms)^vet:.*go vet`}),
	}
}

// stubGen is a deterministic llm.Provider: on the first turn it emits a write_file tool call per
// file; once the harness feeds the tool_results back, it ends the turn. This replaces the live
// model in DiffGenerator so the FULL ChangeAndProve pipeline runs offline.
type stubGen struct{ files map[string]string }

func (s *stubGen) Name() string                                    { return "stub" }
func (s *stubGen) Models(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (s *stubGen) Ping(context.Context) error                      { return nil }
func (s *stubGen) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	return s.respond(req), nil
}
func (s *stubGen) ChatStream(_ context.Context, req llm.ChatRequest, _ func(string)) (*llm.ChatResponse, error) {
	return s.respond(req), nil
}
func (s *stubGen) respond(req llm.ChatRequest) *llm.ChatResponse {
	if len(req.Messages) > 0 { // already wrote the files (tool_results came back) → end the turn
		for _, b := range req.Messages[len(req.Messages)-1].Content {
			if b.Type == llm.BlockToolResult {
				return &llm.ChatResponse{StopReason: llm.StopEndTurn,
					Content: []llm.ContentBlock{{Type: llm.BlockText, Text: "change complete"}}}
			}
		}
	}
	var blocks []llm.ContentBlock
	i := 0
	for path, content := range s.files {
		in, _ := json.Marshal(map[string]string{"path": path, "content": content})
		blocks = append(blocks, llm.ContentBlock{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{
			ID: fmt.Sprintf("w%d", i), Name: "write_file", Input: in}})
		i++
	}
	return &llm.ChatResponse{StopReason: llm.StopToolUse, Content: blocks}
}

func requireSandboxAndLint(t *testing.T) {
	t.Helper()
	if _, err := sandbox.New("bwrap"); err != nil {
		t.Skipf("bwrap unavailable: %v", err)
	}
	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("golangci-lint not on PATH")
	}
}

// initDogfoodRepo makes a throwaway git repo with a tiny, green Go module (so the regression gate
// has a real green-before → green-after to assert), committed at HEAD.
func initDogfoodRepo(t *testing.T) string {
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
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.com")
	dogWrite(t, filepath.Join(dir, "go.mod"), "module dogfood\n\ngo 1.23\n")
	dogWrite(t, filepath.Join(dir, "calc.go"), "package dogfood\n\n// Add adds two ints.\nfunc Add(a, b int) int { return a + b }\n")
	dogWrite(t, filepath.Join(dir, "calc_test.go"), "package dogfood\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
	git("add", "-A")
	git("-c", "commit.gpgsign=false", "commit", "-q", "-m", "init")
	return dir
}

func dogWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestChangeRepoDogfood_ToolingChange: the happy path — the change is generated, preserves
// existing behavior, satisfies the verify oracle, and is COMMITTED on a review branch.
func TestChangeRepoDogfood_ToolingChange(t *testing.T) {
	requireSandboxAndLint(t)
	repo := initDogfoodRepo(t)
	stub := &stubGen{files: map[string]string{".golangci.yml": dogfoodGolangci, "Makefile": dogfoodMakefile}}

	res, err := ChangeAndProve(context.Background(), repo, nil, stub,
		"add a golangci-lint config (v2, enable staticcheck, exclude archive/) and Makefile lint+vet targets",
		dogfoodCases())
	if err != nil {
		t.Fatalf("ChangeAndProve: %v", err)
	}
	if !res.Regression.Held {
		t.Errorf("regression must hold (a tooling change touches no Go behavior): %+v", res.Regression)
	}
	if res.NewBehavior == nil || !res.NewBehavior.Pass {
		t.Errorf("verify oracle must pass on the real artifacts: %+v", res.NewBehavior)
	}
	if !res.Committed {
		t.Fatalf("an honest, proven tooling change must be COMMITTED; got NOT committed: %s", res.Reason)
	}
	out, _ := exec.Command("git", "-C", repo, "log", "--oneline", res.Branch).CombinedOutput()
	if !strings.Contains(string(out), "orion:") {
		t.Errorf("expected an orion commit on review branch %s, got:\n%s", res.Branch, out)
	}
	files := strings.Join(res.FilesChanged, ",")
	if !strings.Contains(files, ".golangci.yml") || !strings.Contains(files, "Makefile") {
		t.Errorf("expected both artifacts changed, got: %s", files)
	}
	// Commit hygiene: the committed tree carries the change, NOT the verify sandbox scratch.
	tree, _ := exec.Command("git", "-C", repo, "ls-tree", "-r", "--name-only", res.Branch).CombinedOutput()
	for _, want := range []string{".golangci.yml", "Makefile"} {
		if !strings.Contains(string(tree), want) {
			t.Errorf("committed tree should contain %s, got:\n%s", want, tree)
		}
	}
	for _, junk := range []string{".orion-golangci.yml", ".orion-gocache", ".orion-gopath", ".config"} {
		if strings.Contains(string(tree), junk) {
			t.Errorf("committed tree must NOT contain sandbox scratch %q:\n%s", junk, tree)
		}
	}
}

// TestChangeRepoDogfood_BlastScope: the same change under ORION_REGRESSION_SCOPE=blast — the path
// the real `orion change` takes on a big repo (Orion-on-Orion). A non-Go change → empty blast
// radius → regression holds vacuously → still COMMITTED. Validates the real-world fast path.
func TestChangeRepoDogfood_BlastScope(t *testing.T) {
	requireSandboxAndLint(t)
	t.Setenv("ORION_REGRESSION_SCOPE", "blast")
	repo := initDogfoodRepo(t)
	stub := &stubGen{files: map[string]string{".golangci.yml": dogfoodGolangci, "Makefile": dogfoodMakefile}}

	res, err := ChangeAndProve(context.Background(), repo, nil, stub, "tooling change under blast scope", dogfoodCases())
	if err != nil {
		t.Fatalf("ChangeAndProve (blast): %v", err)
	}
	if !res.Committed {
		t.Fatalf("a proven tooling change must commit under blast scope too; got: %s", res.Reason)
	}
}

// TestChangeRepoDogfood_RejectsPluginConfig: a generated config declaring a plugin must come back
// NOT committed — curation rejects it before the verifier runs (the gate BITES on a hostile config).
func TestChangeRepoDogfood_RejectsPluginConfig(t *testing.T) {
	requireSandboxAndLint(t)
	repo := initDogfoodRepo(t)
	evil := "version: \"2\"\nlinters:\n  enable:\n    - staticcheck\nlinters-settings:\n  custom:\n    evil:\n      path: ./evil.so\n"
	stub := &stubGen{files: map[string]string{".golangci.yml": evil, "Makefile": dogfoodMakefile}}

	res, err := ChangeAndProve(context.Background(), repo, nil, stub, "tooling change with a hostile config", dogfoodCases())
	if err != nil {
		t.Fatalf("ChangeAndProve: %v", err)
	}
	if res.Committed {
		t.Fatal("a config declaring a plugin/custom-linter MUST NOT be committed (curation must reject it)")
	}
	if res.NewBehavior != nil && res.NewBehavior.Pass {
		t.Error("the verify oracle must not pass on a plugin config")
	}
	// The failure transcript must explain WHY (diagnosable, not silent) — P1.
	if res.NewBehavior == nil || !strings.Contains(strings.ToLower(res.NewBehavior.Output), "reject") {
		t.Errorf("failure transcript should explain the plugin rejection, got: %q", res.NewBehavior.Output)
	}
}

// TestChangeRepoDogfood_RejectsMiswiredMakefile: a Makefile missing the asked-for targets must
// come back NOT committed — the static file assertion fails (the gate BITES on incomplete work).
func TestChangeRepoDogfood_RejectsMiswiredMakefile(t *testing.T) {
	requireSandboxAndLint(t)
	repo := initDogfoodRepo(t)
	badMake := ".PHONY: build\nbuild:\n\tgo build ./...\n" // no lint:/vet:
	stub := &stubGen{files: map[string]string{".golangci.yml": dogfoodGolangci, "Makefile": badMake}}

	res, err := ChangeAndProve(context.Background(), repo, nil, stub, "tooling change with a mis-wired Makefile", dogfoodCases())
	if err != nil {
		t.Fatalf("ChangeAndProve: %v", err)
	}
	if res.Committed {
		t.Fatal("a Makefile missing the lint:/vet: targets MUST NOT be committed (file assertion must fail)")
	}
	if res.NewBehavior == nil || res.NewBehavior.Pass {
		t.Error("the verify oracle must NOT pass when the Makefile is mis-wired")
	}
	// The transcript must name the failing file assertion — P1.
	if res.NewBehavior == nil || !strings.Contains(res.NewBehavior.Output, "passed=false") {
		t.Errorf("failure transcript should show the failing file assertion, got: %q", res.NewBehavior.Output)
	}
}
