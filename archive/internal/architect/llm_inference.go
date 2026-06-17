package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/revelara-ai/orion/internal/llm"
)

// llmEnrichmentMaxFiles caps how many source files we send to the LLM per
// service to bound prompt size and cost. Files are picked deterministically
// (sorted by path) and truncated.
const (
	llmEnrichmentMaxFiles      = 25
	llmEnrichmentMaxFileBytes  = 4096
	llmEnrichmentTotalMaxBytes = 60_000
)

// llmServiceFinding is the shape we ask the LLM to emit per service.
type llmServiceFinding struct {
	Endpoints      []llmEndpoint      `json:"endpoints"`
	DownstreamDeps []llmDownstreamDep `json:"downstream_deps"`
}

type llmEndpoint struct {
	Kind       string `json:"kind"`        // "http" | "grpc"
	Method     string `json:"method"`      // "GET /foo" or RPC name
	Service    string `json:"service"`     // gRPC service name (optional)
	SourceFile string `json:"source_file"` // optional
}

type llmDownstreamDep struct {
	TargetName string `json:"target_name"`
	Kind       string `json:"kind"`     // "service" | "store"
	Protocol   string `json:"protocol"` // "grpc" | "http" | ...
}

// enrichWithLLM runs one LLM call per service using gen and merges the
// result into model.Services. Errors per-service are logged via the
// returned slice (best-effort enrichment); they do NOT abort the whole
// inference.
func enrichWithLLM(ctx context.Context, gen llm.Generator, repoPath string, model *ArchitecturalModel) []error {
	if gen == nil {
		return nil
	}
	var errs []error

	for i := range model.Services {
		svc := &model.Services[i]
		if svc.SourceDir == "" {
			// Nothing to feed the LLM.
			continue
		}
		body, err := buildServicePrompt(repoPath, svc)
		if err != nil {
			errs = append(errs, fmt.Errorf("service %s: prompt build: %w", svc.Name, err))
			continue
		}
		if body == "" {
			continue
		}
		resp, err := gen.Generate(ctx, llm.GenerateRequest{
			System:      llmSystemPrompt,
			User:        body,
			Temperature: 0,
			MaxTokens:   2048,
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("service %s: llm: %w", svc.Name, err))
			continue
		}
		var finding llmServiceFinding
		if perr := parseLLMJSON(resp.Text, &finding); perr != nil {
			errs = append(errs, fmt.Errorf("service %s: parse: %w", svc.Name, perr))
			continue
		}
		mergeLLMFinding(svc, finding)
	}
	return errs
}

const llmSystemPrompt = `You are an architectural inferer. Given source files for a single service from a polyglot microservices repo, identify:

  - HTTP endpoints (kind=http, method="<HTTP-METHOD> <path>")
  - gRPC endpoints implemented (kind=grpc, method=<RPC name>, service=<grpc service name>)
  - Downstream dependencies (calls to other services or persistent stores)

Output ONLY a JSON object with this exact shape and no surrounding text:
{
  "endpoints": [
    {"kind": "http"|"grpc", "method": "...", "service": "...", "source_file": "..."}
  ],
  "downstream_deps": [
    {"target_name": "...", "kind": "service"|"store", "protocol": "grpc"|"http"|"sql"|"redis"|"kafka"|"amqp"}
  ]
}

Rules:
  - If you cannot determine endpoints or deps, return empty arrays. NEVER guess.
  - target_name should match the canonical service name when known.
  - Skip framework-internal endpoints (health checks, metrics scrapers).
`

// buildServicePrompt returns a token-bounded user prompt embedding service
// metadata + a deterministic file inventory + truncated file contents.
// Returns "" if no source files were found (skip LLM).
func buildServicePrompt(repoPath string, svc *Service) (string, error) {
	srcAbs := filepath.Join(repoPath, svc.SourceDir)
	info, err := os.Stat(srcAbs)
	if err != nil || !info.IsDir() {
		return "", nil
	}

	var files []fileEntry
	_ = filepath.WalkDir(srcAbs, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if isUninterestingFile(p) {
			return nil
		}
		if fi, statErr := d.Info(); statErr == nil {
			rel, _ := filepath.Rel(repoPath, p)
			files = append(files, fileEntry{rel: rel, size: fi.Size()})
		}
		return nil
	})
	if len(files) == 0 {
		return "", nil
	}
	// Stable order, source-likely files first.
	sortFilesForPrompt(files)
	if len(files) > llmEnrichmentMaxFiles {
		files = files[:llmEnrichmentMaxFiles]
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## Service\n\nname: %s\nlanguage: %s\nsource_dir: %s\n\n## Files\n\n", svc.Name, svc.Language, svc.SourceDir)
	totalBytes := b.Len()
	for _, f := range files {
		body, err := os.ReadFile(filepath.Join(repoPath, f.rel)) //#nosec G304 -- f.rel is under the constrained walk root
		if err != nil {
			continue
		}
		if len(body) > llmEnrichmentMaxFileBytes {
			body = body[:llmEnrichmentMaxFileBytes]
		}
		header := fmt.Sprintf("### %s\n\n```\n", f.rel)
		footer := "\n```\n\n"
		need := len(header) + len(body) + len(footer)
		if totalBytes+need > llmEnrichmentTotalMaxBytes {
			break
		}
		b.WriteString(header)
		b.Write(body)
		b.WriteString(footer)
		totalBytes += need
	}
	return b.String(), nil
}

