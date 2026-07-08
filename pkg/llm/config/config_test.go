package config

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/pkg/llm"
)

func TestDefaultRegistry(t *testing.T) {
	cfg := Default()
	for _, name := range []string{"anthropic", "ollama", "lmstudio", "gemini"} {
		if _, ok := cfg.Providers[name]; !ok {
			t.Errorf("default registry missing %q", name)
		}
	}
	if cfg.Providers["ollama"].BaseURL != "http://localhost:11434/v1" {
		t.Errorf("ollama base_url wrong: %q", cfg.Providers["ollama"].BaseURL)
	}
	if cfg.Providers["lmstudio"].BaseURL != "http://localhost:1234/v1" {
		t.Errorf("lmstudio base_url wrong: %q", cfg.Providers["lmstudio"].BaseURL)
	}
	if cfg.Providers["anthropic"].APIKeyEnv != "ANTHROPIC_API_KEY" {
		t.Errorf("anthropic api_key_env wrong: %q", cfg.Providers["anthropic"].APIKeyEnv)
	}
}

func TestParseMergesOverDefaults(t *testing.T) {
	cfg, err := Parse([]byte(`
model: lmstudio/qwen3-32b
providers:
  lmstudio:
    type: openai
    base_url: http://gpubox:1234/v1
    context_window: 32768
  openrouter:
    type: openai
    base_url: https://openrouter.ai/api/v1
    api_key_env: OPENROUTER_API_KEY
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "lmstudio/qwen3-32b" {
		t.Errorf("model = %q", cfg.Model)
	}
	if cfg.Providers["lmstudio"].BaseURL != "http://gpubox:1234/v1" {
		t.Errorf("user entry must override built-in: %q", cfg.Providers["lmstudio"].BaseURL)
	}
	if cfg.Providers["lmstudio"].ContextWindow != 32768 {
		t.Errorf("context_window not parsed: %d", cfg.Providers["lmstudio"].ContextWindow)
	}
	if _, ok := cfg.Providers["openrouter"]; !ok {
		t.Error("new user provider missing")
	}
	if _, ok := cfg.Providers["anthropic"]; !ok {
		t.Error("built-in anthropic must survive the merge")
	}
}

func TestParseMalformed(t *testing.T) {
	if _, err := Parse([]byte("model: [broken")); err == nil {
		t.Fatal("malformed YAML must error")
	}
}

func TestSplitRef(t *testing.T) {
	cases := []struct{ ref, wantProv, wantModel string }{
		{"lmstudio/qwen3-32b", "lmstudio", "qwen3-32b"},
		{"openrouter/meta-llama/llama-3.3-70b", "openrouter", "meta-llama/llama-3.3-70b"}, // first slash only
		{"claude-sonnet-5", "", "claude-sonnet-5"},
	}
	for _, c := range cases {
		p, m := SplitRef(c.ref)
		if p != c.wantProv || m != c.wantModel {
			t.Errorf("SplitRef(%q) = (%q,%q), want (%q,%q)", c.ref, p, m, c.wantProv, c.wantModel)
		}
	}
}

func TestBuild(t *testing.T) {
	cfg := Default()

	t.Run("openai local, no key needed", func(t *testing.T) {
		prov, name, model, err := Build(cfg, "lmstudio/qwen3-32b")
		if err != nil {
			t.Fatal(err)
		}
		if name != "lmstudio" || model != "qwen3-32b" || prov.Name() != "lmstudio" {
			t.Errorf("got %q %q %q", name, model, prov.Name())
		}
	})

	t.Run("unknown provider lists configured", func(t *testing.T) {
		_, _, _, err := Build(cfg, "nope/m")
		if err == nil || !strings.Contains(err.Error(), "anthropic") || !strings.Contains(err.Error(), "lmstudio") {
			t.Errorf("error must list configured providers: %v", err)
		}
	})

	t.Run("missing key env names the var", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "")
		_, _, _, err := Build(cfg, "anthropic/claude-opus-4-8")
		if err == nil || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
			t.Errorf("error must name the env var: %v", err)
		}
	})

	t.Run("anthropic with key, bare-model default provider", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "sk-test")
		prov, name, model, err := Build(cfg, "claude-sonnet-5") // no slash → anthropic
		if err != nil {
			t.Fatal(err)
		}
		if name != "anthropic" || model != "claude-sonnet-5" || prov.Name() != "anthropic" {
			t.Errorf("got %q %q %q", name, model, prov.Name())
		}
	})

	t.Run("empty ref falls back to cfg.Model then built-in default", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "sk-test")
		_, name, model, err := Build(cfg, "")
		if err != nil {
			t.Fatal(err)
		}
		if name != "anthropic" || model != llm.DefaultAnthropicModel {
			t.Errorf("default ref wrong: %q %q", name, model)
		}
	})

	t.Run("openai without base_url errors", func(t *testing.T) {
		bad := Default()
		bad.Providers["broken"] = Provider{Type: "openai"}
		_, _, _, err := Build(bad, "broken/m")
		if err == nil || !strings.Contains(err.Error(), "base_url") {
			t.Errorf("must demand base_url: %v", err)
		}
	})

	t.Run("gemini requires key", func(t *testing.T) {
		t.Setenv("GEMINI_API_KEY", "")
		_, _, _, err := Build(cfg, "gemini/gemini-2.5-pro")
		if err == nil || !strings.Contains(err.Error(), "GEMINI_API_KEY") {
			t.Errorf("must demand GEMINI_API_KEY: %v", err)
		}
	})

	t.Run("unknown type errors", func(t *testing.T) {
		bad := Default()
		bad.Providers["x"] = Provider{Type: "cohere"}
		_, _, _, err := Build(bad, "x/m")
		if err == nil || !strings.Contains(err.Error(), "cohere") {
			t.Errorf("must reject unknown type: %v", err)
		}
	})
}
