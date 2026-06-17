package llm

import (
	"fmt"
	"os"
)

const (
	// DefaultModel is the default Vertex / Gemini model name. Chosen for
	// cost/latency tradeoff during v1 development; override per-call or
	// via $ORION_LLM_MODEL.
	DefaultModel = "gemini-2.0-flash"
)

// Config holds the credentials and routing config for one LLM client.
//
// Required: ProjectID + Location for Vertex AI (preferred). Model name
// always required.
//
// Per SPEC §6.2: secrets MUST come from env or vault, never from a
// committed config file. LoadFromEnv is the canonical entry point.
type Config struct {
	// ProjectID is the GCP project for Vertex AI calls. Set via
	// $GOOGLE_CLOUD_PROJECT.
	ProjectID string

	// Location is the GCP region for Vertex AI calls. Set via
	// $GOOGLE_CLOUD_LOCATION (e.g., "us-central1").
	Location string

	// Model is the default model identifier. Set via $ORION_LLM_MODEL;
	// falls back to DefaultModel.
	Model string

	// Temperature is the default generation temperature. v1 defaults to
	// 0 (greedy) for maximal determinism; override per-call as needed.
	Temperature float32
}

// LoadFromEnv reads Config from the standard env vars and validates it.
func LoadFromEnv() (Config, error) {
	cfg := Config{
		ProjectID: os.Getenv("GOOGLE_CLOUD_PROJECT"),
		Location:  os.Getenv("GOOGLE_CLOUD_LOCATION"),
		Model:     os.Getenv("ORION_LLM_MODEL"),
	}
	if cfg.Model == "" {
		cfg.Model = DefaultModel
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate returns ErrAuthMissing or ErrInvalidConfig as appropriate.
func (c Config) Validate() error {
	if c.ProjectID == "" || c.Location == "" {
		return fmt.Errorf("%w: GOOGLE_CLOUD_PROJECT=%q GOOGLE_CLOUD_LOCATION=%q (both required)",
			ErrAuthMissing, c.ProjectID, c.Location)
	}
	if c.Model == "" {
		return fmt.Errorf("%w: model name is empty", ErrInvalidConfig)
	}
	return nil
}
