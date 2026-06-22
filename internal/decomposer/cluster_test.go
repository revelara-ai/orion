package decomposer

import "testing"

// Tasks with overlapping FileScope coalesce into one cluster; disjoint scopes stay
// separate; inter-cluster deps are derived and the cluster graph is acyclic
// (returned in dependency order).
func TestClusterCoupledTasksByFileScope(t *testing.T) {
	tasks := []Task{
		{Key: "scaffold", FileScope: "go.mod,cmd/"},
		{Key: "handler", FileScope: "internal/server/", DependsOn: []string{"scaffold"}},
		{Key: "capacity", FileScope: "internal/server/", DependsOn: []string{"handler"}},
		{Key: "security", FileScope: "internal/server/handler.go", DependsOn: []string{"handler"}},
		{Key: "docs", FileScope: "docs/", DependsOn: []string{"scaffold"}},
	}
	clusters, err := Cluster(tasks)
	if err != nil {
		t.Fatalf("Cluster: %v", err)
	}

	byMember := map[string]TaskCluster{}
	for _, c := range clusters {
		for _, m := range c.Members {
			byMember[m] = c
		}
	}
	// handler + capacity + security all touch internal/server/ → one cluster.
	if byMember["handler"].Key != byMember["capacity"].Key || byMember["handler"].Key != byMember["security"].Key {
		t.Fatalf("overlapping internal/server tasks not clustered together: %+v", clusters)
	}
	// scaffold and docs have disjoint scopes → separate clusters.
	if byMember["scaffold"].Key == byMember["handler"].Key {
		t.Fatal("disjoint scaffold clustered with the server cluster")
	}
	if byMember["docs"].Key == byMember["handler"].Key {
		t.Fatal("disjoint docs clustered with the server cluster")
	}
	// Inter-cluster dep derived: server cluster depends on the scaffold cluster.
	if !contains(byMember["handler"].DependsOn, byMember["scaffold"].Key) {
		t.Fatalf("server cluster should depend on scaffold cluster; deps=%v", byMember["handler"].DependsOn)
	}
	// Dependency order: a cluster appears after the clusters it depends on.
	pos := map[string]int{}
	for i, c := range clusters {
		pos[c.Key] = i
	}
	if pos[byMember["scaffold"].Key] > pos[byMember["handler"].Key] {
		t.Fatalf("cluster order violated: scaffold cluster must precede the server cluster; order=%v", clusters)
	}
}

// Clustering that would induce a cycle (A→B→C with A,C sharing a scope, B not) is
// reported as an error, never silently merged into a cyclic schedule.
func TestClusterDetectsInducedCycle(t *testing.T) {
	tasks := []Task{
		{Key: "A", FileScope: "x/"},
		{Key: "B", FileScope: "y/", DependsOn: []string{"A"}},
		{Key: "C", FileScope: "x/", DependsOn: []string{"B"}},
	}
	if _, err := Cluster(tasks); err == nil {
		t.Fatal("expected an induced cluster-cycle error, got nil")
	}
}
