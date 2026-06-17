package sandbox

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestWorkspaceKey_IsDeterministic(t *testing.T) {
	org := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	claim := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	run := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	a := WorkspaceKey(org, claim, run)
	b := WorkspaceKey(org, claim, run)
	if a != b {
		t.Errorf("WorkspaceKey is non-deterministic: %q vs %q", a, b)
	}
	if len(a) != 16 {
		t.Errorf("WorkspaceKey length = %d; want 16 (8-byte hex)", len(a))
	}
}

func TestWorkspaceKey_DifferentInputsDiffer(t *testing.T) {
	a := WorkspaceKey(uuid.New(), uuid.New(), uuid.New())
	b := WorkspaceKey(uuid.New(), uuid.New(), uuid.New())
	if a == b {
		t.Errorf("expected different keys for different inputs, got %q", a)
	}
}

func TestInMemoryPodCreator_FirstCreateSucceeds(t *testing.T) {
	c := NewInMemoryPodCreator()
	res, err := c.Create(context.Background(), PodCreateIntent{
		Namespace:    "orion-tenant-a",
		PodName:      "worker-1",
		WorkspaceKey: "ws-aaaa",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !res.Created {
		t.Errorf("Created = false; want true on first create")
	}
	if res.WorkspaceKey != "ws-aaaa" {
		t.Errorf("WorkspaceKey = %q; want %q", res.WorkspaceKey, "ws-aaaa")
	}
}

func TestInMemoryPodCreator_DuplicateKeyIsIdempotent(t *testing.T) {
	c := NewInMemoryPodCreator()
	intent := PodCreateIntent{
		Namespace:    "orion-tenant-a",
		PodName:      "worker-1",
		WorkspaceKey: "ws-aaaa",
	}
	if _, err := c.Create(context.Background(), intent); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	res, err := c.Create(context.Background(), intent)
	if err != nil {
		t.Fatalf("duplicate Create returned error; want success-with-Created=false: %v", err)
	}
	if res.Created {
		t.Errorf("Created = true on duplicate; want false (idempotency)")
	}
	if res.WorkspaceKey != "ws-aaaa" {
		t.Errorf("WorkspaceKey = %q; want %q", res.WorkspaceKey, "ws-aaaa")
	}
}

func TestInMemoryPodCreator_ConcurrentSameKey_OneWins(t *testing.T) {
	c := NewInMemoryPodCreator()
	intent := PodCreateIntent{
		Namespace:    "orion-tenant-a",
		PodName:      "worker-race",
		WorkspaceKey: "ws-race",
	}
	const N = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	var winners int
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := c.Create(context.Background(), intent)
			if err != nil {
				t.Errorf("concurrent Create: %v", err)
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
		t.Errorf("winners = %d; want exactly 1 (idempotency under contention)", winners)
	}
}

func TestInMemoryPodCreator_DifferentKeysProceedIndependently(t *testing.T) {
	c := NewInMemoryPodCreator()
	ctx := context.Background()

	resA, err := c.Create(ctx, PodCreateIntent{Namespace: "ns", PodName: "a", WorkspaceKey: "k-a"})
	if err != nil {
		t.Fatalf("Create A: %v", err)
	}
	resB, err := c.Create(ctx, PodCreateIntent{Namespace: "ns", PodName: "b", WorkspaceKey: "k-b"})
	if err != nil {
		t.Fatalf("Create B: %v", err)
	}
	if !resA.Created || !resB.Created {
		t.Errorf("both keys should win their first create: A=%v B=%v", resA.Created, resB.Created)
	}
	if got := len(c.Pods()); got != 2 {
		t.Errorf("Pods snapshot len = %d; want 2", got)
	}
}

func TestInMemoryPodCreator_RejectsEmptyFields(t *testing.T) {
	c := NewInMemoryPodCreator()
	cases := []PodCreateIntent{
		{Namespace: "ns", PodName: "p"},      // empty WorkspaceKey
		{WorkspaceKey: "k", PodName: "p"},    // empty Namespace
		{WorkspaceKey: "k", Namespace: "ns"}, // empty PodName
	}
	for i, in := range cases {
		if _, err := c.Create(context.Background(), in); err == nil {
			t.Errorf("case %d: expected error for missing required field", i)
		}
	}
}
