package agentruntime

import (
	"slices"
	"testing"
)

// TestAgentPresetClaudeDefault: the registry default is Claude Code with native
// ACP, the `claude` launch command, and the Max/Pro-login env allowlist.
func TestAgentPresetClaudeDefault(t *testing.T) {
	r := DefaultPresetRegistry()

	def := r.Default()
	if def.Name != "claude" {
		t.Fatalf("default preset = %q, want claude", def.Name)
	}
	got, ok := r.Get("claude")
	if !ok {
		t.Fatal("claude preset not registered")
	}
	if got.ACPMode != ACPNative {
		t.Fatalf("claude ACP mode = %q, want native", got.ACPMode)
	}
	// Native ACP: launch argv is just the command (no activation token).
	if argv := got.LaunchArgs(); len(argv) != 1 || argv[0] != "claude" {
		t.Fatalf("claude launch argv = %v, want [claude]", argv)
	}
	// Max/Pro login + optional overrides are on the allowlist.
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY"} {
		if !got.Allows(k) {
			t.Fatalf("claude preset should allow env key %q", k)
		}
	}
	if got.ProcessName != "claude" {
		t.Fatalf("claude process name = %q", got.ProcessName)
	}
	// claude is discoverable by name.
	if !slices.Contains(r.Names(), "claude") {
		t.Fatal("Names() omits claude")
	}
}

// TestPresetEnvAndACPMode: ACP activation differs by mode, and env injection is
// strictly the allowlist intersected with the source — never the ambient host env.
func TestPresetEnvAndACPMode(t *testing.T) {
	r := DefaultPresetRegistry()

	// ACP mode → activation token in argv.
	cases := []struct {
		name      string
		wantMode  ACPMode
		wantToken string // expected argv[1] for non-native
	}{
		{"claude", ACPNative, ""},
		{"gemini", ACPFlag, "--acp"},
		{"codex", ACPSubcommand, "acp"},
	}
	for _, c := range cases {
		p, ok := r.Get(c.name)
		if !ok {
			t.Fatalf("%s preset missing", c.name)
		}
		if p.ACPMode != c.wantMode {
			t.Fatalf("%s ACP mode = %q, want %q", c.name, p.ACPMode, c.wantMode)
		}
		argv := p.LaunchArgs()
		if c.wantToken == "" {
			if len(argv) != 1 {
				t.Fatalf("%s native argv = %v, want [%s]", c.name, argv, c.name)
			}
		} else {
			if len(argv) < 2 || argv[1] != c.wantToken {
				t.Fatalf("%s argv = %v, want activation token %q at [1]", c.name, argv, c.wantToken)
			}
		}
	}

	// Env injection: only allowlisted keys present in source are passed; a real
	// secret that is NOT on the allowlist must never leak to the agent.
	claude, _ := r.Get("claude")
	source := map[string]string{
		"CLAUDE_CONFIG_DIR": "/home/dev/.claude",
		"ANTHROPIC_API_KEY": "sk-ant-xxx",
		"AWS_SECRET_KEY":    "leak-me",  // NOT on the allowlist
		"PATH":              "/usr/bin", // ambient — not allowlisted
	}
	env := claude.Env(source)
	if !slices.Contains(env, "CLAUDE_CONFIG_DIR=/home/dev/.claude") {
		t.Fatalf("allowlisted key not injected: %v", env)
	}
	if !slices.Contains(env, "ANTHROPIC_API_KEY=sk-ant-xxx") {
		t.Fatalf("allowlisted override not injected: %v", env)
	}
	for _, e := range env {
		if len(e) >= 14 && e[:14] == "AWS_SECRET_KEY" {
			t.Fatalf("non-allowlisted secret leaked to agent env: %v", env)
		}
		if len(e) >= 4 && e[:4] == "PATH" {
			t.Fatalf("ambient host env leaked to agent: %v", env)
		}
	}
	// An allowlisted key absent from source is simply omitted (no fabrication).
	if slices.ContainsFunc(env, func(s string) bool { return len(s) >= 18 && s[:18] == "ANTHROPIC_BASE_URL" }) {
		t.Fatalf("absent key fabricated: %v", env)
	}
}

// TestUnknownPresetNotFound.
func TestUnknownPresetNotFound(t *testing.T) {
	r := DefaultPresetRegistry()
	if _, ok := r.Get("nonexistent"); ok {
		t.Fatal("unknown preset should not resolve")
	}
}
