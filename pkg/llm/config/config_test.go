package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
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

func TestLoadFile(t *testing.T) {
	// 1. A valid YAML file in t.TempDir() parses and merges over Default()
	t.Run("valid yaml", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "valid_config.yaml")
		content := []byte(`
model: lmstudio/qwen3-32b
providers:
  lmstudio:
    type: openai
    base_url: http://gpubox:1234/v1
`)
		if err := os.WriteFile(filePath, content, 0644); err != nil {
			t.Fatal(err)
		}

		cfg, err := LoadFile(filePath)
		if err != nil {
			t.Fatalf("LoadFile failed: %v", err)
		}
		if cfg.Model != "lmstudio/qwen3-32b" {
			t.Errorf("expected Model to be 'lmstudio/qwen3-32b', got %q", cfg.Model)
		}
		if cfg.Providers["lmstudio"].BaseURL != "http://gpubox:1234/v1" {
			t.Errorf("expected BaseURL to be 'http://gpubox:1234/v1', got %q", cfg.Providers["lmstudio"].BaseURL)
		}
		// check that default providers still exist (merged over Default())
		if _, ok := cfg.Providers["anthropic"]; !ok {
			t.Errorf("default provider 'anthropic' missing after merge")
		}
	})

	// 2. A missing path returns an error satisfying errors.Is(err, fs.ErrNotExist)
	t.Run("missing path", func(t *testing.T) {
		_, err := LoadFile("this_file_does_not_exist_at_all_123456789.yaml")
		if err == nil {
			t.Fatal("expected error for missing file, got nil")
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("expected error to satisfy errors.Is(err, fs.ErrNotExist), got %v", err)
		}
	})

	// 3. A malformed file returns an error containing the file path
	t.Run("malformed file", func(t *testing.T) {
		tmpDir := t.TempDir()
		filePath := filepath.Join(tmpDir, "malformed_config.yaml")
		content := []byte("model: [broken")
		if err := os.WriteFile(filePath, content, 0644); err != nil {
			t.Fatal(err)
		}

		_, err := LoadFile(filePath)
		if err == nil {
			t.Fatal("expected error for malformed file, got nil")
		}
		if !strings.Contains(err.Error(), filePath) {
			t.Errorf("expected error string to contain file path %q, got %q", filePath, err.Error())
		}
	})
}
