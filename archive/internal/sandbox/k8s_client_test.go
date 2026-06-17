package sandbox

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestKubernetesPodCreator_Create_CreatesPod(t *testing.T) {
	client := fake.NewClientset()
	c := NewKubernetesPodCreator(client, "orion-worker:test")
	res, err := c.Create(context.Background(), PodCreateIntent{
		Namespace: "orion-run-x", PodName: "worker-1", WorkspaceKey: "wsk",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !res.Created {
		t.Error("Created = false; want true on first create")
	}
	pod, err := client.CoreV1().Pods("orion-run-x").Get(context.Background(), "worker-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("pod not created: %v", err)
	}
	if pod.Labels["orion.revelara.ai/workspace-key"] != "wsk" {
		t.Errorf("workspace-key label missing or wrong: %v", pod.Labels)
	}
}

func TestKubernetesPodCreator_DuplicateIsIdempotent(t *testing.T) {
	client := fake.NewClientset()
	c := NewKubernetesPodCreator(client, "img")
	intent := PodCreateIntent{Namespace: "ns", PodName: "p", WorkspaceKey: "k"}
	if _, err := c.Create(context.Background(), intent); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	res, err := c.Create(context.Background(), intent)
	if err != nil {
		t.Fatalf("duplicate Create returned error; want success-with-Created=false: %v", err)
	}
	if res.Created {
		t.Error("Created = true on duplicate; want false (idempotency)")
	}
}

func TestKubernetesNamespaceProvisioner_Provision(t *testing.T) {
	client := fake.NewClientset()
	p := NewKubernetesNamespaceProvisioner(client)
	runID := uuid.New()
	res, err := p.Provision(context.Background(), NamespaceSpec{
		RunID: runID, Name: RunNamespaceName(runID),
		CPULimit: "8", MemoryLimit: "16Gi",
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if !res.Created {
		t.Error("Created = false; want true")
	}
	ns, err := client.CoreV1().Namespaces().Get(context.Background(), res.Name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("namespace not in client: %v", err)
	}
	if ns.Labels["pod-security.kubernetes.io/enforce"] != "restricted" {
		t.Errorf("pod-security enforce label missing: %v", ns.Labels)
	}
}

func TestKubernetesNamespaceProvisioner_DuplicateIsIdempotent(t *testing.T) {
	client := fake.NewClientset()
	p := NewKubernetesNamespaceProvisioner(client)
	runID := uuid.New()
	spec := NamespaceSpec{RunID: runID, Name: RunNamespaceName(runID)}
	if _, err := p.Provision(context.Background(), spec); err != nil {
		t.Fatalf("first Provision: %v", err)
	}
	res, err := p.Provision(context.Background(), spec)
	if err != nil {
		t.Fatalf("duplicate Provision: %v", err)
	}
	if res.Created {
		t.Error("Created = true on duplicate")
	}
}

func TestKubernetesNamespaceProvisioner_DeleteAbsentIsNoError(t *testing.T) {
	client := fake.NewClientset()
	p := NewKubernetesNamespaceProvisioner(client)
	if err := p.Delete(context.Background(), "absent"); err != nil {
		t.Errorf("Delete absent: %v", err)
	}
}

func TestKubernetesNetworkPolicyApplier_Apply(t *testing.T) {
	client := fake.NewClientset()
	a := NewKubernetesNetworkPolicyApplier(client)
	if err := a.Apply(context.Background(), NetworkPolicySpec{
		Namespace:          "orion-run-x",
		AllowedEgressCIDRs: []string{"10.0.0.0/8"},
		AllowedEgressPorts: []int32{443, 5432},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Both policies should exist.
	for _, name := range []string{"orion-worker-default-deny", "orion-worker-egress-allow"} {
		if _, err := client.NetworkingV1().NetworkPolicies("orion-run-x").Get(context.Background(), name, metav1.GetOptions{}); err != nil {
			t.Errorf("policy %q missing: %v", name, err)
		}
	}
}

func TestKubernetesNetworkPolicyApplier_Apply_Idempotent(t *testing.T) {
	client := fake.NewClientset()
	a := NewKubernetesNetworkPolicyApplier(client)
	spec := NetworkPolicySpec{Namespace: "ns"}
	if err := a.Apply(context.Background(), spec); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if err := a.Apply(context.Background(), spec); err != nil {
		t.Errorf("idempotent re-Apply failed: %v", err)
	}
}

func TestKubernetesNetworkPolicyApplier_Remove(t *testing.T) {
	client := fake.NewClientset()
	a := NewKubernetesNetworkPolicyApplier(client)
	_ = a.Apply(context.Background(), NetworkPolicySpec{Namespace: "ns"})
	if err := a.Remove(context.Background(), "ns"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	_, err := client.NetworkingV1().NetworkPolicies("ns").Get(context.Background(), "orion-worker-default-deny", metav1.GetOptions{})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("policy still present after Remove: err=%v", err)
	}
}

func TestKubernetesPodCreator_RejectsEmptyFields(t *testing.T) {
	c := NewKubernetesPodCreator(fake.NewClientset(), "img")
	cases := []PodCreateIntent{
		{Namespace: "ns", PodName: "p"},
		{WorkspaceKey: "k", PodName: "p"},
		{WorkspaceKey: "k", Namespace: "ns"},
	}
	for i, in := range cases {
		if _, err := c.Create(context.Background(), in); err == nil {
			t.Errorf("case %d: expected error for missing field", i)
		}
	}
}
