package proof

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/revelara-ai/orion/internal/delivery"
	"github.com/revelara-ai/orion/internal/proof/truthalign"
	"github.com/revelara-ai/orion/internal/reliabilitytier"
	"github.com/revelara-ai/orion/internal/sandbox"
)

// TestHardcodedSecretBlocksDeliveryBar: an artifact with a hardcoded secret is
// flagged by the secret scan and escalated by the deployment bar — even on an
// otherwise-Accept verdict. A clean artifact passes the gate.
func TestHardcodedSecretBlocksDeliveryBar(t *testing.T) {
	// Clean artifact (Orion-generated) → no secrets → bar can deliver.
	clean := t.TempDir()
	if _, err := sandbox.GenerateFixtureService(clean, sandbox.GenSpec{Route: "/time", Port: 8080, Format: "json", TimeZone: "UTC"}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if !SecurityClean(clean) {
		t.Fatalf("clean artifact flagged secrets: %v", SecretScan(clean))
	}

	// Artifact with a hardcoded secret.
	dirty := t.TempDir()
	_ = os.WriteFile(filepath.Join(dirty, "go.mod"), []byte("module d\n\ngo 1.25\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dirty, "main.go"), []byte(`package main
const apiKey = "sk_live_DEADBEEFCAFE1234"
func main(){ _ = apiKey }
`), 0o644)
	secrets := SecretScan(dirty)
	if len(secrets) == 0 {
		t.Fatal("hardcoded secret not detected")
	}

	// The bar escalates (does not deliver) when the security gate fails, even with
	// a full Accept verdict.
	env := delivery.OperatingEnvelope{}
	res := delivery.EvaluateBar(truthalign.Accept,
		[]string{"behavioral", "empirical", "hazard"},
		reliabilitytier.PolicyFor(reliabilitytier.Standard), env, SecurityClean(dirty))
	if res.Decision != delivery.Escalate {
		t.Fatalf("hardcoded secret must block the bar; got %s", res.Decision)
	}
}