func isUninterestingFile(path string) bool {
	base := filepath.Base(path)
	if base == "go.sum" || strings.HasSuffix(base, ".lock") || strings.HasSuffix(base, ".png") || strings.HasSuffix(base, ".jpg") {
		return true
	}
	if strings.Contains(path, "/vendor/") || strings.Contains(path, "/node_modules/") || strings.Contains(path, "/.git/") {
		return true
	}
	return false
}

func sortFilesForPrompt(files []fileEntry) {
	// Source files (.go, .py, .java, .cs, .js, .ts) first; others after.
	priority := func(p string) int {
		switch strings.ToLower(filepath.Ext(p)) {
		case ".go", ".py", ".java", ".kt", ".cs", ".js", ".ts", ".mjs", ".cjs", ".rb", ".rs":
			return 0
		case ".proto":
			return 1
		case ".yaml", ".yml":
			return 2
		default:
			return 3
		}
	}
	// Stable sort by (priority, path).
	for i := 1; i < len(files); i++ {
		for j := i; j > 0; j-- {
			pa, pb := priority(files[j-1].rel), priority(files[j].rel)
			if pa < pb || (pa == pb && files[j-1].rel <= files[j].rel) {
				break
			}
			files[j-1], files[j] = files[j], files[j-1]
		}
	}
}

// fileEntry is a small helper struct for prompt building. Lives at file
// scope so sortFilesForPrompt can be unit-tested separately if needed.
type fileEntry struct {
	rel  string
	size int64
}

// parseLLMJSON tries to parse the LLM response as a JSON object matching
// llmServiceFinding. Tolerates the model wrapping the JSON in a fenced
// code block.
func parseLLMJSON(raw string, out *llmServiceFinding) error {
	trimmed := strings.TrimSpace(raw)

	// Strip ```json ... ``` or ``` ... ``` fences if present.
	if strings.HasPrefix(trimmed, "```") {
		// Drop the first line (```json or ```).
		if i := strings.Index(trimmed, "\n"); i >= 0 {
			trimmed = trimmed[i+1:]
		}
		if i := strings.LastIndex(trimmed, "```"); i >= 0 {
			trimmed = trimmed[:i]
		}
		trimmed = strings.TrimSpace(trimmed)
	}
	if trimmed == "" {
		return fmt.Errorf("empty LLM response")
	}
	return json.Unmarshal([]byte(trimmed), out)
}

// mergeLLMFinding folds llm-discovered endpoints + deps into the service
// model, marking each as provenance="llm" and avoiding duplicates of
// structurally-discovered items.
func mergeLLMFinding(svc *Service, finding llmServiceFinding) {
	// Endpoint dedup key: (kind, service, method).
	type ekey struct{ kind, service, method string }
	have := map[ekey]bool{}
	for _, ep := range svc.Endpoints {
		have[ekey{ep.Kind, ep.Service, ep.Method}] = true
	}
	for _, ep := range finding.Endpoints {
		k := ekey{ep.Kind, ep.Service, ep.Method}
		if ep.Kind == "" || ep.Method == "" {
			continue
		}
		if have[k] {
			continue
		}
		have[k] = true
		svc.Endpoints = append(svc.Endpoints, Endpoint{
			Kind:             ep.Kind,
			Method:           ep.Method,
			Service:          ep.Service,
			SourceFile:       ep.SourceFile,
			SourceProvenance: "llm",
		})
	}

	// Dep dedup key: (target_name, kind).
	type dkey struct{ name, kind string }
	haveDep := map[dkey]bool{}
	for _, d := range svc.DownstreamDeps {
		haveDep[dkey{d.TargetName, d.Kind}] = true
	}
	for _, d := range finding.DownstreamDeps {
		k := dkey{d.TargetName, d.Kind}
		if d.TargetName == "" {
			continue
		}
		if haveDep[k] {
			continue
		}
		haveDep[k] = true
		svc.DownstreamDeps = append(svc.DownstreamDeps, DownstreamDep{
			TargetName:       d.TargetName,
			Kind:             d.Kind,
			Protocol:         d.Protocol,
			SourceProvenance: "llm",
		})
	}
}
