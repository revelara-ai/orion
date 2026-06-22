package conductor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

// answerCanonicalTimeService submits + answers the blocking functional decisions
// and ratifies — leaving an accepted, anchored spec ready to build.
func ratifiedTimeService(t *testing.T) (*orchestrator.Conductor, context.Context) {
	t.Helper()
	withGitRepo(t) // builds run in their own git repo (worktree-per-cluster isolation)
	oc := orchestrator.NewWithStore(openStore(t))
	ctx := context.Background()
	if _, err := oc.Submit(ctx, "Build an HTTP service that returns the current time."); err != nil {
		t.Fatalf("submit: %v", err)
	}
	for _, a := range [][2]string{{"response_format", "json"}, {"timezone", "UTC"}, {"port", "8080"}, {"route", "/time"}} {
		if err := oc.RecordAnswer(ctx, a[0], a[1]); err != nil {
			t.Fatalf("answer %s: %v", a[0], err)
		}
	}
	// DECLARE the time behavior (the general-harness way) — the default case no longer
	// assumes a "time" key, so the time-service example must state its own contract.
	if err := oc.AddRequirement(ctx, spec.Requirement{
		Source: completeness.DimFunctional,
		Text:   `GET /time returns the current time as an RFC3339 "time" JSON key`,
		Cases: []spec.BehavioralCase{{
			Request: spec.RequestShape{Method: "GET", Path: "/time"},
			Expect: spec.ExpectShape{Status: 200, ContentType: "application/json",
				Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyRFC3339, Key: "time"}}},
		}},
	}); err != nil {
		t.Fatalf("declare time requirement: %v", err)
	}
	if _, err := oc.ApproveSpec(ctx); err != nil {
		t.Fatalf("approve: %v", err)
	}
	return oc, ctx
}

// TestBuildAndProveFixture: the one-shot pipeline builds the canonical
// time-service from the ratified spec and PROVES it green end-to-end (decompose →
// fixture generate → behavioral+empirical+hazard proof → gate → bar). This is the
// "build to the spec" guarantee the user asked for.
func TestBuildAndProveFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a service + proof; skipped in -short")
	}
	oc, ctx := ratifiedTimeService(t)

	res, err := BuildAndProve(ctx, oc.Store(), nil, nil, nil, "")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.TaskID == "" || res.Verdict == "" || res.Delivery == "" {
		t.Fatalf("pipeline did not run to completion: %+v", res)
	}
	if res.Verdict != "Accept" || !res.Closed {
		t.Fatalf("canonical fixture must prove green and close the task: %+v", res)
	}
}

// TestBuildExportsProvenCodeToRepo: on Accept, the proven code is written into the
// developer-visible output root (not just the store) — main.go + go.mod + an ORION.md
// provenance note — so the developer can open and use it in the repo they work in.
func TestBuildExportsProvenCodeToRepo(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + proves a service; skipped in -short")
	}
	oc, ctx := ratifiedTimeService(t)
	outRoot := t.TempDir() // stand-in for the developer's working repo

	res, err := BuildAndProve(ctx, oc.Store(), nil, nil, nil, outRoot)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.Verdict != "Accept" {
		t.Fatalf("fixture should prove Accept: %+v", res)
	}
	if res.OutputDir == "" || !strings.HasPrefix(res.OutputDir, outRoot) {
		t.Fatalf("proven code was not exported under the output root: OutputDir=%q root=%q", res.OutputDir, outRoot)
	}
	for _, f := range []string{"main.go", "go.mod", "ORION.md"} {
		if _, statErr := os.Stat(filepath.Join(res.OutputDir, f)); statErr != nil {
			t.Fatalf("expected %s in the exported code dir %s: %v", f, res.OutputDir, statErr)
		}
	}
	// The export must NEVER carry the proof corpus into the developer's repo.
	entries, _ := os.ReadDir(res.OutputDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "_test.go") {
			t.Fatalf("proof corpus leaked into the exported code: %s", e.Name())
		}
	}
	// The provenance note records the anchor + Accept, so the code is self-describing.
	note, _ := os.ReadFile(filepath.Join(res.OutputDir, "ORION.md"))
	if !strings.Contains(string(note), "proven") {
		t.Fatalf("ORION.md should document the proof provenance:\n%s", note)
	}
}

// TestShowCodeReportsLocationAndContent: the conductor's show_code tool answers
// "where is the code / what was produced" — it returns the on-disk path plus the
// generated source, so the agent can answer the developer truthfully.
func TestShowCodeReportsLocationAndContent(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + proves a service; skipped in -short")
	}
	oc, ctx := ratifiedTimeService(t)
	root := t.TempDir()
	t.Setenv("ORION_OUTPUT_DIR", root) // show_code + the build resolve the same OutputRoot()

	if _, err := BuildAndProve(ctx, oc.Store(), nil, nil, nil, root); err != nil {
		t.Fatalf("build: %v", err)
	}
	out, isErr := specTools(oc, nil).Dispatch(ctx, "show_code", json.RawMessage(`{}`))
	if isErr {
		t.Fatalf("show_code errored: %s", out)
	}
	if !strings.Contains(out, root) {
		t.Fatalf("show_code did not report the code location under %q:\n%s", root, out)
	}
	if !strings.Contains(out, "main.go") || !strings.Contains(out, "package main") {
		t.Fatalf("show_code did not return the generated source:\n%s", out)
	}
}

// TestBuildDAGResolvesManagedRepoNotCwd: the build no longer depends on a cwd git
// repo — it resolves Orion's managed repo under the store dir. Run from a non-git
// cwd, the build succeeds and the managed repo exists.
func TestBuildDAGResolvesManagedRepoNotCwd(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a service + proof; skipped in -short")
	}
	oc, ctx := ratifiedTimeService(t) // ratifies (chdirs into a throwaway git repo)
	t.Chdir(t.TempDir())              // move to a NON-git cwd

	if _, err := BuildDAG(ctx, oc.Store(), nil, nil, nil, ""); err != nil {
		t.Fatalf("BuildDAG should resolve the managed repo from a non-git cwd: %v", err)
	}
	managedGit := filepath.Join(oc.Store().Dir(), "repo", ".git")
	if _, err := os.Stat(managedGit); err != nil {
		t.Fatalf("managed repo should exist at <store.Dir()>/repo: %v", err)
	}
}
