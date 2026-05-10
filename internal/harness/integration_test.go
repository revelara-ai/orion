//go:build integration

// Build-tag-gated live K8s materialization test. Run with:
//
//	go test -tags=integration ./internal/harness/... \
//	  -run TestMaterializeAndTeardownLive -timeout=5m
//
// Requires:
//
//	ORION_K8S_CONTEXT  kubeconfig context for a writable test cluster
//	                   (typically minikube)
//	kubectl in PATH
//
// Skips cleanly when ORION_K8S_CONTEXT is unset.
//
// This test is a STUB for the deferred K8s materializer (filed as a
// follow-up issue against orion-sfp). It documents the contract the
// real materializer MUST satisfy:
//
//   1. `kubectl apply -f -` of plan.ManifestYAML succeeds
//   2. The namespace exists after apply
//   3. The NetworkPolicy denies egress (verified by a curl probe pod)
//   4. Teardown removes the namespace within plan.GraceSeconds
//
// Until the materializer ships, this test only verifies (1) and (2).
package harness

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestMaterializeAndTeardownLive(t *testing.T) {
	ctx := os.Getenv("ORION_K8S_CONTEXT")
	if ctx == "" {
		t.Skip("integration test requires ORION_K8S_CONTEXT")
	}
	if _, err := exec.LookPath("kubectl"); err != nil {
		t.Skip("kubectl not in PATH")
	}

	model := sampleModel()
	h, err := Synthesize(SynthesizeOptions{RunID: "ittest", Model: model, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := Materialize(h)
	if err != nil {
		t.Fatal(err)
	}

	tctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	apply := exec.CommandContext(tctx, "kubectl", "--context", ctx, "apply", "-f", "-") //#nosec G204 -- kubectl with operator-controlled context
	apply.Stdin = strings.NewReader(plan.ManifestYAML)
	var out bytes.Buffer
	apply.Stdout = &out
	apply.Stderr = &out
	if err := apply.Run(); err != nil {
		t.Fatalf("kubectl apply: %v\n%s", err, out.String())
	}

	// Cleanup at exit.
	defer func() {
		td, _ := Teardown(h)
		dctx, dcancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer dcancel()
		_ = exec.CommandContext(dctx, "kubectl", "--context", ctx, "delete", "namespace", td.Namespace, "--ignore-not-found").Run() //#nosec G204
	}()

	get := exec.CommandContext(tctx, "kubectl", "--context", ctx, "get", "namespace", h.Namespace, "-o", "name") //#nosec G204
	out.Reset()
	get.Stdout = &out
	if err := get.Run(); err != nil {
		t.Fatalf("kubectl get namespace: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), h.Namespace) {
		t.Errorf("namespace not present: %s", out.String())
	}

	policy := exec.CommandContext(tctx, "kubectl", "--context", ctx, "-n", h.Namespace, "get", "networkpolicy", "deny-egress", "-o", "name") //#nosec G204
	out.Reset()
	policy.Stdout = &out
	if err := policy.Run(); err != nil {
		t.Fatalf("kubectl get networkpolicy: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "deny-egress") {
		t.Errorf("NetworkPolicy not present: %s", out.String())
	}
}
