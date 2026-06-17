package architect

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// discoverServicesFromManifests walks Kubernetes manifests + helm chart
// templates + kustomize bases under the repo and returns the deduplicated
// set of Kubernetes Service names. This is the deterministic backbone of
// service discovery: anything under apiVersion: v1 / kind: Service.
//
// The parser is intentionally simple (line-oriented) instead of pulling
// in the k8s YAML libraries: orion's needs are "find Service objects and
// extract metadata.name", which doesn't require full schema validation.
// Trade-off: we don't catch templated names like {{ .Values.x }}; that's
// acceptable for v1 since microservices-demo's static manifests under
// kubernetes-manifests/ have concrete names.
func discoverServicesFromManifests(repoPath string) []string {
	roots := []string{
		filepath.Join(repoPath, "kubernetes-manifests"),
		filepath.Join(repoPath, "k8s"),
		filepath.Join(repoPath, "manifests"),
		filepath.Join(repoPath, "kustomize"),
		filepath.Join(repoPath, "helm-chart", "templates"),
	}

	seen := map[string]bool{}
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			continue
		}
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(p))
			if ext != ".yaml" && ext != ".yml" {
				return nil
			}
			body, readErr := os.ReadFile(p) //nolint:gosec // G304/G122: p is from a constrained walk under repoPath
			if readErr != nil {
				return nil
			}
			for _, name := range extractServiceNames(string(body)) {
				if name != "" {
					seen[name] = true
				}
			}
			return nil
		})
	}

	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// extractServiceNames does a small line-oriented scan for YAML documents
// whose `kind: Service` is set, returning the metadata.name of each.
// Multi-document files (separated by `---`) are honored. Templated names
// (containing `{{`) are rejected.
func extractServiceNames(yaml string) []string {
	var out []string
	docs := strings.Split(yaml, "\n---")
	for _, doc := range docs {
		var kind, name string
		var inMetadata bool
		for _, raw := range strings.Split(doc, "\n") {
			line := strings.TrimRight(raw, "\r")
			trimmed := strings.TrimSpace(line)

			// Detect indentation to track when we leave the metadata block.
			leadingSpaces := len(line) - len(strings.TrimLeft(line, " "))

			switch {
			case strings.HasPrefix(trimmed, "kind:"):
				kind = strings.TrimSpace(strings.TrimPrefix(trimmed, "kind:"))
			case strings.HasPrefix(trimmed, "metadata:"):
				inMetadata = true
			case inMetadata && strings.HasPrefix(trimmed, "name:") && leadingSpaces >= 2:
				name = strings.TrimSpace(strings.TrimPrefix(trimmed, "name:"))
				name = strings.Trim(name, `"'`)
				inMetadata = false
			case inMetadata && leadingSpaces == 0 && trimmed != "":
				// We left the metadata block without finding a name.
				inMetadata = false
			}
		}
		if kind == "Service" && name != "" && !strings.Contains(name, "{{") {
			out = append(out, name)
		}
	}
	return out
}
