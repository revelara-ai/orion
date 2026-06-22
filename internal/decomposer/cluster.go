package decomposer

import (
	"fmt"
	"strings"
)

// TaskCluster groups tasks whose declared FileScopes overlap, so coupled work goes
// to one agent / worktree rather than clobbering a shared path. Clusters are
// returned in dependency (topological) order; DependsOn carries the inter-cluster
// edges derived from member task dependencies (or-tcs.1.2).
type TaskCluster struct {
	Key        string   // stable cluster key (the lowest member key)
	Members    []string // member task keys, in input order
	FileScopes []string // the distinct declared file scopes the cluster touches
	DependsOn  []string // keys of prerequisite clusters
}

// Cluster groups tasks by overlapping declared FileScope into clusters, returned in
// dependency order. Tasks whose scopes overlap — directly or transitively, at a
// directory-prefix granularity — coalesce into one cluster; disjoint scopes stay
// separate. Inter-cluster dependencies are derived from member task DependsOn edges.
// Clustering can, in principle, induce a cycle in an otherwise-acyclic task graph
// (A→B→C with A,C sharing a scope but B not); that is reported as an error rather
// than silently merged, so the caller never schedules a cyclic cluster graph.
func Cluster(tasks []Task) ([]TaskCluster, error) {
	n := len(tasks)
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	find := func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	union := func(a, b int) {
		if ra, rb := find(a), find(b); ra != rb {
			parent[ra] = rb
		}
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if scopesOverlap(tasks[i].FileScope, tasks[j].FileScope) {
				union(i, j)
			}
		}
	}

	// Group tasks by connected component, preserving first-seen order.
	rootMembers := map[int][]int{}
	var rootOrder []int
	for i := 0; i < n; i++ {
		r := find(i)
		if _, ok := rootMembers[r]; !ok {
			rootOrder = append(rootOrder, r)
		}
		rootMembers[r] = append(rootMembers[r], i)
	}

	clusterKeyByTask := map[string]string{}
	clusters := map[string]*TaskCluster{}
	var clusterKeys []string
	for _, r := range rootOrder {
		members := rootMembers[r]
		key := tasks[members[0]].Key
		for _, m := range members {
			if tasks[m].Key < key {
				key = tasks[m].Key
			}
		}
		c := &TaskCluster{Key: key}
		seenScope := map[string]bool{}
		for _, m := range members {
			c.Members = append(c.Members, tasks[m].Key)
			clusterKeyByTask[tasks[m].Key] = key
			if fs := strings.TrimSpace(tasks[m].FileScope); fs != "" && !seenScope[fs] {
				seenScope[fs] = true
				c.FileScopes = append(c.FileScopes, fs)
			}
		}
		clusters[key] = c
		clusterKeys = append(clusterKeys, key)
	}

	// Derive inter-cluster dependency edges from member task deps.
	for _, t := range tasks {
		ck := clusterKeyByTask[t.Key]
		for _, dep := range t.DependsOn {
			dk, ok := clusterKeyByTask[dep]
			if !ok {
				return nil, fmt.Errorf("task %s depends on unknown task %s", t.Key, dep)
			}
			if dk != ck && !contains(clusters[ck].DependsOn, dk) {
				clusters[ck].DependsOn = append(clusters[ck].DependsOn, dk)
			}
		}
	}

	ordered, err := topoClusters(clusterKeys, clusters)
	if err != nil {
		return nil, err
	}
	out := make([]TaskCluster, 0, len(ordered))
	for _, k := range ordered {
		out = append(out, *clusters[k])
	}
	return out, nil
}

// topoClusters returns cluster keys in dependency order via Kahn's algorithm,
// erroring on a cycle.
func topoClusters(keys []string, clusters map[string]*TaskCluster) ([]string, error) {
	indeg := make(map[string]int, len(keys))
	for _, k := range keys {
		indeg[k] = 0
	}
	adj := map[string][]string{}
	for _, k := range keys {
		for _, dep := range clusters[k].DependsOn {
			adj[dep] = append(adj[dep], k)
			indeg[k]++
		}
	}
	var queue []string
	for _, k := range keys {
		if indeg[k] == 0 {
			queue = append(queue, k)
		}
	}
	out := make([]string, 0, len(keys))
	for len(queue) > 0 {
		k := queue[0]
		queue = queue[1:]
		out = append(out, k)
		for _, nb := range adj[k] {
			indeg[nb]--
			if indeg[nb] == 0 {
				queue = append(queue, nb)
			}
		}
	}
	if len(out) != len(keys) {
		return nil, fmt.Errorf("clustering induced a cycle in the cluster graph (sorted %d of %d clusters)", len(out), len(keys))
	}
	return out, nil
}

// scopesOverlap reports whether two declared file scopes touch overlapping paths.
// A scope may list several comma-separated paths; overlap is directory-prefix
// (a path equal to, or an ancestor directory of, another).
func scopesOverlap(a, b string) bool {
	for _, pa := range splitScope(a) {
		for _, pb := range splitScope(b) {
			if pathOverlap(pa, pb) {
				return true
			}
		}
	}
	return false
}

func splitScope(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, strings.TrimSuffix(p, "/"))
		}
	}
	return out
}

func pathOverlap(a, b string) bool {
	if a == b {
		return true
	}
	return strings.HasPrefix(b, a+"/") || strings.HasPrefix(a, b+"/")
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
