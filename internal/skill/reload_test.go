package skill

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReloadAppliesGenerationEdits (or-ykz.3): editing a generation skill on disk and reloading
// the SAME registry instance applies the change — without recreating the registry.
func TestReloadAppliesGenerationEdits(t *testing.T) {
	root := t.TempDir()
	writeSkillDir(t, root, "evolving", md("evolving", "version one"))
	r := New()
	if _, err := r.LoadDir(root, TrustGeneration); err != nil {
		t.Fatal(err)
	}
	if s, _ := r.Get("evolving"); s.Description != "version one" {
		t.Fatalf("initial load: %q", s.Description)
	}

	writeSkillDir(t, root, "evolving", md("evolving", "version two"))
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	if s, _ := r.Get("evolving"); s.Description != "version two" {
		t.Fatalf("reload did not apply the edit: %q", s.Description)
	}
}

// TestReloadPreservesProofRefreshesGeneration: proof skills survive a reload even if their file
// is removed (immutable, never re-scanned — invariant 8), while generation skills refresh.
func TestReloadPreservesProofRefreshesGeneration(t *testing.T) {
	proofRoot, genRoot := t.TempDir(), t.TempDir()
	writeSkillDir(t, proofRoot, "builtin", md("builtin", "curated"))
	writeSkillDir(t, genRoot, "userskill", md("userskill", "v1"))
	r := New()
	_, _ = r.LoadDir(proofRoot, TrustProof)
	_, _ = r.LoadDir(genRoot, TrustGeneration)

	if err := os.RemoveAll(filepath.Join(proofRoot, "builtin")); err != nil {
		t.Fatal(err)
	}
	writeSkillDir(t, genRoot, "userskill", md("userskill", "v2"))
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get("builtin"); !ok {
		t.Fatal("a proof skill must survive reload even after its file is removed (immutable)")
	}
	if s, _ := r.Get("userskill"); s.Description != "v2" {
		t.Fatalf("a generation skill should refresh on reload: %q", s.Description)
	}
}

// TestReloadPicksUpNewAndDropsRemoved: a new generation skill appears and a removed one
// disappears after reload.
func TestReloadPicksUpNewAndDropsRemoved(t *testing.T) {
	root := t.TempDir()
	writeSkillDir(t, root, "first", md("first", "f"))
	r := New()
	_, _ = r.LoadDir(root, TrustGeneration)

	writeSkillDir(t, root, "second", md("second", "s"))
	if err := os.RemoveAll(filepath.Join(root, "first")); err != nil {
		t.Fatal(err)
	}
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Get("second"); !ok {
		t.Fatal("a new skill was not picked up on reload")
	}
	if _, ok := r.Get("first"); ok {
		t.Fatal("a removed skill should be gone after reload")
	}
}

// TestProofSkillNotShadowedByGeneration: a generation skill cannot shadow a same-named
// proof skill, regardless of load order (invariant 8).
func TestProofSkillNotShadowedByGeneration(t *testing.T) {
	proofRoot, genRoot := t.TempDir(), t.TempDir()
	writeSkillDir(t, proofRoot, "shared", md("shared", "the immutable one"))
	writeSkillDir(t, genRoot, "shared", md("shared", "an impostor"))
	r := New()
	_, _ = r.LoadDir(proofRoot, TrustProof)
	_, _ = r.LoadDir(genRoot, TrustGeneration) // attempts to shadow the proof skill
	s, _ := r.Get("shared")
	if s.Description != "the immutable one" || s.Trust != TrustProof {
		t.Fatalf("a generation skill must not shadow a proof skill: got %q (%s)", s.Description, s.Trust)
	}
}
