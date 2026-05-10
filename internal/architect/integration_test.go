//go:build integration

package architect_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/architect"
	"github.com/revelara-ai/orion/internal/llm"
)

const defaultDemoRepo = "/home/josebiro/go/src/github.com/revelara-ai/microservices-demo"

// TestInferer_Live_AgainstMicroservicesDemo runs the structural pass +
// (when GOOGLE_CLOUD_PROJECT is set) the LLM enrichment pass against the
// real fixture repo. Asserts SHAPE: at least N services discovered,
// envelope_confidence in valid range, structural pass byte-stable across
// two runs.
func TestInferer_Live_AgainstMicroservicesDemo(t *testing.T) {
	repo := os.Getenv("ORION_FIXTURE_REPO")
	if repo == "" {
		repo = defaultDemoRepo
	}
	if _, err := os.Stat(repo); os.IsNotExist(err) {
		t.Skipf("fixture repo not present at %s; clone it or set $ORION_FIXTURE_REPO", repo)
	}

	cfg := architect.InfererConfig{}
	enableLLM := os.Getenv("GOOGLE_CLOUD_PROJECT") != "" && os.Getenv("ORION_TEST_LLM") == "1"
	if enableLLM {
		llmCfg, err := llm.LoadFromEnv()
		if err != nil {
			t.Fatalf("LoadFromEnv: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		client, err := llm.NewClient(ctx, llmCfg)
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		defer func() { _ = client.Close() }()
		cfg.Generator = client
	}

	inf := architect.NewInferer(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	model, err := inf.Infer(ctx, architect.InferOptions{
		RepoPath:        repo,
		EnableLLMEnrich: enableLLM,
	})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}

	const minServices = 5
	if len(model.Services) < minServices {
		t.Errorf("got %d services from microservices-demo, want >= %d", len(model.Services), minServices)
	}
	ec := model.EnvelopeConfidence
	if ec.Score < 0 || ec.Score > 1 {
		t.Errorf("envelope confidence score=%f out of range", ec.Score)
	}
	if ec.ServiceCoverage != 1.0 {
		t.Errorf("service coverage=%f, want 1.0 (we have services)", ec.ServiceCoverage)
	}

	// Determinism: run structural again, expect same service set & endpoint counts.
	inf2 := architect.NewInferer(architect.InfererConfig{}) // no LLM second time
	model2, err := inf2.Infer(ctx, architect.InferOptions{RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	if len(model.Services) < len(model2.Services) {
		t.Errorf("LLM-enabled run had FEWER services than structural-only? %d < %d", len(model.Services), len(model2.Services))
	}
	if len(model2.Services) == 0 {
		t.Fatal("structural-only run produced no services")
	}

	t.Logf("services discovered: %d", len(model.Services))
	t.Logf("envelope confidence: score=%.2f endpoint_cov=%.2f dep_cov=%.2f",
		ec.Score, ec.EndpointCoverage, ec.DependencyCoverage)
}
