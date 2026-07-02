package polaris

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config is the persisted, TUI-editable revelara.ai client config — chiefly the MCP endpoint the
// developer set via `/mcp set` (or `orion login --mcp-url`). It is separate from the token so the
// endpoint can be configured BEFORE authenticating; the env var still overrides it. Stored 0600 in
// the credentials dir, outside the Context Store and unreachable from the sandbox.
type Config struct {
	MCPURL string `json:"mcp_url,omitempty"`
}

// ConfigStore persists Config to a 0600 file in the credentials dir.
type ConfigStore struct{ dir string }

// NewConfigStore stores config under dir (created 0700) — the same credentials dir as the token.
func NewConfigStore(dir string) (*ConfigStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("polaris: config dir: %w", err)
	}
	return &ConfigStore{dir: dir}, nil
}

// Path is the config file path.
func (s *ConfigStore) Path() string { return filepath.Join(s.dir, "config.json") }

// Load reads the config, returning a zero Config when none is stored.
func (s *ConfigStore) Load() (Config, error) {
	b, err := os.ReadFile(s.Path())
	if os.IsNotExist(err) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("polaris: read config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, fmt.Errorf("polaris: parse config: %w", err)
	}
	return c, nil
}

// Save writes the config 0600, atomically (temp + rename).
func (s *ConfigStore) Save(c Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.Path() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("polaris: write config: %w", err)
	}
	if err := os.Rename(tmp, s.Path()); err != nil {
		return fmt.Errorf("polaris: persist config: %w", err)
	}
	return nil
}

// DefaultMCPURL is the production revelara.ai MCP endpoint (docs/runbooks/mcp-production-setup.md).
const DefaultMCPURL = "https://api.revelara.ai/mcp"

// ResolveMCPURL picks the revelara.ai MCP endpoint in priority order: an explicit env override
// (ORION_POLARIS_MCP_URL, passed in), then the persisted config, then the token's own endpoint (an
// OAuth token carries the MCP endpoint it was issued for as its BaseURL), then the production default.
func ResolveMCPURL(envURL string, cfg Config, tok Token) string {
	switch {
	case envURL != "":
		return envURL
	case cfg.MCPURL != "":
		return cfg.MCPURL
	case tok.BaseURL != "":
		return tok.BaseURL
	default:
		return DefaultMCPURL
	}
}
