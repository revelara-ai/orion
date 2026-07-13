package conductor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// or-tcs.4: the diagnosis is deterministic — flat trajectories across
// multiple informed refinements read as a spec defect; any movement reads
// as code-level; a single attempt never blames the spec.
func TestClassifyFailureStep(t *testing.T) {
	cases := []struct {
		traj []int
		want string
	}{
		{[]int{2, 2, 2}, "spec"}, // zero progress: re-coding is not converging
		{[]int{0, 0}, "spec"},    // flat at zero is still flat
		{[]int{1, 2, 2}, "code"}, // progress happened, then stalled — code-level
		{[]int{3, 1}, "code"},    // movement (even downward) is a code search
		{[]int{2}, "code"},       // one attempt is no trend
		{nil, "code"},
	}
	for _, tc := range cases {
		if got := classifyFailureStep(tc.traj); got != tc.want {
			t.Fatalf("classify(%v) = %s, want %s", tc.traj, got, tc.want)
		}
	}
}

// The or-tcs.4 acceptance: a task that exhausts refinement with ZERO
// obligation progress is routed BACKWARD — the escalation is spec-framed
// and the spec is re-opened as an amendment draft (lineage preserved) —
// instead of dead-ending as another delivery-framed re-code escalation.
func TestSpecDefectReopensSpec(t *testing.T) {
	oc, ctx := ratifiedTimeService(t)

	// Equally broken EVERY attempt, but byte-distinct (a nonce file) so the
	// no-change guard doesn't stop the loop and the trajectory has >= 2
	// flat entries.
	n := 0
	gen := func(_ context.Context, gs sandbox.GenSpec, dir, _ string) (sandbox.GeneratedArtifact, error) {
		n++
		art, err := writeBrokenTimeService(dir, gs)
		if err != nil {
			return art, err
		}
		if werr := os.WriteFile(filepath.Join(dir, "attempt_nonce.go"), []byte(fmt.Sprintf("package main\n\n// attempt %d\n", n)), 0o600); werr != nil {
			return art, werr
		}
		return art, nil
	}

	res, err := BuildAndProve(ctx, oc.Store(), gen, nil, nil, "")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if res.Verdict == "Accept" {
		t.Fatalf("broken every attempt must not Accept: %+v", res)
	}

	var specReason string
	if err := oc.Store().WithTx(ctx, func(tx *contextstore.Tx) error {
		open, e := tx.Escalations().ListOpen(ctx)
		if e != nil {
			return e
		}
		for _, esc := range open {
			if strings.Contains(esc.Reason, "spec defect suspected") {
				specReason = esc.Reason
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if specReason == "" {
		t.Fatal("exhaustion with zero progress must file a SPEC-framed escalation")
	}
	if !strings.Contains(specReason, "RE-OPENED") {
		t.Fatalf("the escalation must name the re-opened draft: %s", specReason)
	}

	// The backward edge is real: a drafting amendment (version parent+1)
	// now exists alongside the accepted spec.
	proj, _, err := oc.Store().CurrentProjectSpec(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	specs, err := oc.Store().SpecsForProject(context.Background(), proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundDraft := false
	for _, sp := range specs {
		if sp.Status == "drafting" && sp.ParentSpecID != "" {
			foundDraft = true
		}
	}
	if !foundDraft {
		t.Fatalf("spec was not re-opened as an amendment draft; specs: %+v", specs)
	}
}
