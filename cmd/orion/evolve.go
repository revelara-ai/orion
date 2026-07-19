package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/memory"
	"github.com/revelara-ai/orion/internal/selfevolve"
	"github.com/revelara-ai/orion/internal/skillcurator"
)

// cmdEvolve implements `orion evolve` (or-qau): the explicit, opt-in trigger for the
// self-evolution loop (DEFAULT OFF — it never runs automatically). It promotes proof-passed
// memory candidates into generation-tier skills under <dataDir>/skills, which `orion skills
// list` then discovers. Promoted skills are generation trust: quarantined from proof.
func cmdEvolve(_ []string) int {
	dir, err := resolveDataDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion evolve:", err)
		return 1
	}
	memDir := filepath.Join(dir, "memory")
	if err := os.MkdirAll(memDir, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "orion evolve:", err)
		return 1
	}
	mem, err := memory.Open(memDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion evolve:", err)
		return 1
	}
	defer func() { _ = mem.Close() }()

	skillsDir := filepath.Join(dir, "skills")
	// or-gb1.6: skills are data-dir GLOBAL — scrub the active project's
	// literals (its route/module surface) at this developer-scope boundary.
	promoted, rejected, err := selfevolve.PromoteCandidatesRedacted(context.Background(), mem, skillsDir, projectRedactLiterals(dir))
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion evolve:", err)
		return 1
	}
	// or-gb1.5: the SkillEval gate — rejected candidates are surfaced with
	// the failing predicate named, never silently skipped.
	for _, r := range rejected {
		fmt.Printf("rejected %s: %s\n", r.CandidateID, r.Reason)
	}
	if len(promoted) > 0 {
		fmt.Printf("orion evolve: promoted %d candidate(s) to generation-tier skills in %s:\n", len(promoted), skillsDir)
		for _, n := range promoted {
			fmt.Println("  -", n)
		}
	} else {
		fmt.Println("orion evolve: no candidates to promote")
	}
	// or-ykz.9: the evolve invocation is the natural inactivity trigger for
	// the skill curator — after promotion, bound the self-evolved set
	// (archive stale, consolidate near-dups; snapshot first, never delete;
	// pinned + non-self-evolved untouched).
	if cur, cerr := skillcurator.Curate(skillsDir, curatorStaleAfter(), time.Now()); cerr == nil {
		if n := len(cur.Archived) + len(cur.Consolidated); n > 0 {
			fmt.Printf("orion evolve: curated %d skill(s) — archived %d, consolidated %d (recoverable under %s/.archive)\n",
				n, len(cur.Archived), len(cur.Consolidated), skillsDir)
		}
	}
	return 0
}

// curatorStaleAfter is the inactivity window before a self-evolved skill is
// archived (or-ykz.9); ORION_SKILL_CURATOR_STALE_DAYS overrides, default 90.
func curatorStaleAfter() time.Duration {
	if v := os.Getenv("ORION_SKILL_CURATOR_STALE_DAYS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			return time.Duration(n) * 24 * time.Hour
		}
	}
	return 90 * 24 * time.Hour
}

// projectRedactLiterals collects the ACTIVE project's identifying literals
// (route + module surface) for redaction at developer-scope boundaries.
// Best-effort: no active project (or no store) means nothing to redact.
func projectRedactLiterals(dataDir string) []string {
	store, err := contextstore.Open(dataDir)
	if err != nil {
		return nil
	}
	defer func() { _ = store.Close() }()
	_, sp, err := store.CurrentProjectSpec(context.Background())
	if err != nil {
		return nil
	}
	var rc struct {
		Route  string `json:"route"`
		Module string `json:"module"`
	}
	_ = json.Unmarshal([]byte(sp.ResponseContract), &rc)
	var out []string
	if strings.TrimSpace(rc.Route) != "" {
		out = append(out, rc.Route)
	}
	if strings.TrimSpace(rc.Module) != "" {
		out = append(out, rc.Module)
	}
	return out
}
