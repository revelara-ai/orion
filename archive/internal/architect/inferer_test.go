package architect_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/revelara-ai/orion/internal/architect"
	"github.com/revelara-ai/orion/internal/llm"
)

// fakeGenerator returns a single canned LLM response per (service, prompt).
// Tests configure it with a map keyed by "<service>".
type fakeGenerator struct {
	responses map[string]string
	errFor    map[string]error
	calls     int
}

func (f *fakeGenerator) Generate(_ context.Context, req llm.GenerateRequest) (llm.GenerateResponse, error) {
	f.calls++
	for svc, errOut := range f.errFor {
		if strings.Contains(req.User, svc) {
			return llm.GenerateResponse{}, errOut
		}
	}
	for svc, body := range f.responses {
		if strings.Contains(req.User, svc) {
			return llm.GenerateResponse{Text: body, Model: "fake"}, nil
		}
	}
	// Fallback: return an empty findings JSON
	return llm.GenerateResponse{Text: `{"endpoints":[],"downstream_deps":[]}`, Model: "fake"}, nil
}

// fixtureRepo returns the path to the test-data fixture repo (a small
// hand-rolled microservices-like layout).
func fixtureRepo(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(here), "testdata", "tiny_demo")
}

func TestInferer_StructuralPassFindsServicesFromManifests(t *testing.T) {
	repo := fixtureRepo(t)
	gen := &fakeGenerator{}

	inf := architect.NewInferer(architect.InfererConfig{Generator: gen})
	model, err := inf.Infer(context.Background(), architect.InferOptions{
		RepoPath: repo,
	})
	if err != nil {
		t.Fatalf("Infer: %v", err)
	}

	if len(model.Services) == 0 {
		t.Fatal("expected at least one service from k8s manifests; got 0")
	}

	// Services should be sorted by Name (deterministic golden compare).
	names := make([]string, len(model.Services))
	for i, s := range model.Services {
		names[i] = s.Name
	}
	sorted := append([]string(nil), names...)
	sort.Strings(sorted)
	for i := range names {
		if names[i] != sorted[i] {
			t.Errorf("services not sorted: got %v, want %v", names, sorted)
			break
		}
	}
}

