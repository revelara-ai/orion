package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/revelara-ai/orion/internal/skill"
)

// cmdSkills implements `orion skills list|show` (or-alp): the first consumer of the
// agentskills.io-compatible registry. It discovers skills from the conventional scopes
// (~/.agents/skills, ~/.claude/skills, ~/.orion/skills + project equivalents), so a skill
// authored for any compatible client is listed unchanged.
func cmdSkills(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "orion skills: expected 'list' or 'show <name>'")
		return 2
	}
	switch args[0] {
	case "list":
		return cmdSkillsList(args[1:])
	case "show":
		return cmdSkillsShow(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "orion skills: unknown subcommand %q (want list|show)\n", args[0])
		return 2
	}
}

// loadSkills builds a registry from the default discovery scopes (project = cwd).
func loadSkills() (*skill.Registry, error) {
	r := skill.New()
	cwd, _ := os.Getwd()
	if err := r.LoadScopes(skill.DefaultScopes(cwd)); err != nil {
		return nil, err
	}
	return r, nil
}

type skillView struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Trust       string `json:"trust"`
	Path        string `json:"path"`
}

func cmdSkillsList(args []string) int {
	fs := flag.NewFlagSet("skills list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	r, err := loadSkills()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion skills list:", err)
		return 1
	}
	skills := r.List()
	if *asJSON {
		views := make([]skillView, 0, len(skills))
		for _, s := range skills {
			views = append(views, skillView{s.Name, s.Description, string(s.Trust), s.Path})
		}
		return emitJSON(views)
	}
	if len(skills) == 0 {
		fmt.Println("no skills found (scanned ~/.agents/skills, ~/.claude/skills, ~/.orion/skills and ./ equivalents)")
		return 0
	}
	for _, s := range skills {
		fmt.Printf("%-28s [%-10s] %s\n", s.Name, s.Trust, firstLine(s.Description))
	}
	return 0
}

func cmdSkillsShow(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "orion skills show: a skill name is required")
		return 2
	}
	r, err := loadSkills()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion skills show:", err)
		return 1
	}
	s, ok := r.Get(args[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "orion skills show: no skill named %q\n", args[0])
		return 1
	}
	fmt.Printf("# %s\n%s\n\nsource: %s (trust=%s)\n\n%s\n", s.Name, s.Description, s.Path, s.Trust, s.Body)
	return 0
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
