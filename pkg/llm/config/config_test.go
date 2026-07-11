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

// TestBuildAcceptsValidAPIKeyEnvNames: legitimate env-var NAMES
// ([A-Za-z_][A-Za-z0-9_]*) must keep working — the grammar check has zero
// false positives. [or-yga9]
func TestBuildAcceptsValidAPIKeyEnvNames(t *testing.T) {
	for _, name := range []string{"MY_KEY_2", "_PRIVATE_KEY", "lowercase_key"} {
		t.Setenv(name, "some-secret-value")
		cfg := Default()
		cfg.Providers["p"] = Provider{Type: "openai", BaseURL: "http://localhost:1/v1", APIKeyEnv: name}
		if _, _, _, err := Build(cfg, "p/m"); err != nil {
			t.Errorf("valid env name %q must pass: %v", name, err)
		}
	}
}

// TestBuildRefusesPastedKeyWithoutEcho: an api_key_env value that is not a
// plausible env-var NAME is refused BEFORE the os.Getenv lookup, and the
// error must never echo the value — Build errors flow into Brain.Reason,
// logs, and the TUI, so a pasted literal key in the message is a leak.
// Secret-shaped values additionally get the remove-and-rotate hint. [or-yga9]
func TestBuildRefusesPastedKeyWithoutEcho(t *testing.T) {
	pasted := []string{
		"sk-ant-api03-AbCdEf0123456789AbCdEf0123456789",       // Anthropic
		"sk-proj-AbCdEf0123456789AbCdEf0123456789",            // OpenAI
		"AIzaSyD-AbCdEf0123456789AbCdEf01234",                 // Google
		"AQ.AbCdEf0123456789AbCdEf0123456789",                 // AQ.-prefixed
		"xoxb-1234567890-AbCdEf0123456789AbCdEf",              // Slack
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.AbCdEf01234567", // JWT (dots)
	}
	for _, key := range pasted {
		cfg := Default()
		cfg.Providers["p"] = Provider{Type: "openai", BaseURL: "http://localhost:1/v1", APIKeyEnv: key}
		_, _, _, err := Build(cfg, "p/m")
		if err == nil {
			t.Fatalf("pasted key (prefix %q) must be refused", key[:4])
		}
		msg := err.Error()
		// The value must not appear anywhere in the error — check the whole
		// key and a distinctive tail substring.
		if strings.Contains(msg, key) || strings.Contains(msg, key[len(key)-12:]) {
			t.Errorf("error echoes the pasted key (prefix %q): %s", key[:4], msg)
		}
		if !strings.Contains(msg, "NAME of an environment variable") || !strings.Contains(msg, "GEMINI_API_KEY") {
			t.Errorf("error must explain api_key_env wants a NAME (e.g. GEMINI_API_KEY): %s", msg)
		}
		if !strings.Contains(msg, "ROTATE") {
			t.Errorf("secret-shaped value must carry the remove-and-rotate hint: %s", msg)
		}
	}
}

// TestBuildRefusesInvalidNameWithoutRotateHint: an invalid name that is NOT
// secret-shaped ("my key") is refused without the value and without the
// alarming rotate hint. [or-yga9]
func TestBuildRefusesInvalidNameWithoutRotateHint(t *testing.T) {
	cfg := Default()
	cfg.Providers["p"] = Provider{Type: "openai", BaseURL: "http://localhost:1/v1", APIKeyEnv: "my key"}
	_, _, _, err := Build(cfg, "p/m")
	if err == nil {
		t.Fatal("invalid env name must be refused")
	}
	msg := err.Error()
	if strings.Contains(msg, "my key") {
		t.Errorf("error must not echo the value: %s", msg)
	}
	if !strings.Contains(msg, "NAME of an environment variable") {
		t.Errorf("error must explain api_key_env wants a NAME: %s", msg)
	}
	if strings.Contains(msg, "ROTATE") {
		t.Errorf("non-secret-shaped value must NOT get the rotate hint: %s", msg)
	}
}

func TestLoadFile(t *testing.T) {
	// 1. Valid YAML file in t.TempDir() merging over Default()
	tmpDir := t.TempDir()
	validPath := filepath.Join(tmpDir, "valid.yaml")
	validContent := []byte(`
model: lmstudio/qwen3-32b
providers:
  lmstudio:
    type: openai
    base_url: http://gpubox:1234/v1
`)
	if err := os.WriteFile(validPath, validContent, 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFile(validPath)
	if err != nil {
		t.Fatalf("expected no error loading valid file, got: %v", err)
	}
	if cfg.Model != "lmstudio/qwen3-32b" {
		t.Errorf("expected merged model lmstudio/qwen3-32b, got %q", cfg.Model)
	}
	if _, ok := cfg.Providers["anthropic"]; !ok {
		t.Error("built-in anthropic must survive the merge in LoadFile")
	}

	// 2. A missing path returning fs.ErrNotExist error
	missingPath := filepath.Join(tmpDir, "nonexistent.yaml")
	_, err = LoadFile(missingPath)
	if err == nil {
		t.Error("expected error for missing file, got nil")
	} else if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("expected error to be fs.ErrNotExist, got: %v", err)
	}

	// 3. A malformed file returning an error with file path
	malformedPath := filepath.Join(tmpDir, "malformed.yaml")
	malformedContent := []byte("model: [broken")
	if err := os.WriteFile(malformedPath, malformedContent, 0644); err != nil {
		t.Fatal(err)
	}
	_, err = LoadFile(malformedPath)
	if err == nil {
		t.Error("expected error for malformed file, got nil")
	} else if !strings.Contains(err.Error(), malformedPath) {
		t.Errorf("expected error message to contain file path %q, got: %v", malformedPath, err)
	}
}
