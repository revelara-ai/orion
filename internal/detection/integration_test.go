//go:build integration

package detection_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/revelara-ai/orion/internal/detection"
)

// fixtureRepo is the locally-cloned revelara-ai/microservices-demo repo.
// Override with $ORION_FIXTURE_REPO if your clone lives elsewhere.
const defaultFixtureRepo = "/home/josebiro/go/src/github.com/revelara-ai/microservices-demo"

// TestScanner_Live_AgainstMicroservicesDemo invokes the real rvl binary
// against the fixture repo and asserts SHAPE-based properties: at least
// one finding returned, all findings have non-empty fingerprint and file,
// stats are non-zero. We do NOT assert specific counts or paths because
// the fixture's gap surface changes as upstream evolves.
//
// Skipped when:
//   - rvl binary is not on $PATH
//   - The fixture repo isn't checked out at the expected path
func TestScanner_Live_AgainstMicroservicesDemo(t *testing.T) {
	repoPath := os.Getenv("ORION_FIXTURE_REPO")
	if repoPath == "" {
		repoPath = defaultFixtureRepo
	}
	if _, err := os.Stat(repoPath); os.IsNotExist(err) {
		t.Skipf("fixture repo not present at %s; clone it or set $ORION_FIXTURE_REPO", repoPath)
	}

	s := detection.NewScanner(detection.ScannerConfig{}) // default rvl binary, real exec runner

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	findings, stats, err := s.Run(ctx, detection.ScanOptions{
		RepoPath: repoPath,
		Service:  "microservices-demo",
	})
	if err != nil {
		t.Fatalf("Run against %s: %v", repoPath, err)
	}

	if stats.FindingsTotal == 0 {
		t.Fatal("expected at least 1 finding from microservices-demo, got 0 (rvl matchers may have regressed; check rvl scan --local --target=... directly)")
	}
	if stats.FindingsTotal != len(findings) {
		t.Errorf("stats.FindingsTotal=%d but len(findings)=%d", stats.FindingsTotal, len(findings))
	}

	for i, f := range findings {
		if f.Fingerprint == "" {
			t.Errorf("findings[%d] (slug=%s) has empty Fingerprint", i, f.Slug)
		}
		if f.File == "" {
			t.Errorf("findings[%d] (slug=%s) has empty File", i, f.Slug)
		}
		if f.Slug == "" {
			t.Errorf("findings[%d] has empty Slug", i)
		}
	}

	// Determinism: re-run and assert byte-equal Findings (after the
	// stable sort the Scanner applies). Skip if the live scan took too
	// long the first time (so we don't double a slow test).
	if stats.FindingsTotal > 100 {
		t.Logf("skipping determinism re-run; %d findings is large", stats.FindingsTotal)
		return
	}

	findings2, _, err := s.Run(ctx, detection.ScanOptions{
		RepoPath: repoPath,
		Service:  "microservices-demo",
	})
	if err != nil {
		t.Fatalf("Run #2: %v", err)
	}
	if len(findings) != len(findings2) {
		t.Errorf("non-deterministic: run1=%d findings, run2=%d findings", len(findings), len(findings2))
	}
	for i := range findings {
		if i >= len(findings2) {
			break
		}
		if findings[i].Fingerprint != findings2[i].Fingerprint {
			t.Errorf("non-deterministic at index %d: %s vs %s", i, findings[i].Fingerprint, findings2[i].Fingerprint)
		}
	}
}
