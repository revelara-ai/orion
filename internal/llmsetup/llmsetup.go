// Package llmsetup is the Orion-specific glue over the host-agnostic
// pkg/llm/config facility: it resolves ~/.orion/config.yaml, applies the
// ORION_MODEL env precedence (env > config model > built-in default), and
// hands the rest of Orion a ready llm.Provider. This is the ONLY place that
// policy lives — pkg/ stays publishable and Orion-free.
package llmsetup

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/revelara-ai/orion/pkg/llm"
	"github.com/revelara-ai/orion/pkg/llm/config"
)

// Brain is the resolved model selection. Provider == nil means Orion runs the
// offline deterministic conductor; Reason says why (missing key, bad config).
type Brain struct {
	Provider     llm.Provider
	ProviderName string // registry name, e.g. "anthropic", "lmstudio"
	Model        string // model id without the provider prefix
	Ref          string // "provider/model"
	Reason       string // set when Provider is nil
}

func configPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".orion", "config.yaml")
}

// loadConfig reads ~/.orion/config.yaml; a missing file is the zero-config
// path (defaults), but a MALFORMED file is an error the user must see — never
// silently fall back as if their config didn't exist.
func loadConfig() (config.Config, error) {
	p := configPath()
	if p == "" {
		return config.Default(), nil
	}
	cfg, err := config.LoadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return config.Default(), nil
	}
	if err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

// Select resolves the active brain: ORION_MODEL > config model > built-in
// default (anthropic). Build errors (missing key, unknown provider) come back
// as an offline Brain with the reason — callers fall back to the
// deterministic conductor exactly as they did pre-config.
func Select() Brain {
	cfg, err := loadConfig()
	if err != nil {
		return Brain{Reason: "config error: " + err.Error()}
	}
	ref := strings.TrimSpace(os.Getenv("ORION_MODEL"))
	prov, name, model, err := config.Build(cfg, ref)
	if err != nil {
		return Brain{Reason: err.Error()}
	}
	return Brain{Provider: prov, ProviderName: name, Model: model, Ref: name + "/" + model}
}

// Rebuild constructs a provider for a /model switch. A bare model id stays on
// the current provider; a "provider/model" ref switches providers. Returns
// the provider and its full ref.
func Rebuild(current Brain, arg string) (llm.Provider, string, error) {
	cfg, err := loadConfig()
	if err != nil {
		return nil, "", err
	}
	ref := strings.TrimSpace(arg)
	if !strings.Contains(ref, "/") && current.ProviderName != "" {
		ref = current.ProviderName + "/" + ref
	}
	prov, name, model, err := config.Build(cfg, ref)
	if err != nil {
		return nil, "", err
	}
	return prov, name + "/" + model, nil
}

// ListModels aggregates Models() across all configured providers as
// "provider/model" refs, best-effort: providers that can't be built (missing
// key) or don't answer within the per-provider timeout are skipped.
func ListModels(ctx context.Context) []string {
	cfg, err := loadConfig()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(cfg.Providers))
	for n := range cfg.Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	var out []string
	for _, n := range names {
		prov, _, _, err := config.Build(cfg, n+"/")
		if err != nil {
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		ms, err := prov.Models(pctx)
		cancel()
		if err != nil {
			continue
		}
		for _, m := range ms {
			out = append(out, n+"/"+m.ID)
		}
	}
	return out
}
