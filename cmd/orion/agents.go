package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/revelara-ai/orion/internal/skill"
)

// cmdAgents implements `orion agents list|show` (or-oy3): discovers open agent definitions
// (subagent .md files) from the conventional scopes (~/.agents/agents, ~/.claude/agents +
// project equivalents) plus a project-root AGENTS.md, so an agent authored for the ecosystem
// is listed unchanged.
func cmdAgents(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "orion agents: expected 'list' or 'show <name>'")
		return 2
	}
	switch args[0] {
	case "list":
		return cmdAgentsList(args[1:])
	case "show":
		return cmdAgentsShow(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "orion agents: unknown subcommand %q (want list|show)\n", args[0])
		return 2
	}
}

func loadAgents() (*skill.AgentRegistry, error) {
	r := skill.NewAgentRegistry()
	cwd, _ := os.Getwd()
	if err := r.LoadScopes(skill.DefaultAgentScopes(cwd)); err != nil {
		return nil, err
	}
	r.DiscoverAgentsDocs(cwd) // a project-root AGENTS.md, if any
	return r, nil
}

type agentView struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Tools       []string `json:"tools,omitempty"`
	Model       string   `json:"model,omitempty"`
	Trust       string   `json:"trust"`
	Path        string   `json:"path"`
}

func cmdAgentsList(args []string) int {
	fs := flag.NewFlagSet("agents list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	r, err := loadAgents()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion agents list:", err)
		return 1
	}
	agents := r.List()
	if *asJSON {
		views := make([]agentView, 0, len(agents))
		for _, a := range agents {
			views = append(views, agentView{a.Name, a.Description, a.Tools, a.Model, string(a.Trust), a.Path})
		}
		return emitJSON(views)
	}
	if len(agents) == 0 && len(r.Docs()) == 0 {
		fmt.Println("no agents found (scanned ~/.agents/agents, ~/.claude/agents and ./ equivalents)")
		return 0
	}
	for _, a := range agents {
		fmt.Printf("%-28s [%-10s] %s\n", a.Name, a.Trust, firstLine(a.Description))
	}
	for _, d := range r.Docs() {
		fmt.Printf("(AGENTS.md context: %s)\n", d.Path)
	}
	return 0
}

func cmdAgentsShow(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "orion agents show: an agent name is required")
		return 2
	}
	r, err := loadAgents()
	if err != nil {
		fmt.Fprintln(os.Stderr, "orion agents show:", err)
		return 1
	}
	a, ok := r.Get(args[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "orion agents show: no agent named %q\n", args[0])
		return 1
	}
	tools := "(inherit all)"
	if len(a.Tools) > 0 {
		tools = strings.Join(a.Tools, ", ")
	}
	model := a.Model
	if model == "" {
		model = "(inherit)"
	}
	fmt.Printf("# %s\n%s\n\ntools: %s\nmodel: %s\nsource: %s (trust=%s)\n\n%s\n",
		a.Name, a.Description, tools, model, a.Path, a.Trust, a.Body)
	return 0
}
