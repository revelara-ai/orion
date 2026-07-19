package conductor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator"
	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// ratifiedPythonLibrary drives the REAL intake to a ratified python-library
// spec: library project type (unregistered → the direction rail rides the
// checklist), direction.language=python (registered since or-4y7.9 — no
// capability refusal, no reduced-proof acknowledgment needed), unit-case
// obligations declared as PYTHON expressions.
func ratifiedPythonLibrary(t *testing.T) (*orchestrator.Conductor, context.Context) {
	t.Helper()
	withGitRepo(t)
	oc := orchestrator.NewWithStore(openStore(t))
	ctx := context.Background()
	if _, err := oc.Submit(ctx, "Build a reusable calculator library with add and divide."); err != nil {
		t.Fatalf("submit: %v", err)
	}
	for _, a := range [][2]string{
		{"direction.stack", "single python package"},
		{"direction.language", "python"},
		{"direction.engine", "none"},
		{"direction.wire_protocol", "text"},
		{"direction.repo_layout", "managed-repo"},
		{"scale_profile", "low"},
		{"observability_signals", "tier-default signal set"},
		{"oncall_escalation", "single owner, log-only alert"},
		{"data_storage", "no persistence"},
		{"slo_targets", "tier-default SLO"},
		{"security_model", "untrusted input, no regulated data"},
		{"dependencies", "none"},
	} {
		if err := oc.RecordAnswer(ctx, a[0], a[1]); err != nil {
			t.Fatalf("answer %s: %v", a[0], err)
		}
	}
	if err := oc.AddRequirement(ctx, spec.Requirement{
		Source: completeness.DimFunctional,
		Text:   "calclib.add sums integers; calclib.div divides and raises on zero",
		Cases: []spec.BehavioralCase{
			{Kind: spec.KindUnit, Unit: &spec.UnitCase{Pkg: "calclib", Steps: []spec.UnitStep{{Call: "add(2, 3)", Want: "5"}}}},
			{Kind: spec.KindUnit, Unit: &spec.UnitCase{Pkg: "calclib", Steps: []spec.UnitStep{{Call: "div(1, 0)", WantErrRE: "division"}}}},
		},
	}); err != nil {
		t.Fatalf("declare python requirement: %v", err)
	}
	if _, err := oc.ApproveAssumptions(ctx); err != nil {
		t.Fatalf("approve assumptions: %v", err)
	}
	if _, err := oc.ApproveSpec(ctx); err != nil {
		t.Fatalf("approve: %v", err)
	}
	return oc, ctx
}

// pyLibGen is the deterministic python generator for the tracer: it writes the
// calculator package the ratified cases call (the python analog of the Go
// time-service fixture, injected since the nil-gen fixture is Go-only).
func pyLibGen(_ context.Context, gs sandbox.GenSpec, dir, _ string) (sandbox.GeneratedArtifact, error) {
	if gs.Language != "python" {
		return sandbox.GeneratedArtifact{}, os.ErrInvalid
	}
	pkg := filepath.Join(dir, "calclib")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		return sandbox.GeneratedArtifact{}, err
	}
	src := "\"\"\"Calculator library (Orion python tracer fixture).\"\"\"\n\n\ndef add(a, b):\n    if not isinstance(a, (int, float)) or not isinstance(b, (int, float)):\n        raise TypeError('add expects numbers')\n    return a + b\n\n\ndef div(a, b):\n    if not isinstance(a, (int, float)) or not isinstance(b, (int, float)):\n        raise TypeError('div expects numbers')\n    return a / b\n"
	if err := os.WriteFile(filepath.Join(pkg, "__init__.py"), []byte(src), 0o644); err != nil {
		return sandbox.GeneratedArtifact{}, err
	}
	return sandbox.GeneratedArtifact{Path: dir}, nil
}

// TestBuildAndProvePythonLibrary (or-4y7.9 DONE-WHEN): the FULL pipeline over a
// ratified python direction — decompose → generate (python) → fast diagnostics
// (py_compile) → behavioral (unittest corpus, REDUCED mutation label) →
// empirical (sandboxed python unit driver) → hazard → wireup (honestly
// Unverified) → bar → deliver. The harness is not a Go-specific harness.
func TestBuildAndProvePythonLibrary(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the sandboxed python proof pipeline; skipped in -short")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on host")
	}
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not on host")
	}
	oc, ctx := ratifiedPythonLibrary(t)
	outRoot := t.TempDir()

	res, err := BuildAndProve(ctx, oc.Store(), pyLibGen, nil, nil, outRoot)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.Verdict != "Accept" || !res.Closed {
		t.Fatalf("the python tracer must prove green and close: %+v", res)
	}

	// The delivered artifact is PYTHON: the export carries the package, never
	// go files, and never the harness corpus/driver.
	var exported []string
	_ = filepath.WalkDir(outRoot, func(p string, d os.DirEntry, _ error) error {
		if d != nil && !d.IsDir() {
			exported = append(exported, p)
		}
		return nil
	})
	joined := strings.Join(exported, "\n")
	if !strings.Contains(joined, "calclib") || !strings.Contains(joined, "__init__.py") {
		t.Fatalf("the python package must be exported, got:\n%s", joined)
	}
	for _, banned := range []string{".go", "orion_behavioral_test.py", "orion_unit_driver.py"} {
		if strings.Contains(joined, banned) {
			t.Fatalf("export must not carry %q:\n%s", banned, joined)
		}
	}
}
