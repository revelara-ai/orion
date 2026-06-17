package harness

import (
	"errors"
	"strings"
	"testing"
)

func TestMaterializeProducesNamespaceAndPolicy(t *testing.T) {
	h, err := Synthesize(SynthesizeOptions{RunID: "abc123", Model: sampleModel(), Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Materialize(h)
	if err != nil {
		t.Fatalf("Materialize: %v", err)
	}
	if plan.Namespace != "orion-run-abc123" {
		t.Errorf("Namespace = %q", plan.Namespace)
	}
	must := []string{
		"kind: Namespace",
		"name: orion-run-abc123",
		"orion.revelara.ai/run-id: abc123",
		"kind: NetworkPolicy",
		"name: deny-egress",
		"policyTypes:",
		"- Egress",
		"egress: []",
	}
	for _, s := range must {
		if !strings.Contains(plan.ManifestYAML, s) {
			t.Errorf("manifest missing %q\n--- manifest ---\n%s", s, plan.ManifestYAML)
		}
	}
}

func TestMaterializeRejectsInvalidNamespace(t *testing.T) {
	h := &Harness{Namespace: "Invalid Caps"}
	_, err := Materialize(h)
	if !errors.Is(err, ErrMaterialization) {
		t.Errorf("expected ErrMaterialization, got %v", err)
	}
}

func TestMaterializeRejectsNil(t *testing.T) {
	if _, err := Materialize(nil); err == nil {
		t.Error("expected error for nil harness")
	}
}

func TestTeardownReturnsDeleteCommand(t *testing.T) {
	h, _ := Synthesize(SynthesizeOptions{RunID: "abc123", Model: sampleModel(), Seed: 1})
	plan, err := Teardown(h)
	if err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	if !strings.Contains(plan.DeleteCommand, "kubectl delete namespace orion-run-abc123") {
		t.Errorf("DeleteCommand = %q", plan.DeleteCommand)
	}
	if plan.GraceSeconds <= 0 {
		t.Errorf("GraceSeconds = %d", plan.GraceSeconds)
	}
}

func TestTeardownRejectsNil(t *testing.T) {
	if _, err := Teardown(nil); err == nil {
		t.Error("expected error for nil harness")
	}
}
