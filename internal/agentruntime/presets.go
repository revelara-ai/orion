package agentruntime

import (
	"sort"
	"strings"
)

// ACPMode is how a vendor agent is told to speak the Agent Client Protocol on
// its stdio transport (SPEC §0).
type ACPMode string

const (
	// ACPNative: the agent speaks ACP on stdio directly, no activation token
	// (Claude Code).
	ACPNative ACPMode = "native"
	// ACPFlag: the agent needs an activation flag, e.g. `--acp` (Gemini).
	ACPFlag ACPMode = "flag"
	// ACPSubcommand: the agent needs an activation subcommand, e.g. `acp` (Codex).
	ACPSubcommand ACPMode = "subcommand"
)

// Preset declares how to launch and drive one vendor coding-agent CLI. Orion is a
// control plane, not an LLM client: it spawns the developer's own agent and
// injects only the env keys on EnvAllow — it never holds or fabricates auth, the
// agent uses its own login (SPEC §0).
type Preset struct {
	Name        string   // stable id: "claude", "gemini", "codex"
	Command     string   // launch binary
	Args        []string // base args after ACP activation
	ACPMode     ACPMode  // native | flag | subcommand
	ACPArg      string   // activation token for flag/subcommand modes ("" for native)
	EnvAllow    []string // allowlist of env keys Orion may pass through to the agent
	ProcessName string   // executable name for process detection
}

// LaunchArgs returns the full argv: Command, the ACP activation token (for
// flag/subcommand modes), then the base Args.
func (p Preset) LaunchArgs() []string {
	argv := []string{p.Command}
	if p.ACPMode != ACPNative && p.ACPArg != "" {
		argv = append(argv, p.ACPArg)
	}
	argv = append(argv, p.Args...)
	return argv
}

// Env returns the KEY=VALUE entries Orion injects into the agent's process,
// drawn ONLY from this preset's allowlist and ONLY for keys present in source.
// Anything not on the allowlist (other secrets, ambient env) is excluded by
// construction — Orion injects env, it does not leak the host environment.
func (p Preset) Env(source map[string]string) []string {
	var out []string
	for _, k := range p.EnvAllow {
		if v, ok := source[k]; ok {
			out = append(out, k+"="+v)
		}
	}
	sort.Strings(out)
	return out
}

// Allows reports whether key is on this preset's injection allowlist.
func (p Preset) Allows(key string) bool {
	for _, k := range p.EnvAllow {
		if k == key {
			return true
		}
	}
	return false
}

// PresetRegistry holds the known agent presets and the default.
type PresetRegistry struct {
	presets map[string]Preset
	def     string
}

// DefaultPresetRegistry returns the built-in presets: Claude Code (default,
// native ACP, Max/Pro login via CLAUDE_CONFIG_DIR + optional Anthropic base/key
// overrides), Gemini (`--acp` flag), and Codex (`acp` subcommand).
func DefaultPresetRegistry() *PresetRegistry {
	r := &PresetRegistry{presets: map[string]Preset{}, def: "claude"}
	r.add(Preset{
		Name:        "claude",
		Command:     "claude",
		ACPMode:     ACPNative,
		EnvAllow:    []string{"CLAUDE_CONFIG_DIR", "ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY"},
		ProcessName: "claude",
	})
	r.add(Preset{
		Name:        "gemini",
		Command:     "gemini",
		ACPMode:     ACPFlag,
		ACPArg:      "--acp",
		EnvAllow:    []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"},
		ProcessName: "gemini",
	})
	r.add(Preset{
		Name:        "codex",
		Command:     "codex",
		ACPMode:     ACPSubcommand,
		ACPArg:      "acp",
		EnvAllow:    []string{"OPENAI_API_KEY", "OPENAI_BASE_URL"},
		ProcessName: "codex",
	})
	return r
}

func (r *PresetRegistry) add(p Preset) { r.presets[p.Name] = p }

// Get returns the preset by name.
func (r *PresetRegistry) Get(name string) (Preset, bool) {
	p, ok := r.presets[strings.ToLower(strings.TrimSpace(name))]
	return p, ok
}

// Default returns the default preset (Claude Code).
func (r *PresetRegistry) Default() Preset { return r.presets[r.def] }

// Names returns the registered preset names, sorted.
func (r *PresetRegistry) Names() []string {
	names := make([]string, 0, len(r.presets))
	for n := range r.presets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
