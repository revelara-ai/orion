package llmsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setHome points HOME at a temp dir so ~/.orion/config.yaml is test-controlled.
func setHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

func writeConfig(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".orion")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSelectZeroConfigWithKey(t *testing.T) {
	setHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	t.Setenv("ORION_MODEL", "")
	b := Select()
	if b.Provider == nil {
		t.Fatalf("want native brain, got offline: %s", b.Reason)
	}
	if b.ProviderName != "anthropic" || !strings.HasPrefix(b.Ref, "anthropic/") {
		t.Errorf("zero-config must select anthropic: %+v", b)
	}
}

func TestSelectZeroConfigNoKeyIsOffline(t *testing.T) {
	setHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ORION_MODEL", "")
	b := Select()
	if b.Provider != nil {
		t.Fatal("no key + no config must be offline")
	}
	if !strings.Contains(b.Reason, "ANTHROPIC_API_KEY") {
		t.Errorf("reason must name the env var: %q", b.Reason)
	}
}

func TestSelectOrionModelEnvOverridesConfig(t *testing.T) {
	home := setHome(t)
	writeConfig(t, home, "model: lmstudio/qwen3-32b\n")
	t.Setenv("ORION_MODEL", "ollama/llama3.3")
	b := Select()
	if b.Provider == nil {
		t.Fatalf("offline: %s", b.Reason)
	}
	if b.ProviderName != "ollama" || b.Model != "llama3.3" {
		t.Errorf("ORION_MODEL must win over config model: %+v", b)
	}
}

func TestSelectConfigModel(t *testing.T) {
	home := setHome(t)
	writeConfig(t, home, "model: lmstudio/qwen3-32b\n")
	t.Setenv("ORION_MODEL", "")
	b := Select()
	if b.Provider == nil {
		t.Fatalf("offline: %s", b.Reason)
	}
	if b.Ref != "lmstudio/qwen3-32b" || b.Provider.Name() != "lmstudio" {
		t.Errorf("config model not honored: %+v", b)
	}
}

func TestSelectMalformedConfigIsOfflineWithReason(t *testing.T) {
	home := setHome(t)
	writeConfig(t, home, "model: [broken")
	b := Select()
	if b.Provider != nil {
		t.Fatal("malformed config must not silently fall back to defaults")
	}
	if !strings.Contains(b.Reason, "config") {
		t.Errorf("reason must mention the config problem: %q", b.Reason)
	}
}

func TestRebuildBareIDStaysOnProvider(t *testing.T) {
	setHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-test")
	cur := Brain{ProviderName: "anthropic", Model: "claude-opus-4-8"}
	prov, ref, err := Rebuild(cur, "claude-sonnet-5")
	if err != nil {
		t.Fatal(err)
	}
	if ref != "anthropic/claude-sonnet-5" || prov.Name() != "anthropic" {
		t.Errorf("bare id must stay on current provider: %s %s", ref, prov.Name())
	}
}

func TestRebuildRefSwitchesProvider(t *testing.T) {
	setHome(t)
	cur := Brain{ProviderName: "anthropic", Model: "claude-opus-4-8"}
	prov, ref, err := Rebuild(cur, "lmstudio/qwen3-32b")
	if err != nil {
		t.Fatal(err)
	}
	if ref != "lmstudio/qwen3-32b" || prov.Name() != "lmstudio" {
		t.Errorf("ref must switch provider: %s %s", ref, prov.Name())
	}
}

func TestRebuildUnknownProviderErrors(t *testing.T) {
	setHome(t)
	_, _, err := Rebuild(Brain{ProviderName: "anthropic"}, "nope/m")
	if err == nil {
		t.Fatal("unknown provider must error")
	}
}
