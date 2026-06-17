package architect

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/revelara-ai/orion/internal/llm"
)

// Sentinel errors for callers to errors.Is against.
var (
	// ErrInvalidOptions: caller passed bad InferOptions (empty RepoPath).
	ErrInvalidOptions = errors.New("architect: invalid options")
)

// InfererConfig is the constructor input for NewInferer.
type InfererConfig struct {
	// Generator is the LLM. Optional; if nil, the inferer skips LLM
	// enrichment regardless of InferOptions.EnableLLMEnrich.
	Generator llm.Generator
}

// InferOptions is per-call input to Inferer.Infer.
type InferOptions struct {
	// RepoPath is the absolute path to the cloned target repo. Required.
	RepoPath string

	// CommitSHA is the pinned commit at which inference is run. Optional;
	// if set, recorded in the model's CommitSHA field.
	CommitSHA string

	// EnableLLMEnrich gates the LLM enrichment pass. False by default
	// because callers may want a structural-only model (cheap, fast,
	// deterministic) or because no Generator is configured.
	EnableLLMEnrich bool
}

// Inferer orchestrates the structural and LLM passes that produce an
// ArchitecturalModel.
type Inferer struct {
	gen llm.Generator
}

// NewInferer constructs an Inferer.
func NewInferer(cfg InfererConfig) *Inferer {
	return &Inferer{gen: cfg.Generator}
}

// Infer runs the structural pass and (if enabled and a Generator is
// configured) the LLM enrichment pass, then computes envelope_confidence.
//
// Returns ErrInvalidOptions on bad InferOptions. LLM enrichment errors
// are best-effort and surfaced via the model's logged provenance, NOT
// via the top-level error (so a partial enrichment still yields a usable
// model).
func (i *Inferer) Infer(ctx context.Context, opts InferOptions) (ArchitecturalModel, error) {
	if opts.RepoPath == "" {
		return ArchitecturalModel{}, fmt.Errorf("%w: RepoPath is empty", ErrInvalidOptions)
	}

	model := ArchitecturalModel{
		Repo:      opts.RepoPath,
		CommitSHA: opts.CommitSHA,
	}

	// === Structural pass: services from manifests ===
	serviceNames := discoverServicesFromManifests(opts.RepoPath)
	for _, name := range serviceNames {
		model.Services = append(model.Services, Service{Name: name})
	}

	// === Structural pass: gRPC endpoints from .proto files ===
	grpcEndpoints := extractGRPCEndpoints(opts.RepoPath)

	// Associate each gRPC endpoint with its application service. v1
	// heuristic: case-insensitive substring match between the proto's
	// service name and the application service name. Endpoints that
	// don't map to a known service are attached to a synthesized
	// "<protoservice>" service entry.
	matchedToApp := map[string]bool{}
	for _, ep := range grpcEndpoints {
		var attached bool
		for j := range model.Services {
			appName := model.Services[j].Name
			if matchesGRPCService(appName, ep.Service) {
				model.Services[j].Endpoints = append(model.Services[j].Endpoints, ep)
				attached = true
				matchedToApp[ep.Service] = true
				break
			}
		}
		if !attached && !matchedToApp[ep.Service] {
			// No matching app-service in manifests; create a stub service
			// entry so the endpoint isn't lost.
			synthName := lowerFirst(ep.Service)
			model.Services = append(model.Services, Service{
				Name:      synthName,
				Endpoints: []Endpoint{ep},
			})
			matchedToApp[ep.Service] = true
		} else if !attached {
			// Already created a stub for this proto service; append.
			synthName := lowerFirst(ep.Service)
			for j := range model.Services {
				if model.Services[j].Name == synthName {
					model.Services[j].Endpoints = append(model.Services[j].Endpoints, ep)
					break
				}
			}
		}
	}

	// Re-sort services by name after potential additions.
	sort.SliceStable(model.Services, func(a, b int) bool {
		return model.Services[a].Name < model.Services[b].Name
	})

	// === Structural pass: language + source-dir annotation ===
	if err := annotateServiceLanguages(opts.RepoPath, &model); err != nil {
		return model, fmt.Errorf("annotate languages: %w", err)
	}

	// === LLM enrichment pass (best-effort) ===
	if opts.EnableLLMEnrich && i.gen != nil {
		_ = enrichWithLLM(ctx, i.gen, opts.RepoPath, &model)
		// Errors are accumulated for telemetry but not surfaced; the
		// structural backbone is the contract.
	}

	// Final stable sort of nested slices for deterministic JSON output.
	for j := range model.Services {
		sort.SliceStable(model.Services[j].Endpoints, func(a, b int) bool {
			ea, eb := model.Services[j].Endpoints[a], model.Services[j].Endpoints[b]
			if ea.Kind != eb.Kind {
				return ea.Kind < eb.Kind
			}
			if ea.Service != eb.Service {
				return ea.Service < eb.Service
			}
			return ea.Method < eb.Method
		})
		sort.SliceStable(model.Services[j].DownstreamDeps, func(a, b int) bool {
			da, db := model.Services[j].DownstreamDeps[a], model.Services[j].DownstreamDeps[b]
			if da.TargetName != db.TargetName {
				return da.TargetName < db.TargetName
			}
			return da.Kind < db.Kind
		})
	}

	// === Envelope confidence ===
	model.EnvelopeConfidence = computeEnvelopeConfidence(&model)

	return model, nil
}

// matchesGRPCService is the case-insensitive substring heuristic used to
// associate a proto-side gRPC service name with an application service
// discovered from manifests.
func matchesGRPCService(appName, grpcName string) bool {
	if appName == "" || grpcName == "" {
		return false
	}
	a := lower(appName)
	g := lower(grpcName)
	return a == g || contains(a, g) || contains(g, a)
}

func lower(s string) string {
	out := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	if s[0] >= 'A' && s[0] <= 'Z' {
		return string(s[0]+'a'-'A') + s[1:]
	}
	return s
}
