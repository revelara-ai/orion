package conductor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/revelara-ai/orion/internal/contextstore"
	"github.com/revelara-ai/orion/pkg/llm"
)

// TestRoleProviderPrecedenceAndFallback (or-kzf.4): env beats the reviewable
// models.yaml, which beats the session brain; an unbuildable ref never turns
// a role off; unrouted roles stay on the commodity default.
func TestRoleProviderPrecedenceAndFallback(t *testing.T) {
	fallback := &fakeLLM{}
	t.Setenv("HOME", t.TempDir()) // clean llmsetup config
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-routing")
	hdir := t.TempDir()
	t.Setenv("ORION_HARNESS_DIR", hdir)

	// Unrouted → commodity default.
	t.Setenv("ORION_MODEL_REVIEW", "")
	if got := RoleProvider("review", fallback); got != llm.Provider(fallback) {
		t.Fatal("an unrouted role must ride the session brain")
	}

	// File routing: reviewable models.yaml.
	if err := os.WriteFile(filepath.Join(hdir, "models.yaml"), []byte("roles:\n  review: anthropic/claude-cheap\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := RoleProvider("review", fallback)
	if got == llm.Provider(fallback) || got.Name() != "anthropic" {
		t.Fatalf("models.yaml must route the role, got %T", got)
	}

	// Env beats the file.
	t.Setenv("ORION_MODEL_REVIEW", "no-such-provider/x")
	if got := RoleProvider("review", fallback); got != llm.Provider(fallback) {
		t.Fatal("an unbuildable env ref must warn + fall back (env still beats the file)")
	}
}

// TestRoutingDecisionRecorded (or-kzf.4 DONE-WHEN): an effective re-route is
// recorded on the project for after-the-fact audit.
func TestRoutingDecisionRecorded(t *testing.T) {
	ctx := context.Background()
	store, pid := surfaceStore(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ANTHROPIC_API_KEY", "sk-test-routing")
	t.Setenv("ORION_HARNESS_DIR", t.TempDir())
	t.Setenv("ORION_MODEL_DISTILL", "anthropic/claude-cheap")

	if got := RoleProvider("distill", &fakeLLM{}); got == nil || got.Name() != "anthropic" {
		t.Fatalf("route must build, got %v", got)
	}
	RecordRoutingToStore(ctx, store)

	var payload string
	_ = store.WithTx(ctx, func(tx *contextstore.Tx) error {
		e, ok, err := tx.PolarisContext().Get(ctx, pid, "model_routing")
		if err == nil && ok {
			payload = e.Payload
		}
		return nil
	})
	var m map[string]string
	if err := json.Unmarshal([]byte(payload), &m); err != nil || m["distill"] == "" {
		t.Fatalf("the routing decision must be recorded on the project, got %q (%v)", payload, err)
	}
}
