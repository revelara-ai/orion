package actuation

import "testing"

// TestMutatingToolsSupportDryRun: every state-mutating tool in the catalog
// honors the dry-run contract and declares its effect + blast radius. A new
// mutating tool that forgets dry-run fails here.
func TestMutatingToolsSupportDryRun(t *testing.T) {
	cat := Catalog()
	if len(cat) == 0 {
		t.Fatal("tool-effects catalog is empty")
	}
	// The known mutating surfaces must all be catalogued.
	want := []string{"sandbox.exec", "sandbox.write", "worktree.create", "worktree.remove", "integration.merge", "polaris.write"}
	have := map[string]bool{}
	for _, e := range cat {
		have[e.Tool] = true
		if !e.DryRun {
			t.Fatalf("mutating tool %q does not support dry_run", e.Tool)
		}
		if e.Effect == "" || e.BlastRadius == "" {
			t.Fatalf("mutating tool %q missing effect/blast radius", e.Tool)
		}
	}
	for _, w := range want {
		if !have[w] {
			t.Fatalf("mutating tool %q not catalogued", w)
		}
	}
}
