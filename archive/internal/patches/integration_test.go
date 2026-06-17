//go:build integration

// Build-tag-gated live integration test. Run with:
//
//	go test -tags=integration ./internal/patches/... \
//	  -run TestSynthesizeLive -timeout=2m
//
// Requires:
//
//	GOOGLE_CLOUD_PROJECT, GOOGLE_CLOUD_LOCATION, ORION_LLM_MODEL
//	(LLM credentials only; the test does NOT call Polaris.)
//
// Builds three synthetic Gaps mirroring the fixture's planted gaps
// (timeout, retry, idempotency) and exercises the synthesizer
// end-to-end. Asserts that each gap produces a valid CandidatePatch
// matching the per-pattern grammar.
package patches

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/enrichment"
	"github.com/revelara-ai/orion/internal/llm"
	"github.com/revelara-ai/orion/internal/polaris"
)

func TestSynthesizeLive(t *testing.T) {
	if os.Getenv("GOOGLE_CLOUD_PROJECT") == "" || os.Getenv("GOOGLE_CLOUD_LOCATION") == "" {
		t.Skip("integration test requires GOOGLE_CLOUD_PROJECT and GOOGLE_CLOUD_LOCATION")
	}
	cfg, err := llm.LoadFromEnv()
	if err != nil {
		t.Skipf("llm config: %v", err)
	}
	if cfg.Model == "" {
		t.Skip("ORION_LLM_MODEL must be set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	gen, err := llm.NewClient(ctx, cfg)
	if err != nil {
		t.Skipf("NewClient: %v", err)
	}
	defer func() { _ = gen.Close() }()

	gaps := []Gap{
		{
			ID: "g-timeout", Pattern: PatternTimeout,
			FilePath: "client.go", LineRange: [2]int{10, 14},
			Description: "outbound HTTP call without context timeout",
			CodeExcerpt: "func Fetch(ctx context.Context, url string) ([]byte, error) {\n    resp, err := http.Get(url)\n    if err != nil { return nil, err }\n    return io.ReadAll(resp.Body)\n}",
		},
		{
			ID: "g-retry", Pattern: PatternRetry,
			FilePath: "retry.go", LineRange: [2]int{1, 12},
			Description: "retry loop without exponential backoff",
			CodeExcerpt: "func Try(do func() error) error {\n    for i := 0; i < 5; i++ {\n        if err := do(); err == nil { return nil }\n        time.Sleep(time.Second)\n    }\n    return errors.New(\"out of retries\")\n}",
		},
		{
			ID: "g-idemp", Pattern: PatternIdempotency,
			FilePath: "handler.go", LineRange: [2]int{1, 8},
			Description: "POST endpoint without Idempotency-Key handling",
			CodeExcerpt: "func Post(w http.ResponseWriter, r *http.Request) {\n    var p Payment\n    json.NewDecoder(r.Body).Decode(&p)\n    process(p)\n    w.WriteHeader(http.StatusOK)\n}",
		},
	}
	ctxBlock := &enrichment.IssueContextBlock{
		Controls: []polaris.Control{
			{ControlCode: "RC-TIMEOUT-1", Name: "Outbound timeouts", Objective: "Every outbound call must have a context timeout."},
		},
		KnowledgeInsights: []polaris.KnowledgeInsight{
			{Title: "Timeout discipline", Body: "Use context.WithTimeout for all outbound HTTP requests."},
		},
	}

	synth := NewSynthesizer(gen, cfg.Model, 0)
	results, errs := synth.Synthesize(ctx, gaps, ctxBlock, SynthesizeOptions{})
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3", len(results))
	}
	failures := 0
	for i, r := range results {
		if errs[i] != nil {
			t.Logf("gap %s: %v", gaps[i].ID, errs[i])
			failures++
			continue
		}
		if r.GapID != gaps[i].ID {
			t.Errorf("result %d gap_id mismatch: %q vs %q", i, r.GapID, gaps[i].ID)
		}
		if r.Pattern != gaps[i].Pattern {
			t.Errorf("result %d pattern mismatch: %q vs %q", i, r.Pattern, gaps[i].Pattern)
		}
		if r.UnifiedDiff == "" {
			t.Errorf("result %d empty diff", i)
			continue
		}
		if !strings.HasPrefix(r.UnifiedDiff, "--- ") {
			t.Errorf("result %d missing --- header", i)
		}
		if r.LLMModel == "" {
			t.Errorf("result %d missing LLMModel", i)
		}
	}
	if failures == len(gaps) {
		t.Fatalf("all %d gaps failed; LLM may not be returning grammar-conformant diffs", len(gaps))
	}
	if errors.Is(errs[0], ErrSynthesisFailed) {
		t.Errorf("hard LLM failure on first gap; check Vertex auth")
	}
}