func TestInferer_StructuralPassExtractsRPCsFromProtos(t *testing.T) {
	repo := fixtureRepo(t)
	gen := &fakeGenerator{}

	inf := architect.NewInferer(architect.InfererConfig{Generator: gen})
	model, err := inf.Infer(context.Background(), architect.InferOptions{RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}

	var grpcEndpoints int
	for _, svc := range model.Services {
		for _, ep := range svc.Endpoints {
			if ep.Kind == "grpc" && ep.SourceProvenance == "structural" {
				grpcEndpoints++
			}
		}
	}
	if grpcEndpoints == 0 {
		t.Error("expected at least one gRPC endpoint from .proto files; got 0")
	}
}

func TestInferer_LLMPassEnrichesEndpoints(t *testing.T) {
	repo := fixtureRepo(t)
	gen := &fakeGenerator{
		responses: map[string]string{
			"frontend": `{"endpoints":[{"kind":"http","method":"GET /","source_file":"src/frontend/main.go"}],"downstream_deps":[{"target_name":"checkoutservice","kind":"service","protocol":"grpc"}]}`,
		},
	}

	inf := architect.NewInferer(architect.InfererConfig{Generator: gen})
	model, err := inf.Infer(context.Background(), architect.InferOptions{
		RepoPath:        repo,
		EnableLLMEnrich: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	var found bool
	for _, svc := range model.Services {
		if svc.Name != "frontend" {
			continue
		}
		for _, ep := range svc.Endpoints {
			if ep.Kind == "http" && ep.SourceProvenance == "llm" {
				found = true
			}
		}
		for _, dep := range svc.DownstreamDeps {
			if dep.TargetName == "checkoutservice" && dep.SourceProvenance == "llm" {
				if dep.Protocol != "grpc" {
					t.Errorf("dep.Protocol=%q, want grpc", dep.Protocol)
				}
			}
		}
	}
	if !found {
		t.Error("expected an LLM-provenance HTTP endpoint on frontend; not found")
	}
}

func TestInferer_LLMDisabledByDefault(t *testing.T) {
	repo := fixtureRepo(t)
	gen := &fakeGenerator{}

	inf := architect.NewInferer(architect.InfererConfig{Generator: gen})
	_, err := inf.Infer(context.Background(), architect.InferOptions{RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	if gen.calls != 0 {
		t.Errorf("LLM should not be called when EnableLLMEnrich=false; got %d calls", gen.calls)
	}
}

func TestInferer_LLMErrorsTreatedAsBestEffort(t *testing.T) {
	repo := fixtureRepo(t)
	gen := &fakeGenerator{
		errFor: map[string]error{
			"frontend": llm.ErrGenerationFailed,
		},
	}

	inf := architect.NewInferer(architect.InfererConfig{Generator: gen})
	model, err := inf.Infer(context.Background(), architect.InferOptions{
		RepoPath:        repo,
		EnableLLMEnrich: true,
	})
	if err != nil {
		t.Fatalf("Infer should succeed even when LLM enrichment fails for some services; got %v", err)
	}
	if len(model.Services) == 0 {
		t.Fatal("structural pass output should still be present")
	}
}

func TestInferer_RequiresRepoPath(t *testing.T) {
	inf := architect.NewInferer(architect.InfererConfig{})
	_, err := inf.Infer(context.Background(), architect.InferOptions{})
	if err == nil {
		t.Fatal("want error on empty RepoPath")
	}
	if !errors.Is(err, architect.ErrInvalidOptions) {
		t.Errorf("err=%v; want ErrInvalidOptions", err)
	}
}

func TestInferer_EnvelopeConfidenceComputed(t *testing.T) {
	repo := fixtureRepo(t)
	gen := &fakeGenerator{}

	inf := architect.NewInferer(architect.InfererConfig{Generator: gen})
	model, err := inf.Infer(context.Background(), architect.InferOptions{RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	ec := model.EnvelopeConfidence
	if ec.Score < 0 || ec.Score > 1 {
		t.Errorf("EnvelopeConfidence.Score=%f, want in [0,1]", ec.Score)
	}
	if ec.EndpointCoverage < 0 || ec.EndpointCoverage > 1 {
		t.Errorf("EndpointCoverage out of range: %f", ec.EndpointCoverage)
	}
}

func TestInferer_StructuralIsDeterministic(t *testing.T) {
	repo := fixtureRepo(t)
	gen := &fakeGenerator{}

	inf := architect.NewInferer(architect.InfererConfig{Generator: gen})
	m1, err := inf.Infer(context.Background(), architect.InferOptions{RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	m2, err := inf.Infer(context.Background(), architect.InferOptions{RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	b1, _ := json.Marshal(m1)
	b2, _ := json.Marshal(m2)
	if string(b1) != string(b2) {
		t.Errorf("non-deterministic structural pass:\n  m1=%s\n  m2=%s", b1, b2)
	}
}

// TestModel_JSONShape ensures the model serializes to a stable JSON shape
// that downstream consumers can rely on. Not a golden-file (the fixture
// is small enough to hand-assert).
func TestModel_JSONShape(t *testing.T) {
	repo := fixtureRepo(t)
	gen := &fakeGenerator{}

	inf := architect.NewInferer(architect.InfererConfig{Generator: gen})
	model, err := inf.Infer(context.Background(), architect.InferOptions{RepoPath: repo})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"services"`, `"envelope_confidence"`, `"score"`, `"endpoint_coverage"`} {
		if !contains(string(b), want) {
			t.Errorf("JSON missing required field %s", want)
		}
	}
	_ = os.Hostname
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
