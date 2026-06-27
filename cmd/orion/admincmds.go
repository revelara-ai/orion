package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/health"
	"github.com/revelara-ai/orion/internal/memory"
	"github.com/revelara-ai/orion/internal/selfevolve"
	"github.com/revelara-ai/orion/internal/tui"
)

// tuiCommands builds the admin/management slash-commands surfaced in the TUI (or-dz9). Each
// returns text that the TUI prints into the transcript. The implementations live here because
// cmd/orion owns store/env/probe access; the TUI owns dispatch + rendering. These mirror the
// CLI subcommands so the TUI is the always-on management surface without dropping to a shell.
func tuiCommands() []tui.Command {
	return []tui.Command{
		{Name: "status", Help: "identity + component readiness summary", Run: func(string) string { return statusText() }},
		{Name: "doctor", Help: "run a fresh component-health check", Run: func(string) string { return doctorText() }},
		{Name: "skills", Help: "list discovered skills (agentskills.io)", Run: func(string) string { return skillsListText() }},
		{Name: "agents", Help: "list discovered agent definitions", Run: func(string) string { return agentsListText() }},
		{Name: "evolve", Help: "promote proof-passed candidates into skills", Run: func(string) string { return evolveText() }},
	}
}

func statusText() string {
	dir, _ := doctorDataDir()
	rep := health.Probe(health.Options{DataDir: dir, LookPath: exec.LookPath, AgentEnv: os.Getenv("ORION_AGENT"), Polaris: cachedPolarisCheck})
	ok, warn, fail := rep.Summary()
	id := bannerIdentity()
	var b strings.Builder
	fmt.Fprintf(&b, "brain:   %s\n", id.Brain)
	fmt.Fprintf(&b, "version: %s · %s\n", id.Version, id.Branch)
	fmt.Fprintf(&b, "cwd:     %s\n", id.Cwd)
	fmt.Fprintf(&b, "session: %s · budget %s\n", id.Session, id.Budget)
	fmt.Fprintf(&b, "ready:   %d/%d · %d warning(s) · %d failing", ok, ok+warn+fail, warn, fail)
	return b.String()
}

func doctorText() string {
	dir, _ := doctorDataDir()
	checks := doctorChecks(dir, exec.LookPath, os.Getenv("ORION_AGENT"), false)
	var b strings.Builder
	failed := 0
	for _, c := range checks {
		fmt.Fprintf(&b, "[%-4s] %-16s %s\n", c.Status, c.Name, c.Detail)
		if c.Status == statusFail {
			failed++
		}
	}
	if failed > 0 {
		fmt.Fprintf(&b, "%d check(s) FAILED", failed)
	} else {
		b.WriteString("all checks passed")
	}
	return b.String()
}

func skillsListText() string {
	r, err := loadSkills()
	if err != nil {
		return "skills: " + err.Error()
	}
	skills := r.List()
	if len(skills) == 0 {
		return "no skills found (scanned ~/.agents/skills, ~/.claude/skills, ~/.orion/skills and ./ equivalents)"
	}
	var b strings.Builder
	for _, s := range skills {
		fmt.Fprintf(&b, "%-28s [%-10s] %s\n", s.Name, s.Trust, firstLine(s.Description))
	}
	return strings.TrimRight(b.String(), "\n")
}

func agentsListText() string {
	r, err := loadAgents()
	if err != nil {
		return "agents: " + err.Error()
	}
	agents := r.List()
	if len(agents) == 0 && len(r.Docs()) == 0 {
		return "no agents found (scanned ~/.agents/agents, ~/.claude/agents and ./ equivalents)"
	}
	var b strings.Builder
	for _, a := range agents {
		fmt.Fprintf(&b, "%-28s [%-10s] %s\n", a.Name, a.Trust, firstLine(a.Description))
	}
	for _, d := range r.Docs() {
		fmt.Fprintf(&b, "(AGENTS.md: %s)\n", d.Path)
	}
	return strings.TrimRight(b.String(), "\n")
}

func evolveText() string {
	dir, err := resolveDataDir()
	if err != nil {
		return "evolve: " + err.Error()
	}
	memDir := filepath.Join(dir, "memory")
	if err := os.MkdirAll(memDir, 0o700); err != nil {
		return "evolve: " + err.Error()
	}
	mem, err := memory.Open(memDir)
	if err != nil {
		return "evolve: " + err.Error()
	}
	defer func() { _ = mem.Close() }()
	promoted, err := selfevolve.PromoteCandidates(context.Background(), mem, filepath.Join(dir, "skills"))
	if err != nil {
		return "evolve: " + err.Error()
	}
	if len(promoted) == 0 {
		return "no candidates to promote"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "promoted %d candidate(s) to generation-tier skills:\n", len(promoted))
	for _, n := range promoted {
		b.WriteString("  - " + n + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
