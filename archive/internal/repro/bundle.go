package repro

import (
	"fmt"
	"time"
)

// Bundle represents a runnable reproduction of an Orion run per §12.8.
type Bundle struct {
	PinnedSHA          string            `json:"pinned_sha"`
	HarnessSeed        int64             `json:"harness_seed"`
	LLMModel           string            `json:"llm_model"`
	LLMProvider        string            `json:"llm_provider"`
	LLMProviderSeed    string            `json:"llm_provider_seed,omitempty"`
	ContainerImageSHAs map[string]string `json:"container_image_shas"`
	CreatedAt          time.Time         `json:"created_at"`
}

// NewBundle creates a Bundle with default envelope values per §12.8.
func NewBundle() *Bundle {
	return &Bundle{
		ContainerImageSHAs: make(map[string]string),
		CreatedAt:          time.Now(),
	}
}

// WithPinnedSHA sets the pinned commit SHA for this bundle.
func (b *Bundle) WithPinnedSHA(sha string) *Bundle {
	b.PinnedSHA = sha
	return b
}

// WithHarnessSeed records the deterministic harness seed.
func (b *Bundle) WithHarnessSeed(seed int64) *Bundle {
	b.HarnessSeed = seed
	return b
}

// WithLLMModel documents which model and provider generated the patches.
func (b *Bundle) WithLLMModel(model, provider string, providerSeed string) *Bundle {
	b.LLMModel = model
	b.LLMProvider = provider
	b.LLMProviderSeed = providerSeed
	return b
}

// WithContainerImage adds a harness component with its pinned image SHA.
func (b *Bundle) WithContainerImage(name, sha string) *Bundle {
	b.ContainerImageSHAs[name] = sha
	return b
}

// Build assembles markdown per §12.8 describing the full supported runtime envelope plus honest caveats.
func (b *Bundle) Build() string {
	images := ""
	for name, sha := range b.ContainerImageSHAs {
		images = images + fmt.Sprintf("  %s: %s\n", name, sha)
	}

	return "# Orion Reproduction Bundle\n" +
		"\n## Supported runtime\n" +
		"x86_64 Linux with Docker 24+ and 16+ GB RAM\n" +
		"\n### Best-effort runtimes\n" +
		"- ARM64 Linux (Graviton, M-series Macs via emulation)\n" +
		"- podman-instead-of-Docker\n" +
		"- Alternative Linux distros\n" +
		"\n### Not supported\n" +
		"- Windows hosts\n" +
		"- Air-gapped networks (bundle pulls container images by SHA from public registries; air-gapped customers MUST mirror images first)\n" +
		"- Any runtime without Linux containers\n" +
		"\n## Pinned commit SHA\n" + b.PinnedSHA + "\n" +
		"\n## Harness seed\n" + fmt.Sprintf("%d", b.HarnessSeed) + "\n" +
		"\n## LLM model and provider seed (where available)\n" +
		"Model: " + b.LLMModel + " | Provider: " + b.LLMProvider + "\n" +
		"\n## Container image SHAs for harness components\n" + images +
		"\n## How to run the bundle\n" +
		"\n```\nbash\n./compatibility_check.sh  # Validates your runtime\ndocker compose up --build  # Start the harness\n```\n" +
		"\n## Honest caveats\n" +
		"LLM-provider nondeterminism and CPU contention may cause minor metric variation.\n" +
		'Reproduction is "behaviorally equivalent within reported CI bounds on the supported runtime," not "bit-identical."\n'
}

// compatibilityCheckScript returns a script that validates the runtime per §12.8.
func compatScript() string {
	return `#!/usr/bin/env bash
set -euo pipefail
echo "Checking Orion reproduction bundle prerequisites..."
if ! command -v docker &> /dev/null; then
  echo "ERROR: Docker is not installed" >&2
  exit 1
fi
if [[ "$(uname -m)" != "x86_64" ]]; then
  echo "WARN: Non-x86 Linux detected — best-effort only" >&2
fi
echo "Docker version: $(docker --version)"
echo "OK: All checks passed."
`
}
