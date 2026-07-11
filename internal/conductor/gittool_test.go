package conductor

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
)

// TestGitToolRegistered: the conductor exposes a general git tool (Destructive) with an args schema.
func TestGitToolRegistered(t *testing.T) {
	r := specTools(orchestrator.NewWithStore(openStore(t)), nil, &changeSession{}, nil)
	tool, ok := r.Get("git")
	if !ok {
		t.Fatal("git tool is not registered")
	}
	if !tool.Safety.Destructive {
		t.Error("git should be Destructive (it can mutate the repo)")
	}
	if !strings.Contains(string(tool.InputSchema), "args") {
		t.Error("git schema should declare args")
	}
}

// TestGitToolRunsInRepo: the tool runs git in the developer's repo and reports exit 0 + output.
func TestGitToolRunsInRepo(t *testing.T) {
	oc := orchestrator.NewWithStore(openStore(t))
	repo := initDogfoodRepo(t)
	t.Chdir(repo)
	tool, _ := specTools(oc, nil, &changeSession{}, nil).Get("git")
	out, err := tool.Run(context.Background(), json.RawMessage(`{"args":["rev-parse","--show-toplevel"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "exit 0") {
		t.Errorf("rev-parse should exit 0, got: %s", out)
	}
}

// TestGitToolReportsFailureAsExitNotError: a failed git op (a non-fast-forward / unknown branch
// merge) comes back as readable output with a non-zero exit — NOT a Go error — so the brain can
// read it and re-prove rather than the tool blowing up.
func TestGitToolReportsFailureAsExitNotError(t *testing.T) {
	oc := orchestrator.NewWithStore(openStore(t))
	repo := initDogfoodRepo(t)
	t.Chdir(repo)
	tool, _ := specTools(oc, nil, &changeSession{}, nil).Get("git")
	out, err := tool.Run(context.Background(), json.RawMessage(`{"args":["merge","--ff-only","orion-does-not-exist"]}`))
	if err != nil {
		t.Fatalf("a failed git op must be reported as output, not a Go error: %v", err)
	}
	if strings.Contains(out, "exit 0") {
		t.Errorf("a failed merge must report a non-zero exit, got: %s", out)
	}
}
