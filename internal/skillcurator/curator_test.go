package skillcurator

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeSelfEvolved writes a self-evolved skill dir with a controllable mtime
// and optional extra frontmatter lines.
func writeSelfEvolved(t *testing.T, root, name, desc string, mtime time.Time, extra string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: " + name + "\ndescription: " + desc + "\nmetadata:\n  orion-source: self-evolved\n" + extra + "---\n\nbody for " + name + "\n"
	p := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(p, []byte(md), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func exists(t *testing.T, root, name string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(root, name, "SKILL.md"))
	return err == nil
}

// TestCuratorConsolidatesAndArchives (or-ykz.9 DONE-WHEN): two near-duplicate
// self-evolved skills consolidate to one; an unused one is archived; the
// snapshot captures the pre-run set.
func TestCuratorConsolidatesAndArchives(t *testing.T) {
	root := t.TempDir()
	now := time.Now()

	// Two near-duplicates (same description, different names) — the fresher
	// one is kept, the older archived.
	writeSelfEvolved(t, root, "learned-aaa", "format Go code before committing", now.Add(-time.Hour), "")
	writeSelfEvolved(t, root, "learned-bbb", "Format  Go  code, before committing!", now, "")
	// A stale, unique skill → archived for inactivity.
	writeSelfEvolved(t, root, "learned-stale", "run the flaky test three times", now.Add(-100*24*time.Hour), "")
	// A fresh, unique skill → kept.
	writeSelfEvolved(t, root, "learned-fresh", "check the changelog for breaking changes", now, "")

	res, err := Curate(root, 30*24*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}

	// Consolidation: exactly one of the near-dup pair survives.
	aaa, bbb := exists(t, root, "learned-aaa"), exists(t, root, "learned-bbb")
	if aaa == bbb {
		t.Fatalf("exactly one near-dup must survive: aaa=%v bbb=%v", aaa, bbb)
	}
	if !bbb {
		t.Fatal("the FRESHER near-dup (bbb) must be the one kept")
	}
	if len(res.Consolidated) != 1 || res.Consolidated[0] != "learned-aaa" {
		t.Fatalf("consolidated must name the archived twin: %+v", res.Consolidated)
	}

	// Archive: the stale unique skill is gone from the live set…
	if exists(t, root, "learned-stale") {
		t.Fatal("a stale skill must be archived")
	}
	// …but MOVED, never deleted.
	if _, err := os.Stat(filepath.Join(root, archiveDir, "learned-stale", "SKILL.md")); err != nil {
		t.Fatalf("archived skill must be recoverable under .archive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, archiveDir, "learned-aaa", "SKILL.md")); err != nil {
		t.Fatalf("consolidated twin must be recoverable under .archive: %v", err)
	}

	// The fresh unique skill is untouched.
	if !exists(t, root, "learned-fresh") {
		t.Fatal("a fresh, unique skill must be kept")
	}

	// Snapshot captured the pre-run set (every managed skill).
	if len(res.Snapshotted) != 4 {
		t.Fatalf("snapshot must cover all 4 self-evolved skills, got %d", len(res.Snapshotted))
	}
	if _, err := os.Stat(filepath.Join(root, snapshotDir, "learned-aaa", "SKILL.md")); err != nil {
		t.Fatal("the consolidated skill must survive in the snapshot")
	}
}

// TestCuratorNeverTouchesPinnedOrForeign (or-ykz.9): pinned skills and
// non-self-evolved skills are bypassed — even when stale/duplicate.
func TestCuratorNeverTouchesPinnedOrForeign(t *testing.T) {
	root := t.TempDir()
	now := time.Now()

	// A PINNED self-evolved skill that is BOTH stale AND a duplicate of a live
	// one — the curator must still leave it alone.
	writeSelfEvolved(t, root, "learned-pinned", "important thing", now.Add(-365*24*time.Hour), "  pinned: \"true\"\n")
	writeSelfEvolved(t, root, "learned-dup-of-pinned", "important thing", now, "")

	// A FOREIGN (not self-evolved) skill, stale — the curator only manages
	// agent-created skills, so a proof/imported/native one is off-limits.
	fdir := filepath.Join(root, "native-skill")
	if err := os.MkdirAll(fdir, 0o755); err != nil {
		t.Fatal(err)
	}
	fmd := "---\nname: native-skill\ndescription: a curated native skill\n---\n\nbody\n"
	fp := filepath.Join(fdir, "SKILL.md")
	if err := os.WriteFile(fp, []byte(fmd), 0o600); err != nil {
		t.Fatal(err)
	}
	old := now.Add(-365 * 24 * time.Hour)
	if err := os.Chtimes(fp, old, old); err != nil {
		t.Fatal(err)
	}

	res, err := Curate(root, 30*24*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}

	if !exists(t, root, "learned-pinned") {
		t.Fatal("a PINNED skill must never be archived, even stale + duplicate")
	}
	if !exists(t, root, "native-skill") {
		t.Fatal("a non-self-evolved (native/proof) skill must be off-limits to the curator")
	}
	for _, n := range append(append([]string{}, res.Archived...), res.Consolidated...) {
		if n == "learned-pinned" || n == "native-skill" {
			t.Fatalf("curator touched a protected skill: %s", n)
		}
	}
}
