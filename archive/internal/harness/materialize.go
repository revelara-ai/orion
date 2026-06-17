package harness

import (
	"bytes"
	"errors"
	"fmt"
	"text/template"
)

// MaterializationPlan is the structured output of Materialize. It
// names what the operator (or the deferred K8s materializer) MUST
// create to run the harness: a per-run namespace, a NetworkPolicy
// denying egress except to harness-owned targets, and (in later
// epics) toxiproxy + testcontainers manifests.
//
// v1 emits the namespace + NetworkPolicy YAML. The toxiproxy and
// per-service test containers are deferred to the K8s-materializer
// follow-up issue (filed against Epic 5/9 by the orchestrator).
type MaterializationPlan struct {
	// Namespace is the canonical namespace name (mirrors Harness.Namespace).
	Namespace string

	// ManifestYAML is a multi-document YAML containing the namespace +
	// NetworkPolicy. Apply with `kubectl apply -f -`.
	ManifestYAML string
}

// namespaceTmpl is the K8s manifest template for the per-run sandbox.
// Egress is denied by default; allow rules for harness-internal
// targets are out of scope for v1 (the deferred K8s materializer
// adds them when toxiproxy + testcontainers land).
const namespaceTmpl = `---
apiVersion: v1
kind: Namespace
metadata:
  name: {{ .Namespace }}
  labels:
    orion.revelara.ai/run-id: {{ .RunID }}
    orion.revelara.ai/managed-by: orion-harness
---
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: deny-egress
  namespace: {{ .Namespace }}
spec:
  podSelector: {}
  policyTypes:
    - Egress
  egress: []
`

type tmplData struct {
	Namespace string
	RunID     string
}

// Materialize returns the v1 MaterializationPlan for h. Does NOT call
// kubectl. Live materialization is the deferred follow-up.
func Materialize(h *Harness) (*MaterializationPlan, error) {
	if h == nil {
		return nil, errors.New("harness: nil harness")
	}
	if h.Namespace == "" || !validNamespace.MatchString(h.Namespace) {
		return nil, fmt.Errorf("%w: namespace %q invalid", ErrMaterialization, h.Namespace)
	}
	t, err := template.New("ns").Parse(namespaceTmpl)
	if err != nil {
		return nil, fmt.Errorf("%w: parse template: %v", ErrMaterialization, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, tmplData{Namespace: h.Namespace, RunID: h.RunID}); err != nil {
		return nil, fmt.Errorf("%w: execute template: %v", ErrMaterialization, err)
	}
	return &MaterializationPlan{
		Namespace:    h.Namespace,
		ManifestYAML: buf.String(),
	}, nil
}

// TeardownPlan is the symmetric output of Teardown. v1 emits the
// `kubectl delete namespace` invocation; the deferred K8s materializer
// will execute it.
type TeardownPlan struct {
	Namespace      string
	DeleteCommand  string
	GraceSeconds   int
	ForceOnTimeout bool
}

// Teardown returns the v1 TeardownPlan for h.
func Teardown(h *Harness) (*TeardownPlan, error) {
	if h == nil {
		return nil, errors.New("harness: nil harness")
	}
	if h.Namespace == "" || !validNamespace.MatchString(h.Namespace) {
		return nil, fmt.Errorf("%w: namespace %q invalid", ErrMaterialization, h.Namespace)
	}
	return &TeardownPlan{
		Namespace:      h.Namespace,
		DeleteCommand:  fmt.Sprintf("kubectl delete namespace %s", h.Namespace),
		GraceSeconds:   30,
		ForceOnTimeout: true,
	}, nil
}
