package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/agentruntime"
)

// agentPrefPath is the persisted coding-agent choice (or-wmv1): what /agent set
// writes and startup restores. The ORION_AGENT env var always wins over it.
func agentPrefPath(dataDir string) string { return filepath.Join(dataDir, "agent") }

// restoreAgentPref applies the persisted choice at startup when ORION_AGENT is
// unset — the explicit env var stays authoritative.
func restoreAgentPref(dataDir string) {
	if os.Getenv("ORION_AGENT") != "" || dataDir == "" {
		return
	}
	if b, err := os.ReadFile(agentPrefPath(dataDir)); err == nil {
		if v := strings.TrimSpace(string(b)); v != "" {
			_ = os.Setenv("ORION_AGENT", v)
		}
	}
}

// agentCommandText implements /agent (TUI) and `orion agent` (CLI):
//
//	show (default) — the active generation arm + every preset with PATH status
//	set <chain>    — validate against the preset registry, apply NOW (env), persist
//	clear          — back to the native provider; removes the persisted choice
func agentCommandText(arg string) string {
	dir, _ := doctorDataDir()
	fields := strings.Fields(arg)
	sub := "show"
	if len(fields) > 0 {
		sub = fields[0]
	}
	switch sub {
	case "show", "":
		return agentShowText()
	case "clear":
		_ = os.Unsetenv("ORION_AGENT")
		if dir != "" {
			_ = os.Remove(agentPrefPath(dir))
		}
		return "coding agent cleared — generation uses the native provider (or the fixture without one)"
	case "set":
		if len(fields) < 2 {
			return "usage: /agent set <preset[,preset…]> — e.g. /agent set claude  or  /agent set claude,gemini"
		}
		chain := fields[1]
		presets, ok := resolveAgentChain(chain, exec.LookPath)
		if !ok {
			return fmt.Sprintf("no usable agent in %q — presets are %s (the CLI must be installed and on PATH; check /agent show)", chain, presetNames())
		}
		_ = os.Setenv("ORION_AGENT", chain)
		if dir != "" {
			_ = os.WriteFile(agentPrefPath(dir), []byte(chain+"\n"), 0o600)
		}
		names := make([]string, 0, len(presets))
		for _, p := range presets {
			names = append(names, p.Name)
		}
		return fmt.Sprintf("coding agent set: %s (failover order) — active now and persisted; ORION_AGENT in the environment overrides this on future launches", strings.Join(names, " → "))
	default:
		return "usage: /agent [show|set <chain>|clear]"
	}
}

// agentShowText renders the active arm + preset availability.
func agentShowText() string {
	var b strings.Builder
	cur := os.Getenv("ORION_AGENT")
	switch {
	case cur != "":
		fmt.Fprintf(&b, "active: %s (vendor agent chain — spawned + driven over ACP)\n", cur)
	default:
		b.WriteString("active: native provider (no vendor agent configured)\n")
	}
	b.WriteString("presets (set with /agent set <name>):\n")
	for _, name := range []string{"claude", "gemini", "codex"} {
		p, _ := agentruntime.DefaultPresetRegistry().Get(name)
		if _, err := exec.LookPath(p.Command); err == nil {
			fmt.Fprintf(&b, "  %-7s ready (%s on PATH)\n", name, p.Command)
		} else {
			fmt.Fprintf(&b, "  %-7s not installed ('%s' not on PATH)\n", name, p.Command)
		}
	}
	b.WriteString("chains allowed: e.g. claude,gemini — failover advances on rate-limit/hang/refusal")
	return b.String()
}

// presetNames lists the registry's preset names for error messages.
func presetNames() string { return "claude | gemini | codex" }

// cmdAgent is the CLI mirror: `orion agent [show|set <chain>|clear]`.
func cmdAgent(args []string) int {
	fmt.Println(agentCommandText(strings.Join(args, " ")))
	return 0
}
