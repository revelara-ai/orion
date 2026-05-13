package harness

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func testHarness() *Harness {
	return &Harness{
		RunID:        "11111111-1111-1111-1111-111111111111",
		Namespace:    "orion-run-test",
		Seed:         42,
		Thoroughness: ThoroughnessStandard,
		Workload: WorkloadConfig{
			Endpoints: []EndpointDist{
				{Service: "cart", Endpoint: "POST /cart", Weight: 1.0, PayloadBytes: 256},
				{Service: "checkout", Endpoint: "POST /checkout", Weight: 0.5},
			},
			TargetRPS:       100,
			DurationSeconds: 30,
		},
		Faults: FaultConfig{
			Faults: []FaultProfile{
				{TargetName: "payments", LatencyP50Ms: 50, LatencyP99Ms: 200, ErrorRate: 0.01},
			},
		},
		SynthesizedAt: time.Now(),
	}
}

func TestK8sMaterializer_Apply_CreatesAllObjects(t *testing.T) {
	client := fake.NewSimpleClientset()
	m := NewK8sMaterializer(client, "orion-worker:test")
	h := testHarness()

	mat, err := m.Apply(context.Background(), h)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if mat.Namespace != h.Namespace {
		t.Errorf("Namespace = %q; want %q", mat.Namespace, h.Namespace)
	}

	// Namespace
	if _, err := client.CoreV1().Namespaces().Get(context.Background(), h.Namespace, metav1.GetOptions{}); err != nil {
		t.Errorf("namespace not created: %v", err)
	}
	// NetworkPolicy
	if _, err := client.NetworkingV1().NetworkPolicies(h.Namespace).Get(context.Background(), "deny-egress", metav1.GetOptions{}); err != nil {
		t.Errorf("network policy not created: %v", err)
	}
	// Toxiproxy ConfigMap (1 fault profile -> 1 proxy)
	if _, err := client.CoreV1().ConfigMaps(h.Namespace).Get(context.Background(), "toxiproxy-config", metav1.GetOptions{}); err != nil {
		t.Errorf("toxiproxy config not created: %v", err)
	}
	// Toxiproxy Deployment
	if _, err := client.AppsV1().Deployments(h.Namespace).Get(context.Background(), "toxiproxy", metav1.GetOptions{}); err != nil {
		t.Errorf("toxiproxy deployment not created: %v", err)
	}
	// Two SUT deployments (cart, checkout)
	for _, svc := range []string{"cart", "checkout"} {
		if _, err := client.AppsV1().Deployments(h.Namespace).Get(context.Background(), svc, metav1.GetOptions{}); err != nil {
			t.Errorf("SUT deployment %q not created: %v", svc, err)
		}
	}
}

func TestK8sMaterializer_Apply_IsIdempotent(t *testing.T) {
	client := fake.NewSimpleClientset()
	m := NewK8sMaterializer(client, "img")
	h := testHarness()

	if _, err := m.Apply(context.Background(), h); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	// Second Apply must not error on AlreadyExists.
	if _, err := m.Apply(context.Background(), h); err != nil {
		t.Errorf("idempotent re-Apply failed: %v", err)
	}
}

func TestK8sMaterializer_Apply_RejectsBadNamespace(t *testing.T) {
	client := fake.NewSimpleClientset()
	m := NewK8sMaterializer(client, "img")
	h := testHarness()
	h.Namespace = "BAD NAME"
	_, err := m.Apply(context.Background(), h)
	if err == nil || !strings.Contains(err.Error(), "namespace") {
		t.Errorf("expected namespace validation error; got %v", err)
	}
}

func TestK8sMaterializer_Teardown_DeletesNamespace(t *testing.T) {
	client := fake.NewSimpleClientset()
	// Pre-create namespace so Delete finds it.
	_, _ = client.CoreV1().Namespaces().Create(context.Background(),
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "orion-run-x"}},
		metav1.CreateOptions{})
	m := NewK8sMaterializer(client, "img")
	if err := m.Teardown(context.Background(), &MaterializedHarness{Namespace: "orion-run-x"}); err != nil {
		t.Fatalf("Teardown: %v", err)
	}
	_, err := client.CoreV1().Namespaces().Get(context.Background(), "orion-run-x", metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("namespace still exists after teardown: %v", err)
	}
}

func TestK8sMaterializer_Teardown_AbsentNamespaceIsNoError(t *testing.T) {
	client := fake.NewSimpleClientset()
	m := NewK8sMaterializer(client, "img")
	err := m.Teardown(context.Background(), &MaterializedHarness{Namespace: "absent"})
	if err != nil {
		t.Errorf("Teardown on absent namespace returned error: %v", err)
	}
}

func TestBuildToxiproxyConfig_EmitsOneProxyPerFault(t *testing.T) {
	h := testHarness()
	cfg, err := BuildToxiproxyConfig(h)
	if err != nil {
		t.Fatalf("BuildToxiproxyConfig: %v", err)
	}
	if !strings.Contains(cfg, "payments") {
		t.Errorf("config missing target name: %s", cfg)
	}
	if !strings.Contains(cfg, "latency") {
		t.Errorf("config missing latency toxic: %s", cfg)
	}
}

func TestBuildToxiproxyConfig_NoFaultsReturnsEmptyArray(t *testing.T) {
	h := testHarness()
	h.Faults.Faults = nil
	cfg, err := BuildToxiproxyConfig(h)
	if err != nil {
		t.Fatalf("BuildToxiproxyConfig: %v", err)
	}
	if strings.TrimSpace(cfg) != "null" && strings.TrimSpace(cfg) != "[]" {
		t.Errorf("expected null or [] for empty faults; got %q", cfg)
	}
}

func TestDistinctServices(t *testing.T) {
	h := testHarness()
	got := distinctServices(h)
	if len(got) != 2 {
		t.Errorf("distinct services = %v; want 2", got)
	}
}
