package agent

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func testWorkspace(t *testing.T) WorkspaceConfig {
	t.Helper()
	root := t.TempDir()
	adr := filepath.Join(root, "docs", "adr")
	return WorkspaceConfig{
		WorkspaceRoot:    root,
		IneligiblePaths:  []string{"vendor/*", "*.lock"},
		WriteableLabels:  []string{"orion:active", "orion:done"},
		CommandWhitelist: []string{"go", "make"},
		ADRRoot:          adr[len(root)+1:],
		IssueExternalID:  "gh#42",
	}
}

func TestRegistry_RegisterRejectsDuplicateName(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(ApplyPatchTool{Cfg: testWorkspace(t)}); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := r.Register(ApplyPatchTool{Cfg: testWorkspace(t)}); err == nil {
		t.Fatal("expected duplicate name to error")
	}
}

func TestApplyPatchTool_RejectsPathEscape(t *testing.T) {
	tool := ApplyPatchTool{Cfg: testWorkspace(t)}
	res := tool.Execute(context.Background(), map[string]any{
		"path":  "../etc/passwd",
		"patch": "hello",
	})
	if res.Status != ToolRejected {
		t.Errorf("expected rejection of path escape; got status=%q reason=%q", res.Status, res.RejectReason)
	}
}

func TestApplyPatchTool_RejectsIneligiblePath(t *testing.T) {
	tool := ApplyPatchTool{Cfg: testWorkspace(t)}
	res := tool.Execute(context.Background(), map[string]any{
		"path":  "vendor/foo.go",
		"patch": "x",
	})
	if res.Status != ToolRejected || !strings.Contains(res.RejectReason, "ineligible") {
		t.Errorf("expected ineligible_pattern rejection; got %+v", res)
	}
}

func TestApplyPatchTool_RejectsOrionIgnoreAnnotation(t *testing.T) {
	tool := ApplyPatchTool{Cfg: testWorkspace(t)}
	res := tool.Execute(context.Background(), map[string]any{
		"path":  "src/main.go",
		"patch": "func main() {\n  // orion:ignore\n}\n",
	})
	if res.Status != ToolRejected || !strings.Contains(res.RejectReason, "orion:ignore") {
		t.Errorf("expected orion:ignore rejection; got %+v", res)
	}
}

func TestApplyPatchTool_AcceptsInScopePath(t *testing.T) {
	tool := ApplyPatchTool{Cfg: testWorkspace(t)}
	res := tool.Execute(context.Background(), map[string]any{
		"path":  "src/main.go",
		"patch": "func main() {}\n",
	})
	if res.Status != ToolAccepted {
		t.Errorf("expected accepted; got %+v", res)
	}
}

func TestRunCommandTool_RejectsNonWhitelistedCommand(t *testing.T) {
	tool := RunCommandTool{Cfg: testWorkspace(t)}
	res := tool.Execute(context.Background(), map[string]any{"command": "curl"})
	if res.Status != ToolRejected || !strings.Contains(res.RejectReason, "whitelist") {
		t.Errorf("expected whitelist rejection; got %+v", res)
	}
}

func TestRunCommandTool_AcceptsWhitelistedCommand(t *testing.T) {
	tool := RunCommandTool{Cfg: testWorkspace(t)}
	res := tool.Execute(context.Background(), map[string]any{"command": "go"})
	if res.Status != ToolAccepted {
		t.Errorf("expected accepted; got %+v", res)
	}
}

func TestReadFileTool_RejectsPathEscape(t *testing.T) {
	tool := ReadFileTool{Cfg: testWorkspace(t)}
	res := tool.Execute(context.Background(), map[string]any{"path": "../../../etc/passwd"})
	if res.Status != ToolRejected {
		t.Errorf("expected rejection; got %+v", res)
	}
}

func TestTrackerCommentTool_RejectsCrossIssueTarget(t *testing.T) {
	tool := TrackerCommentTool{Cfg: testWorkspace(t), Commenter: fakeCommenter{}}
	res := tool.Execute(context.Background(), map[string]any{
		"target": "gh#999",
		"body":   "hi",
	})
	if res.Status != ToolRejected || !strings.Contains(res.RejectReason, "not the claimed issue") {
		t.Errorf("expected cross-issue rejection; got %+v", res)
	}
}

func TestTrackerCommentTool_RejectsCredentialsInBody(t *testing.T) {
	tool := TrackerCommentTool{Cfg: testWorkspace(t), Commenter: fakeCommenter{}}
	res := tool.Execute(context.Background(), map[string]any{
		"target": "gh#42",
		"body":   "Bearer abcdef0123456789",
	})
	if res.Status != ToolRejected || !strings.Contains(res.RejectReason, "credentials") {
		t.Errorf("expected credentials rejection; got %+v", res)
	}
}

