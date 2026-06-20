package conductor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/revelara-ai/orion/internal/harness"
	"github.com/revelara-ai/orion/internal/llm"
	"github.com/revelara-ai/orion/internal/orchestrator/spec"
	"github.com/revelara-ai/orion/internal/sandbox"
	"github.com/revelara-ai/orion/internal/tools"
)

// NativeGenerator returns a Generator that produces the program with a model: the
// harness loop, primed with the spec's behavioral contract and a write_file tool,
// writes ARBITRARY code to satisfy the cases. This is what makes Orion general —
// the proof stays the constant; generation is no longer a fixed time-service
// template. The model never sees the proof corpus (it gets the contract — the
// cases — not the harness-authored tests), so it still cannot grade its own
// homework: the (independent) proof holds whatever it writes accountable.
func NativeGenerator(provider llm.Provider) Generator {
	return func(ctx context.Context, gs sandbox.GenSpec, buildDir string) (sandbox.GeneratedArtifact, error) {
		reg := tools.NewRegistry()
		reg.Register(writeFileTool(buildDir))
		loop := harness.Loop{
			Provider:   provider,
			Tools:      reg,
			System:     generationRole(gs),
			Supervisor: harness.Supervisor{MaxIterations: 24},
		}
		start := []llm.Message{llm.TextMessage(llm.RoleUser, "Generate the program now: write each file with write_file (a complete go.mod and main.go), then end your turn.")}
		if _, _, err := loop.Run(ctx, start, nil); err != nil {
			return sandbox.GeneratedArtifact{}, fmt.Errorf("native generation: %w", err)
		}
		return sandbox.ArtifactFromDir(buildDir)
	}
}

// writeFileTool lets the generator write files into the build dir only (a path
// that escapes the dir is rejected — the model's output is untrusted).
func writeFileTool(buildDir string) tools.Tool {
	root := filepath.Clean(buildDir)
	return tools.Tool{
		Name:        "write_file",
		Description: "Write one file of the program. path is relative to the module root (e.g. \"main.go\", \"go.mod\"). Overwrites if it exists. Call once per file.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`),
		Safety:      tools.Safety{Destructive: true},
		Run: func(_ context.Context, in json.RawMessage) (string, error) {
			var p struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			if strings.TrimSpace(p.Path) == "" {
				return "", fmt.Errorf("path is required")
			}
			full := filepath.Join(root, filepath.Clean("/"+p.Path))
			if full != root && !strings.HasPrefix(full, root+string(os.PathSeparator)) {
				return "", fmt.Errorf("path %q escapes the build directory", p.Path)
			}
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(full, []byte(p.Content), 0o644); err != nil {
				return "", err
			}
			return "wrote " + p.Path + fmt.Sprintf(" (%d bytes)", len(p.Content)), nil
		},
	}
}

// generationRole primes the model with the behavioral contract. It is
// domain-agnostic: whatever cases the spec carries, the program must satisfy them.
// The handleTime symbol is the proof harness's stable entry point (the behavioral
// mode calls it in-process); the model writes whatever logic satisfies the cases
// behind it.
func generationRole(gs sandbox.GenSpec) string {
	module := gs.Module
	if module == "" {
		module = "orion-generated/svc"
	}
	var b strings.Builder
	b.WriteString("You are Orion's code generator. Write a COMPLETE, COMPILABLE Go HTTP service that satisfies the behavioral contract below.\n\n")
	b.WriteString("Hard requirements:\n")
	b.WriteString("- A go.mod with `module " + module + "` and a recent `go` line (e.g. go 1.25).\n")
	b.WriteString("- main.go exposing the request handler as a top-level func `handleTime(w http.ResponseWriter, r *http.Request)` (the proof harness calls this symbol directly).\n")
	b.WriteString("- A main() that mounts handleTime at the route and listens on $PORT (default the port below), with server timeouts + graceful shutdown.\n")
	b.WriteString("- If a case uses an unknown/invalid input, return the exact status + body the case requires (do not crash).\n")
	b.WriteString("- Real logic, not hardcoded responses: the proof probes the LIVE service at the current time and runs mutation testing.\n\n")
	fmt.Fprintf(&b, "Route: %s\nDefault response format: %s\nDefault timezone: %s\nPort: %d\n\n", gs.Route, fmtOr(gs.Format, "json"), fmtOr(gs.TimeZone, "UTC"), portOr(gs.Port))
	b.WriteString("The service MUST satisfy these behavioral cases (request → expected response):\n")
	cases := append([]spec.BehavioralCase(nil), gs.Cases...)
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	if len(cases) == 0 {
		fmt.Fprintf(&b, "- GET %s → 200, %s, a current timestamp.\n", gs.Route, fmtOr(gs.Format, "json"))
	}
	for _, c := range cases {
		b.WriteString(renderCaseForGen(c))
	}
	b.WriteString("\nWrite go.mod and main.go via write_file, then end your turn.")
	return b.String()
}

func renderCaseForGen(c spec.BehavioralCase) string {
	method := c.Request.Method
	if method == "" {
		method = "GET"
	}
	q := ""
	if len(c.Request.Query) > 0 {
		var parts []string
		for k, v := range c.Request.Query {
			parts = append(parts, k+"="+v)
		}
		sort.Strings(parts)
		q = "?" + strings.Join(parts, "&")
	}
	var asserts []string
	for _, a := range c.Expect.Assertions {
		switch a.Kind {
		case spec.AssertJSONKeyPresent:
			asserts = append(asserts, "JSON has non-empty \""+a.Key+"\"")
		case spec.AssertJSONKeyRFC3339:
			asserts = append(asserts, "JSON \""+a.Key+"\" is an RFC3339 timestamp")
		case spec.AssertJSONKeyInTZ:
			asserts = append(asserts, "JSON \""+a.Key+"\" is an RFC3339 timestamp in timezone "+a.Value)
		case spec.AssertJSONErrorPresent:
			asserts = append(asserts, "JSON has a non-empty \"error\"")
		case spec.AssertBodyRFC3339:
			asserts = append(asserts, "body is an RFC3339 timestamp")
		}
	}
	tail := ""
	if len(asserts) > 0 {
		tail = "; " + strings.Join(asserts, "; ")
	}
	return fmt.Sprintf("- %s %s%s → status %d, content-type %s%s\n", method, c.Request.Path, q, c.Expect.Status, c.Expect.ContentType, tail)
}

func fmtOr(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}
func portOr(p int) int {
	if p == 0 {
		return 8080
	}
	return p
}
