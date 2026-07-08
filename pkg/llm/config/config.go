// Package config is the provider-selection facility for the pkg/llm module: a
// YAML schema of named providers plus "provider/model" refs, resolved into a
// constructed llm.Provider. It is deliberately host-agnostic — no fixed file
// path, no host env-var conventions (Orion's live in internal/llmsetup). API
// keys are referenced by env-var NAME only and never stored.
package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/revelara-ai/orion/pkg/llm"
)

// Provider is one named entry in the providers map.
type Provider struct {
	Type          string `yaml:"type"`           // anthropic | openai | gemini
	BaseURL       string `yaml:"base_url"`       // required for type openai
	APIKeyEnv     string `yaml:"api_key_env"`    // env var NAME holding the key
	ContextWindow int    `yaml:"context_window"` // for models that don't advertise one
	MaxTokens     int    `yaml:"max_tokens"`     // default output cap
}

// Config is the parsed configuration.
type Config struct {
	Model     string              `yaml:"model"` // default "provider/model" ref
	Providers map[string]Provider `yaml:"providers"`
}

// Default is the built-in registry: always-resolvable names covering the
// default cloud provider and the standard local endpoints. User config entries
// with the same name override these.
func Default() Config {
	return Config{
		Providers: map[string]Provider{
			"anthropic": {Type: "anthropic", APIKeyEnv: "ANTHROPIC_API_KEY"},
			"ollama":    {Type: "openai", BaseURL: "http://localhost:11434/v1"},
			"lmstudio":  {Type: "openai", BaseURL: "http://localhost:1234/v1"},
			"gemini":    {Type: "gemini", APIKeyEnv: "GEMINI_API_KEY"},
		},
	}
}

// Parse parses YAML and merges it over Default(): the user's model ref wins,
// and user provider entries override same-named built-ins.
func Parse(data []byte) (Config, error) {
	var user Config
	if err := yaml.Unmarshal(data, &user); err != nil {
		return Config{}, err
	}
	cfg := Default()
	if user.Model != "" {
		cfg.Model = user.Model
	}
	for name, p := range user.Providers {
		cfg.Providers[name] = p
	}
	return cfg, nil
}

// LoadFile reads and parses a config file. A missing file surfaces the
// os.ReadFile error unchanged so callers can branch on fs.ErrNotExist and fall
// back to Default().
func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	cfg, err := Parse(data)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// SplitRef splits a "provider/model" ref on the FIRST slash only — model ids
// may themselves contain slashes (OpenRouter's "meta-llama/llama-3.3-70b").
// A ref with no slash returns ("", ref).
func SplitRef(ref string) (provider, model string) {
	if i := strings.Index(ref, "/"); i >= 0 {
		return ref[:i], ref[i+1:]
	}
	return "", ref
}

// Build resolves ref against cfg and constructs the provider. An empty ref
// falls back to cfg.Model, then to the built-in Anthropic default. A bare
// model id (no slash) resolves against the "anthropic" provider for backward
// compatibility with ORION_MODEL=claude-….
func Build(cfg Config, ref string) (prov llm.Provider, name, model string, err error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = cfg.Model
	}
	if ref == "" {
		ref = "anthropic/" + llm.DefaultAnthropicModel
	}
	name, model = SplitRef(ref)
	if name == "" {
		name = "anthropic"
	}
	p, ok := cfg.Providers[name]
	if !ok {
		return nil, "", "", fmt.Errorf("unknown provider %q (configured: %s)", name, strings.Join(providerNames(cfg), ", "))
	}
	var key string
	if p.APIKeyEnv != "" {
		key = strings.TrimSpace(os.Getenv(p.APIKeyEnv))
		if key == "" {
			return nil, "", "", fmt.Errorf("provider %q: set %s", name, p.APIKeyEnv)
		}
	}
	switch p.Type {
	case "anthropic":
		if key == "" {
			return nil, "", "", fmt.Errorf("provider %q: type anthropic requires api_key_env", name)
		}
		if model == "" {
			model = llm.DefaultAnthropicModel
		}
		return llm.NewAnthropic(key, model).WithBaseURL(p.BaseURL), name, model, nil
	case "openai":
		if p.BaseURL == "" {
			return nil, "", "", fmt.Errorf("provider %q: type openai requires base_url", name)
		}
		return llm.NewOpenAI(llm.OpenAIConfig{
			Name: name, BaseURL: p.BaseURL, APIKey: key, Model: model,
			ContextWindow: p.ContextWindow, MaxOutput: p.MaxTokens,
		}), name, model, nil
	case "gemini":
		if key == "" {
			return nil, "", "", fmt.Errorf("provider %q: type gemini requires api_key_env", name)
		}
		return llm.NewGemini(llm.GeminiConfig{
			Name: name, APIKey: key, Model: model, BaseURL: p.BaseURL,
			ContextWindow: p.ContextWindow, MaxOutput: p.MaxTokens,
		}), name, model, nil
	default:
		return nil, "", "", fmt.Errorf("provider %q: unknown type %q (want anthropic, openai, or gemini)", name, p.Type)
	}
}

func providerNames(cfg Config) []string {
	names := make([]string, 0, len(cfg.Providers))
	for n := range cfg.Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
