package reliabilityscan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/internal/reliabilitytier"
	"github.com/revelara-ai/orion/internal/sandbox"
)

func writeArtifact(t *testing.T, dir, main string) {
	t.Helper()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module t\n\ngo 1.25\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte(main), 0o644)
}

// TestScanWritesRisks: scanning a deficient artifact surfaces risks (missing
// timeouts + hardcoded secret) and writes them to the register, retrievable.
func TestScanWritesRisks(t *testing.T) {
	ctx := context.Background()
	s, err := contextstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	var pid string
	_ = s.WithTx(ctx, func(tx *contextstore.Tx) error {
		pid, _ = tx.Projects().Create(ctx, "demo", "time service", "http-service")
		return nil
	})

	deficient := t.TempDir()
	writeArtifact(t, deficient, `package main
import "net/http"
const apiKey = "sk_live_DEADBEEF"
func main(){ http.ListenAndServe(":8080", nil) }
`)
	findings, err := ScanAndRecord(ctx, s, pid, deficient)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(findings) < 2 {
		t.Fatalf("expected multiple findings on a deficient artifact, got %d", len(findings))
	}
	hasSecret := false
	for _, f := range findings {
		if f.Detector == "rvl:security-supply-chain-pro" {
			hasSecret = true
		}
	}
	if !hasSecret {
		t.Fatal("hardcoded secret not detected")
	}

	// Risks are persisted + retrievable.
	loaded, err := LoadRisks(ctx, s, pid)
	if err != nil || len(loaded) != len(findings) {
		t.Fatalf("retrieved %d risks, want %d (err=%v)", len(loaded), len(findings), err)
	}
}

// TestConformingArtifactScansClean: the Orion-generated service (timeouts + UTC)
// has fewer findings; a secret-free, timeout-rich artifact is low-risk.
func TestConformingArtifactScansClean(t *testing.T) {
	dir := t.TempDir()
	if _, err := sandbox.GenerateTimeServiceFixture(dir, sandbox.GenSpec{Module: "orion-generated/svc", Route: "/time", Port: 8080, Format: "json", TimeZone: "UTC"}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	findings, err := ScanArtifact(dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, f := range findings {
		if f.Detector == "rvl:resilience-pro" || f.Detector == "rvl:security-supply-chain-pro" {
			t.Fatalf("conforming service should not trip %s: %s", f.Detector, f.Risk)
		}
	}
}

// TestScanFeedsTierClassification: a secret finding raises data sensitivity →
// Critical tier (scan informs rigor).
func TestScanFeedsTierClassification(t *testing.T) {
	findings := []Finding{{Detector: "rvl:security-supply-chain-pro", Risk: "secret", Severity: "high"}}
	tier := reliabilitytier.Classify(DeriveDimensions(findings))
	if tier != reliabilitytier.Critical {
		t.Fatalf("secret finding should classify Critical, got %s", tier)
	}
}
