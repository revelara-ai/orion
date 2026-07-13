package skill

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Discovery bounds (agentskills.io client guide): never runaway-scan a large tree, and never
// read an unbounded file (project-supplied SKILL.md files are untrusted).
const (
	maxScanDepth  = 6
	maxScanDirs   = 2000
	maxSkillBytes = 1 << 20 // 1 MiB per SKILL.md — bounds a DoS via an oversized file
)

// skipDirs are never descended during discovery.
var skipDirs = map[string]bool{".git": true, "node_modules": true, "vendor": true}

// Registry is an in-memory set of loaded skills keyed by name, the discovery scopes it was
// loaded from (so it can hot-reload), plus the non-fatal diagnostics gathered while loading
// (agentskills.io discovery + tier-1 catalog).
type Registry struct {
	skills   map[string]Skill
	scopes   []Scope
	warnings []string
}

// New returns an empty registry.
func New() *Registry { return &Registry{skills: map[string]Skill{}} }

// LoadDir discovers skills under root — each a subdirectory containing a SKILL.md — and adds
// them at the given trust tier. TRUST IS ASSIGNED BY THE SCOPE (this call), never read from
// the skill file: an untrusted project-supplied skill cannot elevate itself. A later LoadDir
// overrides an earlier one on a name collision, so call user/built-in scopes first and the
// project scope last to get the standard project-over-user precedence. A missing root is not
// an error (just no skills there); per-skill parse failures are recorded as warnings.
func (r *Registry) LoadDir(root string, trust Trust) (int, error) {
	r.scopes = append(r.scopes, Scope{Root: root, Trust: trust})
	return r.scan(root, trust)
}

// scan loads skills under root at the given trust WITHOUT recording the scope, so Reload can
// re-run an already-recorded scope without duplicating it.
func (r *Registry) scan(root string, trust Trust) (int, error) {
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return 0, nil
	}
	loaded, scanned := 0, 0
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry — skip it, don't abort the whole scan
		}
		if d.IsDir() {
			if path != root && skipDirs[d.Name()] {
				return fs.SkipDir
			}
			if depth(root, path) > maxScanDepth {
				return fs.SkipDir
			}
			if scanned++; scanned > maxScanDirs {
				return fs.SkipAll
			}
			return nil
		}
		if d.Name() != "SKILL.md" {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			// Never follow a symlinked SKILL.md — it could point outside the scan root (e.g.
			// SKILL.md -> /etc/passwd) and leak file contents into the registry/diagnostics.
			r.warnings = append(r.warnings, fmt.Sprintf("%s: SKILL.md is a symlink — skipped (not followed)", path))
			return nil
		}
		sk, ws, perr := Load(path, trust)
		r.warnings = append(r.warnings, ws...)
		if perr != nil {
			r.warnings = append(r.warnings, fmt.Sprintf("%s: %v", path, perr))
			return nil
		}
		if existing, exists := r.skills[sk.Name]; exists {
			// A proof-trust skill is immutable and cannot be shadowed by a generation skill of
			// the same name (invariant 8) — regardless of load order.
			if existing.Trust == TrustProof && sk.Trust != TrustProof {
				r.warnings = append(r.warnings, fmt.Sprintf("skill %q at %s ignored — a proof-trust skill of that name is immutable", sk.Name, sk.Path))
				return nil
			}
			r.warnings = append(r.warnings, fmt.Sprintf("skill %q at %s shadows earlier load at %s", sk.Name, sk.Path, existing.Path))
		}
		r.skills[sk.Name] = sk
		loaded++
		return nil
	})
	return loaded, walkErr
}

// readCapped reads at most max bytes from path, erroring if the file is larger (so an
// oversized untrusted SKILL.md cannot be slurped wholesale into memory).
func readCapped(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	content, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("SKILL.md exceeds %d bytes", limit)
	}
	return content, nil
}

func depth(root, path string) int {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." {
		return 0
	}
	return strings.Count(rel, string(filepath.Separator)) + 1
}

// Load reads + parses one SKILL.md at path (size-capped), assigning trust and resolving Path/Dir.
func Load(path string, trust Trust) (Skill, []string, error) {
	content, err := readCapped(path, maxSkillBytes)
	if err != nil {
		return Skill{}, nil, err
	}
	sk, ws, err := Parse(content)
	if err != nil {
		return Skill{}, ws, err
	}
	if abs, aerr := filepath.Abs(path); aerr == nil {
		path = abs
	}
	sk.Path = path
	sk.Dir = filepath.Dir(path)
	sk.Trust = trust
	return sk, ws, nil
}

// Get returns the skill registered under name.
func (r *Registry) Get(name string) (Skill, bool) {
	s, ok := r.skills[name]
	return s, ok
}

// List returns all registered skills, sorted by name (deterministic).
func (r *Registry) List() []Skill {
	out := make([]Skill, 0, len(r.skills))
	for _, s := range r.skills {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Warnings returns the non-fatal diagnostics accumulated during loading.
func (r *Registry) Warnings() []string { return r.warnings }

// Reload hot-reloads the registry IN PLACE (or-ykz.3, the Pi /reload analogue): it re-scans
// the recorded GENERATION-trust scopes — picking up edited, added, and removed generation
// skills — while preserving proof-trust skills untouched. Proof skills are immutable and are
// never re-read (invariant 8). The *Registry instance is preserved, so any session state that
// holds it keeps working; only the generation skill set is refreshed. Diagnostics are reset to
// reflect the fresh scan.
func (r *Registry) Reload() error {
	kept := make(map[string]Skill, len(r.skills))
	for name, s := range r.skills {
		if s.Trust == TrustProof {
			kept[name] = s
		}
	}
	r.skills = kept
	r.warnings = nil
	for _, sc := range r.scopes {
		if sc.Trust == TrustProof {
			continue // proof scopes are immutable — never re-scanned
		}
		if _, err := r.scan(sc.Root, sc.Trust); err != nil {
			return err
		}
	}
	return nil
}

// Catalog renders the tier-1 progressive-disclosure catalog — one "name: description" line
// per skill (~50-100 tokens each) — for injection into the model's context so it knows which
// skills exist without loading any full SKILL.md body. Empty when no skills are registered (so
// callers can omit the section entirely rather than show an empty block).
func (r *Registry) Catalog() string {
	skills := r.List()
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# AVAILABLE SKILLS — load a skill's full instructions only when its description matches the task\n")
	for _, s := range skills {
		fmt.Fprintf(&b, "- %s: %s\n", s.Name, oneLine(s.Description))
	}
	return b.String()
}

// CatalogForGeneration renders the catalog for injection into a code generator's prompt: like
// Catalog, but each line carries the skill's SKILL.md path so a file-read-capable generator can
// ACTIVATE a skill (read its full instructions) per agentskills.io progressive disclosure.
func (r *Registry) CatalogForGeneration() string {
	skills := r.List()
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("# AVAILABLE SKILLS — read a skill's file for its full instructions only when its description matches the task\n")
	for _, s := range skills {
		fmt.Fprintf(&b, "- %s — %s (load: %s)\n", s.Name, oneLine(s.Description), s.Path)
	}
	return b.String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
