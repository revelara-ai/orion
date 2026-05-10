package architect

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	protoServiceRe = regexp.MustCompile(`(?m)^\s*service\s+(\w+)\s*\{`)
	protoRPCRe     = regexp.MustCompile(`(?m)^\s*rpc\s+(\w+)\s*\(`)
)

// extractGRPCEndpoints walks .proto files under the repo and returns one
// gRPC Endpoint per (service, rpc) pair. Endpoints are returned sorted by
// (Service, Method) for deterministic output.
//
// Limitations:
//   - We don't disambiguate which service owns which proto when a single
//     proto has rpcs for multiple services. The Endpoint's `Service` field
//     captures the proto-side service name; mapping to repo-side
//     application services is left to the LLM enrichment pass.
//   - We rely on a regex rather than a full proto parser. Multi-line
//     service definitions and macros are not handled. Adequate for v1.
func extractGRPCEndpoints(repoPath string) []Endpoint {
	roots := []string{
		filepath.Join(repoPath, "protos"),
		filepath.Join(repoPath, "proto"),
		filepath.Join(repoPath, "src"),
	}

	type key struct{ Service, Method, File string }
	seen := map[key]bool{}
	var out []Endpoint

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
			if filepath.Ext(p) != ".proto" {
				return nil
			}
			body, readErr := os.ReadFile(p) //#nosec G304 -- p is from a constrained walk under repoPath
			if readErr != nil {
				return nil
			}
			rel, _ := filepath.Rel(repoPath, p)
			for _, ep := range parseProtoBody(string(body), rel) {
				k := key{ep.Service, ep.Method, ep.SourceFile}
				if seen[k] {
					continue
				}
				seen[k] = true
				out = append(out, ep)
			}
			return nil
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Service != out[j].Service {
			return out[i].Service < out[j].Service
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// parseProtoBody scans body for `service X { ... rpc Y( ... ) ... }`
// blocks and emits one Endpoint per RPC. Service association is by
// nearest-preceding `service` declaration.
func parseProtoBody(body, sourceFile string) []Endpoint {
	var out []Endpoint
	var currentService string
	for _, line := range strings.Split(body, "\n") {
		if m := protoServiceRe.FindStringSubmatch(line); m != nil {
			currentService = m[1]
			continue
		}
		if m := protoRPCRe.FindStringSubmatch(line); m != nil && currentService != "" {
			out = append(out, Endpoint{
				Kind:             "grpc",
				Service:          currentService,
				Method:           m[1],
				SourceFile:       sourceFile,
				SourceProvenance: "structural",
			})
		}
	}
	return out
}
