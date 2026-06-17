package sandbox

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestInMemoryNamespaceProvisioner_FirstProvision(t *testing.T) {
	p := NewInMemoryNamespaceProvisioner()
	runID := uuid.New()
	res, err := p.Provision(context.Background(), NamespaceSpec{
		RunID: runID, Name: RunNamespaceName(runID), PodSecurity: "restricted",
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if !res.Created {
		t.Error("Created = false on first provision; want true")
	}
}

func TestInMemoryNamespaceProvisioner_DuplicateIsIdempotent(t *testing.T) {
	p := NewInMemoryNamespaceProvisioner()
	runID := uuid.New()
	spec := NamespaceSpec{RunID: runID, Name: RunNamespaceName(runID)}
	first, _ := p.Provision(context.Background(), spec)
	second, err := p.Provision(context.Background(), spec)
	if err != nil {
		t.Fatalf("second Provision: %v", err)
	}
	if second.Created {
		t.Error("second Created = true; want false (idempotent)")
	}
	if second.UID != first.UID {
		t.Errorf("UID changed across idempotent calls: %s vs %s", second.UID, first.UID)
	}
}

func TestInMemoryNamespaceProvisioner_ConcurrentSameNameOneWins(t *testing.T) {
	p := NewInMemoryNamespaceProvisioner()
	runID := uuid.New()
	const N = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := 0
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := p.Provision(context.Background(), NamespaceSpec{
				RunID: runID, Name: RunNamespaceName(runID),
			})
			if err != nil {
				t.Errorf("Provision: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if res.Created {
				winners++
			}
		}()
	}
	wg.Wait()
	if winners != 1 {
		t.Errorf("winners = %d; want exactly 1", winners)
	}
}

func TestInMemoryNamespaceProvisioner_RejectsEmptyName(t *testing.T) {
	p := NewInMemoryNamespaceProvisioner()
	_, err := p.Provision(context.Background(), NamespaceSpec{RunID: uuid.New()})
	if err == nil {
		t.Fatal("expected error for empty Name")
	}
}

func TestInMemoryNamespaceProvisioner_DeleteRemoves(t *testing.T) {
	p := NewInMemoryNamespaceProvisioner()
	runID := uuid.New()
	name := RunNamespaceName(runID)
	if _, err := p.Provision(context.Background(), NamespaceSpec{RunID: runID, Name: name}); err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if err := p.Delete(context.Background(), name); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := p.Snapshot()[name]; ok {
		t.Errorf("namespace still in snapshot after Delete")
	}
	// Delete of absent namespace is a no-op.
	if err := p.Delete(context.Background(), name); err != nil {
		t.Errorf("Delete absent namespace returned error: %v", err)
	}
}

func TestInMemoryNetworkPolicyApplier_Apply(t *testing.T) {
	a := NewInMemoryNetworkPolicyApplier()
	spec := NetworkPolicySpec{Namespace: "orion-run-x", AllowedEgressPorts: []int32{443, 5432}}
	if err := a.Apply(context.Background(), spec); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, ok := a.Get("orion-run-x")
	if !ok {
		t.Fatal("policy not recorded")
	}
	if len(got.AllowedEgressPorts) != 2 {
		t.Errorf("AllowedEgressPorts len = %d; want 2", len(got.AllowedEgressPorts))
	}
}

func TestInMemoryNetworkPolicyApplier_ApplyRejectsEmptyNamespace(t *testing.T) {
	a := NewInMemoryNetworkPolicyApplier()
	if err := a.Apply(context.Background(), NetworkPolicySpec{}); err == nil {
		t.Error("expected error for empty Namespace")
	}
}

func TestInMemoryNetworkPolicyApplier_RemoveDropsPolicy(t *testing.T) {
	a := NewInMemoryNetworkPolicyApplier()
	_ = a.Apply(context.Background(), NetworkPolicySpec{Namespace: "orion-run-x"})
	_ = a.Remove(context.Background(), "orion-run-x")
	if _, ok := a.Get("orion-run-x"); ok {
		t.Error("policy still present after Remove")
	}
}

func TestRunNamespaceName(t *testing.T) {
	runID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	name := RunNamespaceName(runID)
	if !strings.HasPrefix(name, "orion-run-") {
		t.Errorf("name = %q; want orion-run-* prefix", name)
	}
	if !strings.Contains(name, runID.String()) {
		t.Errorf("name = %q; want to contain run UUID", name)
	}
}
