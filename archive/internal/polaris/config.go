package polaris

import (
	"fmt"
	"os"
	"time"
)

// Config holds connection and credential config for one Polaris client.
//
// Required: BaseURL + APIKey. Both MUST come from env (POLARIS_BASE_URL,
// POLARIS_API_KEY) per SPEC §6.2; secrets in .revelara.yaml are forbidden.
type Config struct {
	// BaseURL is the Polaris API base URL, e.g., "https://polaris.revelara.ai".
	// No trailing slash; client appends "/api/v1/...".
	BaseURL string

	// APIKey is the per-tenant API key. Bearer-token auth.
	APIKey string //#nosec G117 -- env-only secret, never serialized to disk; orion never logs the field value

	// Timeout is the per-request HTTP timeout. Defaults to 30s.
	Timeout time.Duration

	// MaxRetries is the maximum number of retries on transient failures
	// (5xx, network). Defaults to 3.
	MaxRetries int

	// BaseDelay is the initial exponential-backoff delay. Doubles per
	// retry. Defaults to 250ms.
	BaseDelay time.Duration
}

const (
	defaultTimeout    = 30 * time.Second
	defaultMaxRetries = 3
	defaultBaseDelay  = 250 * time.Millisecond
)

// LoadFromEnv reads Config from POLARIS_BASE_URL and POLARIS_API_KEY.
func LoadFromEnv() (Config, error) {
	cfg := Config{
		BaseURL: os.Getenv("POLARIS_BASE_URL"),
		APIKey:  os.Getenv("POLARIS_API_KEY"),
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate returns ErrAuthMissing or ErrInvalidConfig as appropriate.
func (c Config) Validate() error {
	if c.BaseURL == "" {
		return fmt.Errorf("%w: POLARIS_BASE_URL is empty", ErrInvalidConfig)
	}
	if c.APIKey == "" {
		return fmt.Errorf("%w: POLARIS_API_KEY is empty", ErrAuthMissing)
	}
	return nil
}

func (c Config) effectiveTimeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return defaultTimeout
}

func (c Config) effectiveMaxRetries() int {
	if c.MaxRetries > 0 {
		return c.MaxRetries
	}
	return defaultMaxRetries
}

func (c Config) effectiveBaseDelay() time.Duration {
	if c.BaseDelay > 0 {
		return c.BaseDelay
	}
	return defaultBaseDelay
}
