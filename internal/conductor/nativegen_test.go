package conductor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/proof"
	"github.com/revelara-ai/orion/internal/proof/testsynth"
	"github.com/revelara-ai/orion/internal/sandbox"
	"github.com/revelara-ai/orion/pkg/llm"
)

// genProvider is a deterministic stand-in for a coding model: it replays a fixed
// sequence of write_file tool calls (then ends), so the native generator can be
// tested without a live model. The point is to prove the GENERATOR + PROOF build
// and verify ARBITRARY software — not a time service.
type genProvider struct {
	resp []*llm.ChatResponse
	i    int
}

func (p *genProvider) Name() string                                    { return "gen-fake" }
func (p *genProvider) Models(context.Context) ([]llm.ModelInfo, error) { return nil, nil }
func (p *genProvider) Ping(context.Context) error                      { return nil }
func (p *genProvider) Chat(context.Context, llm.ChatRequest) (*llm.ChatResponse, error) {
	r := p.resp[p.i]
	if p.i < len(p.resp)-1 {
		p.i++
	}
	return r, nil
}
func (p *genProvider) ChatStream(ctx context.Context, req llm.ChatRequest, onText func(string)) (*llm.ChatResponse, error) {
	r, err := p.Chat(ctx, req)
	if err != nil {
		return nil, err
	}
	if t := r.Text(); t != "" {
		onText(t)
	}
	return r, nil
}

func writeFileCall(id, path, content string) *llm.ChatResponse {
	in, _ := json.Marshal(map[string]string{"path": path, "content": content})
	return &llm.ChatResponse{StopReason: llm.StopToolUse, Content: []llm.ContentBlock{
		{Type: llm.BlockToolUse, ToolUse: &llm.ToolUse{ID: id, Name: "write_file", Input: in}},
	}}
}
func endGen() *llm.ChatResponse {
	return &llm.ChatResponse{StopReason: llm.StopEndTurn, Content: []llm.ContentBlock{{Type: llm.BlockText, Text: "done"}}}
}

// A NON-time service: GET /greet → {"message":"hello"}. handleTime is the proof
// harness's stable symbol (the model writes whatever logic sits behind it).
const greetService = `package main

import (
	"encoding/json"
	"net/http"
	"os"
)

func handleTime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "hello"})
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/greet", handleTime)
	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}
	_ = http.ListenAndServe(addr, mux)
}
`

// TestNativeGeneratorBuildsArbitraryService proves the harness is GENERAL, not a
// time-service builder: the native generator writes an arbitrary (greeting)
// service to a spec, and the SAME general proof verifies it green against that
// spec's behavioral case.
func TestNativeGeneratorBuildsArbitraryService(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles + runs a service + proof")
	}
	cases := []spec.BehavioralCase{
		{ID: "greet", Request: spec.RequestShape{Method: "GET", Path: "/greet"}, Expect: spec.ExpectShape{Status: 200, ContentType: "application/json", Assertions: []spec.BodyAssertion{{Kind: spec.AssertJSONKeyPresent, Key: "message"}}}},
	}
	gs := sandbox.GenSpec{Module: "orion-generated/svc", Route: "/greet", Format: "json", Cases: cases}

	gen := &genProvider{resp: []*llm.ChatResponse{
		writeFileCall("1", "go.mod", "module orion-generated/svc\n\ngo 1.25\n"),
		writeFileCall("2", "main.go", greetService),
		endGen(),
	}}

	dir := t.TempDir()
	art, err := NativeGenerator(gen, nil, nil)(context.Background(), gs, dir, "")
	if err != nil {
		t.Fatalf("native generate: %v", err)
	}
	if art.ContentHash == "" {
		t.Fatal("no artifact produced")
	}

	rep, err := proof.Prove(context.Background(), dir, testsynth.Contract{Route: "/greet", Cases: cases})
	if err != nil {
		t.Fatalf("prove: %v", err)
	}
	if rep.Outcome.Verdict != "Accept" {
		t.Fatalf("an arbitrary (non-time) service built to spec should prove green: verdict=%s", rep.Outcome.Verdict)
	}
	if o := rep.ObligationResults["greet"]; !o.Executed || !o.Passed {
		t.Fatalf("greet obligation not proven: %+v", rep.ObligationResults)
	}
}

// TestNativeGeneratorRejectsPathEscape: the write_file tool must not let generated
// output escape the build dir.
func TestNativeGeneratorRejectsPathEscape(t *testing.T) {
	dir := t.TempDir()
	gen := &genProvider{resp: []*llm.ChatResponse{
		writeFileCall("1", "../escape.go", "package main"),
		endGen(),
	}}
	// The escape is rejected inside the loop (tool error); generation then ends with
	// no files → ArtifactFromDir errors. Either way, nothing is written outside dir.
	_, _ = NativeGenerator(gen, nil, nil)(context.Background(), sandbox.GenSpec{Module: "orion-generated/svc", Route: "/x", Format: "json"}, dir, "")
	if _, err := os.Stat(filepath.Join(dir, "..", "escape.go")); err == nil {
		t.Fatal("write_file allowed a path escaping the build dir")
	}
}
