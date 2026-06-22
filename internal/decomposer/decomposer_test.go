package decomposer

import (
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/completeness"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
)

func acceptedSpec(t *testing.T) spec.ExecutableSpec {
	t.Helper()
	answers := map[string]string{
		"response_format": "json", "timezone": "UTC", "port": "8080", "route": "/time",
		"scale_profile": "medium", "observability_signals": "logs", "oncall_escalation": "owner",
		"data_storage": "none", "slo_targets": "tier-default", "security_model": "untrusted", "dependencies": "none",
	}
	kinds := map[string]string{"scale_profile": "fallback_preset"}
	es, err := spec.Compile("Build an HTTP service that returns the current time.", answers, kinds,
		completeness.NewAnalyzer("http-service").Checklist(), nil)
	if err != nil {
		t.Fatalf("compile spec: %v", err)
	}
	return es
}

// TestEverySpecRequirementHasProofObligation: decomposition covers every spec
// requirement with at least one ProofObligation, and no task lacks one.
func TestEverySpecRequirementHasProofObligation(t *testing.T) {
	es := acceptedSpec(t)
	epic := Decompose(es, "http-service")

	if len(epic.Tasks) == 0 {
		t.Fatal("decomposition produced no tasks")
	}
	for _, task := range epic.Tasks {
		if strings.TrimSpace(task.ProofObligation) == "" {
			t.Fatalf("task %q has empty ProofObligation", task.Key)
		}
		if strings.TrimSpace(task.FileScope) == "" {
			t.Fatalf("task %q has empty file scope (needed for path leasing)", task.Key)
		}
	}
	if err := CoverageGate(es, epic); err != nil {
		t.Fatalf("coverage gate failed: %v", err)
	}
}

// TestStatedScaleDimensionProducesCapacityProofObligation: a stated scale
// dimension yields a capacity ProofObligation carrying the concrete threshold.
func TestStatedScaleDimensionProducesCapacityProofObligation(t *testing.T) {
	es := acceptedSpec(t)
	epic := Decompose(es, "http-service")

	var capacity *Task
	for i := range epic.Tasks {
		for _, c := range epic.Tasks[i].Covers {
			if c == string(completeness.DimScale) {
				capacity = &epic.Tasks[i]
			}
		}
	}
	if capacity == nil {
		t.Fatal("no task covers the scale dimension")
	}
	// medium preset → 1000 req/minute; the obligation must carry that concrete target.
	if !strings.Contains(capacity.ProofObligation, "1000") {
		t.Fatalf("capacity obligation lacks the concrete scale threshold: %q", capacity.ProofObligation)
	}
}

// TestCoverageGateDetectsGap: removing a covering task trips the coverage gate.
func TestCoverageGateDetectsGap(t *testing.T) {
	es := acceptedSpec(t)
	epic := Decompose(es, "http-service")
	// Drop the capacity task → scale becomes uncovered.
	var trimmed []Task
	for _, task := range epic.Tasks {
		if task.Key != "capacity" {
			trimmed = append(trimmed, task)
		}
	}
	if err := CoverageGate(es, Epic{Title: epic.Title, Tasks: trimmed}); err == nil {
		t.Fatal("coverage gate should fail when the scale dimension is uncovered")
	}
}

// TestDependenciesFormDAG: declared dependencies reference existing task keys
// (no dangling edges, no self-loops).
func TestDependenciesFormDAG(t *testing.T) {
	epic := Decompose(acceptedSpec(t), "http-service")
	keys := map[string]bool{}
	for _, task := range epic.Tasks {
		keys[task.Key] = true
	}
	for _, task := range epic.Tasks {
		for _, dep := range task.DependsOn {
			if dep == task.Key {
				t.Fatalf("task %q depends on itself", task.Key)
			}
			if !keys[dep] {
				t.Fatalf("task %q depends on unknown task %q", task.Key, dep)
			}
		}
	}
}
