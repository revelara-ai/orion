// Package skillcurator is the inactivity-triggered lifecycle manager for
// GENERATION-DOMAIN, agent-created skills (or-ykz.9, Hermes curator): it
// archives unused skills, consolidates near-duplicates, and snapshots the set
// before every run — and it NEVER deletes. Proof-domain skills and pinned
// skills are untouched by construction: the curator only ever considers
// self-evolved (Metadata orion-source=self-evolved) generation-trust skills,
// so a curated built-in or a human-pinned skill can never be moved.
//
// No daemon: Curate is a function a caller invokes at a natural idle point
// (e.g. session start). It operates on the on-disk self-evolved skills dir.
package skillcurator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/skill"
)

const (
	snapshotDir = ".curator-snapshot"
	archiveDir  = ".archive"
	sourceKey   = "orion-source"
	selfEvolved = "self-evolved"
)

// Result reports what one Curate run did.
type Result struct {
	Snapshotted  []string // skill names snapshotted before curating
	Archived     []string // unused skills moved to .archive
	Consolidated []string // near-duplicate skills archived (a canonical twin was kept)
}

// managed is one self-evolved skill under consideration.
type managed struct {
	name  string
	dir   string
	sk    skill.Skill
	mtime time.Time
}

// Curate runs the lifecycle pass over dir: snapshot → consolidate duplicates →
// archive stale. staleAfter is the inactivity window; a self-evolved skill
// whose SKILL.md has not changed within it is archived. now is injected for
// testability. Non-self-evolved, pinned, and proof-trust skills are never
// touched. Best-effort per skill (a bad SKILL.md is skipped, not fatal).
func Curate(dir string, staleAfter time.Duration, now time.Time) (Result, error) {
	var res Result
	entries, err := os.ReadDir(dir)
	if err != nil {
		return res, err
	}

	var items []managed
	for _, e := range entries {
		if !e.IsDir() || e.Name() == snapshotDir || e.Name() == archiveDir {
			continue
		}
		md := filepath.Join(dir, e.Name(), "SKILL.md")
		st, serr := os.Stat(md)
		if serr != nil {
			continue
		}
		sk, _, lerr := skill.Load(md, skill.TrustGeneration)
		if lerr != nil {
			continue
		}
		// The curator manages ONLY self-evolved generation skills. A pinned
		// skill or any non-self-evolved one (imported, native, curated) is
		// left exactly where it is.
		if sk.Metadata[sourceKey] != selfEvolved || isPinned(sk) {
			continue
		}
		items = append(items, managed{name: sk.Name, dir: filepath.Join(dir, e.Name()), sk: sk, mtime: st.ModTime()})
	}

	// Snapshot BEFORE any change — the last-good set is recoverable.
	for _, m := range items {
		if err := snapshot(dir, m); err == nil {
			res.Snapshotted = append(res.Snapshotted, m.name)
		}
	}

	// Consolidate: group by canonical key; keep the FRESHEST, archive the rest.
	byKey := map[string][]managed{}
	for _, m := range items {
		byKey[canonicalKey(m.sk)] = append(byKey[canonicalKey(m.sk)], m)
	}
	consolidated := map[string]bool{}
	for _, group := range byKey {
		if len(group) < 2 {
			continue
		}
		keep := group[0]
		for _, m := range group[1:] {
			if m.mtime.After(keep.mtime) {
				keep = m
			}
		}
		for _, m := range group {
			if m.name == keep.name {
				continue
			}
			if err := archive(dir, m); err == nil {
				res.Consolidated = append(res.Consolidated, m.name)
				consolidated[m.name] = true
			}
		}
	}

	// Archive stale (unused): older than the inactivity window, and not
	// already consolidated away this run.
	for _, m := range items {
		if consolidated[m.name] {
			continue
		}
		if now.Sub(m.mtime) > staleAfter {
			if err := archive(dir, m); err == nil {
				res.Archived = append(res.Archived, m.name)
			}
		}
	}
	return res, nil
}

// isPinned reports a human-pinned skill (frontmatter `pinned: true`, or
// metadata pinned=true) — the curator never touches it.
func isPinned(sk skill.Skill) bool {
	if v, ok := sk.Extension("pinned"); ok && strings.EqualFold(strings.TrimSpace(v), "true") {
		return true
	}
	return strings.EqualFold(sk.Metadata["pinned"], "true")
}

var wsRE = regexp.MustCompile(`\s+`)

// canonicalKey is the dedup identity: the skill's description normalized to
// lowercase, single-spaced, punctuation-stripped. Two self-evolved skills
// that describe the same procedure collapse to one key.
func canonicalKey(sk skill.Skill) string {
	s := strings.ToLower(sk.Description)
	s = regexp.MustCompile(`[^\w\s]`).ReplaceAllString(s, " ")
	return strings.TrimSpace(wsRE.ReplaceAllString(s, " "))
}

func snapshot(root string, m managed) error {
	dst := filepath.Join(root, snapshotDir, m.name)
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	b, err := os.ReadFile(filepath.Join(m.dir, "SKILL.md")) // #nosec G304 -- curator-owned skills dir
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, "SKILL.md"), b, 0o644)
}

// archive MOVES a skill dir into .archive — never a delete. A name collision
// in the archive is disambiguated by a numeric suffix.
func archive(root string, m managed) error {
	base := filepath.Join(root, archiveDir)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(base, m.name)
	for i := 2; ; i++ {
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			break
		}
		dst = filepath.Join(base, fmt.Sprintf("%s-%d", m.name, i))
	}
	return os.Rename(m.dir, dst)
}