func TestTrackerCommentTool_AcceptsCleanBody(t *testing.T) {
	tool := TrackerCommentTool{Cfg: testWorkspace(t), Commenter: fakeCommenter{}}
	res := tool.Execute(context.Background(), map[string]any{
		"target": "gh#42",
		"body":   "Orion proposed a patch; see attached ADR.",
	})
	if res.Status != ToolAccepted {
		t.Errorf("expected accepted; got %+v", res)
	}
}

func TestTrackerLabelTool_RejectsNonWhitelistedLabel(t *testing.T) {
	tool := TrackerLabelTool{Cfg: testWorkspace(t), Labeller: fakeLabeller{}}
	res := tool.Execute(context.Background(), map[string]any{
		"target": "gh#42",
		"add":    []string{"random-label"},
	})
	if res.Status != ToolRejected || !strings.Contains(res.RejectReason, "writeable_labels") {
		t.Errorf("expected label-whitelist rejection; got %+v", res)
	}
}

func TestTrackerLabelTool_AcceptsWhitelistedLabel(t *testing.T) {
	tool := TrackerLabelTool{Cfg: testWorkspace(t), Labeller: fakeLabeller{}}
	res := tool.Execute(context.Background(), map[string]any{
		"target": "gh#42",
		"add":    []string{"orion:active"},
	})
	if res.Status != ToolAccepted {
		t.Errorf("expected accepted; got %+v", res)
	}
}

func TestCreateADRTool_RejectsOutsideADRRoot(t *testing.T) {
	cfg := testWorkspace(t)
	tool := CreateADRTool{Cfg: cfg}
	res := tool.Execute(context.Background(), map[string]any{
		"path":    "src/not_an_adr.md",
		"content": "x",
	})
	if res.Status != ToolRejected || !strings.Contains(res.RejectReason, "ADR root") {
		t.Errorf("expected ADR-root rejection; got %+v", res)
	}
}

func TestCreateADRTool_AcceptsInADRRoot(t *testing.T) {
	cfg := testWorkspace(t)
	tool := CreateADRTool{Cfg: cfg}
	res := tool.Execute(context.Background(), map[string]any{
		"path":    filepath.Join(cfg.ADRRoot, "0001-first.md"),
		"content": "# ADR",
		"append":  false,
	})
	if res.Status != ToolAccepted {
		t.Errorf("expected accepted; got %+v", res)
	}
}

func TestSubmitPatchForVerificationTool_DispatchesToVerifier(t *testing.T) {
	cfg := testWorkspace(t)
	tool := SubmitPatchForVerificationTool{Cfg: cfg, Verifier: fakeVerifier{accept: true}}
	res := tool.Execute(context.Background(), map[string]any{
		"path":  "src/main.go",
		"patch": "x",
	})
	if res.Status != ToolAccepted {
		t.Errorf("expected accepted; got %+v", res)
	}
}

func TestSubmitPatchForVerificationTool_RejectionPropagates(t *testing.T) {
	cfg := testWorkspace(t)
	tool := SubmitPatchForVerificationTool{Cfg: cfg, Verifier: fakeVerifier{accept: false, reason: "fixture"}}
	res := tool.Execute(context.Background(), map[string]any{
		"path":  "src/main.go",
		"patch": "x",
	})
	if res.Status != ToolRejected || res.RejectReason != "fixture" {
		t.Errorf("expected fixture rejection; got %+v", res)
	}
}

func TestQueryRunSnapshotTool_ReadsFromSnapshot(t *testing.T) {
	tool := QueryRunSnapshotTool{Snapshots: fakeSnapshots{"controls": "snap-data"}, RunID: "r1"}
	res := tool.Execute(context.Background(), map[string]any{"key": "controls"})
	if res.Status != ToolAccepted {
		t.Errorf("expected accepted; got %+v", res)
	}
	if res.Result["found"] != true {
		t.Errorf("expected found=true; got %+v", res.Result)
	}
}

func TestQueryRunSnapshotTool_MissingKeyIsNotFound(t *testing.T) {
	tool := QueryRunSnapshotTool{Snapshots: fakeSnapshots{}, RunID: "r1"}
	res := tool.Execute(context.Background(), map[string]any{"key": "nope"})
	if res.Status != ToolAccepted || res.Result["found"] != false {
		t.Errorf("expected accepted with found=false; got %+v", res)
	}
}

// --- fakes ---

type fakeCommenter struct{}

func (fakeCommenter) Comment(_ context.Context, _, _ string) error { return nil }

type fakeLabeller struct{}

func (fakeLabeller) Label(_ context.Context, _ string, _, _ []string) error { return nil }

type fakeVerifier struct {
	accept bool
	reason string
	err    error
}

func (v fakeVerifier) Accept(_ context.Context, _, _ string) (bool, string, error) {
	return v.accept, v.reason, v.err
}

type fakeSnapshots map[string]any

func (s fakeSnapshots) Snapshot(_ context.Context, _ string) (map[string]any, error) {
	if s == nil {
		return nil, errors.New("nil snapshot")
	}
	return s, nil
}
