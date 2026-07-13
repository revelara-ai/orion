package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/revelara-ai/orion/internal/memory"
	"github.com/revelara-ai/orion/internal/selfevolve"
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
	promoted, rejected, err := selfevolve.PromoteCandidates(context.Background(), mem, skillsDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion evolve:", err)
		return 1
	}
	// or-gb1.5: the SkillEval gate — rejected candidates are surfaced with
	// the failing predicate named, never silently skipped.
	for _, r := range rejected {
		fmt.Printf("rejected %s: %s\n", r.CandidateID, r.Reason)
	}
	if len(promoted) == 0 {
		fmt.Println("orion evolve: no candidates to promote")
		return 0
	}
	fmt.Printf("orion evolve: promoted %d candidate(s) to generation-tier skills in %s:\n", len(promoted), skillsDir)
	for _, n := range promoted {
		fmt.Println("  -", n)
	}
	return 0
}
